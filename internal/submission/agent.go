package submission

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"apm/internal/agentartifact"
	"apm/internal/domain"
)

type agentSourceLoader struct{}

// Load 只接受 Agent 资源根目录：按 Agent 内容策略冻结整棵树，再转换为逻辑快照交给
// agentartifact.Validate 统一校验（根闭集、可执行文件、嵌套归档、avatar 与 agent.yaml
// 解码）。单个 YAML 文件、预构建 gzip 与远程 URL 均被拒绝；symlink 与特殊文件由
// ValidatePhysicalTree 拦截。构建只消费冻结快照，不重读源目录。
func (agentSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	snapshot, err := captureSourceTreeWithPolicy(ctx, path, agentSourceContentPolicy)
	if err != nil {
		return source{}, err
	}
	entries := make([]agentartifact.Entry, 0, len(snapshot.entries))
	for _, entry := range snapshot.entries {
		entries = append(entries, agentartifact.Entry{
			Path:      strings.TrimSuffix(entry.name, "/"),
			Directory: entry.typeflag == tar.TypeDir,
			Mode:      fs.FileMode(entry.mode),
			Data:      entry.body,
		})
	}
	validated, err := agentartifact.Validate(entries, nil)
	if err != nil {
		return source{}, fmt.Errorf("validate agent artifact: %w", errors.Join(ErrInvalid, err))
	}
	// 提交侧只接受 schema_version 2 且本地化展示字段必填；安装、缓存与事务恢复路径
	// 继续使用 agentartifact.Validate 的 v1+v2 宽松语义（ADR-038）。
	if err := validated.Config.ValidateSubmit(); err != nil {
		return source{}, fmt.Errorf("validate agent artifact: %w", errors.Join(ErrInvalid, err))
	}
	return source{
		validation: Validation{
			Type: domain.PowerTypeAgent, Name: validated.Config.Agent.Name, Version: validated.Config.Agent.Version,
			Description: validated.Config.Agent.Description, FileCount: validated.FileCount, ContentSize: validated.ContentSize,
			Warnings: agentIdentityWarnings(validated.Config.Agent),
		},
		contentType: "application/gzip",
		build: func() ([]byte, error) {
			return agentTarGz(snapshot)
		},
	}, nil
}

// agentIdentityWarnings 报告不影响 APM 校验、但会影响 Runtime 可用性的字段取值问题。
// 目前只有一条：agent.id 必须与 agent.name 相同。Bibit 按 agent.id 定位已安装的
// agents/<agent.name> 目录，取值不同时 Agent 仍能安装与列出，但 Runtime 按 ID 找不到；
// APM 只校验 id trim 后非空、不比较二者（ADR-023/ADR-033），故这里只产出 warning，
// 不改变 validate 的成功结果。返回空切片而非 nil，保持 `--json` 的 warnings 为 `[]`。
func agentIdentityWarnings(info domain.AgentInfo) []string {
	warnings := []string{}
	if info.ID != info.Name {
		warnings = append(warnings, fmt.Sprintf(
			"agent.id %q must equal agent.name %q: Bibit locates the installed agents/<name> directory by agent.id, so a mismatch makes the Agent unreachable by ID at runtime",
			info.ID, info.Name))
	}
	return warnings
}
