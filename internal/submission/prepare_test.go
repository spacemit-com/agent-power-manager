package submission

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"math"
	"path/filepath"
	"reflect"
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

func TestPrepareSkillRejectsPackedExpansionAboveLimit(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	writeTestFile(t, filepath.Join(dir, "highly-compressible.txt"), strings.Repeat("a", 1<<20))

	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Message: "first"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "decompression ratio") {
		t.Fatalf("Prepare() error = %v, want decompression-ratio ErrInvalid", err)
	}
}

func TestValidatePackedExpansionRatioBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		sourceSize  int64
		packedSize  int64
		wantInvalid bool
	}{
		{name: "exactly one hundred", sourceSize: 10_000, packedSize: 100},
		{name: "above one hundred", sourceSize: 10_001, packedSize: 100, wantInvalid: true},
		{name: "large values do not overflow", sourceSize: math.MaxInt64, packedSize: math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePackedExpansionRatio(tt.sourceSize, tt.packedSize)
			if gotInvalid := errors.Is(err, ErrInvalid); gotInvalid != tt.wantInvalid {
				t.Fatalf("validatePackedExpansionRatio(%d, %d) error = %v, invalid=%t want %t", tt.sourceSize, tt.packedSize, err, gotInvalid, tt.wantInvalid)
			}
		})
	}
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
	wantDef := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "@foo/mcp"}}
	if got.Definition == nil || !reflect.DeepEqual(*got.Definition, wantDef) {
		t.Fatalf("Definition = %#v, want %#v", got.Definition, wantDef)
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
	dir := t.TempDir()
	writeValidAgentDir(t, dir)
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareAgentWhitespaceChangesMD5(t *testing.T) {
	dir := t.TempDir()
	raw1 := "schema_version: 1\nagent:\n  id: demo\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n"
	raw2 := raw1 + "\n"
	firstRoot := filepath.Join(dir, "a")
	secondRoot := filepath.Join(dir, "b")
	writeAgentYAMLOnlyDir(t, firstRoot, raw1)
	writeAgentYAMLOnlyDir(t, secondRoot, raw2)
	first, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: firstRoot, Message: "first"})
	if err != nil {
		t.Fatalf("Prepare(a) error = %v", err)
	}
	second, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: secondRoot, Message: "first"})
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
	agentDir := t.TempDir()
	writeValidAgentDir(t, agentDir)

	tests := []struct {
		name      string
		powerType domain.PowerType
		path      string
	}{
		{name: "skill", powerType: domain.PowerTypeSkill, path: skillDir},
		{name: "plugin", powerType: domain.PowerTypePlugin, path: pluginDir},
		{name: "mcp", powerType: domain.PowerTypeMCP, path: mcpPath},
		{name: "agent", powerType: domain.PowerTypeAgent, path: agentDir},
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
			if test.powerType == domain.PowerTypeMCP {
				if got.Definition == nil {
					t.Fatalf("MCP Definition = nil, want non-nil")
				}
			} else if got.Definition != nil {
				t.Fatalf("%s Definition = %#v, want nil", test.powerType, got.Definition)
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
