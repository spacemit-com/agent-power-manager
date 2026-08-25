package local_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/domain"
)

func TestNewRejectsRelativeResourceRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := local.New("resources"); err == nil {
		t.Fatal("New() error = nil, want relative-path error")
	}
}

func TestLocalClientImplementsCatalogSource(t *testing.T) {
	client, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var source cloud.CatalogSource = client
	if source == nil {
		t.Fatal("Local CatalogSource is nil")
	}
}

func TestLocalClientListsCommittedSkillFixture(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
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
			if err := os.WriteFile(filepath.Join(dir, "skill-card.md"), []byte("card"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeSkill("zeta", "v2.0.0", true)
	writeSkill("alpha", "v1.1.0", true)
	writeSkill("alpha", "v1.0.0", true)
	writeSkill("ignored", "v1.0.0", false)

	client, err := local.New(root)
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

func TestLocalClientRejectsNonCanonicalPowerNames(t *testing.T) {
	for _, name := range []string{"Bad_Name", "a--b", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeCloudMarker(t, filepath.Join(root, "skills", name, "v1.0.0", "SKILL.md"), "skill")
			writeCloudMarker(t, filepath.Join(root, "skills", name, "v1.0.0", "skill-card.md"), "card")
			writeCloudMarker(t, filepath.Join(root, "mcps", name, "v1.0.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
			writeCloudMarker(t, filepath.Join(root, "agents", name, "v1.0.0", "agent.yaml"), "agent: demo\n")

			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			refs, err := client.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(refs) != 0 {
				t.Fatalf("List() = %#v, want invalid names excluded", refs)
			}

			operations := []struct {
				name string
				ref  domain.PowerRef
				run  func() error
			}{
				{name: "Resolve", ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: name}, run: func() error {
					_, resolveErr := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: name})
					return resolveErr
				}},
				{name: "DownloadSkill", ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: name, Version: "v1.0.0"}, run: func() error {
					return client.DownloadSkill(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: name, Version: "v1.0.0"}, filepath.Join(t.TempDir(), "skill"))
				}},
				{name: "GetMCP", ref: domain.PowerRef{Type: domain.PowerTypeMCP, Name: name, Version: "v1.0.0"}, run: func() error {
					_, getErr := client.GetMCP(context.Background(), domain.PowerRef{Type: domain.PowerTypeMCP, Name: name, Version: "v1.0.0"})
					return getErr
				}},
				{name: "DownloadAgent", ref: domain.PowerRef{Type: domain.PowerTypeAgent, Name: name, Version: "v1.0.0"}, run: func() error {
					return client.DownloadAgent(context.Background(), domain.PowerRef{Type: domain.PowerTypeAgent, Name: name, Version: "v1.0.0"}, filepath.Join(t.TempDir(), "agent.yaml"))
				}},
			}
			for _, operation := range operations {
				if err := operation.run(); !errors.Is(err, cloud.ErrNotFound) {
					t.Errorf("%s(%+v) error = %v, want ErrNotFound", operation.name, operation.ref, err)
				}
			}
		})
	}
}

func TestLocalClientListExcludesSkillWithoutCompleteMarkers(t *testing.T) {
	root := t.TempDir()
	writeCloudMarker(t, filepath.Join(root, "skills", "demo", "v1.0.0", "SKILL.md"), "skill")
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("List() = %#v, want incomplete Skill excluded", refs)
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
		if err := os.WriteFile(filepath.Join(filepath.Dir(marker), "skill-card.md"), []byte("card"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	client, err := local.New(root)
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

	client, err := local.New(root)
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
	configureUnprivilegedProcess(t, cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unprivileged marker-error check failed: %v\n%s", err, output)
	}
}

func TestLocalClientListRequiresReadableResourceRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "missing")
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want missing resource-root error")
	}
}

func TestLocalClientListReturnsEmptyWithoutSkillsDirectory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := local.New(t.TempDir())
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
	client, err := local.New(root)
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
