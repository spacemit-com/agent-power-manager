package local_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/utils/filesystem"
)

func TestLocalClientDownloadsOneSharedPluginPackage(t *testing.T) {
	// 捕获错误变更：Local 下载夹带 APM 派生文件，或遗漏 canonical 索引与 Skill markers。
	root, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	entry := catalogEntryFromLocalProvider(t, client, ref)
	destination := filepath.Join(t.TempDir(), "plugin")
	if err := client.DownloadPlugin(context.Background(), entry, destination); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	for _, required := range []string{
		"apc-plugin.json",
		filepath.Join("skills", "demo-plugin", "SKILL.md"),
		filepath.Join("skills", "demo-plugin", "skill-card.md"),
	} {
		if _, err := os.Stat(filepath.Join(destination, required)); err != nil {
			t.Fatalf("stat downloaded shared entry %q: %v", required, err)
		}
	}
	for _, forbidden := range []string{".codex-plugin", ".claude-plugin", ".hermes-plugin", "plugin.yaml", "__init__.py", ".agents"} {
		if _, err := os.Lstat(filepath.Join(destination, forbidden)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("generated path %q stat error = %v, want absent", forbidden, err)
		}
	}
	for _, forbidden := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes} {
		if _, err := os.Lstat(filepath.Join(destination, string(forbidden))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Agent package directory %q stat error = %v, want absent", forbidden, err)
		}
	}
	assertTreeHasNoPluginMarketplace(t, destination)
}

func TestLocalClientDownloadPluginRejectsDestinationInsideSourceWithoutMutation(t *testing.T) {
	// 捕获错误变更：将目标建在源包内部，导致 CopyDir 递归把源树复制进自身。
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	for _, existing := range []bool{false, true} {
		name := "missing nested destination"
		if existing {
			name = "existing empty nested destination"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := writeRawSharedPluginPackage(t, root, ref)
			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			entry := catalogEntryFromLocalProvider(t, client, ref)
			destination := filepath.Join(source, "nested", "plugin")
			if existing {
				destination = filepath.Join(source, "nested")
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, source)

			if err := client.DownloadPlugin(context.Background(), entry, destination); err == nil || errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("DownloadPlugin() error = %v, want source-containment error", err)
			}
			after := snapshotTree(t, source)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("source tree changed: before entries=%d, after entries=%d", len(before), len(after))
			}
		})
	}
}

func TestLocalClientDownloadPluginRejectsNonPhysicalSourceTreeBeforeCopy(t *testing.T) {
	// 捕获错误变更：CopyDir 在完整预检前跟随源树 symlink，或为特殊文件留下半成品目标。
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	for _, scenario := range []string{
		"source root symlink",
		"source ancestor symlink",
		"manifest symlink",
		"payload symlink",
		"nested directory symlink",
		"socket special file",
	} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			if scenario == "socket special file" {
				var err error
				root, err = os.MkdirTemp("", "apmc-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(root) })
			}
			source := writeRawSharedPluginPackage(t, root, ref)
			externalSentinel := filepath.Join(t.TempDir(), "outside-sentinel")
			if err := os.WriteFile(externalSentinel, []byte("outside must remain unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}

			switch scenario {
			case "source root symlink":
				target := t.TempDir()
				if err := os.WriteFile(filepath.Join(target, "outside-sentinel"), []byte("outside must remain unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
				externalSentinel = filepath.Join(target, "outside-sentinel")
				if err := os.RemoveAll(source); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, source); err != nil {
					t.Fatal(err)
				}
			case "source ancestor symlink":
				externalRoot := t.TempDir()
				externalSource := writeRawSharedPluginPackage(t, externalRoot, ref)
				externalSentinel = filepath.Join(externalSource, "skills", ref.Name, "payload.txt")
				logicalRoot := filepath.Join(root, "plugins", ref.Name)
				if err := os.RemoveAll(logicalRoot); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(externalRoot, "plugins", ref.Name), logicalRoot); err != nil {
					t.Fatal(err)
				}
			case "manifest symlink":
				manifest := filepath.Join(source, "apc-plugin.json")
				if err := os.Remove(manifest); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(externalSentinel, manifest); err != nil {
					t.Fatal(err)
				}
			case "payload symlink":
				payload := filepath.Join(source, "skills", ref.Name, "payload.txt")
				if err := os.Remove(payload); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(externalSentinel, payload); err != nil {
					t.Fatal(err)
				}
			case "nested directory symlink":
				target := t.TempDir()
				externalSentinel = filepath.Join(target, "outside-sentinel")
				if err := os.WriteFile(externalSentinel, []byte("outside must remain unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(source, "linked-payload")); err != nil {
					t.Fatal(err)
				}
			case "socket special file":
				listener, err := net.Listen("unix", filepath.Join(source, "payload.sock"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
			}
			before, err := os.ReadFile(externalSentinel)
			if err != nil {
				t.Fatal(err)
			}
			client, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "destination")

			err = client.DownloadPlugin(context.Background(), catalogEntryForLocalTest(ref), destination)
			if err == nil || errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("DownloadPlugin() error = %v, want physical-source rejection", err)
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination stat error = %v, want absent", err)
			}
			after, err := os.ReadFile(externalSentinel)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("external sentinel = %q, %v, want unchanged %q", after, err, before)
			}
		})
	}
}

func TestLocalClientDownloadPluginAllowsDestinationWithSourceNamePrefix(t *testing.T) {
	// 捕获错误变更：使用字符串前缀判定包含关系，误拒绝同级 codex-copy 目标。
	root := t.TempDir()
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	source := writeRawSharedPluginPackage(t, root, ref)
	destination := source + "-copy"
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	entry := catalogEntryFromLocalProvider(t, client, ref)
	if err := client.DownloadPlugin(context.Background(), entry, destination); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "skills", ref.Name, "payload.txt")); err != nil || string(got) != "payload" {
		t.Fatalf("copied payload = %q, %v, want payload", got, err)
	}
}

func TestLocalClientDownloadPluginReturnsExactMissesBeforeTouchingDestination(t *testing.T) {
	// 捕获错误变更：先拼接未校验的 Agent/ref 路径，或把精确 miss 暴露为文件系统错误。
	root, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ref  domain.PowerRef
	}{
		{name: "missing logical name", ref: domain.PowerRef{Name: "missing", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "missing version", ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v9.9.9"}},
		{name: "wrong Power type", ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypeSkill, Version: "v0.1.0"}},
		{name: "escaping logical name", ref: domain.PowerRef{Name: "../demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "escaping version", ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "../v0.1.0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "untouched")
			err := client.DownloadPlugin(context.Background(), catalogEntryForLocalTest(test.ref), destination)
			if !errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("DownloadPlugin() error = %v, want ErrNotFound", err)
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination stat error = %v, want not exist", err)
			}
		})
	}
}

func TestLocalClientDownloadPluginRejectsUnsafeDestinations(t *testing.T) {
	// 捕获错误变更：跟随软链接目标或覆盖调用方已有内容。
	root, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	entry := catalogEntryFromLocalProvider(t, client, ref)

	nonEmpty := t.TempDir()
	sentinel := filepath.Join(nonEmpty, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadPlugin(context.Background(), entry, nonEmpty); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("non-empty destination error = %v, want destination error", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("non-empty sentinel = %q, %v, want preserved", got, err)
	}

	symlinkTarget := t.TempDir()
	symlinkDestination := filepath.Join(t.TempDir(), "plugin")
	if err := os.Symlink(symlinkTarget, symlinkDestination); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadPlugin(context.Background(), entry, symlinkDestination); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("symlink destination error = %v, want destination error", err)
	}
	entries, err := os.ReadDir(symlinkTarget)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink target entries = %v, %v, want empty", entries, err)
	}

	ancestorTarget := t.TempDir()
	ancestorLink := filepath.Join(t.TempDir(), "linked-parent")
	if err := os.Symlink(ancestorTarget, ancestorLink); err != nil {
		t.Fatal(err)
	}
	throughAncestor := filepath.Join(ancestorLink, "plugin")
	if err := client.DownloadPlugin(context.Background(), entry, throughAncestor); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("symlink ancestor destination error = %v, want destination error", err)
	}
	entries, err = os.ReadDir(ancestorTarget)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink ancestor target entries = %v, %v, want empty", entries, err)
	}
}

func TestLocalClientDownloadPluginAllowsCallerControlledRelativeDestination(t *testing.T) {
	// 捕获错误变更：将“调用方控制”额外收窄为只允许绝对路径。
	root, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	working := t.TempDir()
	t.Chdir(working)
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	entry := catalogEntryFromLocalProvider(t, client, ref)
	if err := client.DownloadPlugin(context.Background(), entry, "plugin"); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(working, "plugin", "apc-plugin.json")); err != nil {
		t.Fatalf("stat relative destination manifest: %v", err)
	}
}

func TestLocalClientDownloadPluginDoesNotCreateCloudOwnedCacheOrStaging(t *testing.T) {
	// 捕获错误变更：新下载边界复用旧 DownloadPlugin 并自行写入 cache/staging。
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	root, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	entry := catalogEntryFromLocalProvider(t, client, ref)
	if err := client.DownloadPlugin(context.Background(), entry, filepath.Join(t.TempDir(), "plugin")); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	for _, path := range []string{
		filepath.Join(cacheHome, "apm", "plugins"),
		filepath.Join(cacheHome, "apm", ".tmp", "plugins"),
		filepath.Join(stateHome, ".apm", "staging"),
		filepath.Join(stateHome, ".apm", ".tmp", "staging"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Cloud-owned path %q stat error = %v, want not exist", path, err)
		}
	}
}

func assertTreeHasNoPluginMarketplace(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == filepath.Join(".agents", "plugins", "marketplace.json") ||
			relative == filepath.Join(".claude-plugin", "marketplace.json") {
			t.Errorf("downloaded tree contains marketplace %q", relative)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk downloaded tree: %v", err)
	}
}

type treeSnapshotEntry struct {
	Mode    os.FileMode
	Content string
}

func writeRawSharedPluginPackage(t *testing.T, root string, ref domain.PowerRef) string {
	t.Helper()
	source := filepath.Join(root, "plugins", ref.Name, ref.Version)
	skillRoot := filepath.Join(source, "skills", ref.Name)
	if err := os.MkdirAll(skillRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"schema_version":1,"name":%q,"version":%q,"description":"Demo","skills":[%q]}`, ref.Name, ref.Version, "skills/"+ref.Name)
	if err := os.WriteFile(filepath.Join(source, "apc-plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"SKILL.md", "skill-card.md"} {
		if err := os.WriteFile(filepath.Join(skillRoot, marker), []byte(marker+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "payload.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	return source
}

func snapshotTree(t *testing.T, root string) map[string]treeSnapshotEntry {
	t.Helper()
	snapshot := make(map[string]treeSnapshotEntry)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := treeSnapshotEntry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Content = string(contents)
		}
		snapshot[relative] = item
		return nil
	}); err != nil {
		t.Fatalf("snapshot tree %q: %v", root, err)
	}
	return snapshot
}

func TestBundledPluginResourcesAreCompleteSharedPackagesForEveryVersion(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "resources", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBundledPluginFixtures(root); err != nil {
		t.Fatal(err)
	}
}

func TestBundledPluginResourceGuardRejectsCapabilityOutsideDeclaredSkill(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "resources", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "plugins")
	if err := filesystem.CopyDir(source, root); err != nil {
		t.Fatal(err)
	}
	capability := filepath.Join(root, "demo-plugin", "v0.0.1", "commands", "demo.md")
	if err := os.MkdirAll(filepath.Dir(capability), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(capability, []byte("command\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateBundledPluginFixtures(root); err == nil {
		t.Fatal("fixture validation accepted capability outside declared Skill")
	}
}

func validateBundledPluginFixtures(root string) error {
	plugins, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, plugin := range plugins {
		if !plugin.IsDir() {
			return fmt.Errorf("Plugin fixture root entry %q is not a directory", plugin.Name())
		}
		pluginRoot := filepath.Join(root, plugin.Name())
		versions, err := os.ReadDir(pluginRoot)
		if err != nil {
			return err
		}
		for _, version := range versions {
			if !version.IsDir() {
				return fmt.Errorf("Plugin fixture version %q is not a directory", filepath.Join(plugin.Name(), version.Name()))
			}
			versionRoot := filepath.Join(pluginRoot, version.Name())
			ref := domain.PowerRef{Name: plugin.Name(), Type: domain.PowerTypePlugin, Version: version.Name()}
			if err := pluginartifact.ValidateRawPackage(ref, versionRoot); err != nil {
				return fmt.Errorf("Plugin fixture %s@%s is not canonical: %w", plugin.Name(), version.Name(), err)
			}
			for _, required := range []string{
				"apc-plugin.json",
				filepath.Join("skills", plugin.Name(), "SKILL.md"),
				filepath.Join("skills", plugin.Name(), "skill-card.md"),
			} {
				info, err := os.Stat(filepath.Join(versionRoot, required))
				if err != nil || !info.Mode().IsRegular() {
					return fmt.Errorf("Plugin fixture %s@%s missing regular %q: %w", plugin.Name(), version.Name(), required, err)
				}
			}
			cardPath := filepath.Join(versionRoot, "skills", plugin.Name(), "skill-card.md")
			if err := validateFixtureSkillCard(cardPath); err != nil {
				return fmt.Errorf("Plugin fixture %s@%s has incomplete skill-card.md: %w", plugin.Name(), version.Name(), err)
			}
		}
	}
	return nil
}

func validateFixtureSkillCard(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sections := make(map[string][]string)
	current := ""
	for _, line := range strings.Split(string(contents), "\n") {
		if strings.HasPrefix(line, "## ") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if current != "" && strings.TrimSpace(line) != "" {
			sections[current] = append(sections[current], strings.TrimSpace(line))
		}
	}
	for _, required := range []string{"Capability", "Use Cases", "Inputs and Outputs", "Dependencies", "Limitations", "Maintainer"} {
		if len(sections[required]) == 0 {
			return fmt.Errorf("section %q is missing or empty", required)
		}
	}
	for _, field := range []string{"Owner", "Support"} {
		prefix := "- " + field + ":"
		value := ""
		for _, line := range sections["Maintainer"] {
			if strings.HasPrefix(line, prefix) {
				value = strings.TrimSpace(strings.TrimPrefix(line, prefix))
				break
			}
		}
		if value == "" {
			return fmt.Errorf("Maintainer %s is missing or empty", field)
		}
	}
	return nil
}

func writeRawPluginPackageDir(t *testing.T, root, logicalName, version string) {
	t.Helper()
	dir := filepath.Join(root, "plugins", logicalName, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
