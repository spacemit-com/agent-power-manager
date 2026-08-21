package cloud_test

import (
	"context"
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
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"},
		{Name: "company-tools", Type: domain.PowerTypeMCP, Version: "v0.0.1"},
		{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"},
		{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.1.0"},
		{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"},
		{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
		{Name: "brief-status", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "checklist-first", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "code-comment-cn", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "commit-message-helper", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "commit-message-helper", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
		{Name: "error-context", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
		{Name: "meeting-notes", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "plain-language", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "test-case-namer", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
		{Name: "test-case-namer", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
		{Name: "worklog-entry", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
	for _, ref := range want {
		if ref.Type != domain.PowerTypeSkill {
			continue
		}
		for _, filename := range []string{"SKILL.md", "skill-card.md"} {
			path := filepath.Join(root, "skills", ref.Name, ref.Version, filename)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %q: %v", path, err)
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("%q is not a regular file", path)
			}
		}
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
	writeSkill("zeta", "v2.0.0", true)
	writeSkill("alpha", "v1.1.0", true)
	writeSkill("alpha", "v1.0.0", true)
	writeSkill("ignored", "v1.0.0", false)

	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "alpha", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		{Name: "alpha", Type: domain.PowerTypeSkill, Version: "v1.1.0"},
		{Name: "zeta", Type: domain.PowerTypeSkill, Version: "v2.0.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListExcludesMarkerBackedInvalidVersion(t *testing.T) {
	// 捕获错误变更：带有效 marker 的非规范版本进入排序，触发 SemVer 解析 panic 或泄露为可安装引用。
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, version := range []string{"v1.0.0", "not-semver"} {
		marker := filepath.Join(root, "skills", "demo", version, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte("skill"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListSkipsSymlinkSkillMarker(t *testing.T) {
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

	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List(context.Background())
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
		if err := os.Chmod(filepath.Join(root, "skills", "demo", "v1.0.0"), 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Error(err)
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	version := filepath.Join(root, "skills", "demo", "v1.0.0")
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
	if _, err = client.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want missing resource-root error")
	}
}

func TestLocalClientListReturnsEmptyWithoutSkillsDirectory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List() = %#v, want empty", got)
	}
}

func assertListMarkerFilesystemError(t *testing.T, root string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.List(context.Background())
	marker := filepath.Join(root, "skills", "demo", "v1.0.0", "SKILL.md")
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
