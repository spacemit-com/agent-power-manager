package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

// GetMCP 直接读取精确版本的 MCP 定义，不写入 Cache 或 staging。
func (c *LocalClient) GetMCP(ref domain.PowerRef) (domain.MCPServer, error) {
	if ref.Name == "" || ref.Version == "" {
		return domain.MCPServer{}, fmt.Errorf("MCP name and version are required")
	}
	if ref.Type != domain.PowerTypeMCP {
		return domain.MCPServer{}, ErrNotFound
	}
	markerPath := filepath.Join(c.resourcesRoot, "mcps", ref.Name, ref.Version, "MCP.json")
	marker, err := os.Lstat(markerPath)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !marker.Mode().IsRegular()) {
		return domain.MCPServer{}, ErrNotFound
	}
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("stat MCP marker %q: %w", markerPath, err)
	}
	contents, err := os.ReadFile(markerPath)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("read MCP definition %q: %w", markerPath, err)
	}
	server, err := decodeMCPServer(contents)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("decode MCP definition %q: %w", markerPath, err)
	}
	return server.Clone(), nil
}

func decodeMCPServer(contents []byte) (domain.MCPServer, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var server domain.MCPServer
	if err := decoder.Decode(&server); err != nil {
		return domain.MCPServer{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return domain.MCPServer{}, fmt.Errorf("decode trailing JSON: %w", err)
	}
	if err := server.Validate(); err != nil {
		return domain.MCPServer{}, err
	}
	return server, nil
}
