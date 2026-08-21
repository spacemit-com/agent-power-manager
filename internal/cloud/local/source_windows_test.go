//go:build windows

package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleSnapshotBackendCapturesValidTree(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := capturePhysicalTree(context.Background(), root, source, nil, nil)
	if err != nil {
		t.Fatalf("capturePhysicalTree() error = %v", err)
	}
	if err := snapshot.requireRegularFile("SKILL.md"); err != nil {
		t.Fatal(err)
	}
}
