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
	request := Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "first"}
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
	writeTestFile(t, path, validMCPSubmission)
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
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
}

func TestPrepareRequiresSkillVersion(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareRequiresChangeSummary(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Prepare() error = %v, want ErrInvalid", err)
	}
}

func TestPrepareRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "no frontmatter")
	_, err := Prepare(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if err == nil {
		t.Fatalf("Prepare() error = nil, want error")
	}
}

func TestPrepareAgentRequiresChangeSummary(t *testing.T) {
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
	first, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: filepath.Join(dir, "a.yaml"), ChangeSummary: "first"})
	if err != nil {
		t.Fatalf("Prepare(a) error = %v", err)
	}
	second, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: filepath.Join(dir, "b.yaml"), ChangeSummary: "first"})
	if err != nil {
		t.Fatalf("Prepare(b) error = %v", err)
	}
	if first.MD5 == second.MD5 {
		t.Fatalf("MD5 identical for whitespace-differing YAML: %q", first.MD5)
	}
	verifyMD5(t, first.Bytes, first.MD5)
	verifyMD5(t, second.Bytes, second.MD5)
}

func verifyMD5(t *testing.T, data []byte, got string) {
	t.Helper()
	sum := md5.Sum(data)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("MD5 = %q, want %q", got, want)
	}
}
