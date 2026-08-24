package submission

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestValidateAgentReturnsRawFileMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	raw := []byte("schema_version: 1\nagent:\n  id: demo-agent\n  version: v1.2.3\n  description: Demo agent\n  tools: ['*']\n")
	writeTestFile(t, path, string(raw))
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Name != "demo-agent" || got.Version != "v1.2.3" || got.Description != "Demo agent" || got.FileCount != 1 || got.ContentSize != int64(len(raw)) {
		t.Fatalf("Validate() = %+v", got)
	}
}

func TestPrepareAgentPreservesRawYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	raw := []byte("# comment\nschema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path, Message: "first"})
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(raw)
	if !bytes.Equal(got.Bytes, raw) || got.ContentType != "application/yaml" || got.MD5 != hex.EncodeToString(sum[:]) {
		t.Fatalf("Prepare() = type=%q md5=%q bytes=%q", got.ContentType, got.MD5, got.Bytes)
	}
}

func TestAgentSourceBuildReturnsDefensiveCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, validAgentYAML)
	loaded, err := (agentSourceLoader{}).Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	first, err := loaded.build()
	if err != nil {
		t.Fatalf("first build() error = %v", err)
	}
	first[0] = '#'
	second, err := loaded.build()
	if err != nil {
		t.Fatalf("second build() error = %v", err)
	}
	if string(second) != validAgentYAML {
		t.Fatalf("second build() = %q, want original raw YAML", second)
	}
}

func TestValidateAgentRejectsHardLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	writeTestFile(t, target, validAgentYAML)
	link := filepath.Join(dir, "link.yaml")
	if err := os.Link(target, link); err != nil {
		t.Skipf("hard link unsupported: %v", err)
	}

	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: link})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}
