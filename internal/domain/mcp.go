package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"apm/internal/utils/jsonstrict"
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

// MCPResource 表示 MCP 资源文件的完整 v1 内容；身份字段必须与引用完全一致。
type MCPResource struct {
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	Description   string    `json:"description"`
	Definition    MCPServer `json:"definition"`
}

type mcpResourceWire struct {
	SchemaVersion int             `json:"schema_version"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Description   string          `json:"description"`
	Definition    json.RawMessage `json:"definition"`
	// 测试环境资源文件额外返回的字段，容忍但不校验。
	Type       json.RawMessage `json:"type"`
	Message    json.RawMessage `json:"message"`
	ContentMD5 json.RawMessage `json:"content_md5"`
}

type presentJSONValue[T any] struct {
	value   T
	present bool
}

func (value *presentJSONValue[T]) UnmarshalJSON(data []byte) error {
	value.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("present MCP field must not be null: %w", ErrInvalidMCPServer)
	}
	return json.Unmarshal(data, &value.value)
}

type mcpServerWire struct {
	Transport MCPTransport               `json:"transport"`
	Command   presentJSONValue[string]   `json:"command"`
	Args      presentJSONValue[[]string] `json:"args"`
	URL       presentJSONValue[string]   `json:"url"`
}

// DecodeMCPServerJSON 严格解码 Cloud MCP 定义，并按字段是否出现校验 stdio/http 判别联合。
// 即使禁用字段的值为 null、空字符串或空数组，只要字段出现就拒绝。
func DecodeMCPServerJSON(contents []byte) (MCPServer, error) {
	var wire mcpServerWire
	if err := jsonstrict.Decode(contents, &wire, true); err != nil {
		return MCPServer{}, err
	}

	server := MCPServer{
		Transport: wire.Transport,
		Command:   wire.Command.value,
		Args:      wire.Args.value,
		URL:       wire.URL.value,
	}
	switch server.Transport {
	case MCPTransportStdio:
		if !wire.Command.present || wire.URL.present {
			return MCPServer{}, fmt.Errorf("stdio requires command presence and forbids URL presence: %w", ErrInvalidMCPServer)
		}
	case MCPTransportHTTP:
		if !wire.URL.present || wire.Command.present || wire.Args.present {
			return MCPServer{}, fmt.Errorf("http requires URL presence and forbids command/args presence: %w", ErrInvalidMCPServer)
		}
	}
	if err := server.Validate(); err != nil {
		return MCPServer{}, err
	}
	return server, nil
}

// DecodeMCPResourceJSON 严格解码闭合的 MCP v1 资源，并递归校验其启动定义。
func DecodeMCPResourceJSON(contents []byte) (MCPResource, error) {
	var wire mcpResourceWire
	if err := jsonstrict.Decode(contents, &wire, true); err != nil {
		return MCPResource{}, err
	}
	if len(wire.Definition) == 0 || bytes.Equal(bytes.TrimSpace(wire.Definition), []byte("null")) {
		return MCPResource{}, fmt.Errorf("MCP resource definition is required")
	}
	definition, err := DecodeMCPServerJSON(wire.Definition)
	if err != nil {
		return MCPResource{}, fmt.Errorf("decode MCP resource definition: %w", err)
	}
	resource := MCPResource{
		SchemaVersion: wire.SchemaVersion,
		Name:          wire.Name,
		Version:       wire.Version,
		Description:   wire.Description,
		Definition:    definition,
	}
	if err := resource.Validate(); err != nil {
		return MCPResource{}, err
	}
	return resource, nil
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
		if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Scheme != "https" || parsed.User != nil {
			return fmt.Errorf("http URL %q: %w", server.URL, ErrInvalidMCPServer)
		}
	default:
		return fmt.Errorf("transport %q: %w", server.Transport, ErrInvalidMCPServer)
	}
	return nil
}

// Validate 校验 MCP v1 资源的身份、描述与启动定义，不补全或改写文件字段。
func (resource MCPResource) Validate() error {
	if resource.SchemaVersion != 1 {
		return fmt.Errorf("MCP resource schema version %d is unsupported", resource.SchemaVersion)
	}
	if !IsCanonicalPowerName(resource.Name) {
		return fmt.Errorf("MCP resource name %q is not canonical", resource.Name)
	}
	if _, err := CanonicalPowerVersion(resource.Version); err != nil {
		return fmt.Errorf("MCP resource version: %w", err)
	}
	if strings.TrimSpace(resource.Description) == "" {
		return errors.New("MCP resource description must not be blank")
	}
	if err := resource.Definition.Validate(); err != nil {
		return fmt.Errorf("MCP resource definition: %w", err)
	}
	return nil
}

// ValidatePowerRef 校验 MCP 资源身份与调用方引用完全匹配，避免目录或调用参数覆盖资源声明。
func (resource MCPResource) ValidatePowerRef(ref PowerRef) error {
	if ref != (PowerRef{Type: PowerTypeMCP, Name: resource.Name, Version: resource.Version}) {
		return fmt.Errorf("MCP resource identity does not match %s/%s@%s", ref.Type, ref.Name, ref.Version)
	}
	return nil
}

// Clone 返回与原定义不共享参数切片的副本。
func (server MCPServer) Clone() MCPServer {
	server.Args = append([]string(nil), server.Args...)
	return server
}
