package submission

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

const validMCPSubmission = `{"name":"demo","version":"v1.0.0","description":"demo mcp","change_summary":"first","definition":{"transport":"stdio","command":"npx","args":["-y","@foo/mcp"]}}`

func TestValidateMCP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, validMCPSubmission)
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Type != domain.PowerTypeMCP || got.Name != "demo" || got.Version != "v1.0.0" || got.Description != "demo mcp" {
		t.Fatalf("Validate() = %#v", got)
	}
	if got.ChangeSummary != "first" {
		t.Fatalf("ChangeSummary = %q", got.ChangeSummary)
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
	writeTestFile(t, path, `{"name":"demo","version":"v1.0.0","description":"d","change_summary":"x","definition":{"transport":"stdio","command":"npx","env":{"TOKEN":"secret"}}}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsMissingDefinition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, `{"name":"demo","version":"v1.0.0","description":"d","change_summary":"x"}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsUnknownOuterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, `{"name":"demo","version":"v1.0.0","description":"d","change_summary":"x","definition":{"transport":"stdio","command":"npx"},"extra":1}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMCPRejectsNonCanonicalVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeTestFile(t, path, `{"name":"demo","version":"1.0.0","description":"d","change_summary":"x","definition":{"transport":"stdio","command":"npx"}}`)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}
