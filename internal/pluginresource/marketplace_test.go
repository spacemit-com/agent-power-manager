package pluginresource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"apm/internal/cloud/local"
	"apm/internal/domain"
)

func TestSharedMarketplaceBuilderBuildsBothDocumentsInOneRoot(t *testing.T) {
	// 捕获错误变更：协调器只生成某个 Agent 的 marketplace，或把两份 source 指向不同 Plugin 叶子。
	root := t.TempDir()
	pluginPath := filepath.Join(root, "plugins", "demo")
	if err := writeSharedTestPluginManifests(pluginPath, `{"name":"demo","version":"1.2.3"}`); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}
	if err := (sharedMarketplaceBuilder{}).Build(ref, root, pluginPath); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	var codex codexMarketplaceDocument
	if err := decodeJSONFile(filepath.Join(root, codexMarketplacePath), &codex); err != nil {
		t.Fatal(err)
	}
	if len(codex.Plugins) != 1 || codex.Plugins[0].Name != "demo" ||
		codex.Plugins[0].Source.Source != "local" || codex.Plugins[0].Source.Path != "./plugins/demo" {
		t.Fatalf("Codex marketplace plugins = %+v, want one logical demo entry", codex.Plugins)
	}

	var claude claudeMarketplaceDocument
	if err := decodeJSONFile(filepath.Join(root, claudeMarketplacePath), &claude); err != nil {
		t.Fatal(err)
	}
	if len(claude.Plugins) != 1 || claude.Plugins[0].Name != "demo" || claude.Plugins[0].Source != "./plugins/demo" {
		t.Fatalf("Claude marketplace plugins = %+v, want one logical demo entry", claude.Plugins)
	}
}

func TestSharedMarketplaceBuilderRejectsInvalidCanonicalIdentity(t *testing.T) {
	// 捕获错误变更：协调器在生成原生 manifest 前未复验 canonical 精确身份。
	for _, test := range []struct {
		name     string
		manifest string
	}{
		{name: "wrong name", manifest: `{"schema_version":1,"name":"other","version":"v1.2.3","description":"Demo","skills":["skills/demo"]}`},
		{name: "wrong version", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.4","description":"Demo","skills":["skills/demo"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			pluginPath := filepath.Join(root, "plugins", "demo")
			if err := writeSharedTestPluginManifests(pluginPath, `{"name":"demo","version":"1.2.3","description":"Demo"}`); err != nil {
				t.Fatal(err)
			}
			if err := writeTestPluginManifest(pluginPath, "apc-plugin.json", test.manifest); err != nil {
				t.Fatal(err)
			}
			ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}
			if err := (sharedMarketplaceBuilder{}).Build(ref, root, pluginPath); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Build() error = %v, want ErrInvalidArtifact", err)
			}
		})
	}
}

func TestSharedMarketplaceNewBuildsCompleteArtifacts(t *testing.T) {
	// 捕获错误变更：公共构造器遗漏共享 builder 或未补全任一 marketplace。
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg-cache"))
	t.Setenv("APM_HOME", filepath.Join(root, "home"))
	download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
		return writeSharedTestPluginManifests(destination, `{"name":"demo","version":"0.1.0"}`)
	})
	service, err := New(download)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ref := testPluginRef()
	artifact, err := service.Ensure(context.Background(), ref)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("Artifact.Validate() error = %v", err)
	}
	for _, marketplacePath := range []string{codexMarketplacePath, claudeMarketplacePath} {
		if _, err := os.Stat(filepath.Join(artifact.Root, marketplacePath)); err != nil {
			t.Fatalf("stat shared marketplace %q: %v", marketplacePath, err)
		}
	}
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		marketplace, err := artifact.MarketplaceFor(agent)
		if err != nil || marketplace.Name != "apm-demo" || marketplace.Source != filepath.Clean(artifact.Root) {
			t.Fatalf("%s marketplace = %+v, %v, want apm-demo/%q", agent, marketplace, err, filepath.Clean(artifact.Root))
		}
	}
}

func TestNewRejectsNilAndTypedNilDownloaders(t *testing.T) {
	// 捕获错误变更：公共构造器延迟解引用 nil 依赖并产生 panic。
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("APM_HOME", filepath.Join(t.TempDir(), "home"))
	if _, err := New(nil); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("New(nil) error = %v, want ErrInvalidArtifact", err)
	}
	var downloader *typedNilDownloader
	if _, err := New(downloader); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("New(typed nil) error = %v, want ErrInvalidArtifact", err)
	}
}

func TestEnsureRejectsInvalidSharedPackagesWithoutPublishingCache(t *testing.T) {
	// 捕获错误变更：信任错误 canonical 身份、跟随 symlink 或接纳 Cloud 自带派生内容。
	tests := []struct {
		name  string
		write func(*testing.T, string) error
	}{
		{
			name: "wrong name",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "apc-plugin.json", `{"schema_version":1,"name":"other","version":"v0.1.0","description":"Demo","skills":["skills/demo"]}`)
			},
		},
		{
			name: "wrong version",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "apc-plugin.json", `{"schema_version":1,"name":"demo","version":"v0.2.0","description":"Demo","skills":["skills/demo"]}`)
			},
		},
		{
			name: "noncanonical canonical version",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "apc-plugin.json", `{"schema_version":1,"name":"demo","version":"0.1.0","description":"Demo","skills":["skills/demo"]}`)
			},
		},
		{
			name: "missing manifest",
			write: func(_ *testing.T, destination string) error {
				return os.Remove(filepath.Join(destination, "apc-plugin.json"))
			},
		},
		{
			name: "second JSON value",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "apc-plugin.json", `{"schema_version":1,"name":"demo","version":"v0.1.0","description":"Demo","skills":["skills/demo"]} {}`)
			},
		},
		{
			name: "symlink manifest",
			write: func(t *testing.T, destination string) error {
				target := filepath.Join(t.TempDir(), "plugin.json")
				if err := os.WriteFile(target, []byte(`{"schema_version":1,"name":"demo","version":"v0.1.0","description":"Demo","skills":["skills/demo"]}`), 0o600); err != nil {
					return err
				}
				manifestPath := filepath.Join(destination, "apc-plugin.json")
				if err := os.Remove(manifestPath); err != nil {
					return err
				}
				return os.Symlink(target, manifestPath)
			},
		},
		{
			name: "raw package includes Codex marketplace",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, codexMarketplacePath, `{}`)
			},
		},
		{
			name: "raw package includes Codex native manifest",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, codexManifestPath, `{}`)
			},
		},
		{
			name: "raw package includes Hermes native manifest",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, hermesManifestPath, "name: demo\n")
			},
		},
		{
			name: "raw package includes Claude marketplace",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, claudeMarketplacePath, `{}`)
			},
		},
		{
			name: "raw package includes arbitrary root payload",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "payload.txt", "payload")
			},
		},
		{
			name: "raw package includes commands capability",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "commands/demo.md", "command")
			},
		},
		{
			name: "raw package includes hooks capability",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "hooks/hooks.json", `{}`)
			},
		},
		{
			name: "raw package includes agents capability",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "agents/demo.yaml", "agent: demo")
			},
		},
		{
			name: "raw package includes MCP capability",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, "MCP/server.json", `{}`)
			},
		},
		{
			name: "raw package includes agents metadata",
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, ".agents/config.json", `{}`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				if err := writeSharedTestPluginManifests(destination, `{"name":"demo","version":"0.1.0"}`); err != nil {
					return err
				}
				return test.write(t, destination)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
			}
			finalRoot := testArtifactRoot(roots.cache, ref)
			if _, err := os.Lstat(finalRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final cache stat error = %v, want not exist", err)
			}
		})
	}
}

func TestEnsureWithLocalCloudRejectsSymlinkedSourceBeforePublishingArtifact(t *testing.T) {
	// 捕获错误变更：Local Cloud 跟随外部 manifest 后，Plugin Resource 将其发布成最终不可变 artifact。
	resourceRoot := filepath.Join(t.TempDir(), "resources")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "xdg-cache"))
	ref := testPluginRef()
	source := filepath.Join(resourceRoot, "plugins", ref.Name, ref.Version)
	manifestPath := filepath.Join(source, "apc-plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	externalManifest := filepath.Join(t.TempDir(), "outside-plugin.json")
	wantExternal := `{"schema_version":1,"name":"demo","version":"v0.1.0","description":"Demo","skills":["skills/demo"]}`
	if err := os.WriteFile(externalManifest, []byte(wantExternal), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalManifest, manifestPath); err != nil {
		t.Fatal(err)
	}
	client, err := local.New(resourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, client, builtInBuilder())

	if _, err := service.Ensure(context.Background(), ref); err == nil {
		t.Fatal("Ensure() error = nil, want non-physical Local Cloud source rejection")
	}
	finalRoot := testArtifactRoot(roots.cache, ref)
	if _, err := os.Lstat(finalRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final artifact stat error = %v, want absent", err)
	}
	contents, err := os.ReadFile(externalManifest)
	if err != nil || string(contents) != wantExternal {
		t.Fatalf("external manifest = %q, %v, want unchanged", contents, err)
	}
}

func TestLookupRequiresBothGeneratedMarketplacesWithoutReplacingImmutableCache(t *testing.T) {
	// 捕获错误变更：cache hit 只复验一份共享 marketplace，或损坏后重新下载修复。
	tests := []struct {
		name     string
		relative string
	}{
		{name: "missing Codex marketplace", relative: codexMarketplacePath},
		{name: "missing Claude marketplace", relative: claudeMarketplacePath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				return writeSharedTestPluginManifests(destination, `{"name":"demo","version":"0.1.0"}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			artifact, err := service.Ensure(context.Background(), ref)
			if err != nil {
				t.Fatalf("initial Ensure() error = %v", err)
			}
			if err := os.Remove(filepath.Join(artifact.Root, test.relative)); err != nil {
				t.Fatal(err)
			}

			if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
			}
			if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Ensure() error = %v, want immutable cache rejection", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("DownloadPlugin calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestLookupRejectsExtraCapabilityNativeAndMarketplaceFilesWithoutReplacingImmutableCache(t *testing.T) {
	// 捕获错误变更：cache hit 把 closed-world 之外的能力目录或派生目录额外条目当作完整 artifact。
	for _, relative := range []string{
		"plugins/demo/commands/demo.md",
		"plugins/demo/.codex-plugin/extra.json",
		"plugins/demo/.claude-plugin/extra.json",
		"plugins/demo/.hermes-plugin/debug.py",
		".agents/plugins/extra.json",
		".claude-plugin/extra.json",
	} {
		t.Run(relative, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				return writeSharedTestPluginManifests(destination, `{"name":"demo","version":"0.1.0"}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			artifact, err := service.Ensure(context.Background(), ref)
			if err != nil {
				t.Fatalf("initial Ensure() error = %v", err)
			}
			if err := writeTestPluginManifest(artifact.Root, relative, "extra\n"); err != nil {
				t.Fatal(err)
			}

			if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
			}
			if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Ensure() error = %v, want immutable cache rejection", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("DownloadPlugin calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestLookupSemanticallyValidatesImmutableMarketplaceAndManifest(t *testing.T) {
	// 捕获错误变更：cache hit 只检查文件存在或复用旧期望值，未复验 manifest 与单条目语义。
	validCodex := `{
  "name":"apm-demo",
  "interface":{"displayName":"demo"},
	  "plugins":[{"name":"demo","source":{"source":"local","path":"./plugins/demo"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Coding"}]
}`
	validClaude := `{
  "name":"apm-demo",
  "owner":{"name":"APM"},
  "metadata":{"description":"Demo","version":"0.1.0"},
	  "plugins":[{"name":"demo","source":"./plugins/demo","description":"Demo"}]
}`
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "missing plugin entry",
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, codexMarketplacePath), `{"name":"apm-demo","interface":{"displayName":"demo"},"plugins":[]}`)
			},
		},
		{
			name: "extra plugin entry",
			mutate: func(t *testing.T, root string) {
				var document map[string]any
				if err := json.Unmarshal([]byte(validCodex), &document); err != nil {
					t.Fatal(err)
				}
				plugins := document["plugins"].([]any)
				document["plugins"] = append(plugins, plugins[0])
				writeJSONForTest(t, filepath.Join(root, codexMarketplacePath), document)
			},
		},
		{
			name: "wrong local source",
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, codexMarketplacePath), `{"name":"apm-demo","interface":{"displayName":"demo"},"plugins":[{"name":"demo","source":{"source":"local","path":"./plugins/other"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Coding"}]}`)
			},
		},
		{
			name: "wrong marketplace name",
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, codexMarketplacePath), strings.Replace(validCodex, "apm-demo", "apm-other", 1))
			},
		},
		{
			name: "wrong native version",
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, claudeMarketplacePath), strings.Replace(validClaude, `"version":"0.1.0"`, `"version":"0.2.0"`, 1))
			},
		},
		{
			name: "wrong Claude source",
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, claudeMarketplacePath), strings.Replace(validClaude, "./plugins/demo", "./plugins/other", 1))
			},
		},
		{
			name: "manifest changed after publish",
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, "plugins", "demo", claudeManifestPath), `{"name":"other","version":"0.1.0","description":"Demo","skills":["./skills/"]}`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				return writeSharedTestPluginManifests(destination, `{"name":"demo","version":"0.1.0"}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			artifact, err := service.Ensure(context.Background(), ref)
			if err != nil {
				t.Fatalf("initial Ensure() error = %v", err)
			}
			test.mutate(t, artifact.Root)

			if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
			}
			if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("DownloadPlugin calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestLookupRejectsCodexKnownFieldTypeCorruptionWithoutReplacingImmutableCache(t *testing.T) {
	// 捕获错误变更：共享 validator 忽略 builder 已知字段的类型损坏，并将不可变 cache 当作有效命中。
	tests := []struct {
		name     string
		manifest string
	}{
		{
			name:     "description changed to object",
			manifest: `{"name":"demo","version":"0.1.0","description":{"text":"Demo"},"skills":"./skills/"}`,
		},
		{
			name:     "skills changed to array",
			manifest: `{"name":"demo","version":"0.1.0","description":"Demo","skills":["./skills/"]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				return writeSharedTestPluginManifests(destination, `{"name":"demo","version":"0.1.0","description":"Demo"}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			artifact, err := service.Ensure(context.Background(), ref)
			if err != nil {
				t.Fatalf("initial Ensure() error = %v", err)
			}
			manifestPath := filepath.Join(artifact.PluginPath, codexManifestPath)
			writeFileForTest(t, manifestPath, test.manifest)

			if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
			}
			if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Ensure() error = %v, want immutable cache rejection", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("DownloadPlugin calls = %d, want 1", calls.Load())
			}
			contents, err := os.ReadFile(manifestPath)
			if err != nil || string(contents) != test.manifest {
				t.Fatalf("immutable manifest = %q, %v, want unchanged corruption", contents, err)
			}
		})
	}
}

func TestEnsureValidatesDownloadedPhysicalTreeBeforeSharedBuilder(t *testing.T) {
	// 捕获错误变更：共享 builder 在物理树校验前跟随 Cloud 包中的 symlink 读取或写入外部目录。
	scenarios := []string{"manifest parent", "marketplace parent", "plugin root"}
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		for _, scenario := range scenarios {
			t.Run(string(agent)+"/"+scenario, func(t *testing.T) {
				roots := newTestRoots(t)
				external := t.TempDir()
				var externalBefore string
				download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
					if err := writeSymlinkScenario(t, agent, scenario, destination, external); err != nil {
						return err
					}
					externalBefore = snapshotTestTree(t, external)
					return nil
				})
				observer := &observingArtifactBuilder{delegate: builtInBuilder()}
				service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, observer)
				ref := testPluginRef()

				if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
					t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
				}
				if observer.buildCalls.Load() != 0 {
					t.Fatalf("builder Build calls = %d, want 0 before physical validation", observer.buildCalls.Load())
				}
				if externalAfter := snapshotTestTree(t, external); externalAfter != externalBefore {
					t.Fatalf("external target changed\nbefore:\n%s\nafter:\n%s", externalBefore, externalAfter)
				}
				finalRoot := testArtifactRoot(roots.cache, ref)
				if _, err := os.Lstat(finalRoot); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("final cache stat error = %v, want not exist", err)
				}
			})
		}
	}
}

type pluginDownloadFunc func(context.Context, domain.PowerRef, string) error

func (download pluginDownloadFunc) DownloadPlugin(ctx context.Context, ref domain.PowerRef, destination string) error {
	return download(ctx, ref, destination)
}

type observingArtifactBuilder struct {
	delegate   artifactBuilder
	buildCalls atomic.Int32
}

func (builder *observingArtifactBuilder) Build(ref domain.PowerRef, root, pluginPath string) error {
	builder.buildCalls.Add(1)
	return builder.delegate.Build(ref, root, pluginPath)
}

func (builder *observingArtifactBuilder) Validate(ref domain.PowerRef, root string) error {
	return builder.delegate.Validate(ref, root)
}

func writeTestPluginManifest(pluginPath, relativePath, content string) error {
	path := filepath.Join(pluginPath, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func writeSharedTestPluginManifests(pluginPath, content string) error {
	var native struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(content), &native); err != nil {
		return err
	}
	if native.Description == "" {
		native.Description = "Demo"
	}
	canonical := map[string]any{
		"schema_version": 1,
		"name":           native.Name,
		"version":        "v" + native.Version,
		"description":    native.Description,
		"skills":         []string{"skills/" + native.Name},
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	if err := writeTestPluginManifest(pluginPath, "apc-plugin.json", string(encoded)); err != nil {
		return err
	}
	skillRoot := filepath.Join(pluginPath, "skills", native.Name)
	for _, marker := range []string{"SKILL.md", "skill-card.md"} {
		if err := writeTestPluginManifest(skillRoot, marker, marker+"\n"); err != nil {
			return err
		}
	}
	return nil
}

func writeFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeJSONForTest(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFileForTest(t, path, string(data))
}

func writeSymlinkScenario(t *testing.T, agent domain.AgentID, scenario, destination, external string) error {
	t.Helper()
	manifestPath := codexManifestPath
	if agent == domain.AgentClaudeCode {
		manifestPath = claudeManifestPath
	}
	manifest := `{"name":"demo","version":"0.1.0"}`
	if err := os.WriteFile(filepath.Join(external, "sentinel.txt"), []byte("outside must remain unchanged"), 0o600); err != nil {
		return err
	}
	switch scenario {
	case "manifest parent":
		if err := os.WriteFile(filepath.Join(external, "plugin.json"), []byte(manifest), 0o600); err != nil {
			return err
		}
		return os.Symlink(external, filepath.Join(destination, filepath.Dir(manifestPath)))
	case "plugin root":
		if err := writeTestPluginManifest(external, manifestPath, manifest); err != nil {
			return err
		}
		if err := os.RemoveAll(destination); err != nil {
			return err
		}
		return os.Symlink(external, destination)
	case "marketplace parent":
		if err := writeTestPluginManifest(destination, manifestPath, manifest); err != nil {
			return err
		}
		artifactRoot := filepath.Dir(filepath.Dir(destination))
		if agent == domain.AgentCodex {
			if err := os.MkdirAll(filepath.Join(external, "plugins"), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(external, "plugins", "marketplace.json"), []byte("outside marketplace"), 0o600); err != nil {
				return err
			}
			return os.Symlink(external, filepath.Join(artifactRoot, ".agents"))
		}
		if err := os.WriteFile(filepath.Join(external, "marketplace.json"), []byte("outside marketplace"), 0o600); err != nil {
			return err
		}
		return os.Symlink(external, filepath.Join(artifactRoot, ".claude-plugin"))
	default:
		return fmt.Errorf("unknown symlink scenario %q", scenario)
	}
}

func snapshotTestTree(t *testing.T, root string) string {
	t.Helper()
	var snapshot strings.Builder
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&snapshot, "%s %s", relative, info.Mode())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, " %q", content)
		}
		snapshot.WriteByte('\n')
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot.String()
}
