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
	"apm/internal/transaction"
	"apm/internal/version"
)

const fakeAgentCLIScript = `#!/bin/sh
agent=${0##*/}
case "$0" in
  "$FAKE_MCP_BIN"/*) ;;
  *) echo "unsafe executable path: $0" >&2; exit 97 ;;
esac
printf '%s|%s\n' "$0" "$*" >> "$FAKE_MCP_CALLS"

case "$agent:$1:$2" in
  codex:mcp:get)
    name=$4; state="$FAKE_MCP_STATE/$agent-$name"
    if [ -f "$state" ]; then printf '{"name":"%s"}\n' "$name"; exit 0; fi
    echo "No MCP server named '$name' found." >&2; exit 1 ;;
  codex:mcp:add)
    name=$3; : > "$FAKE_MCP_STATE/$agent-$name" ;;
  codex:mcp:remove)
    name=$3; /bin/rm -f "$FAKE_MCP_STATE/$agent-$name" ;;
  claude:mcp:get)
    name=$3; state="$FAKE_MCP_STATE/$agent-$name"
    if [ -f "$state" ]; then printf '%s:\n  Scope: User\n' "$name"; exit 0; fi
    echo "No MCP server found with name: $name" >&2; exit 1 ;;
  claude:mcp:add)
    if [ "$5" = "--transport" ]; then name=$7; else name=$5; fi
    : > "$FAKE_MCP_STATE/$agent-$name" ;;
  claude:mcp:remove)
    name=$5; /bin/rm -f "$FAKE_MCP_STATE/$agent-$name" ;;
  hermes:mcp:list)
    found=
    for state in "$FAKE_MCP_STATE"/hermes-*; do
      [ -f "$state" ] || continue
      name=${state##*/hermes-}
      [ -z "$found" ] && printf 'Name Transport Tools Status\n'
      printf '%s stdio 0 connected\n' "$name"
      found=1
    done
    [ -n "$found" ] || echo 'No MCP servers configured.' ;;
  hermes:mcp:add)
    name=$3; : > "$FAKE_MCP_STATE/$agent-$name"; printf "Saved '%s'\n" "$name" ;;
  hermes:mcp:remove)
    name=$3; /bin/rm -f "$FAKE_MCP_STATE/$agent-$name"; printf "Removed '%s'\n" "$name" ;;
  *) echo "unsupported fake invocation: $agent $*" >&2; exit 98 ;;
esac
`

type isolatedMCPHarness struct {
	t          *testing.T
	root       string
	fakeBin    string
	state      string
	calls      string
	cacheHome  string
	repository *config.JSONRepository
	registry   *adapter.Registry
	cloud      cloud.Client
	install    *InstallService
	uninstall  *UninstallService
}

func newIsolatedMCPHarness(t *testing.T, installedAgent domain.AgentID) *isolatedMCPHarness {
	t.Helper()
	root := t.TempDir()
	fakeBin := filepath.Join(root, "fake-bin")
	state := filepath.Join(root, "fake-state")
	for _, path := range []string{fakeBin, state} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, executable := range []string{"codex", "claude", "hermes"} {
		if err := os.WriteFile(filepath.Join(fakeBin, executable), []byte(fakeAgentCLIScript), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	calls := filepath.Join(root, "calls.log")
	cacheHome := filepath.Join(root, "cache")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("FAKE_MCP_BIN", fakeBin)
	t.Setenv("FAKE_MCP_STATE", state)
	t.Setenv("FAKE_MCP_CALLS", calls)
	t.Setenv("APM_HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	agentRoots := map[domain.AgentID]string{
		domain.AgentCodex:      filepath.Join(root, "agents", "codex"),
		domain.AgentClaudeCode: filepath.Join(root, "agents", "claude"),
		domain.AgentHermes:     filepath.Join(root, "agents", "hermes"),
	}
	t.Setenv("CODEX_HOME", agentRoots[domain.AgentCodex])
	t.Setenv("CLAUDE_CONFIG_DIR", agentRoots[domain.AgentClaudeCode])
	t.Setenv("HERMES_HOME", agentRoots[domain.AgentHermes])
	if err := os.MkdirAll(agentRoots[installedAgent], 0o700); err != nil {
		t.Fatal(err)
	}

	resources, err := filepath.Abs(filepath.Join("..", "cloud", "resources"))
	if err != nil {
		t.Fatal(err)
	}
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
	installer, err := NewInstall(locker, transactions, cloudClient, powerCache, registry, repository, version.NewSelector())
	if err != nil {
		t.Fatal(err)
	}
	uninstaller, err := NewUninstall(locker, transactions, cloudClient, registry, repository)
	if err != nil {
		t.Fatal(err)
	}
	return &isolatedMCPHarness{
		t: t, root: root, fakeBin: fakeBin, state: state, calls: calls, cacheHome: cacheHome,
		repository: repository, registry: registry, cloud: cloudClient, install: installer, uninstall: uninstaller,
	}
}

func TestMCPIntegrationInstallUpgradeUninstallForEveryAgent(t *testing.T) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes} {
		t.Run(string(agent), func(t *testing.T) {
			harness := newIsolatedMCPHarness(t, agent)
			flags := cli.Flags{Agent: string(agent), Type: "mcp", Name: "filesystem", Version: "v0.0.1"}
			installed, err := harness.install.Install(context.Background(), flags)
			if err != nil || len(installed.Items) != 1 || installed.Items[0].Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("Install(v0.0.1) = %+v, %v", installed, err)
			}
			harness.assertManagedMCP(agent, "filesystem", "v0.0.1", true)
			harness.assertMCPOnlyPathsAbsent()

			flags.Version = "v0.1.0"
			upgraded, err := harness.install.Install(context.Background(), flags)
			if err != nil || len(upgraded.Items) != 1 || upgraded.Items[0].Outcome != cli.InstallOutcomeUpgraded {
				t.Fatalf("Install(v0.1.0) = %+v, %v", upgraded, err)
			}
			harness.assertManagedMCP(agent, "filesystem", "v0.1.0", true)
			harness.assertMCPOnlyPathsAbsent()

			removed, err := harness.uninstall.Uninstall(context.Background(), cli.Flags{Agent: string(agent), Type: "mcp", Name: "filesystem"})
			if err != nil || len(removed.Items) != 1 || removed.Items[0].Outcome != cli.UninstallOutcomeCompleted {
				t.Fatalf("Uninstall() = %+v, %v", removed, err)
			}
			harness.assertManagedMCP(agent, "filesystem", "", false)
			harness.assertMCPOnlyPathsAbsent()
			harness.assertOnlyFakeExecutables()
		})
	}
}

func TestMCPIntegrationHTTPConflictAndStaleRepair(t *testing.T) {
	t.Run("HTTP install and uninstall", func(t *testing.T) {
		harness := newIsolatedMCPHarness(t, domain.AgentCodex)
		flags := cli.Flags{Agent: "codex", Type: "mcp", Name: "company-tools", Version: "v0.0.1"}
		if _, err := harness.install.Install(context.Background(), flags); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.uninstall.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "company-tools"}); err != nil {
			t.Fatal(err)
		}
		harness.assertManagedMCP(domain.AgentCodex, "company-tools", "", false)
		harness.assertOnlyFakeExecutables()
	})

	t.Run("unmanaged conflict", func(t *testing.T) {
		harness := newIsolatedMCPHarness(t, domain.AgentCodex)
		if err := os.WriteFile(filepath.Join(harness.state, "codex-filesystem"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := harness.install.Install(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "filesystem", Version: "v0.0.1"})
		if !errors.Is(err, adapter.ErrMCPConflict) {
			t.Fatalf("Install() error = %v, want ErrMCPConflict", err)
		}
		harness.assertManagedMCP(domain.AgentCodex, "filesystem", "", true)
		harness.assertMCPOnlyPathsAbsent()
	})

	t.Run("stale manifest removes one entry", func(t *testing.T) {
		harness := newIsolatedMCPHarness(t, domain.AgentCodex)
		stale := domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"}
		keep := domain.PowerRef{Name: "keep", Type: domain.PowerTypeMCP, Version: "v9.9.9"}
		if err := harness.repository.Set(domain.AgentCodex, stale); err != nil {
			t.Fatal(err)
		}
		if err := harness.repository.Set(domain.AgentCodex, keep); err != nil {
			t.Fatal(err)
		}
		_, err := harness.install.Install(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "filesystem", Version: "v0.0.1"})
		if !errors.Is(err, transaction.ErrStaleManifest) {
			t.Fatalf("Install() error = %v, want ErrStaleManifest", err)
		}
		if _, found, err := harness.repository.Find(domain.AgentCodex, stale.Type, stale.Name); err != nil || found {
			t.Fatalf("stale entry found/error = %v/%v", found, err)
		}
		if got, found, err := harness.repository.Find(domain.AgentCodex, keep.Type, keep.Name); err != nil || !found || got != keep {
			t.Fatalf("keep entry = %+v, %v, %v", got, found, err)
		}
		harness.assertMCPOnlyPathsAbsent()
	})
}

func TestMCPIntegrationSearchPreservesAndFixRecoversInterruptedAgentCLIJournal(t *testing.T) {
	harness := newIsolatedMCPHarness(t, domain.AgentCodex)
	ref := domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"}
	server, err := harness.cloud.GetMCP(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harness.state, "codex-filesystem"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := transaction.Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        transaction.DriverAgentCLI,
		Operation:     transaction.OperationInstall,
		Phase:         transaction.PhaseAddingNew,
		Agent:         domain.AgentCodex,
		Power:         ref,
		AgentCLI:      &transaction.AgentCLIJournalPayload{Server: server},
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
		if _, found, err := harness.repository.Find(domain.AgentCodex, ref.Type, ref.Name); err != nil || found {
			t.Fatalf("read-only manifest found/error = %v/%v, want absent", found, err)
		}
		if _, err := os.Stat(filepath.Join(harness.state, "codex-filesystem")); err != nil {
			t.Fatalf("read-only command changed MCP target: %v", err)
		}
	},
		func() error {
			_, err := search.Search(context.Background(), cli.Flags{Keyword: "filesystem"})
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
	if _, err := os.Stat(filepath.Join(harness.state, "codex-filesystem")); err != nil {
		t.Fatalf("Fix changed committed MCP target: %v", err)
	}
	contents, err = os.ReadFile(harness.calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(contents)), "\n")); got != 1 {
		t.Fatalf("Fix CLI calls = %d, want exactly 1 recovery inspection: %s", got, contents)
	}
	manifestPath, err := paths.ManifestPath(domain.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	assertPathsWithinRoot(t, harness.root, journalPath, manifestPath, filepath.Join(harness.state, "codex-filesystem"))
	harness.assertOnlyFakeExecutables()
}

func (h *isolatedMCPHarness) assertManagedMCP(agent domain.AgentID, name, version string, statePresent bool) {
	h.t.Helper()
	_, stateErr := os.Stat(filepath.Join(h.state, fakeAgentName(agent)+"-"+name))
	if statePresent != (stateErr == nil) {
		h.t.Fatalf("fake MCP state present = %v, stat error %v", statePresent, stateErr)
	}
	ref, found, err := h.repository.Find(agent, domain.PowerTypeMCP, name)
	if err != nil {
		h.t.Fatal(err)
	}
	if version == "" {
		if found {
			h.t.Fatalf("manifest entry remains: %+v", ref)
		}
		return
	}
	if !found || ref.Version != version || ref.Name != name || ref.Type != domain.PowerTypeMCP {
		h.t.Fatalf("manifest entry = %+v, found %v, want version %q", ref, found, version)
	}
}

func (h *isolatedMCPHarness) assertMCPOnlyPathsAbsent() {
	h.t.Helper()
	for _, path := range []string{
		filepath.Join(h.cacheHome, "apm"),
		filepath.Join(h.root, "home", ".apm", "staging"),
		filepath.Join(h.root, "home", ".apm", ".tmp", "staging"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			h.t.Fatalf("MCP-only path %q exists or cannot be inspected: %v", path, err)
		}
	}
}

func (h *isolatedMCPHarness) assertOnlyFakeExecutables() {
	h.t.Helper()
	contents, err := os.ReadFile(h.calls)
	if err != nil {
		h.t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		h.t.Fatal("fake Agent CLI was never invoked")
	}
	prefix := h.fakeBin + string(filepath.Separator)
	for _, line := range lines {
		path, _, found := strings.Cut(line, "|")
		if !found || !strings.HasPrefix(path, prefix) {
			h.t.Fatalf("Agent CLI invocation escaped fake-bin: %q", line)
		}
	}
}

func fakeAgentName(agent domain.AgentID) string {
	if agent == domain.AgentClaudeCode {
		return "claude"
	}
	if agent == domain.AgentHermes {
		return "hermes"
	}
	return "codex"
}
