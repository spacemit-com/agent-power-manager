package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
	"apm/internal/paths"
)

func TestJSONRepositoryWriteRenameFailurePreservesTargetAndCleansTemporaryFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	target, err := paths.ManifestPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "marker")
	if err := os.WriteFile(marker, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = (&JSONRepository{}).write("codex", domain.PowerGroups{
		domain.PowerTypeSkill:  {{Name: "power", Type: domain.PowerTypeSkill, Version: "1"}},
		domain.PowerTypePlugin: {},
		domain.PowerTypeMCP:    {},
	})
	if err == nil {
		t.Fatal("write() error = nil, want rename failure")
	}
	for _, want := range []string{"replace manifest", "codex", target} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("write() error = %q, want context %q", err, want)
		}
	}
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		t.Fatalf("write() error = %v, want wrapped *os.LinkError", err)
	}
	contents, readErr := os.ReadFile(marker)
	if readErr != nil || string(contents) != "preserved" {
		t.Fatalf("target marker = %q, %v; want preserved", contents, readErr)
	}
	entries, readErr := os.ReadDir(filepath.Dir(target))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		t.Fatalf("manifest directory entries = %#v, want only target directory", entries)
	}
}
