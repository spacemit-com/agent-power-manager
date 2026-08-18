package pluginresource

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"apm/internal/domain"
)

func TestNewSelectsAgentBuildersAndReturnsCompleteArtifacts(t *testing.T) {
	// 捕获错误变更：公共构造器遗漏内建 Agent builder 或未补全 marketplace 身份。
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg-cache"))
	t.Setenv("APM_HOME", filepath.Join(root, "home"))
	download := pluginDownloadFunc(func(_ context.Context, agent domain.AgentID, _ domain.PowerRef, destination string) error {
		switch agent {
		case domain.AgentCodex:
			return writeTestPluginManifest(destination, codexManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
		case domain.AgentClaudeCode:
			return writeTestPluginManifest(destination, claudeManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
		case domain.AgentHermes:
			return os.WriteFile(filepath.Join(destination, "plugin.txt"), []byte("hermes"), 0o600)
		default:
			return errors.New("unexpected agent")
		}
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
		if agent == domain.AgentHermes {
			if artifact.MarketplaceName != "" || artifact.MarketplaceSource != "" {
				t.Fatalf("Hermes marketplace = %q/%q, want empty", artifact.MarketplaceName, artifact.MarketplaceSource)
			}
			continue
		}
		if artifact.MarketplaceName != "apm-demo" || artifact.MarketplaceSource != filepath.Clean(artifact.Root) {
			t.Fatalf("%s marketplace = %q/%q, want apm-demo/%q", agent, artifact.MarketplaceName, artifact.MarketplaceSource, filepath.Clean(artifact.Root))
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			download := pluginDownloadFunc(func(_ context.Context, _ domain.AgentID, _ domain.PowerRef, destination string) error {
				return test.write(t, destination)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilders())
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			roots := newTestRoots(t)
			var calls atomic.Int32
			download := pluginDownloadFunc(func(_ context.Context, agent domain.AgentID, _ domain.PowerRef, destination string) error {
				calls.Add(1)
				if agent == domain.AgentCodex {
					return writeTestPluginManifest(destination, codexManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
				}
				return writeTestPluginManifest(destination, claudeManifestPath, `{"name":"demo-v0.1.0","version":"0.1.0"}`)
			})
			service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, builtInBuilders())
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

type pluginDownloadFunc func(context.Context, domain.AgentID, domain.PowerRef, string) error

func (download pluginDownloadFunc) DownloadPlugin(ctx context.Context, agent domain.AgentID, ref domain.PowerRef, destination string) error {
	return download(ctx, agent, ref, destination)
}

func writeTestPluginManifest(pluginPath, relativePath, content string) error {
	path := filepath.Join(pluginPath, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
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
