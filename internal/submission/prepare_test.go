package submission

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestPrepareSkillProducesDeterministicGzip(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	request := Request{Type: domain.PowerTypeSkill, Path: dir, Message: "first"}
	first, err := Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	second, err := Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if first.ContentType != "application/gzip" {
		t.Fatalf("ContentType = %q", first.ContentType)
	}
	if len(first.Bytes) == 0 {
		t.Fatalf("Bytes empty")
	}
	if string(first.Bytes) != string(second.Bytes) {
		t.Fatalf("Prepare() not deterministic")
	}
	if first.MD5 != second.MD5 {
		t.Fatalf("MD5 differs")
	}
	if first.Name != "demo" || first.Version != "v1.0.0" {
		t.Fatalf("identity = %#v", first)
	}
	verifyMD5(t, first.Bytes, first.MD5)
}

func TestPrepareMCPProducesCanonicalJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, validMCPResource)
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path, Message: "first"})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got.ContentType != "application/json" {
		t.Fatalf("ContentType = %q", got.ContentType)
	}
	if len(got.Bytes) == 0 {
		t.Fatalf("Bytes empty")
	}
	if !strings.HasPrefix(string(got.Bytes), "{") {
		t.Fatalf("MCP bytes not JSON object: %q", got.Bytes)
	}
	verifyMD5(t, got.Bytes, got.MD5)
	const want = `{"definition":{"args":["-y","@foo/mcp"],"command":"npx","transport":"stdio"},"description":"demo mcp","name":"demo","schema_version":1,"version":"v1.0.0"}`
	if string(got.Bytes) != want {
		t.Fatalf("Bytes = %s, want full canonical resource %s", got.Bytes, want)
	}
}

func TestPrepareRequiresMessage(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Message: " \t "})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "no frontmatter")
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Message: "x"})
	if err == nil {
		t.Fatalf("Prepare() error = nil, want error")
	}
}

func TestPrepareAgentRequiresMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, validAgentYAML)
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareAgentWhitespaceChangesMD5(t *testing.T) {
	dir := t.TempDir()
	raw1 := []byte("schema_version: 1\nagent:\n  id: demo\n  version: v1.0.0\n  description: demo\n")
	raw2 := []byte("schema_version: 1\nagent:\n  id: demo\n  version: v1.0.0\n  description: demo\n\n")
	writeTestFile(t, filepath.Join(dir, "a.yaml"), string(raw1))
	writeTestFile(t, filepath.Join(dir, "b.yaml"), string(raw2))
	first, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: filepath.Join(dir, "a.yaml"), Message: "first"})
	if err != nil {
		t.Fatalf("Prepare(a) error = %v", err)
	}
	second, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: filepath.Join(dir, "b.yaml"), Message: "first"})
	if err != nil {
		t.Fatalf("Prepare(b) error = %v", err)
	}
	if first.MD5 == second.MD5 {
		t.Fatalf("MD5 identical for whitespace-differing YAML: %q", first.MD5)
	}
	verifyMD5(t, first.Bytes, first.MD5)
	verifyMD5(t, second.Bytes, second.MD5)
}

func TestPrepareAllTypesUseRequestMessageOnly(t *testing.T) {
	skillDir := t.TempDir()
	writeValidSkill(t, skillDir)
	pluginDir := t.TempDir()
	writeValidPlugin(t, pluginDir)
	mcpPath := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, mcpPath, validMCPResource)
	agentPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, agentPath, validAgentYAML)

	tests := []struct {
		name      string
		powerType domain.PowerType
		path      string
	}{
		{name: "skill", powerType: domain.PowerTypeSkill, path: skillDir},
		{name: "plugin", powerType: domain.PowerTypePlugin, path: pluginDir},
		{name: "mcp", powerType: domain.PowerTypeMCP, path: mcpPath},
		{name: "agent", powerType: domain.PowerTypeAgent, path: agentPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Prepare(context.Background(), Request{Type: test.powerType, Path: test.path, Message: "  first release  "})
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			if got.Message != "first release" {
				t.Fatalf("Message = %q", got.Message)
			}
		})
	}
}

func verifyMD5(t *testing.T, data []byte, got string) {
	t.Helper()
	sum := md5.Sum(data)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("MD5 = %q, want %q", got, want)
	}
}
