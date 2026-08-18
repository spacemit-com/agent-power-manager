package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/paths"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
	"apm/internal/version"
)

func newIntegrationPluginResources(t *testing.T, source pluginresource.Downloader) pluginresource.Manager {
	t.Helper()
	manager, err := pluginresource.New(source)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

const fakeHermesPluginScript = `#!/bin/sh
case "$0" in "$FAKE_PLUGIN_BIN"/*) ;; *) exit 97 ;; esac
printf '%s|%s\n' "$0" "$*" >> "$FAKE_PLUGIN_CALLS"
case "$1:$2" in
  plugins:list)
    printf '{"plugins":['
    separator=
    for state in "$FAKE_PLUGIN_STATE"/*; do
      [ -f "$state" ] || continue
      name=${state##*/}
      printf '%s{"name":"%s","status":"enabled"}' "$separator" "$name"
      separator=,
    done
    printf ']}\n' ;;
  plugins:enable) : > "$FAKE_PLUGIN_STATE/$3" ;;
  plugins:disable) /bin/rm -f "$FAKE_PLUGIN_STATE/$3" ;;
  *) exit 98 ;;
esac
`

const fakeMarketplacePluginScript = `#!/bin/sh
agent=${0##*/}
case "$0" in "$FAKE_PLUGIN_BIN"/*) ;; *) exit 97 ;; esac
printf '%s|%s\n' "$0" "$*" >> "$FAKE_PLUGIN_CALLS"
market="$FAKE_PLUGIN_STATE/marketplace-$agent"
plugin="$FAKE_PLUGIN_STATE/plugin-$agent"
marketplace_name=${FAKE_PLUGIN_MARKETPLACE_NAME:-apm-demo-plugin}
case "$agent:$1:$2:$3" in
  codex:plugin:marketplace:list)
    if [ -f "$market" ]; then
      printf '{"marketplaces":[{"name":"%s","marketplaceSource":{"sourceType":"local","source":"%s"}}]}\n' "$marketplace_name" "$(/bin/cat "$market")"
    else printf '{"marketplaces":[]}\n'; fi ;;
  codex:plugin:marketplace:add) printf '%s' "$4" > "$market" ;;
  codex:plugin:marketplace:remove) /bin/rm -f "$market" ;;
  codex:plugin:marketplace:upgrade) : ;;
  codex:plugin:list:--json)
    if [ -f "$plugin" ]; then id=$(/bin/cat "$plugin"); name=${id%@*}; printf '{"installed":[{"name":"%s","pluginId":"%s","enabled":false}]}\n' "$name" "$id"
    else printf '{"installed":[]}\n'; fi ;;
  codex:plugin:add:*) printf '%s' "$3" > "$plugin" ;;
  codex:plugin:remove:*) /bin/rm -f "$plugin" ;;
  claude:plugin:marketplace:list)
    if [ -f "$market" ]; then printf '[{"name":"%s","source":"directory","url":"%s"}]\n' "$marketplace_name" "$(/bin/cat "$market")"
    else printf '[]\n'; fi ;;
  claude:plugin:marketplace:add) printf '%s' "$4" > "$market" ;;
  claude:plugin:marketplace:remove) /bin/rm -f "$market" ;;
  claude:plugin:marketplace:update) : ;;
  claude:plugin:list:--json)
    if [ -f "$plugin" ]; then id=$(/bin/cat "$plugin"); printf '[{"id":"%s","scope":"user","enabled":false}]\n' "$id"
    else printf '[]\n'; fi ;;
  claude:plugin:install:*) printf '%s' "$3" > "$plugin" ;;
  claude:plugin:uninstall:*) /bin/rm -f "$plugin" ;;
  *) echo "unsupported fake invocation: $agent $*" >&2; exit 98 ;;
esac
`

func TestPluginIntegrationHermesVersionLifecycleIsIsolated(t *testing.T) {
	root := t.TempDir()
	fakeBin := filepath.Join(root, "fake-bin")
	state := filepath.Join(root, "state")
	for _, path := range []string{fakeBin, state} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "hermes"), []byte(fakeHermesPluginScript), 0o700); err != nil {
		t.Fatal(err)
	}
	hermesHome := filepath.Join(root, "agents", "hermes")
	if err := os.MkdirAll(hermesHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
	t.Setenv("FAKE_PLUGIN_STATE", state)
	t.Setenv("FAKE_PLUGIN_CALLS", filepath.Join(root, "calls.log"))
	t.Setenv("APM_HOME", filepath.Join(root, "apm-home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HERMES_HOME", hermesHome)
	t.Setenv("CODEX_HOME", filepath.Join(root, "absent-codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "absent-claude"))
	resources, _ := filepath.Abs(filepath.Join("..", "cloud", "resources"))
	cloudClient, err := cloud.NewLocalClient(resources)
	if err != nil {
		t.Fatal(err)
	}
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		t.Fatal(err)
	}
	locker, _ := lock.NewDefault()
	powerCache, _ := cache.NewFilesystem()
	pluginResources := newIntegrationPluginResources(t, cloudClient)
	installer, err := NewInstall(locker, transactions, cloudClient, powerCache, pluginResources, registry, repository, version.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	uninstaller, err := NewUninstall(locker, transactions, cloudClient, pluginResources, registry, repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		version string
		outcome cli.InstallOutcome
	}{
		{"v0.0.1", cli.InstallOutcomeInstalled},
		{"v0.0.1", cli.InstallOutcomeUnchanged},
		{"v0.1.0", cli.InstallOutcomeUpgraded},
		{"v0.0.1", cli.InstallOutcomeDowngraded},
	} {
		result, err := installer.Install(context.Background(), cli.Flags{Agent: string(domain.AgentHermes), Type: "plugin", Name: "demo-plugin", Version: target.version})
		if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != target.outcome {
			t.Fatalf("install %s = %+v, %v", target.version, result, err)
		}
		actualName, err := domain.ActualPluginName(domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: target.version})
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Join(hermesHome, "plugins", actualName)); err != nil || !info.IsDir() {
			t.Fatalf("Hermes target %q is not a directory: %v", actualName, err)
		}
		for _, otherVersion := range []string{"v0.0.1", "v0.1.0"} {
			if otherVersion == target.version {
				continue
			}
			otherName, _ := domain.ActualPluginName(domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: otherVersion})
			if _, err := os.Lstat(filepath.Join(hermesHome, "plugins", otherName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old Hermes target %q remains: %v", otherName, err)
			}
		}
		if _, err := os.Lstat(filepath.Join(root, "apm-home", ".apm", "staging")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Hermes staging remains after %s: %v", target.version, err)
		}
	}
	result, err := uninstaller.Uninstall(context.Background(), cli.Flags{Agent: string(domain.AgentHermes), Type: "plugin", Name: "demo-plugin"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeCompleted {
		t.Fatalf("uninstall = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(hermesHome, "plugins", "demo-plugin-v0.0.1")); !os.IsNotExist(err) {
		t.Fatalf("Hermes target remains: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		if !strings.HasPrefix(line, fakeBin+string(filepath.Separator)) || strings.Contains(line, "/home/liangff/.local/bin/hermes") {
			t.Fatalf("unsafe CLI call %q", line)
		}
	}
}

func TestPluginIntegrationMarketplaceAgentVersionLifecycleIsIsolated(t *testing.T) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		t.Run(string(agent), func(t *testing.T) {
			root := t.TempDir()
			fakeBin := filepath.Join(root, "fake-bin")
			state := filepath.Join(root, "state")
			for _, path := range []string{fakeBin, state} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, executable := range []string{"codex", "claude"} {
				if err := os.WriteFile(filepath.Join(fakeBin, executable), []byte(fakeMarketplacePluginScript), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			agentRoots := map[domain.AgentID]string{
				domain.AgentCodex: filepath.Join(root, "agents", "codex"), domain.AgentClaudeCode: filepath.Join(root, "agents", "claude"),
			}
			if err := os.MkdirAll(agentRoots[agent], 0o700); err != nil {
				t.Fatal(err)
			}
			resources, _ := filepath.Abs(filepath.Join("..", "cloud", "resources"))
			calls := filepath.Join(root, "calls.log")
			cacheHome := filepath.Join(root, "cache")
			t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin:/bin")
			t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
			t.Setenv("FAKE_PLUGIN_STATE", state)
			t.Setenv("FAKE_PLUGIN_CALLS", calls)
			t.Setenv("FAKE_PLUGIN_MARKETPLACE", filepath.Join(resources, "plugins"))
			t.Setenv("APM_HOME", filepath.Join(root, "apm-home"))
			t.Setenv("XDG_CACHE_HOME", cacheHome)
			t.Setenv("CODEX_HOME", agentRoots[domain.AgentCodex])
			t.Setenv("CLAUDE_CONFIG_DIR", agentRoots[domain.AgentClaudeCode])
			t.Setenv("HERMES_HOME", filepath.Join(root, "absent-hermes"))
			cloudClient, err := cloud.NewLocalClient(resources)
			if err != nil {
				t.Fatal(err)
			}
			repository := config.NewJSONRepository()
			registry := adapter.NewRegistry()
			transactions, err := transaction.NewManager(repository, registry)
			if err != nil {
				t.Fatal(err)
			}
			locker, _ := lock.NewDefault()
			powerCache, _ := cache.NewFilesystem()
			pluginResources := newIntegrationPluginResources(t, cloudClient)
			installer, _ := NewInstall(locker, transactions, cloudClient, powerCache, pluginResources, registry, repository, version.NewSelector())
			uninstaller, _ := NewUninstall(locker, transactions, cloudClient, pluginResources, registry, repository)
			for _, target := range []struct {
				version string
				outcome cli.InstallOutcome
			}{
				{"v0.0.1", cli.InstallOutcomeInstalled}, {"v0.0.1", cli.InstallOutcomeUnchanged},
				{"v0.1.0", cli.InstallOutcomeUpgraded}, {"v0.0.1", cli.InstallOutcomeDowngraded},
			} {
				result, err := installer.Install(context.Background(), cli.Flags{Agent: string(agent), Type: "plugin", Name: "demo-plugin", Version: target.version})
				if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != target.outcome {
					t.Fatalf("install %s = %+v, %v", target.version, result, err)
				}
			}
			result, err := uninstaller.Uninstall(context.Background(), cli.Flags{Agent: string(agent), Type: "plugin", Name: "demo-plugin"})
			if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeCompleted {
				t.Fatalf("uninstall = %+v, %v", result, err)
			}
			contents, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
				if !strings.HasPrefix(line, fakeBin+string(os.PathSeparator)) || strings.Contains(line, "/home/liangff/.local/bin/hermes") {
					t.Fatalf("unsafe CLI call %q", line)
				}
			}
			if info, err := os.Stat(filepath.Join(cacheHome, "apm", "plugins", string(agent), "demo-plugin")); err != nil || !info.IsDir() {
				t.Fatalf("marketplace install did not persist Plugin cache: %v", err)
			}
		})
	}
}

type isolatedPluginHarness struct {
	t          *testing.T
	root       string
	fakeBin    string
	state      string
	calls      string
	cacheHome  string
	repository *config.JSONRepository
	registry   *adapter.Registry
	cloud      cloud.Client
	plugins    pluginresource.Manager
	install    *InstallService
	uninstall  *UninstallService
}

func newIsolatedPluginHarness(t *testing.T, installedAgents ...domain.AgentID) *isolatedPluginHarness {
	t.Helper()
	root := t.TempDir()
	fakeBin := filepath.Join(root, "fake-bin")
	state := filepath.Join(root, "state")
	for _, path := range []string{fakeBin, state} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for executable, script := range map[string]string{
		"codex": fakeMarketplacePluginScript, "claude": fakeMarketplacePluginScript, "hermes": fakeHermesPluginScript,
	} {
		if err := os.WriteFile(filepath.Join(fakeBin, executable), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	agentRoots := map[domain.AgentID]string{
		domain.AgentCodex:      filepath.Join(root, "agents", "codex"),
		domain.AgentClaudeCode: filepath.Join(root, "agents", "claude"),
		domain.AgentHermes:     filepath.Join(root, "agents", "hermes"),
	}
	for _, agent := range installedAgents {
		if err := os.MkdirAll(agentRoots[agent], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resources, err := filepath.Abs(filepath.Join("..", "cloud", "resources"))
	if err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls.log")
	cacheHome := filepath.Join(root, "cache")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
	t.Setenv("FAKE_PLUGIN_STATE", state)
	t.Setenv("FAKE_PLUGIN_CALLS", calls)
	t.Setenv("FAKE_PLUGIN_MARKETPLACE", filepath.Join(resources, "plugins"))
	t.Setenv("APM_HOME", filepath.Join(root, "apm-home"))
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("CODEX_HOME", agentRoots[domain.AgentCodex])
	t.Setenv("CLAUDE_CONFIG_DIR", agentRoots[domain.AgentClaudeCode])
	t.Setenv("HERMES_HOME", agentRoots[domain.AgentHermes])
	cloudClient, err := cloud.NewLocalClient(resources)
	if err != nil {
		t.Fatal(err)
	}
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		t.Fatal(err)
	}
	locker, err := lock.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	powerCache, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	pluginResources := newIntegrationPluginResources(t, cloudClient)
	installer, err := NewInstall(locker, transactions, cloudClient, powerCache, pluginResources, registry, repository, version.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	uninstaller, err := NewUninstall(locker, transactions, cloudClient, pluginResources, registry, repository)
	if err != nil {
		t.Fatal(err)
	}
	return &isolatedPluginHarness{
		t: t, root: root, fakeBin: fakeBin, state: state, calls: calls, cacheHome: cacheHome,
		repository: repository, registry: registry, cloud: cloudClient, plugins: pluginResources, install: installer, uninstall: uninstaller,
	}
}

func TestPluginIntegrationRejectsUnmanagedTargetAndMarketplaceConflict(t *testing.T) {
	t.Run("unmanaged target", func(t *testing.T) {
		harness := newIsolatedPluginHarness(t, domain.AgentCodex)
		if err := os.WriteFile(filepath.Join(harness.state, "plugin-codex"), []byte("demo-plugin-v0.0.1@foreign"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := harness.install.Install(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo-plugin", Version: "v0.0.1"})
		if !errors.Is(err, adapter.ErrPluginConflict) || !errors.Is(err, adapter.ErrTargetConflict) {
			t.Fatalf("Install() error = %v, want Plugin and target conflict", err)
		}
		if _, found, findErr := harness.repository.Find(domain.AgentCodex, domain.PowerTypePlugin, "demo-plugin"); findErr != nil || found {
			t.Fatalf("manifest found/error = %v/%v, want absent", found, findErr)
		}
		harness.assertOnlyFakeExecutables()
	})

	t.Run("marketplace source conflict", func(t *testing.T) {
		harness := newIsolatedPluginHarness(t, domain.AgentCodex)
		if err := os.WriteFile(filepath.Join(harness.state, "marketplace-codex"), []byte(filepath.Join(harness.root, "foreign-marketplace")), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAKE_PLUGIN_MARKETPLACE", filepath.Join(harness.root, "foreign-marketplace"))
		_, err := harness.install.Install(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo-plugin", Version: "v0.0.1"})
		if !errors.Is(err, adapter.ErrMarketplaceConflict) {
			t.Fatalf("Install() error = %v, want ErrMarketplaceConflict", err)
		}
		if _, statErr := os.Lstat(filepath.Join(harness.state, "plugin-codex")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("conflicting marketplace mutated Plugin state: %v", statErr)
		}
		harness.assertMarketplaceCachePresentAndStagingAbsent(domain.AgentCodex, "v0.0.1")
		harness.assertOnlyFakeExecutables()
	})
}

func TestPluginIntegrationUninstallRepairsMissingTargetManifest(t *testing.T) {
	harness := newIsolatedPluginHarness(t, domain.AgentCodex)
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"}
	keep := domain.PowerRef{Name: "keep-plugin", Type: domain.PowerTypePlugin, Version: "v9.9.9"}
	if err := harness.repository.Set(domain.AgentCodex, ref); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.Set(domain.AgentCodex, keep); err != nil {
		t.Fatal(err)
	}
	result, err := harness.uninstall.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo-plugin"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeCompleted {
		t.Fatalf("Uninstall() = %+v, %v", result, err)
	}
	if _, found, findErr := harness.repository.Find(domain.AgentCodex, ref.Type, ref.Name); findErr != nil || found {
		t.Fatalf("missing target manifest found/error = %v/%v", found, findErr)
	}
	if got, found, findErr := harness.repository.Find(domain.AgentCodex, keep.Type, keep.Name); findErr != nil || !found || got != keep {
		t.Fatalf("unrelated manifest = %+v, %v, %v", got, found, findErr)
	}
	harness.assertOnlyFakeExecutables()
}

func TestPluginIntegrationSearchPreservesAndFixRecoversInterruptedMarketplaceJournal(t *testing.T) {
	harness := newIsolatedPluginHarness(t, domain.AgentCodex)
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"}
	artifact, err := harness.plugins.Ensure(context.Background(), domain.AgentCodex, ref)
	if err != nil {
		t.Fatal(err)
	}
	selector := artifact.ActualName + "@" + artifact.MarketplaceName
	if err := os.WriteFile(filepath.Join(harness.state, "plugin-codex"), []byte(selector), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harness.state, "marketplace-codex"), []byte(artifact.MarketplaceSource), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := transaction.Journal{
		SchemaVersion: 1, TransactionID: "00112233445566778899aabbccddeeff", Driver: transaction.DriverPlugin,
		Operation: transaction.OperationInstall, Phase: transaction.PhaseAddingNewPlugin, Agent: domain.AgentCodex, Power: ref,
		Plugin: &transaction.PluginJournalPayload{
			Deployment: domain.PluginDeploymentMarketplace,
			Target:     adapter.PluginTarget{ActualName: artifact.ActualName, Selector: selector},
			Marketplace: &transaction.PluginMarketplaceJournalPayload{
				Name: artifact.MarketplaceName, TargetSource: artifact.MarketplaceSource,
			},
		},
	}
	contents, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	journalPath, err := paths.JournalPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	originalJournal := readJournalBytes(t, journalPath)
	transactions, err := transaction.NewManager(harness.repository, harness.registry)
	if err != nil {
		t.Fatal(err)
	}
	search, err := NewSearch(transactions, harness.cloud)
	if err != nil {
		t.Fatal(err)
	}
	list, err := NewList(transactions, harness.cloud, harness.registry, harness.repository)
	if err != nil {
		t.Fatal(err)
	}
	assertActiveJournalReadOnly(t, journalPath, originalJournal, func() {
		if _, found, findErr := harness.repository.Find(domain.AgentCodex, ref.Type, ref.Name); findErr != nil || found {
			t.Fatalf("read-only manifest found/error = %v/%v, want absent", found, findErr)
		}
		if contents, statErr := os.ReadFile(filepath.Join(harness.state, "plugin-codex")); statErr != nil || string(contents) != selector {
			t.Fatalf("read-only command changed Plugin target: contents=%q err=%v", contents, statErr)
		}
	},
		func() error {
			_, err := search.Search(context.Background(), cli.Flags{Keyword: "demo-plugin"})
			return err
		},
		func() error { _, err := list.List(context.Background(), cli.Flags{}); return err },
		func() error {
			_, err := list.List(context.Background(), cli.Flags{Installed: true, Agent: string(domain.AgentCodex)})
			return err
		},
	)
	if _, err := os.ReadFile(harness.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only commands must not invoke recovery target actions: %v", err)
	}

	recoverer := &countingRecoverer{Recoverer: transactions}
	fixer := newIntegrationFixer(t, recoverer)
	if err := fixer.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recoverer.calls != 1 {
		t.Fatalf("Recover calls = %d, want 1", recoverer.calls)
	}
	assertJournalAbsent(t, journalPath)
	if got, found, err := harness.repository.Find(domain.AgentCodex, ref.Type, ref.Name); err != nil || !found || got != ref {
		t.Fatalf("Fix manifest = %+v, found=%v err=%v, want %+v", got, found, err, ref)
	}
	if contents, statErr := os.ReadFile(filepath.Join(harness.state, "plugin-codex")); statErr != nil || string(contents) != selector {
		t.Fatalf("Fix changed committed Plugin target: contents=%q err=%v", contents, statErr)
	}
	contents, err = os.ReadFile(harness.calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(contents)), "\n")); got != 2 {
		t.Fatalf("Fix CLI calls = %d, want marketplace and Plugin recovery inspections: %s", got, contents)
	}
	manifestPath, err := paths.ManifestPath(domain.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	assertPathsWithinRoot(t, harness.root, journalPath, manifestPath, filepath.Join(harness.state, "plugin-codex"))
	harness.assertOnlyFakeExecutables()
}

func TestPluginIntegrationSearchPreservesAndFixRecoversInterruptedHermesFilesystemJournal(t *testing.T) {
	harness := newIsolatedPluginHarness(t, domain.AgentHermes)
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"}
	actualName, err := domain.ActualPluginName(ref)
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := harness.registry.ResolvePlugin(domain.AgentHermes)
	if err != nil {
		t.Fatal(err)
	}
	target, err := plugin.TargetPath(actualName)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "PLUGIN.md"), []byte("interrupted target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "plugins", ref.Name), 0o700); err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	txDir := filepath.Join(filepath.Dir(target), ".apm-transactions", id)
	journal := transaction.Journal{
		SchemaVersion: 1, TransactionID: id, Driver: transaction.DriverPlugin,
		Operation: transaction.OperationInstall, Phase: transaction.PhaseAddingNew,
		Agent: domain.AgentHermes, Power: ref,
		Plugin: &transaction.PluginJournalPayload{
			Deployment: domain.PluginDeploymentFilesystem,
			Target:     adapter.PluginTarget{ActualName: actualName, Selector: actualName},
			Filesystem: &transaction.PluginFilesystemJournalPayload{
				StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", ref.Name),
				TargetPath: target, TransactionDir: txDir, PublishPath: filepath.Join(txDir, "publish"),
			},
		},
	}
	journalPath := writeIntegrationJournal(t, journal)
	originalJournal := readJournalBytes(t, journalPath)
	transactions, err := transaction.NewManager(harness.repository, harness.registry)
	if err != nil {
		t.Fatal(err)
	}
	search, err := NewSearch(transactions, harness.cloud)
	if err != nil {
		t.Fatal(err)
	}
	list, err := NewList(transactions, harness.cloud, harness.registry, harness.repository)
	if err != nil {
		t.Fatal(err)
	}
	assertActiveJournalReadOnly(t, journalPath, originalJournal, func() {
		if _, found, err := harness.repository.Find(domain.AgentHermes, ref.Type, ref.Name); err != nil || found {
			t.Fatalf("read-only manifest found/error = %v/%v, want absent", found, err)
		}
		assertFileContents(t, filepath.Join(target, "PLUGIN.md"), "interrupted target\n")
	},
		func() error {
			_, err := search.Search(context.Background(), cli.Flags{Keyword: "demo-plugin"})
			return err
		},
		func() error { _, err := list.List(context.Background(), cli.Flags{}); return err },
		func() error {
			_, err := list.List(context.Background(), cli.Flags{Installed: true, Agent: string(domain.AgentHermes)})
			return err
		},
	)
	if _, err := os.ReadFile(harness.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only commands must not invoke recovery target actions: %v", err)
	}

	recoverer := &countingRecoverer{Recoverer: transactions}
	if err := newIntegrationFixer(t, recoverer).Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recoverer.calls != 1 {
		t.Fatalf("Recover calls = %d, want 1", recoverer.calls)
	}
	assertJournalAbsent(t, journalPath)
	if got, found, err := harness.repository.Find(domain.AgentHermes, ref.Type, ref.Name); err != nil || !found || got != ref {
		t.Fatalf("Fix manifest = %+v, found=%v err=%v, want %+v", got, found, err, ref)
	}
	assertFileContents(t, filepath.Join(target, "PLUGIN.md"), "interrupted target\n")
	contents, err := os.ReadFile(harness.calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(contents)), "\n")); got != 2 {
		t.Fatalf("Fix CLI calls = %d, want exactly 2 recovery calls: %s", got, contents)
	}
	manifestPath, err := paths.ManifestPath(domain.AgentHermes)
	if err != nil {
		t.Fatal(err)
	}
	assertPathsWithinRoot(t, harness.root, journalPath, manifestPath, target, txDir)
	harness.assertOnlyFakeExecutables()
}

func TestPluginIntegrationMultiAgentInstallFailsFast(t *testing.T) {
	harness := newIsolatedPluginHarness(t, domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes)
	if err := os.WriteFile(filepath.Join(harness.state, "marketplace-claude"), []byte(filepath.Join(harness.root, "foreign-marketplace")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PLUGIN_MARKETPLACE", filepath.Join(harness.root, "foreign-marketplace"))
	result, err := harness.install.Install(context.Background(), cli.Flags{Agent: string(domain.AllAgents), Type: "plugin", Name: "demo-plugin", Version: "v0.0.1"})
	if !errors.Is(err, adapter.ErrMarketplaceConflict) {
		t.Fatalf("Install(all) error = %v, want ErrMarketplaceConflict", err)
	}
	if len(result.Items) != 3 || result.Items[0].Agent != domain.AgentClaudeCode || result.Items[0].Outcome != cli.InstallOutcomeFailed ||
		result.Items[1].Outcome != cli.InstallOutcomeUnexecuted || result.Items[2].Outcome != cli.InstallOutcomeUnexecuted {
		t.Fatalf("Install(all) result = %+v, want failed then unexecuted", result)
	}
	contents, readErr := os.ReadFile(harness.calls)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(contents), filepath.Join(harness.fakeBin, "codex")+"|") || strings.Contains(string(contents), filepath.Join(harness.fakeBin, "hermes")+"|") {
		t.Fatalf("fail-fast invoked later Agent CLI:\n%s", contents)
	}
	harness.assertMarketplaceCachePresentAndStagingAbsent(domain.AgentClaudeCode, "v0.0.1")
	harness.assertOnlyFakeExecutables()
}

func (h *isolatedPluginHarness) assertMarketplaceCachePresentAndStagingAbsent(agent domain.AgentID, version string) {
	h.t.Helper()
	cachePath := filepath.Join(h.cacheHome, "apm", "plugins", string(agent), "demo-plugin", version)
	if info, err := os.Stat(cachePath); err != nil || !info.IsDir() {
		h.t.Fatalf("Plugin cache %q is not persisted: %v", cachePath, err)
	}
	for _, path := range []string{
		filepath.Join(h.root, "apm-home", ".apm", "staging"),
		filepath.Join(h.root, "apm-home", ".apm", ".tmp", "staging"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			h.t.Fatalf("marketplace Plugin path %q exists or cannot be inspected: %v", path, err)
		}
	}
}

func (h *isolatedPluginHarness) assertOnlyFakeExecutables() {
	h.t.Helper()
	contents, err := os.ReadFile(h.calls)
	if err != nil {
		h.t.Fatal(err)
	}
	prefix := h.fakeBin + string(filepath.Separator)
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		path, _, found := strings.Cut(line, "|")
		if !found || !strings.HasPrefix(path, prefix) || strings.Contains(line, "/home/liangff/.local/bin/hermes") {
			h.t.Fatalf("Agent CLI invocation escaped fake-bin: %q", line)
		}
	}
}
