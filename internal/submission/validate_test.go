package submission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestValidateSourceTreeRejectsBinary(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "payload.bin"), string([]byte{0x7f, 'E', 'L', 'F', 'x', 'y', 'z'}))
	_, err := validateSourceTree(dir)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateSourceTree() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSourceTreeRejectsNestedArchive(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "payload.gz"), string([]byte{0x1f, 0x8b, 0x08, 0x00, 0x00}))
	_, err := validateSourceTree(dir)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateSourceTree() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSourceTreeRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "real.txt"), "hello")
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err := validateSourceTree(dir)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateSourceTree() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSourceTreeRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSingleFileSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = validateSourceTree(dir)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateSourceTree() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSourceTreeRejectsDeepPath(t *testing.T) {
	dir := t.TempDir()
	nested := dir
	for i := 0; i < 17; i++ {
		nested = filepath.Join(nested, "d")
	}
	writeTestFile(t, filepath.Join(nested, "file.txt"), "x")
	_, err := validateSourceTree(dir)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateSourceTree() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSourceTreeRejectsLongPath(t *testing.T) {
	dir := t.TempDir()
	longDir := strings.Repeat("a", 200)
	longFile := strings.Repeat("b", 100) + ".txt"
	writeTestFile(t, filepath.Join(dir, longDir, longFile), "x")
	_, err := validateSourceTree(dir)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("validateSourceTree() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSourceTreeAcceptsTextTree(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "README.md"), "hello world\n")
	writeTestFile(t, filepath.Join(dir, "scripts", "run.sh"), "#!/bin/sh\necho hi\n")
	stats, err := validateSourceTree(dir)
	if err != nil {
		t.Fatalf("validateSourceTree() error = %v", err)
	}
	if stats.fileCount != 2 {
		t.Fatalf("fileCount = %d, want 2", stats.fileCount)
	}
	if stats.totalSize == 0 {
		t.Fatalf("totalSize = 0, want non-zero")
	}
}

const validAgentYAML = "schema_version: 1\nagent:\n  id: demo\n  version: v1.0.0\n  description: demo\n"

func TestValidateAgentRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, "")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.yaml")
	writeTestFile(t, realPath, validAgentYAML)
	linkPath := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: linkPath})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsBinaryArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, string([]byte{0x1f, 0x8b, 0x08, 0x00, 0x00}))
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSingleFileSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentRejectsVersionFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path, Version: "v1.0.0"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateAgentHonorsCancelledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeTestFile(t, path, validAgentYAML)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Validate(ctx, Request{Type: domain.PowerTypeAgent, Path: path})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Validate() error = %v, want context.Canceled", err)
	}
}
