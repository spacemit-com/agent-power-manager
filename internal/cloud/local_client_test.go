package cloud_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
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
	want := []domain.PowerRef{{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.0.1"}}
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

func TestLocalClientListSkipsSymlinkSkillMarker(t *testing.T) {
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
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List() = %#v, want symlink marker skipped", got)
	}
}

func TestLocalClientListPreservesSkillMarkerFilesystemError(t *testing.T) {
	if root := os.Getenv("APM_TEST_UNREADABLE_MARKER_ROOT"); root != "" {
		assertListMarkerFilesystemError(t, root)
		return
	}

	root, err := os.MkdirTemp("", "apm-cloud-marker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(filepath.Join(root, "skills", "demo", "1.0.0"), 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Error(err)
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	version := filepath.Join(root, "skills", "demo", "1.0.0")
	if err := os.MkdirAll(version, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(version, 0); err != nil {
		t.Fatal(err)
	}

	if os.Geteuid() != 0 {
		assertListMarkerFilesystemError(t, root)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestLocalClientListPreservesSkillMarkerFilesystemError$")
	cmd.Env = append(os.Environ(), "APM_TEST_UNREADABLE_MARKER_ROOT="+root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unprivileged marker-error check failed: %v\n%s", err, output)
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

func TestLocalClientGetMapsSymlinkSkillMarkerToNotFound(t *testing.T) {
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

func assertListMarkerFilesystemError(t *testing.T, root string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.List()
	marker := filepath.Join(root, "skills", "demo", "1.0.0", "SKILL.md")
	if err == nil {
		t.Fatal("List() error = nil, want marker filesystem error")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("List() error = %v, want non-ENOENT marker error", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("List() error = %v, want permission error", err)
	}
	if !strings.Contains(err.Error(), marker) {
		t.Fatalf("List() error = %q, want marker path %q", err, marker)
	}
}
