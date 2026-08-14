package cloud_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalClientDownloadSkillValidatesRefAndNotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.DownloadSkill(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill}); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("empty version error = %v, want non-sentinel parameter error", err)
	}
	for _, ref := range []domain.PowerRef{
		{Name: "demo", Type: domain.PowerTypePlugin, Version: "1.0.0"},
		{Name: "missing", Type: domain.PowerTypeSkill, Version: "1.0.0"},
	} {
		if err = client.DownloadSkill(ref); !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("DownloadSkill(%#v) error = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestLocalClientDownloadSkillMapsInvalidSourceLayoutToNotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(root, "skills", "demo", "1.0.0")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a skill directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	err = client.DownloadSkill(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"})
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadSkill() error = %v, want ErrNotFound", err)
	}
}

func TestLocalClientDownloadSkillMapsSymlinkMarkerToNotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	version := filepath.Join(root, "skills", "demo", "1.0.0")
	if err := os.MkdirAll(version, 0o755); err != nil {
		t.Fatal(err)
	}
	realMarker := filepath.Join(root, "real-SKILL.md")
	if err := os.WriteFile(realMarker, []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realMarker, filepath.Join(version, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}

	err = client.DownloadSkill(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"})
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadSkill() error = %v, want ErrNotFound", err)
	}
}

func TestLocalClientDownloadSkillCopiesAtomicallyAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	source := filepath.Join(root, "skills", "demo", "1.0.0")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "skill-card.md"), []byte("card"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	if err = client.DownloadSkill(ref); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cacheHome, "apm", "skills", "demo", "1.0.0")
	for _, name := range []string{"SKILL.md", "skill-card.md"} {
		if _, err = os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(filepath.Join(cacheHome, "apm", ".tmp")); !os.IsNotExist(err) {
		t.Fatalf("cache temp remains: %v", err)
	}
	if err = os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err = client.DownloadSkill(ref); err != nil {
		t.Fatalf("idempotent DownloadSkill() error = %v", err)
	}
}
