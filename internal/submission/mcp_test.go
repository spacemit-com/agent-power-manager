package submission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

const validMCPResource = `{"schema_version":2,"name":"demo","name_zh":"演示","name_en":"Demo","version":"v1.0.0","description":"demo mcp","description_zh":"演示 MCP","definition":{"transport":"stdio","command":"npx","args":["-y","@foo/mcp"]}}`

// validMCPResourceV1 是历史安装侧 v1 MCP 资源；提交侧必须拒绝，用于锁定版本门禁。
const validMCPResourceV1 = `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"demo mcp","definition":{"transport":"stdio","command":"npx","args":["-y","@foo/mcp"]}}`

func TestValidateMCP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, validMCPResource)
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Type != domain.PowerTypeMCP || got.Name != "demo" || got.Version != "v1.0.0" || got.Description != "demo mcp" {
		t.Fatalf("Validate() = %#v", got)
	}
	if got.FileCount != 1 {
		t.Fatalf("FileCount = %d, want 1", got.FileCount)
	}
	if got.ContentSize == 0 {
		t.Fatalf("ContentSize = 0, want non-zero")
	}
}

func TestValidateMCPRejectsSecretField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, `{"schema_version":2,"name":"demo","name_zh":"演示","name_en":"Demo","version":"v1.0.0","description":"d","description_zh":"演示","definition":{"transport":"stdio","command":"npx","env":{"TOKEN":"secret"}}}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsMissingDefinition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, `{"schema_version":2,"name":"demo","name_zh":"演示","name_en":"Demo","version":"v1.0.0","description":"d","description_zh":"演示"}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsUnknownOuterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, `{"schema_version":2,"name":"demo","name_zh":"演示","name_en":"Demo","version":"v1.0.0","description":"d","description_zh":"演示","definition":{"transport":"stdio","command":"npx"},"extra":1}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsNonCanonicalVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, `{"schema_version":2,"name":"demo","name_zh":"演示","name_en":"Demo","version":"1.0.0","description":"d","description_zh":"演示","definition":{"transport":"stdio","command":"npx"}}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsLegacySubmissionWrapper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, path, `{"name":"demo","version":"v1.0.0","description":"d","change_summary":"x","definition":{"transport":"stdio","command":"npx"}}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	writeTestFile(t, target, validMCPResource)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: link})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsHardLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	writeTestFile(t, target, validMCPResource)
	link := filepath.Join(dir, "link.json")
	if err := os.Link(target, link); err != nil {
		t.Skipf("hard link unsupported: %v", err)
	}

	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: link})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}
