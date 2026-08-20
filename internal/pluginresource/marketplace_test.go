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

	"apm/internal/cloud"
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

func TestSharedMarketplaceBuilderRejectsEitherInvalidNativeManifest(t *testing.T) {
	// 捕获错误变更：协调器只校验一份原生 manifest，或容忍版本/逻辑名称不精确匹配。
	for _, test := range []struct {
		name           string
		codexManifest  string
		claudeManifest string
	}{
		{name: "Codex versioned name", codexManifest: `{"name":"demo-v1.2.3","version":"1.2.3"}`, claudeManifest: `{"name":"demo","version":"1.2.3"}`},
		{name: "Claude wrong version", codexManifest: `{"name":"demo","version":"1.2.3"}`, claudeManifest: `{"name":"demo","version":"1.2.4"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			pluginPath := filepath.Join(root, "plugins", "demo")
			if err := writeTestPluginManifest(pluginPath, codexManifestPath, test.codexManifest); err != nil {
				t.Fatal(err)
			}
			if err := writeTestPluginManifest(pluginPath, claudeManifestPath, test.claudeManifest); err != nil {
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
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes} {
		artifact, err := service.Ensure(context.Background(), agent, ref)
		if err != nil {
			t.Fatalf("Ensure(%s) error = %v", agent, err)
		}
		if err := artifact.Validate(); err != nil {
			t.Fatalf("Artifact.Validate(%s) error = %v", agent, err)
		}
		for _, marketplacePath := range []string{codexMarketplacePath, claudeMarketplacePath} {
			if _, err := os.Stat(filepath.Join(artifact.Root, marketplacePath)); err != nil {
				t.Fatalf("stat shared marketplace %q: %v", marketplacePath, err)
			}
		}
		if agent == domain.AgentHermes {
			continue
		}
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

func TestEnsureRejectsInvalidAgentPackagesWithoutPublishingCache(t *testing.T) {
	// 捕获错误变更：信任 Cloud manifest 身份、跟随 symlink 或接纳 Cloud 自带 marketplace。
	validCodex := func(destination string) error {
		return writeTestPluginManifest(destination, codexManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
	}
	validClaude := func(destination string) error {
		return writeTestPluginManifest(destination, claudeManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
	}
	validHermes := func(destination string) error {
		return writeTestPluginManifest(destination, claudeManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
	}
	tests := []struct {
		name  string
		agent domain.AgentID
		write func(*testing.T, string) error
	}{
		{
			name:  "wrong name",
			agent: domain.AgentCodex,
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, codexManifestPath, `{"name":"other-v0.1.0","version":"0.1.0"}`)
			},
		},
		{
			name:  "wrong version",
			agent: domain.AgentClaudeCode,
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, claudeManifestPath, `{"name":"demo-v0.1.0","version":"0.2.0"}`)
			},
		},
		{
			name:  "noncanonical native version",
			agent: domain.AgentCodex,
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, codexManifestPath, `{"name":"demo-v0.1.0","version":"0.1"}`)
			},
		},
		{
			name:  "missing manifest",
			agent: domain.AgentClaudeCode,
			write: func(_ *testing.T, destination string) error {
				return os.WriteFile(filepath.Join(destination, "plugin.txt"), []byte("payload"), 0o600)
			},
		},
		{
			name:  "second JSON value",
			agent: domain.AgentCodex,
			write: func(_ *testing.T, destination string) error {
				return writeTestPluginManifest(destination, codexManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"} {}`)
			},
		},
		{
			name:  "symlink manifest",
			agent: domain.AgentClaudeCode,
			write: func(t *testing.T, destination string) error {
				target := filepath.Join(t.TempDir(), "plugin.json")
				if err := os.WriteFile(target, []byte(`{"name":"demo-v0.1.0","version":"0.1.0"}`), 0o600); err != nil {
					return err
				}
				manifestPath := filepath.Join(destination, claudeManifestPath)
				if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
					return err
				}
				return os.Symlink(target, manifestPath)
			},
		},
		{
			name:  "Codex package includes Codex marketplace",
			agent: domain.AgentCodex,
			write: func(_ *testing.T, destination string) error {
				if err := validCodex(destination); err != nil {
					return err
				}
				return writeTestPluginManifest(destination, codexMarketplacePath, `{}`)
			},
		},
		{
			name:  "Codex package includes Claude marketplace",
			agent: domain.AgentCodex,
			write: func(_ *testing.T, destination string) error {
				if err := validCodex(destination); err != nil {
					return err
				}
				return writeTestPluginManifest(destination, claudeMarketplacePath, `{}`)
			},
		},
		{
			name:  "Claude package includes Codex marketplace",
			agent: domain.AgentClaudeCode,
			write: func(_ *testing.T, destination string) error {
				if err := validClaude(destination); err != nil {
					return err
				}
				return writeTestPluginManifest(destination, codexMarketplacePath, `{}`)
			},
		},
		{
			name:  "Claude package includes Claude marketplace",
			agent: domain.AgentClaudeCode,
			write: func(_ *testing.T, destination string) error {
				if err := validClaude(destination); err != nil {
					return err
				}
				return writeTestPluginManifest(destination, claudeMarketplacePath, `{}`)
			},
		},
		{
			name:  "Hermes package includes Codex marketplace",
			agent: domain.AgentHermes,
			write: func(_ *testing.T, destination string) error {
				if err := validHermes(destination); err != nil {
					return err
				}
				return writeTestPluginManifest(destination, codexMarketplacePath, `{}`)
			},
		},
		{
			name:  "Hermes package includes Claude marketplace",
			agent: domain.AgentHermes,
			write: func(_ *testing.T, destination string) error {
				if err := validHermes(destination); err != nil {
					return err
				}
				return writeTestPluginManifest(destination, claudeMarketplacePath, `{}`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				return test.write(t, destination)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			if _, err := service.Ensure(context.Background(), test.agent, ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
			}
			finalRoot := testArtifactRoot(roots.cache, test.agent, ref)
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
	manifestPath := filepath.Join(source, codexManifestPath)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	externalManifest := filepath.Join(t.TempDir(), "outside-plugin.json")
	wantExternal := `{"name":"demo-v0.1.0","version":"0.1.0"}`
	if err := os.WriteFile(externalManifest, []byte(wantExternal), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalManifest, manifestPath); err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(resourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, client, builtInBuilder())

	if _, err := service.Ensure(context.Background(), domain.AgentCodex, ref); err == nil {
		t.Fatal("Ensure() error = nil, want non-physical Local Cloud source rejection")
	}
	finalRoot := testArtifactRoot(roots.cache, domain.AgentCodex, ref)
	if _, err := os.Lstat(finalRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final artifact stat error = %v, want absent", err)
	}
	contents, err := os.ReadFile(externalManifest)
	if err != nil || string(contents) != wantExternal {
		t.Fatalf("external manifest = %q, %v, want unchanged", contents, err)
	}
}

func TestLookupRejectsMarketplaceFilesOutsideAgentClosedWorld(t *testing.T) {
	// 捕获错误变更：cache hit 只复验本 Agent 的必需 marketplace，却容忍 Cloud/外部注入的其他 marketplace。
	tests := []struct {
		name     string
		agent    domain.AgentID
		relative string
		nested   bool
	}{
		{name: "Codex foreign Claude marketplace", agent: domain.AgentCodex, relative: claudeMarketplacePath},
		{name: "Claude foreign Codex marketplace", agent: domain.AgentClaudeCode, relative: codexMarketplacePath},
		{name: "Hermes root Codex marketplace", agent: domain.AgentHermes, relative: codexMarketplacePath},
		{name: "Hermes root Claude marketplace", agent: domain.AgentHermes, relative: claudeMarketplacePath},
		{name: "Hermes nested Codex marketplace", agent: domain.AgentHermes, relative: codexMarketplacePath, nested: true},
		{name: "Hermes nested Claude marketplace", agent: domain.AgentHermes, relative: claudeMarketplacePath, nested: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				return writeSharedTestPluginManifests(destination, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			artifact, err := service.Ensure(context.Background(), test.agent, ref)
			if err != nil {
				t.Fatalf("initial Ensure() error = %v", err)
			}
			base := artifact.Root
			if test.nested {
				base = artifact.PluginPath
			}
			if err := writeTestPluginManifest(base, test.relative, `{}`); err != nil {
				t.Fatal(err)
			}

			if _, err := service.Lookup(test.agent, ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
			}
			if _, err := service.Ensure(context.Background(), test.agent, ref); !errors.Is(err, ErrInvalidArtifact) {
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
  "plugins":[{"name":"demo-v0.1.0","source":{"source":"local","path":"./plugins/demo-v0.1.0"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Coding"}]
}`
	validClaude := `{
  "name":"apm-demo",
  "owner":{"name":"APM"},
  "metadata":{"description":"APM local marketplace for demo","version":"0.1.0"},
  "plugins":[{"name":"demo-v0.1.0","source":"./plugins/demo-v0.1.0","description":"APM local marketplace for demo"}]
}`
	tests := []struct {
		name   string
		agent  domain.AgentID
		mutate func(*testing.T, string)
	}{
		{
			name:  "missing plugin entry",
			agent: domain.AgentCodex,
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, codexMarketplacePath), `{"name":"apm-demo","interface":{"displayName":"demo"},"plugins":[]}`)
			},
		},
		{
			name:  "extra plugin entry",
			agent: domain.AgentCodex,
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
			name:  "wrong local source",
			agent: domain.AgentCodex,
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, codexMarketplacePath), `{"name":"apm-demo","interface":{"displayName":"demo"},"plugins":[{"name":"demo-v0.1.0","source":{"source":"local","path":"./plugins/other"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Coding"}]}`)
			},
		},
		{
			name:  "wrong marketplace name",
			agent: domain.AgentCodex,
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, codexMarketplacePath), strings.Replace(validCodex, "apm-demo", "apm-other", 1))
			},
		},
		{
			name:  "wrong native version",
			agent: domain.AgentClaudeCode,
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, claudeMarketplacePath), strings.Replace(validClaude, `"version":"0.1.0"`, `"version":"0.2.0"`, 1))
			},
		},
		{
			name:  "wrong Claude source",
			agent: domain.AgentClaudeCode,
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, claudeMarketplacePath), strings.Replace(validClaude, "./plugins/demo-v0.1.0", "./plugins/other", 1))
			},
		},
		{
			name:  "manifest changed after publish",
			agent: domain.AgentClaudeCode,
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, "plugins", "demo-v0.1.0", claudeManifestPath), `{"name":"other-v0.1.0","version":"0.1.0"}`)
			},
		},
		{
			name:  "Hermes manifest changed after publish",
			agent: domain.AgentHermes,
			mutate: func(t *testing.T, root string) {
				writeFileForTest(t, filepath.Join(root, "plugins", "demo-v0.1.0", claudeManifestPath), `{"name":"other-v0.1.0","version":"0.1.0"}`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				return writeSharedTestPluginManifests(destination, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			artifact, err := service.Ensure(context.Background(), test.agent, ref)
			if err != nil {
				t.Fatalf("initial Ensure() error = %v", err)
			}
			test.mutate(t, artifact.Root)

			if _, err := service.Lookup(test.agent, ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
			}
			if _, err := service.Ensure(context.Background(), test.agent, ref); !errors.Is(err, ErrInvalidArtifact) {
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
			manifest: `{"name":"demo-v0.1.0","version":"0.1.0","description":{"text":"Demo"},"author":{"name":"APM"}}`,
		},
		{
			name:     "author changed to string",
			manifest: `{"name":"demo-v0.1.0","version":"0.1.0","description":"Demo","author":"APM"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				return writeTestPluginManifest(destination, codexManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0","description":"Demo","author":{"name":"APM"}}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilder())
			ref := testPluginRef()
			artifact, err := service.Ensure(context.Background(), domain.AgentCodex, ref)
			if err != nil {
				t.Fatalf("initial Ensure() error = %v", err)
			}
			manifestPath := filepath.Join(artifact.PluginPath, codexManifestPath)
			writeFileForTest(t, manifestPath, test.manifest)

			if _, err := service.Lookup(domain.AgentCodex, ref); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
			}
			if _, err := service.Ensure(context.Background(), domain.AgentCodex, ref); !errors.Is(err, ErrInvalidArtifact) {
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

func TestEnsureValidatesDownloadedPhysicalTreeBeforeAgentBuilder(t *testing.T) {
	// 捕获错误变更：Agent builder 在物理树校验前跟随 Cloud 包中的 symlink 读取或写入外部目录。
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

				if _, err := service.Ensure(context.Background(), agent, ref); !errors.Is(err, ErrInvalidArtifact) {
					t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
				}
				if observer.buildCalls.Load() != 0 {
					t.Fatalf("builder Build calls = %d, want 0 before physical validation", observer.buildCalls.Load())
				}
				if externalAfter := snapshotTestTree(t, external); externalAfter != externalBefore {
					t.Fatalf("external target changed\nbefore:\n%s\nafter:\n%s", externalBefore, externalAfter)
				}
				finalRoot := testArtifactRoot(roots.cache, agent, ref)
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
	if err := writeTestPluginManifest(pluginPath, codexManifestPath, content); err != nil {
		return err
	}
	return writeTestPluginManifest(pluginPath, claudeManifestPath, content)
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
	manifest := `{"name":"demo-v0.1.0","version":"0.1.0"}`
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
