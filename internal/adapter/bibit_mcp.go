package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"apm/internal/domain"
)

const bibitMCPExecutable = "bianbu-agent"

// bibitMCPAdapter 通过 bianbu-agent CLI 管理 Bibit 的用户级 MCP 配置。
//
// 契约对齐板端真实 bianbu-agent 0.1.69（依据仓库 bianbu-agent-cli-guide.md 与实测）：
// mcp get <name> 无 --json，短名归一化为公开 ID mcp.<name>，存在返回含 id 的 JSON 对象、不存在返回 null；
// mcp add 以互斥的 --command（stdio）与 --url（HTTP）登记服务器，并自行把目录包写入 CLI 的 data_dir，
// 因此 APM 不再预写目录包（旧版的 mcp install 子命令在新版 CLI 中已移除）。
type bibitMCPAdapter struct {
	runner agentCLIRunner
}

func newBibitMCPAdapter(runner agentCLIRunner) *bibitMCPAdapter {
	return &bibitMCPAdapter{runner: runner}
}

// bibitPublicMCPID 返回短名对应的公开 ID；APM 规范名恒为小写 kebab、不含点，
// 因此 CLI 内部归一化后恒为 "mcp.<name>"。
func bibitPublicMCPID(name string) string {
	return "mcp." + name
}

// bibitMCPDocumentID 从 mcp get 与 mcp add 的 stdout 取公开 MCP ID。
// 只接受恰好一个含非空 id 的 JSON 对象；空输出、非 JSON、缺 id 或出现第二个 JSON 值都返回错误，
// 由调用方按 unknown/fail closed 处理。
func bibitMCPDocumentID(stdout string) (string, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(stdout))
	var document struct {
		ID string `json:"id"`
	}
	if err := decoder.Decode(&document); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return "", err
	}
	if document.ID == "" {
		return "", errors.New("returned document has no id")
	}
	return document.ID, nil
}

// bibitMCPAddApplied 判断 mcp add 的输出是否确认登记了目标公开 ID，
// 避免退出码为 0 但实际未登记（或登记成别的名字）时误报 applied。
func bibitMCPAddApplied(stdout string, name string) bool {
	id, err := bibitMCPDocumentID(stdout)
	return err == nil && id == bibitPublicMCPID(name)
}

func (adapter *bibitMCPAdapter) Inspect(ctx context.Context, name string) (MCPPresence, error) {
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, []string{"mcp", "get", name}, "")
	if result.StdoutTruncated || result.StderrTruncated {
		return inspectTruncated(domain.AgentBibit, name, result)
	}
	if runErr != nil {
		return inspectUnknown(domain.AgentBibit, name, result, runErr)
	}
	// 不存在时 CLI 输出字面量 null（exit 0）；必须先于结构解码判断，
	// 因为把 null 解进 struct 不会报错且字段全零。
	if strings.TrimSpace(result.Stdout) == "null" {
		return MCPAbsent, nil
	}
	id, err := bibitMCPDocumentID(result.Stdout)
	if err != nil {
		return inspectUnknown(domain.AgentBibit, name, result, err)
	}
	if id != bibitPublicMCPID(name) {
		return inspectUnknown(domain.AgentBibit, name, result, fmt.Errorf("returned id %q", id))
	}
	return MCPPresent, nil
}

func (adapter *bibitMCPAdapter) Add(ctx context.Context, ref domain.PowerRef, server domain.MCPServer) (MCPMutationResult, error) {
	if err := validateMCPAdd(ref, server); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	// 与 Hermes 一致，直接以 mcp add 登记；CLI 自行把服务器定义写入自己的 data_dir。
	args := []string{"mcp", "add", ref.Name}
	if server.Transport == domain.MCPTransportHTTP {
		// 显式声明无凭据：默认值同为 none，显式传值可确保不进入交互式取 token 的分支。
		args = append(args, "--url", server.URL, "--auth", "none")
	} else {
		args = append(args, "--command", server.Command)
		if len(server.Args) > 0 {
			args = append(args, "--args")
			args = append(args, server.Args...)
		}
	}
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, args, "")
	success := runErr == nil && bibitMCPAddApplied(result.Stdout, ref.Name)
	return mutationFromCommand(domain.AgentBibit, "add", ref, result, runErr, success)
}

func (adapter *bibitMCPAdapter) Remove(ctx context.Context, ref domain.PowerRef) (MCPMutationResult, error) {
	if err := validateMCPRef(ref); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, []string{"mcp", "remove", ref.Name}, "")
	return mutationFromCommand(domain.AgentBibit, "remove", ref, result, runErr, runErr == nil)
}

var _ MCPAdapter = (*bibitMCPAdapter)(nil)
