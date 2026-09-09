package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
)

const bibitMCPExecutable = "bianbu-agent"

// bibitHTTPTransport 是 bianbu-agent 0.1.17 的 mcp.json 中 HTTP 传输的字段取值。
// APM 的 domain.MCPTransportHTTP 对应 CLI 的 "streamable-http"。
const bibitHTTPTransport = "streamable-http"

// bibitHTTPMCPDocument 描述 HTTP MCP 目录包内的 mcp.json，仅包含 CLI 校验允许的字段。
type bibitHTTPMCPDocument struct {
	DisplayName string  `json:"display_name"`
	Transport   string  `json:"transport"`
	URL         string  `json:"url"`
	Timeout     float64 `json:"timeout_seconds"`
}

// bibitMCPAdapter 通过 bianbu-agent CLI 管理 Bibit 的用户级 MCP 配置。
//
// 契约对齐板端真实 bianbu-agent 0.1.17（依据仓库 bianbu-agent-cli-guide.md 与实测）：
// mcp get <name> 无 --json，短名归一化为公开 ID mcp.<name>，存在返回 JSON 对象、不存在返回 null；
// mcp add 仅支持 stdio（-- 命令形式）；HTTP 传输通过目录包 mcp install <目录> 安装。
type bibitMCPAdapter struct {
	runner    agentCLIRunner
	mkdtemp   func(string, string) (string, error)
	mkdirAll  func(string, os.FileMode) error
	writeFile func(string, []byte, os.FileMode) error
	removeAll func(string) error
}

func newBibitMCPAdapter(runner agentCLIRunner) *bibitMCPAdapter {
	return &bibitMCPAdapter{
		runner:    runner,
		mkdtemp:   os.MkdirTemp,
		mkdirAll:  os.MkdirAll,
		writeFile: os.WriteFile,
		removeAll: os.RemoveAll,
	}
}

// bibitPublicMCPID 返回短名对应的公开 ID；APM 规范名恒为小写 kebab、不含点，
// 因此 CLI 内部归一化后恒为 "mcp.<name>"。
func bibitPublicMCPID(name string) string {
	return "mcp." + name
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

	decoder := json.NewDecoder(bytes.NewBufferString(result.Stdout))
	var document struct {
		ID string `json:"id"`
	}
	if err := decoder.Decode(&document); err != nil {
		return inspectUnknown(domain.AgentBibit, name, result, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return inspectUnknown(domain.AgentBibit, name, result, err)
	}
	if document.ID != bibitPublicMCPID(name) {
		return inspectUnknown(domain.AgentBibit, name, result, fmt.Errorf("returned id %q", document.ID))
	}
	return MCPPresent, nil
}

func (adapter *bibitMCPAdapter) Add(ctx context.Context, ref domain.PowerRef, server domain.MCPServer) (MCPMutationResult, error) {
	if err := validateMCPAdd(ref, server); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	if server.Transport == domain.MCPTransportHTTP {
		return adapter.addHTTP(ctx, ref, server)
	}
	args := []string{"mcp", "add", ref.Name, "--", server.Command}
	args = append(args, server.Args...)
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, args, "")
	return mutationFromCommand(domain.AgentBibit, "add", ref, result, runErr, runErr == nil)
}

// addHTTP 把 HTTP MCP 定义写为临时目录包并以 mcp install 安装；
// 目录名必须是带分隔符的完整 ID mcp.<name>（CLI 对单段名拒绝安装）。
func (adapter *bibitMCPAdapter) addHTTP(ctx context.Context, ref domain.PowerRef, server domain.MCPServer) (MCPMutationResult, error) {
	packageID := bibitPublicMCPID(ref.Name)
	temporary, err := adapter.mkdtemp("", "apm-bibit-mcp-")
	if err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, fmt.Errorf("bibit MCP add %q: create temporary package directory: %w", ref.Name, err)
	}
	defer func() {
		_ = adapter.removeAll(temporary)
	}()
	packageDir := filepath.Join(temporary, packageID)
	if err := adapter.mkdirAll(packageDir, 0o700); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, fmt.Errorf("bibit MCP add %q: create package directory %q: %w", ref.Name, packageDir, err)
	}
	contents, err := json.Marshal(bibitHTTPMCPDocument{
		DisplayName: ref.Name,
		Transport:   bibitHTTPTransport,
		URL:         server.URL,
		Timeout:     30.0,
	})
	if err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, fmt.Errorf("bibit MCP add %q: encode mcp.json: %w", ref.Name, err)
	}
	if err := adapter.writeFile(filepath.Join(packageDir, "mcp.json"), contents, 0o600); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, fmt.Errorf("bibit MCP add %q: write mcp.json: %w", ref.Name, err)
	}
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, []string{"mcp", "install", packageDir}, "")
	return mutationFromCommand(domain.AgentBibit, "add", ref, result, runErr, runErr == nil)
}

func (adapter *bibitMCPAdapter) Remove(ctx context.Context, ref domain.PowerRef) (MCPMutationResult, error) {
	if err := validateMCPRef(ref); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, []string{"mcp", "remove", ref.Name}, "")
	return mutationFromCommand(domain.AgentBibit, "remove", ref, result, runErr, runErr == nil)
}

var _ MCPAdapter = (*bibitMCPAdapter)(nil)
