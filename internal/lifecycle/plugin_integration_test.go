package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/paths"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
	"apm/internal/utils/filesystem"
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

const fakeBibitPluginScript = `#!/bin/sh
case "$0" in "$FAKE_PLUGIN_BIN"/*) ;; *) exit 97 ;; esac
printf '%s|%s\n' "$0" "$*" >> "$FAKE_PLUGIN_CALLS"
exit 98
`

func writePluginHarnessExecutables(t *testing.T, fakeBin string) {
	t.Helper()
	for executable, script := range map[string]string{
		"bianbu-agent": fakeBibitPluginScript,
		"codex":        fakeMarketplacePluginScript,
		"claude":       fakeMarketplacePluginScript,
		"hermes":       fakeHermesPluginScript,
	} {
		if err := os.WriteFile(filepath.Join(fakeBin, executable), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func assertPluginHarnessEnvironment(t *testing.T, fakeBin, isolatedHome string) {
	t.Helper()
	if got := os.Getenv("PATH"); got != fakeBin {
		t.Fatalf("PATH = %q, want fake-bin only %q", got, fakeBin)
	}
	if got := os.Getenv("HOME"); got != isolatedHome {
		t.Fatalf("HOME = %q, want isolated home %q", got, isolatedHome)
	}
	for _, executable := range []string{"bianbu-agent", "codex", "claude", "hermes"} {
		resolved, err := exec.LookPath(executable)
		within, withinErr := filesystem.PathEqualOrWithin(fakeBin, resolved)
		if err != nil || withinErr != nil || !within || resolved != filepath.Join(fakeBin, executable) {
			t.Fatalf("resolved %s = %q, lookup=%v containment=%v; want fake-bin", executable, resolved, err, withinErr)
		}
	}
}

func TestPluginIntegrationHermesVersionLifecycleIsIsolated(t *testing.T) {
	root := t.TempDir()
	fakeBin := filepath.Join(root, "fake-bin")
	hermesHome := filepath.Join(root, "agents", "hermes")
	state := filepath.Join(hermesHome, ".apm-fake", "plugins")
	for _, path := range []string{fakeBin, state} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writePluginHarnessExecutables(t, fakeBin)
	isolatedHome := filepath.Join(root, "user-home")
	if err := os.MkdirAll(isolatedHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("HOME", isolatedHome)
	t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
	t.Setenv("FAKE_PLUGIN_STATE", state)
	t.Setenv("FAKE_PLUGIN_CALLS", filepath.Join(root, "calls.log"))
	t.Setenv("APM_HOME", filepath.Join(root, "apm-home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("HERMES_HOME", hermesHome)
	t.Setenv("CODEX_HOME", filepath.Join(root, "absent-codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "absent-claude"))
	t.Setenv("BIANBU_AGENT_HOME", filepath.Join(root, "absent-bibit"))
	assertPluginHarnessEnvironment(t, fakeBin, isolatedHome)
	resources, _ := filepath.Abs(filepath.Join("..", "cloud", "resources"))
	cloudClient, err := local.New(resources)
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
	powerCache, _ := cache.NewFilesystem(cloudClient)
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
		actualName, err := domain.PluginRuntimeName(domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: target.version})
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Join(hermesHome, "plugins", actualName)); err != nil || !info.IsDir() {
			t.Fatalf("Hermes target %q is not a directory: %v", actualName, err)
		}
		manifest, err := os.ReadFile(filepath.Join(hermesHome, "plugins", actualName, ".hermes-plugin", "plugin.yaml"))
		if err != nil || !strings.Contains(string(manifest), "version: "+strings.TrimPrefix(target.version, "v")) {
			t.Fatalf("Hermes logical target version = %q, %v; want %s", manifest, err, target.version)
		}
		if _, err := os.Lstat(filepath.Join(root, "apm-home", ".apm", "staging")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Hermes staging remains after %s: %v", target.version, err)
		}
	}
	result, err := uninstaller.Uninstall(context.Background(), cli.Flags{Agent: string(domain.AgentHermes), Type: "plugin", Name: "demo-plugin"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeCompleted {
		t.Fatalf("uninstall = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(hermesHome, "plugins", "demo-plugin")); !os.IsNotExist(err) {
		t.Fatalf("Hermes target remains: %v", err)
	}
	for _, release := range []string{"v0.0.1", "v0.1.0"} {
		cachePath := filepath.Join(root, "cache", "apm", "plugins", "demo-plugin", release)
		if info, err := os.Stat(cachePath); err != nil || !info.IsDir() {
			t.Fatalf("Hermes shared immutable cache %q missing: %v", cachePath, err)
		}
	}
	contents, err := os.ReadFile(filepath.Join(root, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		if !strings.HasPrefix(line, fakeBin+string(filepath.Separator)) {
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
			writePluginHarnessExecutables(t, fakeBin)
			agentRoots := map[domain.AgentID]string{
				domain.AgentBibit: filepath.Join(root, "agents", "bibit"), domain.AgentCodex: filepath.Join(root, "agents", "codex"),
				domain.AgentClaudeCode: filepath.Join(root, "agents", "claude"), domain.AgentHermes: filepath.Join(root, "agents", "hermes"),
			}
			if err := os.MkdirAll(agentRoots[agent], 0o700); err != nil {
				t.Fatal(err)
			}
			resources, _ := filepath.Abs(filepath.Join("..", "cloud", "resources"))
			calls := filepath.Join(root, "calls.log")
			cacheHome := filepath.Join(root, "cache")
			isolatedHome := filepath.Join(root, "user-home")
			if err := os.MkdirAll(isolatedHome, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", fakeBin)
			t.Setenv("HOME", isolatedHome)
			t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
			t.Setenv("FAKE_PLUGIN_STATE", state)
			t.Setenv("FAKE_PLUGIN_CALLS", calls)
			t.Setenv("FAKE_PLUGIN_MARKETPLACE", filepath.Join(resources, "plugins"))
			t.Setenv("APM_HOME", filepath.Join(root, "apm-home"))
			t.Setenv("XDG_CACHE_HOME", cacheHome)
			t.Setenv("CODEX_HOME", agentRoots[domain.AgentCodex])
			t.Setenv("CLAUDE_CONFIG_DIR", agentRoots[domain.AgentClaudeCode])
			t.Setenv("HERMES_HOME", agentRoots[domain.AgentHermes])
			t.Setenv("BIANBU_AGENT_HOME", agentRoots[domain.AgentBibit])
			assertPluginHarnessEnvironment(t, fakeBin, isolatedHome)
			cloudClient, err := local.New(resources)
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
			powerCache, _ := cache.NewFilesystem(cloudClient)
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
				if !strings.HasPrefix(line, fakeBin+string(os.PathSeparator)) {
					t.Fatalf("unsafe CLI call %q", line)
				}
			}
			if info, err := os.Stat(filepath.Join(cacheHome, "apm", "plugins", "demo-plugin")); err != nil || !info.IsDir() {
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
	agentRoots map[domain.AgentID]string
	repository *config.JSONRepository
	manifests  *acceptanceManifestRepository
	registry   *adapter.Registry
	cloud      cloud.Client
	plugins    pluginresource.Manager
	downloader *countingPluginDownloader
	install    *InstallService
	uninstall  *UninstallService
	fix        *FixService
}

type countingPluginDownloader struct {
	source pluginresource.Downloader
	calls  int
}

func (d *countingPluginDownloader) DownloadPlugin(ctx context.Context, ref domain.PowerRef, destination string) error {
	d.calls++
	return d.source.DownloadPlugin(ctx, ref, destination)
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
	writePluginHarnessExecutables(t, fakeBin)
	agentRoots := map[domain.AgentID]string{
		domain.AgentBibit:      filepath.Join(root, "agents", "bibit"),
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
	isolatedHome := filepath.Join(root, "user-home")
	if err := os.MkdirAll(isolatedHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("HOME", isolatedHome)
	t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
	t.Setenv("FAKE_PLUGIN_STATE", state)
	t.Setenv("FAKE_PLUGIN_CALLS", calls)
	t.Setenv("FAKE_PLUGIN_MARKETPLACE", filepath.Join(resources, "plugins"))
	t.Setenv("APM_HOME", filepath.Join(root, "apm-home"))
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("CODEX_HOME", agentRoots[domain.AgentCodex])
	t.Setenv("CLAUDE_CONFIG_DIR", agentRoots[domain.AgentClaudeCode])
	t.Setenv("HERMES_HOME", agentRoots[domain.AgentHermes])
	t.Setenv("BIANBU_AGENT_HOME", agentRoots[domain.AgentBibit])
	assertPluginHarnessEnvironment(t, fakeBin, isolatedHome)
	cloudClient, err := local.New(resources)
	if err != nil {
		t.Fatal(err)
	}
	repository := config.NewJSONRepository()
	manifests := &acceptanceManifestRepository{base: repository}
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(manifests, registry)
	if err != nil {
		t.Fatal(err)
	}
	locker, err := lock.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	powerCache, err := cache.NewFilesystem(cloudClient)
	if err != nil {
		t.Fatal(err)
	}
	downloader := &countingPluginDownloader{source: cloudClient}
	pluginResources := newIntegrationPluginResources(t, downloader)
	installer, err := NewInstall(locker, transactions, cloudClient, powerCache, pluginResources, registry, manifests, version.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	uninstaller, err := NewUninstall(locker, transactions, cloudClient, pluginResources, registry, manifests)
	if err != nil {
		t.Fatal(err)
	}
	fixer, err := NewFix(locker, transactions)
	if err != nil {
		t.Fatal(err)
	}
	return &isolatedPluginHarness{
		t: t, root: root, fakeBin: fakeBin, state: state, calls: calls, cacheHome: cacheHome,
		agentRoots: agentRoots, repository: repository, manifests: manifests, registry: registry, cloud: cloudClient,
		plugins: pluginResources, downloader: downloader, install: installer, uninstall: uninstaller, fix: fixer,
	}
}

var pluginAcceptanceAgents = []domain.AgentID{
	domain.AgentCodex,
	domain.AgentClaudeCode,
	domain.AgentHermes,
	domain.AgentBibit,
}

func (h *isolatedPluginHarness) installPlugin(agent domain.AgentID, release string) (cli.InstallResult, error) {
	h.t.Helper()
	return h.install.Install(context.Background(), cli.Flags{
		Agent: string(agent), Type: "plugin", Name: "demo-plugin", Version: release,
	})
}

func (h *isolatedPluginHarness) uninstallPlugin(agent domain.AgentID) (cli.UninstallResult, error) {
	h.t.Helper()
	return h.uninstall.Uninstall(context.Background(), cli.Flags{
		Agent: string(agent), Type: "plugin", Name: "demo-plugin",
	})
}

func (h *isolatedPluginHarness) pluginTarget(agent domain.AgentID) string {
	h.t.Helper()
	switch agent {
	case domain.AgentCodex:
		return filepath.Join(h.state, "plugin-codex")
	case domain.AgentClaudeCode:
		return filepath.Join(h.state, "plugin-claude")
	case domain.AgentHermes, domain.AgentBibit:
		pluginAdapter, err := h.registry.ResolvePlugin(agent)
		if err != nil {
			h.t.Fatal(err)
		}
		target, err := pluginAdapter.TargetPath("demo-plugin")
		if err != nil {
			h.t.Fatal(err)
		}
		return target
	default:
		h.t.Fatalf("unsupported test Agent %q", agent)
		return ""
	}
}

func (h *isolatedPluginHarness) marketplaceState(agent domain.AgentID) string {
	h.t.Helper()
	switch agent {
	case domain.AgentCodex:
		return filepath.Join(h.state, "marketplace-codex")
	case domain.AgentClaudeCode:
		return filepath.Join(h.state, "marketplace-claude")
	default:
		return ""
	}
}

func (h *isolatedPluginHarness) hermesActivationState() string {
	return filepath.Join(h.state, "demo-plugin")
}

func (h *isolatedPluginHarness) assertManagedPlugin(agent domain.AgentID, release string, present bool) {
	h.t.Helper()
	ref, found, err := h.repository.Find(agent, domain.PowerTypePlugin, "demo-plugin")
	if err != nil || found != present {
		h.t.Fatalf("manifest for %q found=%v ref=%+v err=%v, want present=%v", agent, found, ref, err, present)
	}
	if present && ref != (domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: release}) {
		h.t.Fatalf("manifest for %q = %+v, want release %s", agent, ref, release)
	}
	target := h.pluginTarget(agent)
	info, statErr := os.Lstat(target)
	if !present {
		if !errors.Is(statErr, os.ErrNotExist) {
			h.t.Fatalf("target for %q remains at %q: %v", agent, target, statErr)
		}
		return
	}
	if agent == domain.AgentCodex || agent == domain.AgentClaudeCode {
		contents, readErr := os.ReadFile(target)
		if readErr != nil || string(contents) != "demo-plugin" {
			h.t.Fatalf("marketplace target for %q = %q, %v", agent, contents, readErr)
		}
		if _, err := os.Stat(h.marketplaceState(agent)); err != nil {
			h.t.Fatalf("marketplace for %q missing: %v", agent, err)
		}
		return
	}
	if statErr != nil || !info.IsDir() {
		h.t.Fatalf("filesystem target for %q = %v, %v", agent, info, statErr)
	}
	manifest, err := os.ReadFile(filepath.Join(target, ".hermes-plugin", "plugin.yaml"))
	if err != nil || !strings.Contains(string(manifest), "version: "+strings.TrimPrefix(release, "v")) {
		h.t.Fatalf("filesystem target for %q version = %q, %v; want %s", agent, manifest, err, release)
	}
}

func (h *isolatedPluginHarness) removeManagedPluginState(agent domain.AgentID) {
	h.t.Helper()
	for _, path := range []string{h.pluginTarget(agent), h.marketplaceState(agent)} {
		if path != "" {
			if err := os.RemoveAll(path); err != nil {
				h.t.Fatal(err)
			}
		}
	}
	if agent == domain.AgentHermes {
		if err := os.RemoveAll(h.hermesActivationState()); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *isolatedPluginHarness) createUnmanagedPluginState(agent domain.AgentID) {
	h.t.Helper()
	target := h.pluginTarget(agent)
	if agent == domain.AgentCodex || agent == domain.AgentClaudeCode {
		if err := os.WriteFile(target, []byte("demo-plugin"), 0o600); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		h.t.Fatal(err)
	}
	if agent == domain.AgentHermes {
		if err := os.WriteFile(h.hermesActivationState(), nil, 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
}

func TestPluginIntegrationFourAgentBibitLifecycleMatrix(t *testing.T) {
	for _, agent := range pluginAcceptanceAgents {
		t.Run(string(agent), func(t *testing.T) {
			h := newIsolatedPluginHarness(t, agent)
			for _, step := range []struct {
				release string
				outcome cli.InstallOutcome
			}{
				{release: "v0.0.1", outcome: cli.InstallOutcomeInstalled},
				{release: "v0.0.1", outcome: cli.InstallOutcomeUnchanged},
				{release: "v0.1.0", outcome: cli.InstallOutcomeUpgraded},
				{release: "v0.0.1", outcome: cli.InstallOutcomeDowngraded},
			} {
				result, err := h.installPlugin(agent, step.release)
				if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != step.outcome {
					t.Fatalf("Install(%s) = %+v, %v; want %s", step.release, result, err, step.outcome)
				}
				h.assertManagedPlugin(agent, step.release, true)
			}

			removed, err := h.uninstallPlugin(agent)
			if err != nil || len(removed.Items) != 1 || removed.Items[0].Outcome != cli.UninstallOutcomeCompleted {
				t.Fatalf("Uninstall() = %+v, %v", removed, err)
			}
			h.assertManagedPlugin(agent, "", false)

			reinstalled, err := h.installPlugin(agent, "v0.0.1")
			if err != nil || len(reinstalled.Items) != 1 || reinstalled.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("reinstall = %+v, %v", reinstalled, err)
			}
			h.assertManagedPlugin(agent, "v0.0.1", true)

			h.removeManagedPluginState(agent)
			cleaned, err := h.uninstallPlugin(agent)
			if err != nil || len(cleaned.Items) != 1 || cleaned.Items[0].Outcome != cli.UninstallOutcomeCompleted {
				t.Fatalf("missing-target Uninstall() = %+v, %v", cleaned, err)
			}
			h.assertManagedPlugin(agent, "", false)

			h.createUnmanagedPluginState(agent)
			if _, err := h.installPlugin(agent, "v0.0.1"); !errors.Is(err, adapter.ErrPluginConflict) || !errors.Is(err, adapter.ErrTargetConflict) {
				t.Fatalf("unmanaged Install() error = %v, want Plugin and target conflict", err)
			}
			if _, found, err := h.repository.Find(agent, domain.PowerTypePlugin, "demo-plugin"); err != nil || found {
				t.Fatalf("unmanaged conflict manifest found=%v err=%v", found, err)
			}
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestFixPluginIntegrationFourAgentBibitRecoveryRetries(t *testing.T) {
	for _, agent := range pluginAcceptanceAgents {
		t.Run(string(agent), func(t *testing.T) {
			h := newIsolatedPluginHarness(t, agent)
			injected := errors.New("injected manifest commit failure")
			h.manifests.setErr = injected
			result, err := h.installPlugin(agent, "v0.0.1")
			if !errors.Is(err, injected) || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
				t.Fatalf("interrupted Install() = %+v, %v", result, err)
			}
			journalPath, err := paths.JournalPath()
			if err != nil {
				t.Fatal(err)
			}
			assertJournalExists(t, journalPath)
			if err := h.fix.Fix(context.Background()); !errors.Is(err, transaction.ErrRecoveryFailed) || !errors.Is(err, injected) {
				t.Fatalf("first Fix() error = %v, want recovery and injected failure", err)
			}
			assertJournalExists(t, journalPath)
			h.manifests.setErr = nil
			if err := h.fix.Fix(context.Background()); err != nil {
				t.Fatalf("second Fix() error = %v", err)
			}
			assertJournalAbsent(t, journalPath)
			h.assertManagedPlugin(agent, "v0.0.1", true)
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestPluginAcceptanceSharedArtifact(t *testing.T) {
	h := newIsolatedPluginHarness(t, pluginAcceptanceAgents...)
	for _, agent := range pluginAcceptanceAgents {
		result, err := h.installPlugin(agent, "v0.1.0")
		if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
			t.Fatalf("Install(%s) = %+v, %v", agent, result, err)
		}
		h.assertManagedPlugin(agent, "v0.1.0", true)
	}
	if h.downloader.calls != 1 {
		t.Fatalf("Cloud Plugin downloads = %d, want 1", h.downloader.calls)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	artifact, err := h.plugins.Lookup(ref)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(h.cacheHome, "apm", "plugins", "demo-plugin", "v0.1.0")
	if artifact.Root != wantRoot {
		t.Fatalf("shared Artifact root = %q, want %q", artifact.Root, wantRoot)
	}
	for _, marketplace := range []string{
		filepath.Join(artifact.Root, ".agents", "plugins", "marketplace.json"),
		filepath.Join(artifact.Root, ".claude-plugin", "marketplace.json"),
	} {
		if info, err := os.Stat(marketplace); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("shared marketplace %q missing: %v", marketplace, err)
		}
	}
	assertCompleteAcceptancePluginTree(t, artifact.PluginPath)
	for _, agent := range []domain.AgentID{domain.AgentHermes, domain.AgentBibit} {
		target := h.pluginTarget(agent)
		assertCompleteAcceptancePluginTree(t, target)
		assertTreesEqual(t, artifact.PluginPath, target)
	}
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		source, err := os.ReadFile(h.marketplaceState(agent))
		if err != nil || string(source) != artifact.Root {
			t.Fatalf("marketplace source for %q = %q, %v; want shared root %q", agent, source, err, artifact.Root)
		}
	}
	h.assertOnlyFakeExecutables()
}

func assertCompleteAcceptancePluginTree(t *testing.T, root string) {
	t.Helper()
	for _, relative := range []string{
		"apc-plugin.json",
		filepath.Join(".codex-plugin", "plugin.json"),
		filepath.Join(".claude-plugin", "plugin.json"),
		filepath.Join(".hermes-plugin", "plugin.yaml"),
		filepath.Join(".hermes-plugin", "__init__.py"),
		filepath.Join("skills", "demo-plugin", "SKILL.md"),
		filepath.Join("skills", "demo-plugin", "skill-card.md"),
	} {
		if info, err := os.Stat(filepath.Join(root, relative)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("complete Plugin tree %q missing %q: %v", root, relative, err)
		}
	}
}

func assertTreesEqual(t *testing.T, wantRoot, gotRoot string) {
	t.Helper()
	snapshot := func(root string) map[string]string {
		result := make(map[string]string)
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[relative] = string(contents)
			return nil
		}); err != nil {
			t.Fatalf("snapshot Plugin tree %q: %v", root, err)
		}
		return result
	}
	want := snapshot(wantRoot)
	got := snapshot(gotRoot)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published Plugin tree %q differs from shared Artifact %q: got=%v want=%v", gotRoot, wantRoot, got, want)
	}
}

func TestPluginIntegrationRejectsUnmanagedTargetAndMarketplaceConflict(t *testing.T) {
	t.Run("unmanaged target", func(t *testing.T) {
		harness := newIsolatedPluginHarness(t, domain.AgentCodex)
		if err := os.WriteFile(filepath.Join(harness.state, "plugin-codex"), []byte("demo-plugin@foreign"), 0o600); err != nil {
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
	artifact, err := harness.plugins.Ensure(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	runtimeName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		t.Fatal(err)
	}
	marketplace, err := artifact.MarketplaceFor(domain.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	selector := runtimeName
	if err := os.WriteFile(filepath.Join(harness.state, "plugin-codex"), []byte(selector), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harness.state, "marketplace-codex"), []byte(marketplace.Source), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := transaction.Journal{
		SchemaVersion: 1, TransactionID: "00112233445566778899aabbccddeeff", Driver: transaction.DriverPlugin,
		Operation: transaction.OperationInstall, Phase: transaction.PhaseAddingNewPlugin, Agent: domain.AgentCodex, Power: ref,
		Plugin: &transaction.PluginJournalPayload{
			Deployment: domain.PluginDeploymentMarketplace,
			Target:     adapter.PluginTarget{ActualName: runtimeName, Selector: selector},
			Marketplace: &transaction.PluginMarketplaceJournalPayload{
				Name: marketplace.Name, TargetSource: marketplace.Source,
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
	actualName, err := domain.PluginRuntimeName(ref)
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
	cachePath := filepath.Join(h.cacheHome, "apm", "plugins", "demo-plugin", version)
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
	assertPluginHarnessEnvironment(h.t, h.fakeBin, filepath.Join(h.root, "user-home"))
	contents, err := os.ReadFile(h.calls)
	if errors.Is(err, os.ErrNotExist) {
		contents = nil
	} else if err != nil {
		h.t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		if line == "" {
			continue
		}
		path, _, found := strings.Cut(line, "|")
		within, withinErr := filesystem.PathEqualOrWithin(h.fakeBin, path)
		if !found || withinErr != nil || !within || path != filepath.Join(h.fakeBin, filepath.Base(path)) {
			h.t.Fatalf("Agent CLI invocation escaped fake-bin: %q", line)
		}
	}
	for _, path := range []string{
		os.Getenv("HOME"), os.Getenv("APM_HOME"), os.Getenv("XDG_CACHE_HOME"),
		os.Getenv("CODEX_HOME"), os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("HERMES_HOME"), os.Getenv("BIANBU_AGENT_HOME"),
	} {
		assertPathsWithinRoot(h.t, h.root, path)
	}
}
