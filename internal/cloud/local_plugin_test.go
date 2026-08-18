package cloud_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalClientDownloadPluginCopiesExactAgentPackage(t *testing.T) {
	// 捕获错误变更：忽略 Agent 维度或把 Cloud marketplace 一起复制到原始包。
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	tests := []struct {
		agent        domain.AgentID
		manifestPath string
		sentinel     string
	}{
		{agent: domain.AgentCodex, manifestPath: filepath.Join(".codex-plugin", "plugin.json"), sentinel: "codex\n"},
		{agent: domain.AgentClaudeCode, manifestPath: filepath.Join(".claude-plugin", "plugin.json"), sentinel: "claude-code\n"},
		{agent: domain.AgentHermes, manifestPath: filepath.Join(".claude-plugin", "plugin.json"), sentinel: "hermes-agent\n"},
	}
	for _, test := range tests {
		t.Run(string(test.agent), func(t *testing.T) {
			destination := t.TempDir()
			if err := client.DownloadPlugin(context.Background(), test.agent, ref, destination); err != nil {
				t.Fatalf("DownloadPlugin() error = %v", err)
			}
			manifestData, err := os.ReadFile(filepath.Join(destination, test.manifestPath))
			if err != nil {
				t.Fatalf("read Agent manifest: %v", err)
			}
			var manifest struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if err := json.Unmarshal(manifestData, &manifest); err != nil {
				t.Fatalf("decode Agent manifest: %v", err)
			}
			if manifest.Name != "demo-plugin-v0.1.0" || manifest.Version != "0.1.0" {
				t.Fatalf("Agent manifest identity = %#v, want demo-plugin-v0.1.0@0.1.0", manifest)
			}
			if _, err := os.Stat(filepath.Join(destination, "skills", "demo-plugin", "SKILL.md")); err != nil {
				t.Fatalf("stat downloaded skill: %v", err)
			}
			sentinel, err := os.ReadFile(filepath.Join(destination, "agent.txt"))
			if err != nil {
				t.Fatalf("read Agent sentinel: %v", err)
			}
			if string(sentinel) != test.sentinel {
				t.Fatalf("Agent sentinel = %q, want %q", sentinel, test.sentinel)
			}
			assertTreeHasNoPluginMarketplace(t, destination)
		})
	}
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
			source := writeRawCodexPackage(t, root, ref)
			destination := filepath.Join(source, "nested", "plugin")
			if existing {
				destination = filepath.Join(source, "nested")
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, source)
			client, err := cloud.NewLocalClient(root)
			if err != nil {
				t.Fatal(err)
			}

			if err := client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, destination); err == nil || errors.Is(err, cloud.ErrNotFound) {
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
			source := writeRawCodexPackage(t, root, ref)
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
				externalSource := writeRawCodexPackage(t, externalRoot, ref)
				externalSentinel = filepath.Join(externalSource, "payload.txt")
				logicalRoot := filepath.Join(root, "plugins", ref.Name)
				if err := os.RemoveAll(logicalRoot); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(externalRoot, "plugins", ref.Name), logicalRoot); err != nil {
					t.Fatal(err)
				}
			case "manifest symlink":
				manifest := filepath.Join(source, ".codex-plugin", "plugin.json")
				if err := os.Remove(manifest); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(externalSentinel, manifest); err != nil {
					t.Fatal(err)
				}
			case "payload symlink":
				payload := filepath.Join(source, "payload.txt")
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
			client, err := cloud.NewLocalClient(root)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "destination")

			err = client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, destination)
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
	source := writeRawCodexPackage(t, root, ref)
	destination := source + "-copy"
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, destination); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "payload.txt")); err != nil || string(got) != "payload" {
		t.Fatalf("copied payload = %q, %v, want payload", got, err)
	}
}

func TestLocalClientDownloadPluginReturnsExactMissesBeforeTouchingDestination(t *testing.T) {
	// 捕获错误变更：先拼接未校验的 Agent/ref 路径，或把精确 miss 暴露为文件系统错误。
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		agent domain.AgentID
		ref   domain.PowerRef
	}{
		{name: "unsupported Agent", agent: "other", ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "missing logical name", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "missing", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "missing version", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v9.9.9"}},
		{name: "wrong Power type", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypeSkill, Version: "v0.1.0"}},
		{name: "escaping logical name", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "../demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "escaping version", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "../v0.1.0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "untouched")
			err := client.DownloadPlugin(context.Background(), test.agent, test.ref, destination)
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
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}

	nonEmpty := t.TempDir()
	sentinel := filepath.Join(nonEmpty, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, nonEmpty); err == nil || errors.Is(err, cloud.ErrNotFound) {
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
	if err := client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, symlinkDestination); err == nil || errors.Is(err, cloud.ErrNotFound) {
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
	if err := client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, throughAncestor); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("symlink ancestor destination error = %v, want destination error", err)
	}
	entries, err = os.ReadDir(ancestorTarget)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink ancestor target entries = %v, %v, want empty", entries, err)
	}
}

func TestLocalClientDownloadPluginAllowsCallerControlledRelativeDestination(t *testing.T) {
	// 捕获错误变更：将“调用方控制”额外收窄为只允许绝对路径。
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	working := t.TempDir()
	t.Chdir(working)
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, "plugin"); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(working, "plugin", ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("stat relative destination manifest: %v", err)
	}
}

func TestLocalClientDownloadPluginDoesNotCreateCloudOwnedCacheOrStaging(t *testing.T) {
	// 捕获错误变更：新下载边界复用旧 DownloadPlugin 并自行写入 cache/staging。
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := client.DownloadPlugin(context.Background(), domain.AgentCodex, ref, filepath.Join(t.TempDir(), "plugin")); err != nil {
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

func writeRawCodexPackage(t *testing.T, root string, ref domain.PowerRef) string {
	t.Helper()
	source := filepath.Join(root, "plugins", ref.Name, ref.Version, string(domain.AgentCodex))
	if err := os.MkdirAll(filepath.Join(source, ".codex-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"demo-plugin-v0.1.0","version":"0.1.0"}`
	if err := os.WriteFile(filepath.Join(source, ".codex-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "payload.txt"), []byte("payload"), 0o600); err != nil {
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

func TestLocalClientListsPluginsFromAgentPackages(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	writeRawPluginPackageDir(t, root, "demo-plugin", "v0.1.0", domain.AgentCodex)
	writeRawPluginPackageDir(t, root, "demo-plugin", "v0.1.0", domain.AgentClaudeCode)
	writeRawPluginPackageDir(t, root, "demo-plugin", "v0.0.1", domain.AgentHermes)
	writeRawPluginPackageDir(t, root, "ignored", "v9.9.9", "unsupported")

	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"},
		{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListIgnoresVersionsWithoutSupportedAgentPackage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, "plugins", "empty", "v1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRawPluginPackageDir(t, root, "unsupported", "v1.0.0", "other")
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil || len(got) != 0 {
		t.Fatalf("List() = %#v, %v; want no Plugin refs", got, err)
	}
}

func TestLocalClientPluginMissingRootReturnsEmptyList(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil || len(got) != 0 {
		t.Fatalf("List() = %#v, %v", got, err)
	}
}

func TestBundledPluginResourcesContainNoMarketplaceOrLegacyLayout(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("resources", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		filepath.Join(root, ".agents", "plugins", "marketplace.json"),
		filepath.Join(root, ".claude-plugin", "marketplace.json"),
		filepath.Join(root, "plugins"),
	} {
		if _, err := os.Lstat(forbidden); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy Cloud Plugin resource %q stat error = %v, want not exist", forbidden, err)
		}
	}
}

func writeRawPluginPackageDir(t *testing.T, root, logicalName, version string, agent domain.AgentID) {
	t.Helper()
	dir := filepath.Join(root, "plugins", logicalName, version, string(agent))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
