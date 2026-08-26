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
	return source{
		validation: Validation{
			Type: domain.PowerTypeAgent, Name: validated.Config.Agent.Name, Version: validated.Config.Agent.Version,
			Description: validated.Config.Agent.Description, FileCount: validated.FileCount, ContentSize: validated.ContentSize, Warnings: []string{},
		},
		contentType: "application/gzip",
		build: func() ([]byte, error) {
			return agentTarGz(snapshot)
		},
	}, nil
}
