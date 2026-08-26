package transaction

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

// staticTarget 是只读目标探针测试用的固定路径目标。
type staticTarget struct {
	path string
}

func (t staticTarget) TargetPath(domain.PowerRef) (string, error) { return t.path, nil }

func TestInspectFilesystemTarget(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}

	t.Run("missing then present physical path", func(t *testing.T) {
		service := newService(nil, nil)
		target := filepath.Join(t.TempDir(), "skills", "demo")

		got, err := service.InspectFilesystemTarget(ref, staticTarget{path: target})
		if err != nil || got != TargetAbsent {
			t.Fatalf("missing = %q, %v", got, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("managed"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err = service.InspectFilesystemTarget(ref, staticTarget{path: target})
		if err != nil || got != TargetPresent {
			t.Fatalf("present = %q, %v", got, err)
		}
	})

	t.Run("unsafe relative target is rejected", func(t *testing.T) {
		service := newService(nil, nil)
		_, err := service.InspectFilesystemTarget(ref, staticTarget{path: "relative/skills/demo"})
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("relative target error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("injected lstat failure is reported", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "skills", "demo")
		lstatErr := errors.New("lstat failed")
		service := newService(nil, nil)
		service.files.lstat = func(string) (os.FileInfo, error) { return nil, lstatErr }

		_, err := service.InspectFilesystemTarget(ref, staticTarget{path: target})
		if !errors.Is(err, lstatErr) {
			t.Fatalf("lstat failure error = %v, want %v", err, lstatErr)
		}
	})
}
