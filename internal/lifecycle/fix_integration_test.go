package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/paths"
	"apm/internal/transaction"
)

// countingRecoverer 保留真实恢复器行为，并记录 Fix 是否只调用一次 Recover。
type countingRecoverer struct {
	transaction.Recoverer
	calls int
}

func (r *countingRecoverer) Recover(ctx context.Context) (transaction.RecoveryResult, error) {
	r.calls++
	return r.Recoverer.Recover(ctx)
}

func TestFixIntegrationFilesystemSkillPreservesJournalUntilFixThenCommits(t *testing.T) {
	harness := newIsolatedMCPHarness(t, domain.AgentCodex)
	ref := domain.PowerRef{Name: "brief-status", Type: domain.PowerTypeSkill, Version: "v0.0.1"}
	skill, err := harness.registry.ResolveSkill(domain.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	target, err := skill.TargetPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("interrupted target\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	id := "00112233445566778899aabbccddeeff"
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	txDir := filepath.Join(filepath.Dir(target), ".apm-transactions", id)
	journal := transaction.Journal{
		SchemaVersion: 1, TransactionID: id, Driver: transaction.DriverFilesystem,
		Operation: transaction.OperationInstall, Phase: transaction.PhasePublishing,
		Agent: domain.AgentCodex, Power: ref,
		Filesystem: &transaction.FilesystemJournalPayload{
			StagingRoot: staging, TargetPath: target, TransactionDir: txDir,
			BackupPath: filepath.Join(txDir, "backup"),
		},
	}
	journalPath := writeIntegrationJournal(t, journal)
	originalJournal := readJournalBytes(t, journalPath)
	transactions := harnessTransactions(t, harness)
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
		assertFileContents(t, filepath.Join(target, "SKILL.md"), "interrupted target\n")
	},
		func() error {
			_, err := search.Search(context.Background(), cli.Flags{Keyword: "brief-status"})
			return err
		},
		func() error { _, err := list.List(context.Background(), cli.Flags{}); return err },
		func() error {
			_, err := list.List(context.Background(), cli.Flags{Installed: true, Agent: string(domain.AgentCodex)})
			return err
		},
	)
	if _, err := os.ReadFile(harness.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only Skill commands unexpectedly invoked an Agent CLI: %v", err)
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
		t.Fatalf("recovered manifest = %+v, found=%v err=%v, want %+v", got, found, err, ref)
	}
	assertFileContents(t, filepath.Join(target, "SKILL.md"), "interrupted target\n")
	if _, err := os.ReadFile(harness.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("filesystem recovery unexpectedly invoked an Agent CLI: %v", err)
	}
	manifestPath, err := paths.ManifestPath(domain.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	assertPathsWithinRoot(t, harness.root, journalPath, manifestPath, target, txDir)
}

func TestFixIntegrationDamagedJournalsStayReadOnlyUntilFixFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		contents []byte
	}{
		{name: "invalid JSON", contents: []byte(`{"schema_version":`)},
		{name: "invalid schema version one envelope", contents: []byte(`{"schema_version":1,"transaction_id":"00112233445566778899aabbccddeeff","driver":"filesystem","operation":"install","phase":"preparing","agent":"codex","power":{"Name":"brief-status","Type":"skill","Version":"v0.0.1"}}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newIsolatedMCPHarness(t, domain.AgentCodex)
			journalPath, err := paths.JournalPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(journalPath, tt.contents, 0o600); err != nil {
				t.Fatal(err)
			}
			transactions := harnessTransactions(t, harness)
			search, err := NewSearch(transactions, harness.cloud)
			if err != nil {
				t.Fatal(err)
			}
			list, err := NewList(transactions, harness.cloud, harness.registry, harness.repository)
			if err != nil {
				t.Fatal(err)
			}

			warnings := captureLifecycleWarnings(t, func() {
				if _, err := search.Search(context.Background(), cli.Flags{Keyword: "brief-status"}); err != nil {
					t.Fatal(err)
				}
				assertJournalBytes(t, journalPath, tt.contents)
				if _, err := list.List(context.Background(), cli.Flags{}); err != nil {
					t.Fatal(err)
				}
				assertJournalBytes(t, journalPath, tt.contents)
				if _, err := list.List(context.Background(), cli.Flags{Installed: true, Agent: string(domain.AgentCodex)}); err != nil {
					t.Fatal(err)
				}
				assertJournalBytes(t, journalPath, tt.contents)
			})
			if got := strings.Count(warnings, "transaction journal is damaged"); got != 3 {
				t.Fatalf("damaged journal warnings = %d, want 3: %s", got, warnings)
			}

			recoverer := &countingRecoverer{Recoverer: transactions}
			err = newIntegrationFixer(t, recoverer).Fix(context.Background())
			if !errors.Is(err, transaction.ErrRecoveryFailed) || !errors.Is(err, transaction.ErrInvalidJournal) {
				t.Fatalf("Fix() error = %v, want ErrRecoveryFailed and ErrInvalidJournal", err)
			}
			if recoverer.calls != 1 {
				t.Fatalf("Recover calls = %d, want 1", recoverer.calls)
			}
			assertJournalBytes(t, journalPath, tt.contents)
			assertPathsWithinRoot(t, harness.root, journalPath)
		})
	}
}

func TestRemoteFixRecoversFilesystemJournalAfterServerClose(t *testing.T) {
	configureLifecycleTrustedTemp(t)
	server := newLifecycleRemoteServer(t)
	harness := newProviderLifecycleHarness(t, server.client(t))
	ref := domain.PowerRef{Name: "brief-status", Type: domain.PowerTypeSkill, Version: "v0.0.1"}
	if _, err := harness.install.Install(context.Background(), cli.Flags{
		Agent: "bibit", Type: "skill", Name: ref.Name, Version: ref.Version,
	}); err != nil {
		t.Fatal(err)
	}
	skill, err := harness.registry.ResolveSkill(domain.AgentBibit)
	if err != nil {
		t.Fatal(err)
	}
	target, err := skill.TargetPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(target, "SKILL.md")
	markerBeforeFix, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.Remove(domain.AgentBibit, ref.Type, ref.Name); err != nil {
		t.Fatal(err)
	}
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	txDir := filepath.Join(filepath.Dir(target), ".apm-transactions", id)
	journal := transaction.Journal{
		SchemaVersion: 1, TransactionID: id, Driver: transaction.DriverFilesystem,
		Operation: transaction.OperationInstall, Phase: transaction.PhasePublishing,
		Agent: domain.AgentBibit, Power: ref,
		Filesystem: &transaction.FilesystemJournalPayload{
			StagingRoot: staging, TargetPath: target, TransactionDir: txDir,
			BackupPath: filepath.Join(txDir, "backup"),
		},
	}
	journalPath := writeIntegrationJournal(t, journal)
	if got := server.transportAttempts(); got == 0 {
		t.Fatal("Remote install made no HTTP transport attempts; offline counter was not exercised")
	}
	server.server.Close()
	server.resetTransportAttempts()

	recoverer := &countingRecoverer{Recoverer: harnessTransactions(t, harness)}
	if err := newIntegrationFixer(t, recoverer).Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recoverer.calls != 1 {
		t.Fatalf("Recover calls = %d, want 1", recoverer.calls)
	}
	if got := server.transportAttempts(); got != 0 {
		t.Fatalf("offline Fix made %d HTTP transport attempts, want 0", got)
	}
	assertJournalAbsent(t, journalPath)
	if got, found, err := harness.repository.Find(domain.AgentBibit, ref.Type, ref.Name); err != nil || !found || got != ref {
		t.Fatalf("Fix manifest = %+v, found=%v err=%v, want %+v", got, found, err, ref)
	}
	markerAfterFix, err := os.ReadFile(markerPath)
	if err != nil || !bytes.Equal(markerAfterFix, markerBeforeFix) {
		t.Fatalf("Fix marker = %q, %v; want %q", markerAfterFix, err, markerBeforeFix)
	}
	assertMCPHarnessEnvironment(t, harness.fakeBin, filepath.Join(harness.root, "user-home"))
	if _, err := os.Lstat(harness.calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("filesystem Skill Fix unexpectedly invoked an Agent CLI: %v", err)
	}
	manifestPath, err := paths.ManifestPath(domain.AgentBibit)
	if err != nil {
		t.Fatal(err)
	}
	assertPathsWithinRoot(t, harness.root, journalPath, manifestPath, target, txDir)
}

func TestFixIntegrationAgentIdentityMismatchFailsClosedThenConverges(t *testing.T) {
	harness := newIsolatedMCPHarness(t, domain.AgentBibit)
	ref := domain.PowerRef{Name: "demo-agent", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	agentAdapter, err := harness.registry.ResolveAgent(domain.AgentBibit)
	if err != nil {
		t.Fatal(err)
	}
	target, err := agentAdapter.TargetPath(ref)
	if err != nil {
		t.Fatal(err)
	}

	id := "00112233445566778899aabbccddeeff"
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	txDir := filepath.Join(filepath.Dir(target), ".apm-transactions", id)
	journal := transaction.Journal{
		SchemaVersion: 1, TransactionID: id, Driver: transaction.DriverFilesystem,
		Operation: transaction.OperationInstall, Phase: transaction.PhasePublishing,
		Agent: domain.AgentBibit, Power: ref,
		Filesystem: &transaction.FilesystemJournalPayload{
			StagingRoot: staging, TargetPath: target, TransactionDir: txDir,
			BackupPath: filepath.Join(txDir, "backup"),
		},
	}

	const wrongYAML = "schema_version: 1\nagent:\n  id: other-agent\n  version: v9.9.9\n  description: wrong identity\n"
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(wrongYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	journalPath := writeIntegrationJournal(t, journal)
	originalJournal := readJournalBytes(t, journalPath)

	recoverer := &countingRecoverer{Recoverer: harnessTransactions(t, harness)}
	err = newIntegrationFixer(t, recoverer).Fix(context.Background())
	if !errors.Is(err, transaction.ErrIndeterminateState) || !errors.Is(err, transaction.ErrRecoveryFailed) {
		t.Fatalf("Fix() error = %v, want ErrIndeterminateState and ErrRecoveryFailed", err)
	}
	if recoverer.calls != 1 {
		t.Fatalf("Recover calls = %d, want 1", recoverer.calls)
	}
	assertJournalBytes(t, journalPath, originalJournal)
	assertFileContents(t, target, wrongYAML)
	if got, found, err := harness.repository.Find(domain.AgentBibit, ref.Type, ref.Name); err != nil || found {
		t.Fatalf("manifest after failed Fix = %+v, found=%v err=%v, want absent", got, found, err)
	}

	const legalYAML = "schema_version: 1\nagent:\n  id: demo-agent\n  version: v1.0.0\n  description: Demo agent\n"
	if err := os.WriteFile(target, []byte(legalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := newIntegrationFixer(t, harnessTransactions(t, harness)).Fix(context.Background()); err != nil {
		t.Fatalf("second Fix() error = %v", err)
	}
	assertJournalAbsent(t, journalPath)
	if got, found, err := harness.repository.Find(domain.AgentBibit, ref.Type, ref.Name); err != nil || !found || got != ref {
		t.Fatalf("recovered manifest = %+v, found=%v err=%v, want %+v", got, found, err, ref)
	}
	assertFileContents(t, target, legalYAML)
	manifestPath, err := paths.ManifestPath(domain.AgentBibit)
	if err != nil {
		t.Fatal(err)
	}
	assertPathsWithinRoot(t, harness.root, journalPath, manifestPath, target, txDir)
}

func harnessTransactions(t *testing.T, harness *isolatedMCPHarness) *transaction.Service {
	t.Helper()
	transactions, err := transaction.NewManager(harness.repository, harness.registry)
	if err != nil {
		t.Fatal(err)
	}
	return transactions
}

func newIntegrationFixer(t *testing.T, recoverer transaction.Recoverer) *FixService {
	t.Helper()
	locker, err := lock.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	fixer, err := NewFix(locker, recoverer)
	if err != nil {
		t.Fatal(err)
	}
	return fixer
}

func writeIntegrationJournal(t *testing.T, journal transaction.Journal) string {
	t.Helper()
	journalPath, err := paths.JournalPath()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return journalPath
}

func captureLifecycleWarnings(t *testing.T, action func()) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	action()
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	return stderr.String()
}

func assertJournalExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("journal missing: %v", err)
	}
}

func assertJournalAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
}

func assertJournalBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("journal bytes = %q, err=%v, want %q", got, err, want)
	}
}

func readJournalBytes(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

// assertActiveJournalReadOnly 断言每个只读命令均告警但不改变 journal 或目标状态。
func assertActiveJournalReadOnly(t *testing.T, journalPath string, originalJournal []byte, assertState func(), operations ...func() error) {
	t.Helper()
	warnings := captureLifecycleWarnings(t, func() {
		for _, operation := range operations {
			if err := operation(); err != nil {
				t.Fatal(err)
			}
			assertJournalBytes(t, journalPath, originalJournal)
			assertState()
		}
	})
	if got, want := strings.Count(warnings, "transaction recovery required"), len(operations); got != want {
		t.Fatalf("recovery warnings = %d, want %d: %s", got, want, warnings)
	}
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file %q = %q, err=%v, want %q", path, got, err, want)
	}
}

func assertPathsWithinRoot(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			t.Fatalf("path %q escapes isolated root %q: relative=%q err=%v", path, root, relative, err)
		}
	}
}
