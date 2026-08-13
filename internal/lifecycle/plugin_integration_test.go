package lifecycle

import (
	"context"
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
	"apm/internal/transaction"
	"apm/internal/version"
)

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
case "$agent:$1:$2:$3" in
  codex:plugin:marketplace:list)
    if [ -f "$market" ]; then
      printf '{"marketplaces":[{"name":"apm","marketplaceSource":{"sourceType":"local","source":"%s"}}]}\n' "$FAKE_PLUGIN_MARKETPLACE"
    else printf '{"marketplaces":[]}\n'; fi ;;
  codex:plugin:marketplace:add) : > "$market" ;;
  codex:plugin:marketplace:upgrade) : ;;
  codex:plugin:list:--json)
    if [ -f "$plugin" ]; then id=$(/bin/cat "$plugin"); name=${id%@*}; printf '{"installed":[{"name":"%s","pluginId":"%s","enabled":false}]}\n' "$name" "$id"
    else printf '{"installed":[]}\n'; fi ;;
  codex:plugin:add:*) printf '%s' "$3" > "$plugin" ;;
  codex:plugin:remove:*) /bin/rm -f "$plugin" ;;
  claude:plugin:marketplace:list)
    if [ -f "$market" ]; then printf '[{"name":"apm","source":"directory","url":"%s"}]\n' "$FAKE_PLUGIN_MARKETPLACE"
    else printf '[]\n'; fi ;;
  claude:plugin:marketplace:add) : > "$market" ;;
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
	installer, err := NewInstall(locker, transactions, cloudClient, powerCache, registry, repository, version.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	uninstaller, err := NewUninstall(locker, transactions, cloudClient, registry, repository)
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
	}
	result, err := uninstaller.Uninstall(context.Background(), cli.Flags{Agent: string(domain.AgentHermes), Type: "plugin", Name: "demo-plugin"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeCompleted {
		t.Fatalf("uninstall = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(hermesHome, "plugins", "demo-plugin-v0.0.1")); !os.IsNotExist(err) {
		t.Fatalf("Hermes target remains: %v", err)
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
			installer, _ := NewInstall(locker, transactions, cloudClient, powerCache, registry, repository, version.NewSelector())
			uninstaller, _ := NewUninstall(locker, transactions, cloudClient, registry, repository)
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
			if _, err := os.Stat(filepath.Join(cacheHome, "apm", "plugins")); !os.IsNotExist(err) {
				t.Fatalf("marketplace install touched Plugin cache: %v", err)
			}
		})
	}
}
