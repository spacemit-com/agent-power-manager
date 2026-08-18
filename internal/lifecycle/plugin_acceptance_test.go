package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
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
require_count() {
  [ "$2" = "$1" ] || exit 98
}
barrier() {
  if [ -f "$FAKE_PLUGIN_BARRIER_KEY" ] && [ "$(/bin/cat "$FAKE_PLUGIN_BARRIER_KEY")" = "$1" ]; then
    printf '%s' "$1" > "$FAKE_PLUGIN_BARRIER_EVENT"
    /bin/cat "$FAKE_PLUGIN_BARRIER_GATE" >/dev/null
  fi
}
self_remove() {
  if [ -f "$FAKE_PLUGIN_SELF_REMOVE" ] && [ "$(/bin/cat "$FAKE_PLUGIN_SELF_REMOVE")" = "$1" ]; then
    /bin/rm -f "$FAKE_PLUGIN_SELF_REMOVE" "$0"
  fi
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
    require_count 4 "$#"; [ "$4" = --json ] || exit 98
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
    require_count 4 "$#"; [ "$4" = --json ] || exit 98
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
  codex:plugin:marketplace:add)
    require_count 5 "$#"; [ "$5" = --json ] || exit 98
    source=$4; name=$(market_name "$source")
    barrier codex:marketplace-add
    printf '%s' "$source" > "$markets/$name"
    self_remove codex:marketplace-add
    interrupt codex:marketplace-add ;;
  claude:plugin:marketplace:add)
    require_count 6 "$#"; [ "$5" = --scope ] && [ "$6" = user ] || exit 98
    source=$4; name=$(market_name "$source")
    barrier claude:marketplace-add
    printf '%s' "$source" > "$markets/$name"
    self_remove claude:marketplace-add
    interrupt claude:marketplace-add ;;
  codex:plugin:marketplace:remove)
    require_count 5 "$#"; [ "$5" = --json ] || exit 98
    name=$4
    barrier codex:marketplace-remove
    /bin/rm -f "$markets/$name"
    self_remove codex:marketplace-remove
    interrupt codex:marketplace-remove ;;
  claude:plugin:marketplace:remove)
    require_count 6 "$#"; [ "$5" = --scope ] && [ "$6" = user ] || exit 98
    name=$4
    barrier claude:marketplace-remove
    /bin/rm -f "$markets/$name"
    self_remove claude:marketplace-remove
    interrupt claude:marketplace-remove ;;
  codex:plugin:list:--json)
    require_count 3 "$#"
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
    require_count 3 "$#"
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
  codex:plugin:add:*)
    require_count 4 "$#"; [ "$4" = --json ] || exit 98
    selector=$3; name=${selector%@*}
    barrier codex:plugin-add
    printf '%s' "$selector" > "$plugins/$name"
    self_remove codex:plugin-add
    interrupt codex:plugin-add ;;
  claude:plugin:install:*)
    require_count 5 "$#"; [ "$4" = --scope ] && [ "$5" = user ] || exit 98
    selector=$3; name=${selector%@*}
    barrier claude:plugin-add
    printf '%s' "$selector" > "$plugins/$name"
    self_remove claude:plugin-add
    interrupt claude:plugin-add ;;
  codex:plugin:remove:*)
    require_count 4 "$#"; [ "$4" = --json ] || exit 98
    selector=$3; name=${selector%@*}
    barrier codex:plugin-remove
    /bin/rm -f "$plugins/$name"
    self_remove codex:plugin-remove
    interrupt codex:plugin-remove ;;
  claude:plugin:uninstall:*)
    require_count 5 "$#"; [ "$4" = --scope ] && [ "$5" = user ] || exit 98
    selector=$3; name=${selector%@*}
    barrier claude:plugin-remove
    /bin/rm -f "$plugins/$name"
    self_remove claude:plugin-remove
    interrupt claude:plugin-remove ;;
  *) printf 'unsupported fake invocation: %s %s\n' "$agent" "$*" >&2; exit 98 ;;
esac
`

type pluginAcceptanceHarness struct {
	t            *testing.T
	root         string
	fakeBin      string
	calls        string
	interrupt    string
	unreadable   string
	barrierKey   string
	barrierEvent string
	barrierGate  string
	selfRemove   string
	cacheHome    string
	agent        domain.AgentID
	agentHome    string
	repository   *config.JSONRepository
	manifests    *acceptanceManifestRepository
	registry     *adapter.Registry
	plugins      pluginresource.Manager
	install      *InstallService
	uninstall    *UninstallService
	fix          *FixService
}

type acceptanceManifestRepository struct {
	base      *config.JSONRepository
	setErr    error
	removeErr error
}

type acceptanceInstallAnswer struct {
	result cli.InstallResult
	err    error
}

type acceptanceUninstallAnswer struct {
	result cli.UninstallResult
	err    error
}

type acceptanceBarrierAnswer struct {
	value string
	err   error
}

func (r *acceptanceManifestRepository) List(agent domain.AgentID) (domain.PowerGroups, error) {
	return r.base.List(agent)
}

func (r *acceptanceManifestRepository) Find(agent domain.AgentID, powerType domain.PowerType, name string) (domain.PowerRef, bool, error) {
	return r.base.Find(agent, powerType, name)
}

func (r *acceptanceManifestRepository) Set(agent domain.AgentID, power domain.PowerRef) error {
	if r.setErr != nil {
		return r.setErr
	}
	return r.base.Set(agent, power)
}

func (r *acceptanceManifestRepository) Remove(agent domain.AgentID, powerType domain.PowerType, name string) error {
	if r.removeErr != nil {
		return r.removeErr
	}
	return r.base.Remove(agent, powerType, name)
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
	barrierKey := filepath.Join(root, "barrier-key")
	barrierEvent := filepath.Join(root, "barrier-event")
	barrierGate := filepath.Join(root, "barrier-gate")
	selfRemove := filepath.Join(root, "self-remove")
	cacheHome := filepath.Join(root, "cache")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("FAKE_PLUGIN_BIN", fakeBin)
	t.Setenv("FAKE_PLUGIN_CALLS", calls)
	t.Setenv("FAKE_PLUGIN_INTERRUPT", interrupt)
	t.Setenv("FAKE_PLUGIN_UNREADABLE", unreadable)
	t.Setenv("FAKE_PLUGIN_BARRIER_KEY", barrierKey)
	t.Setenv("FAKE_PLUGIN_BARRIER_EVENT", barrierEvent)
	t.Setenv("FAKE_PLUGIN_BARRIER_GATE", barrierGate)
	t.Setenv("FAKE_PLUGIN_SELF_REMOVE", selfRemove)
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
	powerCache, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := pluginresource.New(cloudClient)
	if err != nil {
		t.Fatal(err)
	}
	installer, err := NewInstall(locker, transactions, cloudClient, powerCache, plugins, registry, manifests, version.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	uninstaller, err := NewUninstall(locker, transactions, cloudClient, plugins, registry, manifests)
	if err != nil {
		t.Fatal(err)
	}
	fixer, err := NewFix(locker, transactions)
	if err != nil {
		t.Fatal(err)
	}
	return &pluginAcceptanceHarness{
		t: t, root: root, fakeBin: fakeBin, calls: calls, interrupt: interrupt, unreadable: unreadable,
		barrierKey: barrierKey, barrierEvent: barrierEvent, barrierGate: barrierGate, selfRemove: selfRemove,
		cacheHome: cacheHome, agent: agent, agentHome: agentHome, repository: repository, manifests: manifests, registry: registry,
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

func (h *pluginAcceptanceHarness) configureBarrier(key string) {
	h.t.Helper()
	for _, path := range []string{h.barrierEvent, h.barrierGate} {
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := os.WriteFile(h.barrierKey, []byte(key), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) disableBarrier() {
	h.t.Helper()
	if err := os.Remove(h.barrierKey); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) barrierEvents() <-chan acceptanceBarrierAnswer {
	answers := make(chan acceptanceBarrierAnswer, 1)
	go func() {
		event, err := os.Open(h.barrierEvent)
		if err != nil {
			answers <- acceptanceBarrierAnswer{err: err}
			return
		}
		contents, readErr := io.ReadAll(event)
		closeErr := event.Close()
		answers <- acceptanceBarrierAnswer{value: string(contents), err: errors.Join(readErr, closeErr)}
	}()
	return answers
}

func (h *pluginAcceptanceHarness) startInstall(ctx context.Context, logical, release string) <-chan acceptanceInstallAnswer {
	answers := make(chan acceptanceInstallAnswer, 1)
	go func() {
		result, err := h.install.Install(ctx, cli.Flags{Agent: string(h.agent), Type: "plugin", Name: logical, Version: release})
		answers <- acceptanceInstallAnswer{result: result, err: err}
	}()
	return answers
}

func (h *pluginAcceptanceHarness) startUninstall(ctx context.Context, logical string) <-chan acceptanceUninstallAnswer {
	answers := make(chan acceptanceUninstallAnswer, 1)
	go func() {
		result, err := h.uninstall.Uninstall(ctx, cli.Flags{Agent: string(h.agent), Type: "plugin", Name: logical})
		answers <- acceptanceUninstallAnswer{result: result, err: err}
	}()
	return answers
}

func (h *pluginAcceptanceHarness) waitInstallBarrier(key string, completed <-chan acceptanceInstallAnswer) {
	h.t.Helper()
	select {
	case event := <-h.barrierEvents():
		if event.err != nil || event.value != key {
			h.t.Fatalf("barrier event = %q, err=%v, want %q", event.value, event.err, key)
		}
	case answer := <-completed:
		h.t.Fatalf("Install completed before barrier: %+v, %v", answer.result, answer.err)
	}
}

func (h *pluginAcceptanceHarness) waitUninstallBarrier(key string, completed <-chan acceptanceUninstallAnswer) {
	h.t.Helper()
	select {
	case event := <-h.barrierEvents():
		if event.err != nil || event.value != key {
			h.t.Fatalf("barrier event = %q, err=%v, want %q", event.value, event.err, key)
		}
	case answer := <-completed:
		h.t.Fatalf("Uninstall completed before barrier: %+v, %v", answer.result, answer.err)
	}
}

func (h *pluginAcceptanceHarness) setSelfRemove(key string) {
	h.t.Helper()
	if err := os.WriteFile(h.selfRemove, []byte(key), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) restoreExecutable() {
	h.t.Helper()
	executable := "codex"
	if h.agent == domain.AgentClaudeCode {
		executable = "claude"
	}
	if err := os.WriteFile(filepath.Join(h.fakeBin, executable), []byte(acceptanceMarketplaceCLIScript), 0o700); err != nil {
		h.t.Fatal(err)
	}
}

func (h *pluginAcceptanceHarness) currentJournal() transaction.Journal {
	h.t.Helper()
	journalPath, err := paths.JournalPath()
	if err != nil {
		h.t.Fatal(err)
	}
	contents, err := os.ReadFile(journalPath)
	if err != nil {
		h.t.Fatal(err)
	}
	var journal transaction.Journal
	if err := json.Unmarshal(contents, &journal); err != nil {
		h.t.Fatal(err)
	}
	return journal
}

func (h *pluginAcceptanceHarness) assertPhase(want transaction.Phase) {
	h.t.Helper()
	if got := h.currentJournal().Phase; got != want {
		h.t.Fatalf("journal phase = %q, want %q", got, want)
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

func (h *pluginAcceptanceHarness) expectedCall(arguments ...string) string {
	h.t.Helper()
	executable := "codex"
	if h.agent == domain.AgentClaudeCode {
		executable = "claude"
	}
	return filepath.Join(h.fakeBin, executable) + "|" + strings.Join(arguments, " ")
}

func (h *pluginAcceptanceHarness) assertCallsSince(start int, want ...string) {
	h.t.Helper()
	all := h.callLines()
	if start < 0 || start > len(all) {
		h.t.Fatalf("call start %d outside %d calls", start, len(all))
	}
	got := all[start:]
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		h.t.Fatalf("calls since %d:\n%s\nwant:\n%s", start, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func (h *pluginAcceptanceHarness) assertNoMutationCallsSince(start int) {
	h.t.Helper()
	for _, line := range h.callLines()[start:] {
		_, argv, found := strings.Cut(line, "|")
		if !found {
			h.t.Fatalf("malformed call log %q", line)
		}
		if strings.HasPrefix(argv, "plugin marketplace add ") || strings.HasPrefix(argv, "plugin marketplace remove ") ||
			strings.HasPrefix(argv, "plugin add ") || strings.HasPrefix(argv, "plugin remove ") ||
			strings.HasPrefix(argv, "plugin install ") || strings.HasPrefix(argv, "plugin uninstall ") {
			h.t.Fatalf("forbidden mutation call after %d: %q", start, line)
		}
	}
}

func (h *pluginAcceptanceHarness) assertMutationCallsSince(start int, want ...string) {
	h.t.Helper()
	mutations := make([]string, 0, len(want))
	for _, line := range h.callLines()[start:] {
		_, argv, found := strings.Cut(line, "|")
		if !found {
			h.t.Fatalf("malformed call log %q", line)
		}
		if strings.HasPrefix(argv, "plugin marketplace add ") || strings.HasPrefix(argv, "plugin marketplace remove ") ||
			strings.HasPrefix(argv, "plugin add ") || strings.HasPrefix(argv, "plugin remove ") ||
			strings.HasPrefix(argv, "plugin install ") || strings.HasPrefix(argv, "plugin uninstall ") {
			mutations = append(mutations, line)
		}
	}
	if strings.Join(mutations, "\n") != strings.Join(want, "\n") {
		h.t.Fatalf("mutation calls since %d:\n%s\nwant:\n%s", start, strings.Join(mutations, "\n"), strings.Join(want, "\n"))
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
			if agent == domain.AgentCodex {
				h.assertMutationCallsSince(0,
					h.expectedCall("plugin", "marketplace", "add", artifact.MarketplaceSource, "--json"),
					h.expectedCall("plugin", "add", "demo-plugin-v0.0.1@apm-demo-plugin", "--json"),
				)
			} else {
				h.assertMutationCallsSince(0,
					h.expectedCall("plugin", "marketplace", "add", artifact.MarketplaceSource, "--scope", "user"),
					h.expectedCall("plugin", "install", "demo-plugin-v0.0.1@apm-demo-plugin", "--scope", "user"),
				)
			}
			callsAfterFirst := len(h.callLines())

			result, err = h.installRef("demo-plugin", "v0.0.1")
			if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeUnchanged {
				t.Fatalf("repeat Install() = %+v, %v", result, err)
			}
			if got := len(h.callLines()); got != callsAfterFirst {
				t.Fatalf("manifest-first repeat grew calls from %d to %d", callsAfterFirst, got)
			}

			uninstallStart := len(h.callLines())
			uninstalled, err := h.uninstallRef("demo-plugin")
			if err != nil || len(uninstalled.Items) != 1 || uninstalled.Items[0].Outcome != cli.UninstallOutcomeCompleted {
				t.Fatalf("Uninstall() = %+v, %v", uninstalled, err)
			}
			h.assertManifest("demo-plugin", "", false)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
			h.assertPlugins()
			if agent == domain.AgentCodex {
				h.assertMutationCallsSince(uninstallStart, h.expectedCall("plugin", "remove", "demo-plugin-v0.0.1@apm-demo-plugin", "--json"))
			} else {
				h.assertMutationCallsSince(uninstallStart, h.expectedCall("plugin", "uninstall", "demo-plugin-v0.0.1@apm-demo-plugin", "--scope", "user"))
			}
			if info, statErr := os.Stat(artifact.Root); statErr != nil || !info.IsDir() {
				t.Fatalf("retained cache = %v, %v", info, statErr)
			}
			callsAfterUninstall := len(h.callLines())
			uninstalled, err = h.uninstallRef("demo-plugin")
			if err != nil || len(uninstalled.Items) != 1 || uninstalled.Items[0].Outcome != cli.UninstallOutcomeUnexecuted || uninstalled.Items[0].Power != nil {
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
			if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("install demo v0.0.1 = %+v, %v", result, err)
			}
			if result, err := h.installRef("side-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
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
				if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != step.outcome {
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
		if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeUnchanged || len(h.callLines()) != before {
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
		if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
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
			start := len(h.callLines())
			if _, err := h.installRef("demo-plugin", "v0.1.0"); err == nil {
				t.Fatal("Install() error = nil, want fail closed")
			}
			h.assertManifest("demo-plugin", "v0.0.1", true)
			assertOptionalFileUnchanged(t, h.marketplacePath("demo-plugin"), marketBefore, marketErr)
			assertOptionalFileUnchanged(t, h.pluginPath("demo-plugin", "v0.0.1"), pluginBefore, pluginErr)
			h.assertNoMutationCallsSince(start)
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
			start := len(h.callLines())
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
			h.assertNoMutationCallsSince(start)
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

func TestPluginAcceptanceActualFirstInstallPhaseWritesInterrupt(t *testing.T) {
	for _, test := range []struct {
		name        string
		barrier     string
		phase       transaction.Phase
		marketplace bool
	}{
		{name: "registering marketplace", barrier: "codex:marketplace-add", phase: transaction.PhaseRegisteringMarketplace},
		{name: "adding Plugin", barrier: "codex:plugin-add", phase: transaction.PhaseAddingNewPlugin, marketplace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			h.configureBarrier(test.barrier)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			completed := h.startInstall(ctx, "demo-plugin", "v0.0.1")
			h.waitInstallBarrier(test.barrier, completed)

			artifact := h.artifact("demo-plugin", "v0.0.1")
			h.assertPhase(test.phase)
			h.assertManifest("demo-plugin", "", false)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, test.marketplace)
			h.assertPlugins()
			wantCalls := []string{
				h.expectedCall("plugin", "marketplace", "list", "--json"),
				h.expectedCall("plugin", "list", "--json"),
				h.expectedCall("plugin", "marketplace", "list", "--json"),
				h.expectedCall("plugin", "list", "--json"),
				h.expectedCall("plugin", "marketplace", "add", artifact.MarketplaceSource, "--json"),
			}
			if test.barrier == "codex:plugin-add" {
				wantCalls = append(wantCalls, h.expectedCall("plugin", "add", "demo-plugin-v0.0.1@apm-demo-plugin", "--json"))
			}
			h.assertCallsSince(0, wantCalls...)

			cancel()
			answer := <-completed
			if answer.err == nil || len(answer.result.Items) != 1 || answer.result.Items[0].Outcome != cli.InstallOutcomeFailed {
				t.Fatalf("interrupted Install() = %+v, %v", answer.result, answer.err)
			}
			h.assertPhase(test.phase)
			h.assertManifest("demo-plugin", "", false)
			h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, test.marketplace)
			h.assertPlugins()
			h.disableBarrier()
			if err := h.fix.Fix(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.assertJournal(false)
			h.assertManifest("demo-plugin", "", false)
			h.assertMarketplace("demo-plugin", "", false)
			h.assertPlugins()
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

func TestPluginAcceptanceActualVersionSwitchPhaseWritesInterrupt(t *testing.T) {
	for _, test := range []struct {
		name        string
		barrier     string
		phase       transaction.Phase
		marketplace string
	}{
		{name: "removing old Plugin", barrier: "codex:plugin-remove", phase: transaction.PhaseRemovingOldPlugin, marketplace: "old"},
		{name: "unregistering old marketplace", barrier: "codex:marketplace-remove", phase: transaction.PhaseUnregisteringOldMarketplace, marketplace: "old"},
		{name: "registering target marketplace", barrier: "codex:marketplace-add", phase: transaction.PhaseRegisteringNewMarketplace},
		{name: "adding target Plugin", barrier: "codex:plugin-add", phase: transaction.PhaseAddingNewPlugin, marketplace: "target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("baseline Install() = %+v, %v", result, err)
			}
			old := h.artifact("demo-plugin", "v0.0.1")
			start := len(h.callLines())
			h.configureBarrier(test.barrier)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			completed := h.startInstall(ctx, "demo-plugin", "v0.1.0")
			h.waitInstallBarrier(test.barrier, completed)
			target := h.artifact("demo-plugin", "v0.1.0")

			h.assertPhase(test.phase)
			h.assertManifest("demo-plugin", "v0.0.1", true)
			switch test.marketplace {
			case "old":
				h.assertMarketplace("demo-plugin", old.MarketplaceSource, true)
			case "target":
				h.assertMarketplace("demo-plugin", target.MarketplaceSource, true)
			default:
				h.assertMarketplace("demo-plugin", "", false)
			}
			if test.phase == transaction.PhaseRemovingOldPlugin {
				h.assertPlugins("demo-plugin-v0.0.1")
			} else {
				h.assertPlugins()
			}
			wantCalls := []string{
				h.expectedCall("plugin", "marketplace", "list", "--json"),
				h.expectedCall("plugin", "list", "--json"),
				h.expectedCall("plugin", "marketplace", "list", "--json"),
				h.expectedCall("plugin", "list", "--json"),
				h.expectedCall("plugin", "list", "--json"),
				h.expectedCall("plugin", "remove", "demo-plugin-v0.0.1@apm-demo-plugin", "--json"),
			}
			if test.phase == transaction.PhaseUnregisteringOldMarketplace || test.phase == transaction.PhaseRegisteringNewMarketplace || test.phase == transaction.PhaseAddingNewPlugin {
				wantCalls = append(wantCalls, h.expectedCall("plugin", "marketplace", "remove", "apm-demo-plugin", "--json"))
			}
			if test.phase == transaction.PhaseRegisteringNewMarketplace || test.phase == transaction.PhaseAddingNewPlugin {
				wantCalls = append(wantCalls, h.expectedCall("plugin", "marketplace", "add", target.MarketplaceSource, "--json"))
			}
			if test.phase == transaction.PhaseAddingNewPlugin {
				wantCalls = append(wantCalls, h.expectedCall("plugin", "add", "demo-plugin-v0.1.0@apm-demo-plugin", "--json"))
			}
			h.assertCallsSince(start, wantCalls...)

			cancel()
			answer := <-completed
			if answer.err == nil || len(answer.result.Items) != 1 || answer.result.Items[0].Outcome != cli.InstallOutcomeFailed {
				t.Fatalf("interrupted switch Install() = %+v, %v", answer.result, answer.err)
			}
			h.assertPhase(test.phase)
			h.disableBarrier()
			if err := h.fix.Fix(context.Background()); err != nil {
				t.Fatal(err)
			}
			h.assertJournal(false)
			h.assertManifest("demo-plugin", "v0.0.1", true)
			h.assertMarketplace("demo-plugin", old.MarketplaceSource, true)
			h.assertPlugins("demo-plugin-v0.0.1")
			h.assertOnlyFakeExecutables()
		})
	}
}

func TestPluginAcceptanceActualCommittingPhaseWritesFailBeforeManifest(t *testing.T) {
	t.Run("first install", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		injected := errors.New("injected manifest set failure")
		h.manifests.setErr = injected
		result, err := h.installRef("demo-plugin", "v0.0.1")
		if !errors.Is(err, injected) || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
			t.Fatalf("Install() = %+v, %v", result, err)
		}
		artifact := h.artifact("demo-plugin", "v0.0.1")
		h.assertPhase(transaction.PhaseCommitting)
		h.assertManifest("demo-plugin", "", false)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.0.1")
		h.manifests.setErr = nil
		if err := h.fix.Fix(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.assertJournal(false)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.0.1")
		h.assertOnlyFakeExecutables()
	})

	t.Run("version switch", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
			t.Fatalf("baseline Install() = %+v, %v", result, err)
		}
		injected := errors.New("injected manifest set failure")
		h.manifests.setErr = injected
		result, err := h.installRef("demo-plugin", "v0.1.0")
		if !errors.Is(err, injected) || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
			t.Fatalf("switch Install() = %+v, %v", result, err)
		}
		target := h.artifact("demo-plugin", "v0.1.0")
		h.assertPhase(transaction.PhaseCommitting)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", target.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.1.0")
		h.manifests.setErr = nil
		if err := h.fix.Fix(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.assertJournal(false)
		h.assertManifest("demo-plugin", "v0.1.0", true)
		h.assertMarketplace("demo-plugin", target.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.1.0")
		h.assertOnlyFakeExecutables()
	})
}

func TestPluginAcceptanceActualRollingBackPhaseWritesFailBeforeRollbackAction(t *testing.T) {
	t.Run("first install", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		h.setSelfRemove("codex:marketplace-add")
		result, err := h.installRef("demo-plugin", "v0.0.1")
		if err == nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
			t.Fatalf("Install() = %+v, %v", result, err)
		}
		artifact := h.artifact("demo-plugin", "v0.0.1")
		h.assertPhase(transaction.PhaseRollingBack)
		h.assertManifest("demo-plugin", "", false)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins()
		h.restoreExecutable()
		if err := h.fix.Fix(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.assertJournal(false)
		h.assertManifest("demo-plugin", "", false)
		h.assertMarketplace("demo-plugin", "", false)
		h.assertPlugins()
		h.assertOnlyFakeExecutables()
	})

	t.Run("version switch", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
			t.Fatalf("baseline Install() = %+v, %v", result, err)
		}
		h.setSelfRemove("codex:marketplace-add")
		result, err := h.installRef("demo-plugin", "v0.1.0")
		if err == nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
			t.Fatalf("switch Install() = %+v, %v", result, err)
		}
		target := h.artifact("demo-plugin", "v0.1.0")
		h.assertPhase(transaction.PhaseRollingBack)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", target.MarketplaceSource, true)
		h.assertPlugins()
		h.restoreExecutable()
		old := h.artifact("demo-plugin", "v0.0.1")
		if err := h.fix.Fix(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.assertJournal(false)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", old.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.0.1")
		h.assertOnlyFakeExecutables()
	})
}

func TestFixPluginAcceptanceRecoversEveryMarketplaceCLIMutation(t *testing.T) {
	for _, test := range []struct {
		name           string
		firstInstall   bool
		interrupt      string
		phase          transaction.Phase
		marketplace    string
		plugins        []string
		wantCommitted  bool
		useNextInstall bool
	}{
		{name: "first marketplace registered unknown", firstInstall: true, interrupt: "codex:marketplace-add", phase: transaction.PhaseRegisteringMarketplace, marketplace: "target", useNextInstall: true},
		{name: "first Plugin added unknown", firstInstall: true, interrupt: "codex:plugin-add", phase: transaction.PhaseAddingNewPlugin, marketplace: "target", plugins: []string{"demo-plugin-v0.0.1"}, wantCommitted: true},
		{name: "old Plugin removed unknown", interrupt: "codex:plugin-remove", phase: transaction.PhaseRemovingOldPlugin, marketplace: "old"},
		{name: "old marketplace removed unknown", interrupt: "codex:marketplace-remove", phase: transaction.PhaseUnregisteringOldMarketplace},
		{name: "target marketplace registered unknown", interrupt: "codex:marketplace-add", phase: transaction.PhaseRegisteringNewMarketplace, marketplace: "target"},
		{name: "target Plugin added unknown", interrupt: "codex:plugin-add", phase: transaction.PhaseAddingNewPlugin, marketplace: "target", plugins: []string{"demo-plugin-v0.1.0"}, wantCommitted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPluginAcceptanceHarness(t, domain.AgentCodex)
			if !test.firstInstall {
				if _, err := h.installRef("demo-plugin", "v0.0.1"); err != nil {
					t.Fatal(err)
				}
			}
			start := len(h.callLines())
			h.setInterrupt(test.interrupt)
			targetRelease := "v0.1.0"
			if test.firstInstall {
				targetRelease = "v0.0.1"
			}
			result, err := h.installRef("demo-plugin", targetRelease)
			if err == nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
				t.Fatalf("interrupted Install() = %+v, %v", result, err)
			}
			target := h.artifact("demo-plugin", targetRelease)
			h.assertPhase(test.phase)
			if test.firstInstall {
				h.assertManifest("demo-plugin", "", false)
			} else {
				h.assertManifest("demo-plugin", "v0.0.1", true)
			}
			switch test.marketplace {
			case "target":
				h.assertMarketplace("demo-plugin", target.MarketplaceSource, true)
			case "old":
				h.assertMarketplace("demo-plugin", h.artifact("demo-plugin", "v0.0.1").MarketplaceSource, true)
			default:
				h.assertMarketplace("demo-plugin", "", false)
			}
			h.assertPlugins(test.plugins...)
			mutations := make([]string, 0, 4)
			if test.firstInstall {
				mutations = append(mutations, h.expectedCall("plugin", "marketplace", "add", target.MarketplaceSource, "--json"))
				if test.phase == transaction.PhaseAddingNewPlugin {
					mutations = append(mutations, h.expectedCall("plugin", "add", target.ActualName+"@apm-demo-plugin", "--json"))
				}
			} else {
				mutations = append(mutations, h.expectedCall("plugin", "remove", "demo-plugin-v0.0.1@apm-demo-plugin", "--json"))
				if test.phase != transaction.PhaseRemovingOldPlugin {
					mutations = append(mutations, h.expectedCall("plugin", "marketplace", "remove", "apm-demo-plugin", "--json"))
				}
				if test.phase == transaction.PhaseRegisteringNewMarketplace || test.phase == transaction.PhaseAddingNewPlugin {
					mutations = append(mutations, h.expectedCall("plugin", "marketplace", "add", target.MarketplaceSource, "--json"))
				}
				if test.phase == transaction.PhaseAddingNewPlugin {
					mutations = append(mutations, h.expectedCall("plugin", "add", target.ActualName+"@apm-demo-plugin", "--json"))
				}
			}
			h.assertMutationCallsSince(start, mutations...)
			wantCommitted := test.wantCommitted
			if test.useNextInstall {
				result, err := h.installRef("demo-plugin", "v0.0.1")
				if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
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
	start := len(h.callLines())
	if err := h.fix.Fix(context.Background()); !errors.Is(err, transaction.ErrRecoveryFailed) || !errors.Is(err, transaction.ErrIndeterminateState) {
		t.Fatalf("Fix() error = %v, want recovery/indeterminate", err)
	}
	h.assertJournal(true)
	h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
	h.assertManifest("demo-plugin", "", false)
	h.assertNoMutationCallsSince(start)
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
		if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
			t.Fatalf("baseline Install() = %+v, %v", result, err)
		}
		artifact := h.artifact("demo-plugin", "v0.0.1")
		start := len(h.callLines())
		h.setInterrupt("codex:plugin-remove")
		result, err := h.uninstallRef("demo-plugin")
		if err == nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeFailed {
			t.Fatalf("interrupted Uninstall() = %+v, %v", result, err)
		}
		h.assertPhase(transaction.PhaseRemovingPlugin)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins()
		h.assertMutationCallsSince(start, h.expectedCall("plugin", "remove", "demo-plugin-v0.0.1@apm-demo-plugin", "--json"))
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

func TestPluginAcceptanceActualUninstallPhaseWritesInterrupt(t *testing.T) {
	t.Run("removing Plugin before mutation", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
			t.Fatalf("baseline Install() = %+v, %v", result, err)
		}
		artifact := h.artifact("demo-plugin", "v0.0.1")
		start := len(h.callLines())
		h.configureBarrier("codex:plugin-remove")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		completed := h.startUninstall(ctx, "demo-plugin")
		h.waitUninstallBarrier("codex:plugin-remove", completed)

		h.assertPhase(transaction.PhaseRemovingPlugin)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.0.1")
		h.assertCallsSince(start,
			h.expectedCall("plugin", "marketplace", "list", "--json"),
			h.expectedCall("plugin", "list", "--json"),
			h.expectedCall("plugin", "marketplace", "list", "--json"),
			h.expectedCall("plugin", "list", "--json"),
			h.expectedCall("plugin", "remove", "demo-plugin-v0.0.1@apm-demo-plugin", "--json"),
		)
		cancel()
		answer := <-completed
		if answer.err == nil || len(answer.result.Items) != 1 || answer.result.Items[0].Outcome != cli.UninstallOutcomeFailed {
			t.Fatalf("interrupted Uninstall() = %+v, %v", answer.result, answer.err)
		}
		h.assertPhase(transaction.PhaseRemovingPlugin)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.0.1")
		h.disableBarrier()
		heldArtifact := artifact.Root + ".held"
		if err := os.Rename(artifact.Root, heldArtifact); err != nil {
			t.Fatal(err)
		}
		if err := h.fix.Fix(context.Background()); !errors.Is(err, transaction.ErrRecoveryFailed) || !errors.Is(err, transaction.ErrIndeterminateState) {
			t.Fatalf("Fix() with unavailable cache = %v, want recovery/indeterminate", err)
		}
		h.assertPhase(transaction.PhaseRollingBack)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.0.1")
		if err := os.Rename(heldArtifact, artifact.Root); err != nil {
			t.Fatal(err)
		}
		if err := h.fix.Fix(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.assertJournal(false)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins("demo-plugin-v0.0.1")
		h.assertOnlyFakeExecutables()
	})

	t.Run("committing before manifest remove", func(t *testing.T) {
		h := newPluginAcceptanceHarness(t, domain.AgentCodex)
		if result, err := h.installRef("demo-plugin", "v0.0.1"); err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeInstalled {
			t.Fatalf("baseline Install() = %+v, %v", result, err)
		}
		artifact := h.artifact("demo-plugin", "v0.0.1")
		injected := errors.New("injected manifest remove failure")
		h.manifests.removeErr = injected
		result, err := h.uninstallRef("demo-plugin")
		if !errors.Is(err, injected) || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeFailed {
			t.Fatalf("Uninstall() = %+v, %v", result, err)
		}
		h.assertPhase(transaction.PhaseCommitting)
		h.assertManifest("demo-plugin", "v0.0.1", true)
		h.assertMarketplace("demo-plugin", artifact.MarketplaceSource, true)
		h.assertPlugins()
		h.manifests.removeErr = nil
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
