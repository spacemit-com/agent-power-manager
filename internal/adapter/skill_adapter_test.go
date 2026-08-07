package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
	"apm/internal/paths"
	"apm/internal/utils/filesystem"
)

func TestSkillAdapterInstallPublishesSkillAndCleansStaging(t *testing.T) {
	config, root, staging, ref := skillAdapterFixture(t, true)
	adapter := newSkillAdapter(config)

	if err := adapter.Install(ref, staging); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	target := filepath.Join(root, "skills", ref.Name)
	assertFile(t, filepath.Join(target, "SKILL.md"), "---\nname: demo\ndescription: demo skill\n---\n# Demo\n", 0o640)
	assertFile(t, filepath.Join(target, ".hidden"), "hidden\n", 0o600)
	assertFile(t, filepath.Join(target, "scripts", "run.sh"), "#!/bin/sh\necho demo\n", 0o751)
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("published target %q is a symlink", target)
	}
	readonly, err := os.Stat(filepath.Join(target, "readonly"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readonly.Mode().Perm(); got != 0o555 {
		t.Fatalf("readonly directory mode = %o, want 555", got)
	}
	assertMissing(t, staging)
	temporary, err := filepath.Glob(filepath.Join(root, "skills", ".apm-demo-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("unpublished temporary directories remain: %v", temporary)
	}
}

func TestSkillAdapterInstallReturnsNotInstalledWithoutCreatingAgentRoot(t *testing.T) {
	config, root, staging, ref := skillAdapterFixture(t, false)
	adapter := newSkillAdapter(config)

	err := adapter.Install(ref, staging)
	if !errors.Is(err, ErrAgentNotInstalled) {
		t.Fatalf("Install() error = %v, want ErrAgentNotInstalled", err)
	}
	assertMissing(t, root)
	assertMissing(t, staging)
}

func TestSkillAdapterInstallCreatesMissingSkillsDirectory(t *testing.T) {
	config, root, staging, ref := skillAdapterFixture(t, true)
	adapter := newSkillAdapter(config)

	if err := adapter.Install(ref, staging); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("skills path is not a directory: %q", filepath.Join(root, "skills"))
	}
	assertFile(t, filepath.Join(root, "skills", ref.Name, "SKILL.md"), "---\nname: demo\ndescription: demo skill\n---\n# Demo\n", 0o640)
}

func TestSkillAdapterInstallCleansValidStagingForInvalidRef(t *testing.T) {
	for _, ref := range []domain.PowerRef{
		{Name: "demo", Type: domain.PowerTypePlugin, Version: "1.0.0"},
		{Name: "demo", Type: domain.PowerTypeSkill},
	} {
		t.Run(string(ref.Type)+"-"+ref.Name+"-"+ref.Version, func(t *testing.T) {
			config, _, staging, _ := skillAdapterFixture(t, true)
			adapter := newSkillAdapter(config)

			err := adapter.Install(ref, staging)
			if !errors.Is(err, ErrInvalidSkill) {
				t.Fatalf("Install() error = %v, want ErrInvalidSkill", err)
			}
			assertMissing(t, staging)
		})
	}
}

func TestSkillAdapterInstallRejectsUnsafeStagingWithoutCleaningIt(t *testing.T) {
	config, _, staging, ref := skillAdapterFixture(t, true)
	volumeRoot := filepath.VolumeName(staging) + string(filepath.Separator)
	outside := filepath.Join(t.TempDir(), "other-staging")
	writeSkillTree(t, skillSource(outside, ref), ref.Name)

	for _, stagingPath := range []string{"", volumeRoot, outside} {
		t.Run(stagingPath, func(t *testing.T) {
			adapter := newSkillAdapter(config)
			cleaned := false
			adapter.removeAll = func(string) error {
				cleaned = true
				return nil
			}

			err := adapter.Install(ref, stagingPath)
			if !errors.Is(err, ErrInvalidSkill) {
				t.Fatalf("Install() error = %v, want ErrInvalidSkill", err)
			}
			if cleaned {
				t.Fatalf("Install() attempted to clean unsafe staging path %q", stagingPath)
			}
		})
	}
	assertFile(t, filepath.Join(skillSource(outside, ref), "SKILL.md"), "---\nname: demo\ndescription: demo skill\n---\n# Demo\n", 0o640)
	assertFile(t, filepath.Join(skillSource(staging, ref), "SKILL.md"), "---\nname: demo\ndescription: demo skill\n---\n# Demo\n", 0o640)
}

func TestSkillAdapterInstallRejectsEmptyStagingWhenWorkingDirectoryMatchesExpectedPath(t *testing.T) {
	config, _, staging, ref := skillAdapterFixture(t, true)
	source := skillSource(staging, ref)
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWorkingDirectory); err != nil {
			t.Fatal(err)
		}
	})
	adapter := newSkillAdapter(config)
	cleaned := false
	adapter.removeAll = func(string) error {
		cleaned = true
		return nil
	}

	err = adapter.Install(ref, "")
	if !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("Install() error = %v, want ErrInvalidSkill", err)
	}
	if cleaned {
		t.Fatal("Install() attempted to clean empty staging path")
	}
	assertFile(t, filepath.Join(source, "SKILL.md"), "---\nname: demo\ndescription: demo skill\n---\n# Demo\n", 0o640)
}

func TestSkillAdapterInstallCleansStagingForEscapingRef(t *testing.T) {
	config, _, staging, _ := skillAdapterFixture(t, true)
	ref := domain.PowerRef{Name: "../../outside", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	adapter := newSkillAdapter(config)

	err := adapter.Install(ref, staging)
	if !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("Install() error = %v, want ErrInvalidSkill", err)
	}
	assertMissing(t, staging)
}

func TestSkillAdapterConflictPreservesExistingTargetAndSkipsAdaptation(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			config, root, staging, ref := skillAdapterFixture(t, true)
			target := filepath.Join(root, "skills", ref.Name)
			if err := os.Mkdir(filepath.Join(root, "skills"), 0o755); err != nil {
				t.Fatal(err)
			}
			var sentinel string
			switch kind {
			case "directory":
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				sentinel = filepath.Join(target, "sentinel")
			case "file":
				sentinel = target
			case "symlink":
				sentinel = filepath.Join(t.TempDir(), "sentinel")
				if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sentinel, target); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "symlink" {
				if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			adapted := false
			config.adapt = func(domain.PowerRef, string) error {
				adapted = true
				return nil
			}
			adapter := newSkillAdapter(config)

			err := adapter.Install(ref, staging)
			if !errors.Is(err, ErrTargetConflict) {
				t.Fatalf("Install() error = %v, want ErrTargetConflict", err)
			}
			if adapted {
				t.Fatal("Install() called adapt hook after detecting target conflict")
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
				t.Fatalf("sentinel content = %q, %v; want keep", got, err)
			}
			if kind == "symlink" {
				info, err := os.Lstat(target)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("target %q is no longer a symlink", target)
				}
			}
			assertMissing(t, staging)
		})
	}
}

func TestSkillAdapterInstallRejectsTargetCreatedAfterCopy(t *testing.T) {
	config, root, staging, ref := skillAdapterFixture(t, true)
	adapter := newSkillAdapter(config)
	target := filepath.Join(root, "skills", ref.Name)
	adapter.copyDir = func(from, to string) error {
		if err := filesystem.CopyDir(from, to); err != nil {
			return err
		}
		return os.WriteFile(target, []byte("created during copy"), 0o600)
	}

	err := adapter.Install(ref, staging)
	if !errors.Is(err, ErrTargetConflict) {
		t.Fatalf("Install() error = %v, want ErrTargetConflict", err)
	}
	assertFile(t, target, "created during copy", 0o600)
	assertMissing(t, staging)
	temporary, err := filepath.Glob(filepath.Join(root, "skills", ".apm-demo-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("unpublished temporary directories remain: %v", temporary)
	}
}

func TestSkillAdapterInstallRejectsMalformedFrontmatterAndCleansStaging(t *testing.T) {
	config, root, staging, ref := skillAdapterFixture(t, true)
	source := skillSource(staging, ref)
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# missing frontmatter\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	adapter := newSkillAdapter(config)

	err := adapter.Install(ref, staging)
	if !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("Install() error = %v, want ErrInvalidSkill", err)
	}
	assertMissing(t, filepath.Join(root, "skills", ref.Name))
	assertMissing(t, staging)
}

func skillAdapterFixture(t *testing.T, makeRoot bool) (agentConfig, string, string, domain.PowerRef) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	config := configForAgent(t, domain.AgentCodex)
	root := filepath.Join(home, "agent-root")
	t.Setenv(config.homeEnv, root)
	if makeRoot {
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	source := skillSource(staging, ref)
	writeSkillTree(t, source, ref.Name)
	return config, root, staging, ref
}

func skillSource(staging string, ref domain.PowerRef) string {
	return filepath.Join(staging, "skills", ref.Name)
}

func writeSkillTree(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "readonly"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct {
		path, contents string
		mode           os.FileMode
	}{
		{"SKILL.md", "---\nname: " + name + "\ndescription: demo skill\n---\n# Demo\n", 0o640},
		{".hidden", "hidden\n", 0o600},
		{filepath.Join("scripts", "run.sh"), "#!/bin/sh\necho demo\n", 0o751},
	} {
		if err := os.WriteFile(filepath.Join(root, file.path), []byte(file.contents), file.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, file.path), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "readonly"), 0o555); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("file %q = %q, want %q", path, got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("file %q mode = %o, want %o", path, got, mode)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %q remains: %v", path, err)
	}
}
