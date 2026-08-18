package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
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

// acceptanceMarketplaceCLIScript 模拟最终 Adapter 使用的原生协议，并将事实写入对应 Agent home。
const acceptanceMarketplaceCLIScript = `#!/bin/sh
agent=${0##*/}
case "$0" in "$FAKE_PLUGIN_BIN"/*) ;; *) exit 97 ;; esac
case "$agent" in
  codex) home=$CODEX_HOME ;;
  claude) home=$CLAUDE_CONFIG_DIR ;;
  *) exit 96 ;;
esac
root=$home/.apm-fake
markets=$root/marketplaces
plugins=$root/plugins
/bin/mkdir -p "$markets" "$plugins"
printf '%s|' "$0" >> "$FAKE_PLUGIN_CALLS"
first=1
for argument in "$@"; do
  if [ "$first" = 1 ]; then first=0; else printf ' ' >> "$FAKE_PLUGIN_CALLS"; fi
  printf '%s' "$argument" >> "$FAKE_PLUGIN_CALLS"
done
printf '\n' >> "$FAKE_PLUGIN_CALLS"

unreadable() {
  [ -f "$FAKE_PLUGIN_UNREADABLE" ] && [ "$(/bin/cat "$FAKE_PLUGIN_UNREADABLE")" = "$1" ] && exit 42
  return 0
}
interrupt() {
  if [ -f "$FAKE_PLUGIN_INTERRUPT" ] && [ "$(/bin/cat "$FAKE_PLUGIN_INTERRUPT")" = "$1" ]; then
    /bin/rm -f "$FAKE_PLUGIN_INTERRUPT"
    exit 42
  fi
}
market_name() {
  if [ "$agent" = codex ]; then document=$1/.agents/plugins/marketplace.json
  else document=$1/.claude-plugin/marketplace.json
  fi
  /usr/bin/awk -F'"' '/"name"/ {print $4; exit}' "$document"
}

case "$agent:$1:$2:$3" in
  codex:plugin:marketplace:list)
    unreadable codex:marketplace-list
    printf '{"marketplaces":['
    separator=
    for entry in "$markets"/*; do
      [ -f "$entry" ] || continue
      name=${entry##*/}; source=$(/bin/cat "$entry")
      printf '%s{"name":"%s","marketplaceSource":{"sourceType":"local","source":"%s"}}' "$separator" "$name" "$source"
      separator=,
    done
    printf ']}\n' ;;
  claude:plugin:marketplace:list)
    unreadable claude:marketplace-list
    printf '['
    separator=
    for entry in "$markets"/*; do
      [ -f "$entry" ] || continue
      name=${entry##*/}; source=$(/bin/cat "$entry")
      printf '%s{"name":"%s","source":"directory","url":"%s"}' "$separator" "$name" "$source"
      separator=,
    done
    printf ']\n' ;;
  codex:plugin:marketplace:add|claude:plugin:marketplace:add)
    source=$4; name=$(market_name "$source")
    printf '%s' "$source" > "$markets/$name"
    interrupt "$agent:marketplace-add" ;;
  codex:plugin:marketplace:remove|claude:plugin:marketplace:remove)
    name=$4
    /bin/rm -f "$markets/$name"
    interrupt "$agent:marketplace-remove" ;;
  codex:plugin:list:--json)
    unreadable codex:plugin-list
    printf '{"installed":['
    separator=
    for entry in "$plugins"/*; do
      [ -f "$entry" ] || continue
      name=${entry##*/}; selector=$(/bin/cat "$entry")
      printf '%s{"name":"%s","pluginId":"%s","enabled":true}' "$separator" "$name" "$selector"
      separator=,
    done
    printf ']}\n' ;;
  claude:plugin:list:--json)
    unreadable claude:plugin-list
    printf '['
    separator=
    for entry in "$plugins"/*; do
      [ -f "$entry" ] || continue
      selector=$(/bin/cat "$entry")
      printf '%s{"id":"%s","scope":"user","enabled":true}' "$separator" "$selector"
      separator=,
    done
    printf ']\n' ;;
  codex:plugin:add:*|claude:plugin:install:*)
    selector=$3; name=${selector%@*}
    printf '%s' "$selector" > "$plugins/$name"
    interrupt "$agent:plugin-add" ;;
  codex:plugin:remove:*|claude:plugin:uninstall:*)
    selector=$3; name=${selector%@*}
    /bin/rm -f "$plugins/$name"
    interrupt "$agent:plugin-remove" ;;
  *) printf 'unsupported fake invocation: %s %s\n' "$agent" "$*" >&2; exit 98 ;;
esac
`

type pluginAcceptanceHarness struct {
	t          *testing.T
	root       string
	fakeBin    string
	calls      string
	interrupt  string
	unreadable string
	cacheHome  string
	agent      domain.AgentID
	agentHome  string
	repository *config.JSONRepository
	registry   *adapter.Registry
	plugins    pluginresource.Manager
	install    *InstallService
	uninstall  *UninstallService
	fix        *FixService
}

func newPluginAcceptanceHarness(t *testing.T, agent domain.AgentID) *pluginAcceptanceHarness {
	t.Helper()
	root := t.TempDir()
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(fakeBin, executable), []byte(acceptanceMarketplaceCLIScript), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	agentHomes := map[domain.AgentID]string{
		domain.AgentCodex:      filepath.Join(root, "agents", "codex"),
		domain.AgentClaudeCode: filepath.Join(root, "agents", "claude"),
	}
	agentHome := agentHomes[agent]
	if err := os.MkdirAll(agentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	resources := filepath.Join(root, "cloud")
	for _, logical := range []string{"demo-plugin", "side-plugin"} {
		for _, release := range []string{"v0.0.1", "v0.1.0"} {
			writeAcceptancePluginPackage(t, resources, agent, logical, release)
		}
	}
	calls := filepath.Join(root, "calls.log")
	interrupt := filepath.Join(root, "interrupt")
	unreadable := filepath.Join(root, "unreadable")
	cacheHome := filepath.Join(root, "cache")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
	t.Setenv("FAKE_PLUGIN_CALLS", calls)
	t.Setenv("FAKE_PLUGIN_INTERRUPT", interrupt)
	t.Setenv("FAKE_PLUGIN_UNREADABLE", unreadable)
	t.Setenv("APM_HOME", filepath.Join(root, "apm-home"))
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("CODEX_HOME", agentHomes[domain.AgentCodex])
	t.Setenv("CLAUDE_CONFIG_DIR", agentHomes[domain.AgentClaudeCode])
	t.Setenv("HERMES_HOME", filepath.Join(root, "agents", "hermes"))

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
	plugins, err := pluginresource.New(cloudClient)
	if err != nil {
		t.Fatal(err)
	}
	installer, err := NewInstall(locker, transactions, cloudClient, powerCache, plugins, registry, repository, version.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	uninstaller, err := NewUninstall(locker, transactions, cloudClient, plugins, registry, repository)
	if err != nil {
		t.Fatal(err)
	}
	fixer, err := NewFix(locker, transactions)
	if err != nil {
		t.Fatal(err)
	}
	return &pluginAcceptanceHarness{
		t: t, root: root, fakeBin: fakeBin, calls: calls, interrupt: interrupt, unreadable: unreadable,
		cacheHome: cacheHome, agent: agent, agentHome: agentHome, repository: repository, registry: registry,
		plugins: plugins, install: installer, uninstall: uninstaller, fix: fixer,
	}
}

func writeAcceptancePluginPackage(t *testing.T, root string, agent domain.AgentID, logical, release string) {
	t.Helper()
	nativeVersion := strings.TrimPrefix(release, "v")
	actual := logical + "-" + release
	var manifestDirectory, manifest string
	switch agent {
	case domain.AgentCodex:
		manifestDirectory = ".codex-plugin"
		manifest = `{"name":"` + actual + `","version":"` + nativeVersion + `","description":"acceptance fixture"}`
	case domain.AgentClaudeCode:
		manifestDirectory = ".claude-plugin"
		manifest = `{"name":"` + actual + `","version":"` + nativeVersion + `","description":"acceptance fixture"}`
	default:
		t.Fatalf("unsupported acceptance Agent %q", agent)
	}
	directory := filepath.Join(root, "plugins", logical, release, string(agent), manifestDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) ref(logical, release string) domain.PowerRef {
	return domain.PowerRef{Name: logical, Type: domain.PowerTypePlugin, Version: release}
}

func (h *pluginAcceptanceHarness) installRef(logical, release string) (cli.InstallResult, error) {
	return h.install.Install(context.Background(), cli.Flags{Agent: string(h.agent), Type: "plugin", Name: logical, Version: release})
}

func (h *pluginAcceptanceHarness) uninstallRef(logical string) (cli.UninstallResult, error) {
	return h.uninstall.Uninstall(context.Background(), cli.Flags{Agent: string(h.agent), Type: "plugin", Name: logical})
}

func (h *pluginAcceptanceHarness) stateRoot() string {
	return filepath.Join(h.agentHome, ".apm-fake")
}

func (h *pluginAcceptanceHarness) marketplacePath(logical string) string {
	return filepath.Join(h.stateRoot(), "marketplaces", "apm-"+logical)
}

func (h *pluginAcceptanceHarness) pluginPath(logical, release string) string {
	return filepath.Join(h.stateRoot(), "plugins", logical+"-"+release)
}

func (h *pluginAcceptanceHarness) artifact(logical, release string) pluginresource.Artifact {
	h.t.Helper()
	artifact, err := h.plugins.Lookup(h.agent, h.ref(logical, release))
	if err != nil {
		h.t.Fatal(err)
	}
	return artifact
}

func (h *pluginAcceptanceHarness) callLines() []string {
	h.t.Helper()
	contents, err := os.ReadFile(h.calls)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(contents))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func (h *pluginAcceptanceHarness) setInterrupt(key string) {
	h.t.Helper()
	if err := os.WriteFile(h.interrupt, []byte(key), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) setUnreadable(key string) {
	h.t.Helper()
	if err := os.WriteFile(h.unreadable, []byte(key), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) clearUnreadable() {
	h.t.Helper()
	if err := os.Remove(h.unreadable); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) setMarketplace(logical, source string) {
	h.t.Helper()
	path := h.marketplacePath(logical)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) setPlugin(logical, release string) {
	h.t.Helper()
	path := h.pluginPath(logical, release)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		h.t.Fatal(err)
	}
	selector := logical + "-" + release + "@apm-" + logical
	if err := os.WriteFile(path, []byte(selector), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) removeState(path string) {
	h.t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) assertManifest(logical, release string, present bool) {
	h.t.Helper()
	got, found, err := h.repository.Find(h.agent, domain.PowerTypePlugin, logical)
	if err != nil {
		h.t.Fatal(err)
	}
	if found != present {
		h.t.Fatalf("manifest %s found = %t, want %t; got=%+v", logical, found, present, got)
	}
	if present && got != h.ref(logical, release) {
		h.t.Fatalf("manifest %s = %+v, want %+v", logical, got, h.ref(logical, release))
	}
}

func (h *pluginAcceptanceHarness) assertMarketplace(logical, source string, present bool) {
	h.t.Helper()
	contents, err := os.ReadFile(h.marketplacePath(logical))
	if !present {
		if !errors.Is(err, os.ErrNotExist) {
			h.t.Fatalf("marketplace %s remains: contents=%q err=%v", logical, contents, err)
		}
		return
	}
	if err != nil || string(contents) != source {
		h.t.Fatalf("marketplace %s source = %q, err=%v, want %q", logical, contents, err, source)
	}
}

func (h *pluginAcceptanceHarness) assertPlugins(want ...string) {
	h.t.Helper()
	directory := filepath.Join(h.stateRoot(), "plugins")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		entries = nil
	} else if err != nil {
		h.t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			got = append(got, entry.Name())
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		h.t.Fatalf("installed Plugins = %v, want %v", got, want)
	}
}

func (h *pluginAcceptanceHarness) assertJournal(present bool) {
	h.t.Helper()
	path, err := paths.JournalPath()
	if err != nil {
		h.t.Fatal(err)
	}
	_, err = os.Lstat(path)
	if present && err != nil {
		h.t.Fatalf("journal missing: %v", err)
	}
	if !present && !errors.Is(err, os.ErrNotExist) {
		h.t.Fatalf("journal remains: %v", err)
	}
}

func (h *pluginAcceptanceHarness) assertOnlyFakeExecutables() {
	h.t.Helper()
	wantPrefix := h.fakeBin + string(filepath.Separator)
	for _, line := range h.callLines() {
		executable, _, found := strings.Cut(line, "|")
		if !found || !strings.HasPrefix(executable, wantPrefix) || executable == "/home/liangff/.local/bin/hermes" ||
			executable != filepath.Join(h.fakeBin, filepath.Base(executable)) {
			h.t.Fatalf("Agent executable escaped fake-bin: %q", line)
		}
	}
	if got, want := os.Getenv("PATH"), h.fakeBin+string(os.PathListSeparator)+"/usr/bin:/bin"; got != want {
		h.t.Fatalf("PATH = %q, want %q", got, want)
	}
	for _, path := range []string{os.Getenv("CODEX_HOME"), os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("HERMES_HOME"), os.Getenv("APM_HOME"), os.Getenv("XDG_CACHE_HOME")} {
		assertPathsWithinRoot(h.t, h.root, path)
	}
}

func assertAcceptanceMarketplaceDocument(t *testing.T, agent domain.AgentID, artifact pluginresource.Artifact, actualName string) {
	t.Helper()
	var path string
	if agent == domain.AgentCodex {
		path = filepath.Join(artifact.Root, ".agents", "plugins", "marketplace.json")
		var document struct {
			Plugins []struct {
				Name   string `json:"name"`
				Source struct {
					Path string `json:"path"`
				} `json:"source"`
			} `json:"plugins"`
		}
		decodeAcceptanceJSON(t, path, &document)
		if len(document.Plugins) != 1 || document.Plugins[0].Name != actualName || document.Plugins[0].Source.Path != "./plugins/"+actualName {
			t.Fatalf("Codex marketplace entries = %+v", document.Plugins)
		}
		return
	}
	path = filepath.Join(artifact.Root, ".claude-plugin", "marketplace.json")
	var document struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	decodeAcceptanceJSON(t, path, &document)
	if len(document.Plugins) != 1 || document.Plugins[0].Name != actualName || document.Plugins[0].Source != "./plugins/"+actualName {
		t.Fatalf("Claude marketplace entries = %+v", document.Plugins)
	}
}

func decodeAcceptanceJSON(t *testing.T, path string, target any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, target); err != nil {
		t.Fatalf("decode %q: %v", path, err)
	}
}

func TestPluginAcceptanceFirstRepeatUninstallAndIsolation(t *testing.T) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		t.Run(string(agent), func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, agent)
			result, err := h.installRef("demo-plugin", "v0.0.1")
			if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("first Install() = %+v, %v", result, err)
			}
			artifact := h.artifact("demo-plugin", "v0.0.1")
			h.assertManifest("demo-plugin", "v0.0.1", true)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
			h.assertPlugins("demo-plugin-v0.0.1")
			assertAcceptanceMarketplaceDocument(t, agent, artifact, "demo-plugin-v0.0.1")
			callsAfterFirst := len(h.callLines())

			result, err = h.installRef("demo-plugin", "v0.0.1")
			if err != nil || result.Items[0].Outcome != cli.InstallOutcomeUnchanged {
				t.Fatalf("repeat Install() = %+v, %v", result, err)
			}
			if got := len(h.callLines()); got != callsAfterFirst {
				t.Fatalf("manifest-first repeat grew calls from %d to %d", callsAfterFirst, got)
			}

			uninstalled, err := h.uninstallRef("demo-plugin")
			if err != nil || len(uninstalled.Items) != 1 || uninstalled.Items[0].Outcome != cli.UninstallOutcomeCompleted {
				t.Fatalf("Uninstall() = %+v, %v", uninstalled, err)
			}
			h.assertManifest("demo-plugin", "", false)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
			h.assertPlugins()
			if info, statErr := os.Stat(artifact.Root); statErr != nil || !info.IsDir() {
				t.Fatalf("retained cache = %v, %v", info, statErr)
			}
			callsAfterUninstall := len(h.callLines())
			uninstalled, err = h.uninstallRef("demo-plugin")
			if err != nil || uninstalled.Items[0].Outcome != cli.UninstallOutcomeUnexecuted || uninstalled.Items[0].Power != nil {
				t.Fatalf("repeat Uninstall() = %+v, %v", uninstalled, err)
			}
			if got := len(h.callLines()); got != callsAfterUninstall {
				t.Fatalf("manifest-first repeat uninstall grew calls from %d to %d", callsAfterUninstall, got)
			}
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestPluginAcceptanceUpgradeDowngradeAndMultiPluginIsolation(t *testing.T) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		t.Run(string(agent), func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, agent)
			if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("install demo v0.0.1 = %+v, %v", result, err)
			}
			if result, err := h.installRef("side-plugin", "v0.0.1"); err != nil || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("install side v0.0.1 = %+v, %v", result, err)
			}
			sideSource := h.artifact("side-plugin", "v0.0.1").MarketplaceSource
			for _, step := range []struct {
				release string
				outcome cli.InstallOutcome
			}{
				{release: "v0.1.0", outcome: cli.InstallOutcomeUpgraded},
				{release: "v0.0.1", outcome: cli.InstallOutcomeDowngraded},
			} {
				result, err := h.installRef("demo-plugin", step.release)
				if err != nil || result.Items[0].Outcome != step.outcome {
					t.Fatalf("install demo %s = %+v, %v", step.release, result, err)
				}
				artifact := h.artifact("demo-plugin", step.release)
				h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
				h.assertMarketplace("side-plugin", sideSource, true)
				h.assertPlugins("demo-plugin-"+step.release, "side-plugin-v0.0.1")
				h.assertManifest("demo-plugin", step.release, true)
				assertAcceptanceMarketplaceDocument(t, agent, artifact, "demo-plugin-"+step.release)
			}
			for _, release := range []string{"v0.0.1", "v0.1.0"} {
				if info, err := os.Stat(h.artifact("demo-plugin", release).Root); err != nil || !info.IsDir() {
					t.Fatalf("immutable demo cache %s missing: %v", release, err)
				}
			}
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestPluginAcceptanceManifestAndAgentAnomalyTable(t *testing.T) {
	t.Run("same version broken state is unchanged without calls", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		if _, err := h.installRef("demo-plugin", "v0.0.1"); err != nil {
			t.Fatal(err)
		}
		artifact := h.artifact("demo-plugin", "v0.0.1")
		h.removeState(h.marketplacePath("demo-plugin"))
		h.removeState(h.pluginPath("demo-plugin", "v0.0.1"))
		before := len(h.callLines())
		result, err := h.installRef("demo-plugin", "v0.0.1")
		if err != nil || result.Items[0].Outcome != cli.InstallOutcomeUnchanged || len(h.callLines()) != before {
			t.Fatalf("same-version broken Install() = %+v, %v; calls=%d want=%d", result, err, len(h.callLines()), before)
		}
		h.assertManifest("demo-plugin", "v0.0.1", true)
		if _, err := os.Stat(artifact.Root); err != nil {
			t.Fatal(err)
		}
		h.assertOnlyFakeExecutables()
	})

	t.Run("stale old double absent cleans and installs", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		if err := h.repository.Set(h.agent, h.ref("demo-plugin", "v0.0.1")); err != nil {
			t.Fatal(err)
		}
		result, err := h.installRef("demo-plugin", "v0.1.0")
		if err != nil || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
			t.Fatalf("stale Install() = %+v, %v", result, err)
		}
		artifact := h.artifact("demo-plugin", "v0.1.0")
		h.assertManifest("demo-plugin", "v0.1.0", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.1.0")
		h.assertOnlyFakeExecutables()
	})

	for _, test := range []struct {
		name   string
		mutate func(*pluginAcceptanceHarness, pluginresource.Artifact)
	}{
		{name: "marketplace only", mutate: func(h *pluginAcceptanceHarness, _ pluginresource.Artifact) {
			h.removeState(h.pluginPath("demo-plugin", "v0.0.1"))
		}},
		{name: "Plugin only", mutate: func(h *pluginAcceptanceHarness, _ pluginresource.Artifact) {
			h.removeState(h.marketplacePath("demo-plugin"))
		}},
		{name: "source conflict", mutate: func(h *pluginAcceptanceHarness, _ pluginresource.Artifact) {
			h.setMarketplace("demo-plugin", filepath.Join(h.root, "foreign"))
		}},
		{name: "missing old cache", mutate: func(h *pluginAcceptanceHarness, artifact pluginresource.Artifact) {
			if err := os.RemoveAll(artifact.Root); err != nil {
				h.t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name+" fails closed", func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			if _, err := h.installRef("demo-plugin", "v0.0.1"); err != nil {
				t.Fatal(err)
			}
			old := h.artifact("demo-plugin", "v0.0.1")
			test.mutate(h, old)
			marketBefore, marketErr := os.ReadFile(h.marketplacePath("demo-plugin"))
			pluginBefore, pluginErr := os.ReadFile(h.pluginPath("demo-plugin", "v0.0.1"))
			if _, err := h.installRef("demo-plugin", "v0.1.0"); err == nil {
				t.Fatal("Install() error = nil, want fail closed")
			}
			h.assertManifest("demo-plugin", "v0.0.1", true)
			assertOptionalFileUnchanged(t, h.marketplacePath("demo-plugin"), marketBefore, marketErr)
			assertOptionalFileUnchanged(t, h.pluginPath("demo-plugin", "v0.0.1"), pluginBefore, pluginErr)
			h.assertJournal(false)
			h.assertOnlyFakeExecutables()
		})
	}

	for _, test := range []struct {
		name        string
		marketplace bool
		plugin      bool
	}{
		{name: "marketplace only", marketplace: true},
		{name: "Plugin only", plugin: true},
		{name: "both", marketplace: true, plugin: true},
	} {
		t.Run("no manifest "+test.name+" remains unmanaged", func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			artifact, err := h.plugins.Ensure(context.Background(), h.agent, h.ref("demo-plugin", "v0.0.1"))
			if err != nil {
				t.Fatal(err)
			}
			if test.marketplace {
				h.setMarketplace("demo-plugin", artifact.MarketplaceSource)
			}
			if test.plugin {
				h.setPlugin("demo-plugin", "v0.0.1")
			}
			if _, err := h.installRef("demo-plugin", "v0.0.1"); err == nil {
				t.Fatal("Install() error = nil, want unmanaged conflict")
			}
			h.assertManifest("demo-plugin", "", false)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, test.marketplace)
			if test.plugin {
				h.assertPlugins("demo-plugin-v0.0.1")
			} else {
				h.assertPlugins()
			}
			h.assertJournal(false)
			h.assertOnlyFakeExecutables()
		})
	}
}

func assertOptionalFileUnchanged(t *testing.T, path string, want []byte, wantErr error) {
	t.Helper()
	got, err := os.ReadFile(path)
	if errors.Is(wantErr, os.ErrNotExist) {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%q appeared: contents=%q err=%v", path, got, err)
		}
		return
	}
	if err != nil || string(got) != string(want) {
		t.Fatalf("%q changed: got=%q err=%v want=%q", path, got, err, want)
	}
}

func (h *pluginAcceptanceHarness) writeInstallJournal(logical, targetRelease, previousRelease string, phase transaction.Phase) string {
	h.t.Helper()
	target := h.artifact(logical, targetRelease)
	journal := transaction.Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        transaction.DriverPlugin,
		Operation:     transaction.OperationInstall,
		Phase:         phase,
		Agent:         h.agent,
		Power:         h.ref(logical, targetRelease),
		Plugin: &transaction.PluginJournalPayload{
			Deployment: domain.PluginDeploymentMarketplace,
			Target: adapter.PluginTarget{
				ActualName: target.ActualName,
				Selector:   target.ActualName + "@" + target.MarketplaceName,
			},
			Marketplace: &transaction.PluginMarketplaceJournalPayload{
				Name:         target.MarketplaceName,
				TargetSource: target.MarketplaceSource,
			},
		},
	}
	if previousRelease != "" {
		previous := h.artifact(logical, previousRelease)
		previousRef := h.ref(logical, previousRelease)
		previousTarget := adapter.PluginTarget{
			ActualName: previous.ActualName,
			Selector:   previous.ActualName + "@" + previous.MarketplaceName,
		}
		journal.Previous = &previousRef
		journal.Plugin.Previous = &previousTarget
		journal.Plugin.PreviousPresent = true
		journal.Plugin.Marketplace.PreviousPresent = true
		journal.Plugin.Marketplace.PreviousSource = previous.MarketplaceSource
	}
	return writeIntegrationJournal(h.t, journal)
}

func (h *pluginAcceptanceHarness) writeUninstallJournal(logical, release string, phase transaction.Phase) string {
	h.t.Helper()
	artifact := h.artifact(logical, release)
	return writeIntegrationJournal(h.t, transaction.Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        transaction.DriverPlugin,
		Operation:     transaction.OperationUninstall,
		Phase:         phase,
		Agent:         h.agent,
		Power:         h.ref(logical, release),
		Plugin: &transaction.PluginJournalPayload{
			Deployment:    domain.PluginDeploymentMarketplace,
			TargetPresent: true,
			Target: adapter.PluginTarget{
				ActualName: artifact.ActualName,
				Selector:   artifact.ActualName + "@" + artifact.MarketplaceName,
			},
			Marketplace: &transaction.PluginMarketplaceJournalPayload{
				Name:         artifact.MarketplaceName,
				TargetSource: artifact.MarketplaceSource,
			},
		},
	})
}

func TestFixPluginAcceptanceFirstInstallJournalPhases(t *testing.T) {
	for _, test := range []struct {
		name            string
		phase           transaction.Phase
		marketplace     bool
		plugin          bool
		wantCommitted   bool
		manifestAlready bool
	}{
		{name: "registering intent before marketplace", phase: transaction.PhaseRegisteringMarketplace},
		{name: "add intent after marketplace", phase: transaction.PhaseAddingNewPlugin, marketplace: true},
		{name: "rollback direction persisted", phase: transaction.PhaseRollingBack, marketplace: true, plugin: true},
		{name: "committing before manifest", phase: transaction.PhaseCommitting, marketplace: true, plugin: true, wantCommitted: true},
		{name: "committing after manifest", phase: transaction.PhaseCommitting, marketplace: true, plugin: true, wantCommitted: true, manifestAlready: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			artifact, err := h.plugins.Ensure(context.Background(), h.agent, h.ref("demo-plugin", "v0.0.1"))
			if err != nil {
				t.Fatal(err)
			}
			if test.marketplace {
				h.setMarketplace("demo-plugin", artifact.MarketplaceSource)
			}
			if test.plugin {
				h.setPlugin("demo-plugin", "v0.0.1")
			}
			if test.manifestAlready {
				if err := h.repository.Set(h.agent, h.ref("demo-plugin", "v0.0.1")); err != nil {
					t.Fatal(err)
				}
			}
			h.writeInstallJournal("demo-plugin", "v0.0.1", "", test.phase)
			if err := h.fix.Fix(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.assertJournal(false)
			h.assertManifest("demo-plugin", "v0.0.1", test.wantCommitted)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, test.wantCommitted)
			if test.wantCommitted {
				h.assertPlugins("demo-plugin-v0.0.1")
			} else {
				h.assertPlugins()
			}
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestFixPluginAcceptanceVersionSwitchJournalPhases(t *testing.T) {
	for _, test := range []struct {
		name          string
		phase         transaction.Phase
		marketplace   string
		oldPlugin     bool
		targetPlugin  bool
		wantCommitted bool
	}{
		{name: "remove old intent", phase: transaction.PhaseRemovingOldPlugin, marketplace: "old", oldPlugin: true},
		{name: "unregister old intent", phase: transaction.PhaseUnregisteringOldMarketplace, marketplace: "old"},
		{name: "register new intent", phase: transaction.PhaseRegisteringNewMarketplace},
		{name: "add target intent", phase: transaction.PhaseAddingNewPlugin, marketplace: "target"},
		{name: "rollback direction persisted", phase: transaction.PhaseRollingBack, marketplace: "target", targetPlugin: true},
		{name: "commit target intent", phase: transaction.PhaseCommitting, marketplace: "target", targetPlugin: true, wantCommitted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			old, err := h.plugins.Ensure(context.Background(), h.agent, h.ref("demo-plugin", "v0.0.1"))
			if err != nil {
				t.Fatal(err)
			}
			target, err := h.plugins.Ensure(context.Background(), h.agent, h.ref("demo-plugin", "v0.1.0"))
			if err != nil {
				t.Fatal(err)
			}
			if err := h.repository.Set(h.agent, h.ref("demo-plugin", "v0.0.1")); err != nil {
				t.Fatal(err)
			}
			switch test.marketplace {
			case "old":
				h.setMarketplace("demo-plugin", old.MarketplaceSource)
			case "target":
				h.setMarketplace("demo-plugin", target.MarketplaceSource)
			}
			if test.oldPlugin {
				h.setPlugin("demo-plugin", "v0.0.1")
			}
			if test.targetPlugin {
				h.setPlugin("demo-plugin", "v0.1.0")
			}
			h.writeInstallJournal("demo-plugin", "v0.1.0", "v0.0.1", test.phase)
			if err := h.fix.Fix(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.assertJournal(false)
			if test.wantCommitted {
				h.assertManifest("demo-plugin", "v0.1.0", true)
				h.assertMarketplace("demo-plugin", target.MarketplaceSource, true)
				h.assertPlugins("demo-plugin-v0.1.0")
			} else {
				h.assertManifest("demo-plugin", "v0.0.1", true)
				h.assertMarketplace("demo-plugin", old.MarketplaceSource, true)
				h.assertPlugins("demo-plugin-v0.0.1")
			}
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestFixPluginAcceptanceRecoversEveryMarketplaceCLIMutation(t *testing.T) {
	for _, test := range []struct {
		name           string
		firstInstall   bool
		interrupt      string
		wantCommitted  bool
		useNextInstall bool
	}{
		{name: "first marketplace registered unknown", firstInstall: true, interrupt: "codex:marketplace-add", useNextInstall: true},
		{name: "first Plugin added unknown", firstInstall: true, interrupt: "codex:plugin-add", wantCommitted: true},
		{name: "old Plugin removed unknown", interrupt: "codex:plugin-remove"},
		{name: "old marketplace removed unknown", interrupt: "codex:marketplace-remove"},
		{name: "target marketplace registered unknown", interrupt: "codex:marketplace-add"},
		{name: "target Plugin added unknown", interrupt: "codex:plugin-add", wantCommitted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			if !test.firstInstall {
				if _, err := h.installRef("demo-plugin", "v0.0.1"); err != nil {
					t.Fatal(err)
				}
			}
			h.setInterrupt(test.interrupt)
			targetRelease := "v0.1.0"
			if test.firstInstall {
				targetRelease = "v0.0.1"
			}
			if _, err := h.installRef("demo-plugin", targetRelease); err == nil {
				t.Fatal("interrupted Install() error = nil")
			}
			h.assertJournal(true)
			wantCommitted := test.wantCommitted
			if test.useNextInstall {
				result, err := h.installRef("demo-plugin", "v0.0.1")
				if err != nil || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
					t.Fatalf("next Install() = %+v, %v", result, err)
				}
				wantCommitted = true
			} else if err := h.fix.Fix(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.assertJournal(false)
			if wantCommitted {
				release := "v0.1.0"
				if test.firstInstall {
					release = "v0.0.1"
				}
				artifact := h.artifact("demo-plugin", release)
				h.assertManifest("demo-plugin", release, true)
				h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
				h.assertPlugins("demo-plugin-" + release)
			} else {
				old := h.artifact("demo-plugin", "v0.0.1")
				h.assertManifest("demo-plugin", "v0.0.1", true)
				h.assertMarketplace("demo-plugin", old.MarketplaceSource, true)
				h.assertPlugins("demo-plugin-v0.0.1")
			}
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestFixPluginAcceptanceUnknownFactsRetainJournalUntilReadable(t *testing.T) {
	h := newPluginAcceptanceHarness(t, domain.AgentCodex)
	artifact, err := h.plugins.Ensure(context.Background(), h.agent, h.ref("demo-plugin", "v0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	h.setMarketplace("demo-plugin", artifact.MarketplaceSource)
	h.writeInstallJournal("demo-plugin", "v0.0.1", "", transaction.PhaseAddingNewPlugin)
	h.setUnreadable("codex:marketplace-list")
	if err := h.fix.Fix(context.Background()); !errors.Is(err, transaction.ErrRecoveryFailed) || !errors.Is(err, transaction.ErrIndeterminateState) {
		t.Fatalf("Fix() error = %v, want recovery/indeterminate", err)
	}
	h.assertJournal(true)
	h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
	h.assertManifest("demo-plugin", "", false)
	h.clearUnreadable()
	if err := h.fix.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.assertJournal(false)
	h.assertMarketplace("demo-plugin", "", false)
	h.assertPlugins()
	h.assertManifest("demo-plugin", "", false)
	h.assertOnlyFakeExecutables()
}

func TestFixPluginAcceptanceUninstallInterruptionTable(t *testing.T) {
	for _, test := range []struct {
		name            string
		phase           transaction.Phase
		pluginPresent   bool
		manifestPresent bool
		wantManifest    bool
	}{
		{name: "before remove rolls back", phase: transaction.PhaseRemovingPlugin, pluginPresent: true, manifestPresent: true, wantManifest: true},
		{name: "rollback direction persisted", phase: transaction.PhaseRollingBack, pluginPresent: true, manifestPresent: true, wantManifest: true},
		{name: "after remove commits", phase: transaction.PhaseRemovingPlugin, manifestPresent: true},
		{name: "before manifest commit", phase: transaction.PhaseCommitting, manifestPresent: true},
		{name: "after manifest commit", phase: transaction.PhaseCommitting},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			artifact, err := h.plugins.Ensure(context.Background(), h.agent, h.ref("demo-plugin", "v0.0.1"))
			if err != nil {
				t.Fatal(err)
			}
			h.setMarketplace("demo-plugin", artifact.MarketplaceSource)
			if test.pluginPresent {
				h.setPlugin("demo-plugin", "v0.0.1")
			}
			if test.manifestPresent {
				if err := h.repository.Set(h.agent, h.ref("demo-plugin", "v0.0.1")); err != nil {
					t.Fatal(err)
				}
			}
			h.writeUninstallJournal("demo-plugin", "v0.0.1", test.phase)
			if err := h.fix.Fix(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.assertJournal(false)
			h.assertManifest("demo-plugin", "v0.0.1", test.wantManifest)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
			if test.pluginPresent {
				h.assertPlugins("demo-plugin-v0.0.1")
			} else {
				h.assertPlugins()
			}
			h.assertOnlyFakeExecutables()
		})
	}

	t.Run("CLI removal unknown commits without removing marketplace", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		if _, err := h.installRef("demo-plugin", "v0.0.1"); err != nil {
			t.Fatal(err)
		}
		artifact := h.artifact("demo-plugin", "v0.0.1")
		h.setInterrupt("codex:plugin-remove")
		if _, err := h.uninstallRef("demo-plugin"); err == nil {
			t.Fatal("interrupted Uninstall() error = nil")
		}
		h.assertJournal(true)
		if err := h.fix.Fix(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.assertJournal(false)
		h.assertManifest("demo-plugin", "", false)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins()
		h.assertOnlyFakeExecutables()
	})
}
