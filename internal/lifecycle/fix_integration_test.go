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

	search, err := NewSearch(harnessTransactions(t, harness), harness.cloud)
	if err != nil {
		t.Fatal(err)
	}
	warnings := captureLifecycleWarnings(t, func() {
		if _, err := search.Search(context.Background(), cli.Flags{Keyword: "brief-status"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(warnings, "run apm fix") {
		t.Fatalf("Search warnings = %q, want recovery hint", warnings)
	}
	assertJournalExists(t, journalPath)
	if _, found, err := harness.repository.Find(domain.AgentCodex, ref.Type, ref.Name); err != nil || found {
		t.Fatalf("Search manifest found/error = %v/%v, want absent", found, err)
	}
	assertFileContents(t, filepath.Join(target, "SKILL.md"), "interrupted target\n")

	transactions := harnessTransactions(t, harness)
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
