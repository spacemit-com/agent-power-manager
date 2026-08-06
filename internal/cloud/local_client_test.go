package cloud_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestNewLocalClientRejectsRelativeResourceRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := cloud.NewLocalClient("resources"); err == nil {
		t.Fatal("NewLocalClient() error = nil, want relative-path error")
	}
}

func TestLocalClientListsCommittedSkillFixture(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "0.0.1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListSortsAndIgnoresInvalidSkillDirectories(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	writeSkill := func(name, version string, valid bool) {
		dir := filepath.Join(root, "skills", name, version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if valid {
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeSkill("zeta", "2.0.0", true)
	writeSkill("alpha", "1.1.0", true)
	writeSkill("alpha", "1.0.0", true)
	writeSkill("ignored", "1.0.0", false)

	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "alpha", Type: domain.PowerTypeSkill, Version: "1.0.0"},
		{Name: "alpha", Type: domain.PowerTypeSkill, Version: "1.1.0"},
		{Name: "zeta", Type: domain.PowerTypeSkill, Version: "2.0.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListRequiresReadableResourceRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "missing")
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.List(); err == nil {
		t.Fatal("List() error = nil, want missing resource-root error")
	}
}

func TestLocalClientListReturnsEmptyWithoutSkillsDirectory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List() = %#v, want empty", got)
	}
}

func TestLocalClientGetValidatesRefAndNotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Get(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill}); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("empty version error = %v, want non-sentinel parameter error", err)
	}
	for _, ref := range []domain.PowerRef{
		{Name: "demo", Type: domain.PowerTypePlugin, Version: "1.0.0"},
		{Name: "missing", Type: domain.PowerTypeSkill, Version: "1.0.0"},
	} {
		if err = client.Get(ref); !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("Get(%#v) error = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestLocalClientGetMapsInvalidSourceLayoutToNotFound(t *testing.T) {
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
	err = client.Get(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"})
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestLocalClientGetCopiesAtomicallyAndIsIdempotent(t *testing.T) {
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
	if err = client.Get(ref); err != nil {
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
	if err = client.Get(ref); err != nil {
		t.Fatalf("idempotent Get() error = %v", err)
	}
}
