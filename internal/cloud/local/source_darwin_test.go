//go:build darwin

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cloud"
)

func TestDarwinSnapshotFailsClosedWithoutPinnedReadableHandle(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := capturePhysicalTree(context.Background(), root, source, nil, nil, nil)
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("capturePhysicalTree() error = %v, want ErrUnsafeContent", err)
	}
}
