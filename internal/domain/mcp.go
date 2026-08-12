package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// MCPTransport 表示 MCP 服务的连接方式。
type MCPTransport string

const (
	// MCPTransportStdio 表示由本地命令启动的标准输入输出连接。
	MCPTransportStdio MCPTransport = "stdio"
	// MCPTransportHTTP 表示远程 HTTP MCP 端点。
	MCPTransportHTTP MCPTransport = "http"
)

// ErrInvalidMCPServer 表示 MCP 启动定义不满足互斥字段约束。
var ErrInvalidMCPServer = errors.New("invalid MCP server")

// MCPServer 描述由 Cloud 提供的 MCP 启动方式；stdio 与 http 字段必须互斥。
type MCPServer struct {
	Transport MCPTransport `json:"transport"`
	Command   string       `json:"command,omitempty"`
	Args      []string     `json:"args,omitempty"`
	URL       string       `json:"url,omitempty"`
}

// Validate 校验 MCP 启动定义的判别联合，不清洗或改写原始值。
func (server MCPServer) Validate() error {
	switch server.Transport {
	case MCPTransportStdio:
		if strings.TrimSpace(server.Command) == "" || server.URL != "" {
			return fmt.Errorf("stdio requires command and forbids URL: %w", ErrInvalidMCPServer)
		}
	case MCPTransportHTTP:
		if server.Command != "" || len(server.Args) != 0 {
			return fmt.Errorf("http forbids command and args: %w", ErrInvalidMCPServer)
		}
		parsed, err := url.Parse(server.URL)
		if err != nil || parsed.IsAbs() == false || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("http URL %q: %w", server.URL, ErrInvalidMCPServer)
		}
	default:
		return fmt.Errorf("transport %q: %w", server.Transport, ErrInvalidMCPServer)
	}
	return nil
}

// Clone 返回与原定义不共享参数切片的副本。
func (server MCPServer) Clone() MCPServer {
	server.Args = append([]string(nil), server.Args...)
	return server
}
