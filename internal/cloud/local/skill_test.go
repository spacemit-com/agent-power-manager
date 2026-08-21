package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/domain"
)

func TestLocalClientDownloadSkillValidatesRefAndNotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "download")
	if err = client.DownloadSkill(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill}, destination); !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("empty version error = %v, want ErrNotFound", err)
	}
	for _, ref := range []domain.PowerRef{
		{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		{Name: "missing", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
	} {
		if err = client.DownloadSkill(context.Background(), ref, destination); !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("DownloadSkill(%#v) error = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestLocalClientDownloadSkillMapsInvalidSourceLayoutToUnsafeContent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(root, "skills", "demo", "v1.0.0")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a skill directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	err = client.DownloadSkill(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, filepath.Join(t.TempDir(), "download"))
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadSkill() error = %v, want ErrUnsafeContent", err)
	}
}

func TestLocalClientDownloadSkillMapsSymlinkMarkerToUnsafeContent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	version := filepath.Join(root, "skills", "demo", "v1.0.0")
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
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}

	err = client.DownloadSkill(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, filepath.Join(t.TempDir(), "download"))
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadSkill() error = %v, want ErrUnsafeContent", err)
	}
}

func TestLocalDownloadSkillWritesDestinationWithoutGlobalCache(t *testing.T) {
	root := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	source := filepath.Join(root, "skills", "demo", "v1.0.0")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "skill-card.md"), []byte("card"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	destination := filepath.Join(t.TempDir(), "download")
	if err = client.DownloadSkill(context.Background(), ref, destination); err != nil {
		t.Fatal(err)
	}
	target := destination
	for _, name := range []string{"SKILL.md", "skill-card.md"} {
		if _, err = os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Lstat(filepath.Join(cacheHome, "apm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Local provider touched global cache: %v", err)
	}
	if err = os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err = client.DownloadSkill(context.Background(), ref, filepath.Join(t.TempDir(), "second")); !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("second DownloadSkill() error = %v, want ErrNotFound", err)
	}
}

func TestLocalDownloadSkillRejectsNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", "demo", "v1.0.0")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "download")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "old"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadSkill(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, destination); err == nil {
		t.Fatal("DownloadSkill() error = nil, want non-empty destination error")
	}
	if got, err := os.ReadFile(filepath.Join(destination, "old")); err != nil || string(got) != "old" {
		t.Fatalf("destination changed = %q, %v", got, err)
	}
}
