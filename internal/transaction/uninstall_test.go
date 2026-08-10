package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type uninstallFixture struct {
	t         *testing.T
	events    []string
	target    string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   *recordingAdapter
	service   *Service
	request   UninstallRequest
}

func newUninstallFixture(t *testing.T) *uninstallFixture {
	t.Helper()
	target := filepath.Join(t.TempDir(), "skills", "demo")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := &uninstallFixture{t: t, target: target}
	fixture.journals = &recordingJournalStore{events: &fixture.events, fail: map[string]error{}}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.adapter = &recordingAdapter{target: target, events: &fixture.events}
	fixture.adapter.onInstall = func() error { return errors.New("uninstall called Adapter.Install") }
	fixture.service = newService(fixture.manifests, fixture.journals)
	fixture.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	fixture.request = UninstallRequest{
		Agent:   domain.AgentClaudeCode,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"},
		Adapter: fixture.adapter,
	}
	return fixture
}

func TestUninstallQuarantinesRemovesManifestAndCleansJournal(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.target, "sentinel"), []byte("installed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fixture.service.Uninstall(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after successful uninstall")
	}
	if fixture.manifests.removed.Name != "demo" || fixture.manifests.removed.Type != domain.PowerTypeSkill {
		t.Fatalf("removed manifest = %+v", fixture.manifests.removed)
	}
	if _, err := os.Lstat(fixture.target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target remains: %v", err)
	}
	assertEventSubsequence(t, fixture.events, []string{"create:removing", "update:committing", "manifest:remove", "delete:journal"})
}

func TestUninstallMissingManagedTargetSelfHealsManifest(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := fixture.service.Uninstall(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if fixture.manifests.removed.Name != "demo" || fixture.journals.found {
		t.Fatalf("removed = %+v, journal found = %v", fixture.manifests.removed, fixture.journals.found)
	}
	assertEventSubsequence(t, fixture.events, []string{"create:removing", "update:committing", "manifest:remove", "delete:journal"})
}

func TestUninstallRenameFailureRollsBackAndDeletesJournal(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	renameErr := errors.New("rename failed")
	fixture.service.files.rename = func(string, string) error { return renameErr }

	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, renameErr) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after successful rollback")
	}
	if _, statErr := os.Stat(fixture.target); statErr != nil {
		t.Fatalf("target changed after rename failure: %v", statErr)
	}
	if fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest unexpectedly removed: %+v", fixture.manifests.removed)
	}
	assertEventSubsequence(t, fixture.events, []string{"create:removing", "update:rolling-back", "delete:journal"})
}

func TestUninstallManifestFailurePreservesCommittingJournal(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.manifests.removeErr = errors.New("remove failed")
	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, fixture.manifests.removeErr) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target restored after commit: %v", statErr)
	}
}

func TestUninstallCommittingUpdateFailurePreservesRemovingEvidence(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	phaseErr := errors.New("committing update failed")
	fixture.journals.fail["update:committing"] = phaseErr
	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, phaseErr) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if fixture.journals.journal.Phase != PhaseRemoving {
		t.Fatalf("persisted phase = %q", fixture.journals.journal.Phase)
	}
	if _, statErr := os.Stat(fixture.journals.journal.QuarantinePath); statErr != nil {
		t.Fatalf("quarantine missing: %v", statErr)
	}
}

func TestUninstallCreateFailureDoesNotTouchTarget(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	createErr := errors.New("create failed")
	fixture.journals.fail["create"] = createErr

	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, createErr) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if _, statErr := os.Stat(fixture.target); statErr != nil {
		t.Fatalf("target changed after journal failure: %v", statErr)
	}
	if fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest unexpectedly removed: %+v", fixture.manifests.removed)
	}
}

func TestUninstallDirectoryCreationFailureRollsBack(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	mkdirErr := errors.New("mkdir failed")
	fixture.service.files.mkdir = func(string, os.FileMode) error { return mkdirErr }

	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, mkdirErr) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after successful rollback")
	}
	if _, statErr := os.Stat(fixture.target); statErr != nil {
		t.Fatalf("target changed after mkdir failure: %v", statErr)
	}
}

func TestUninstallRollbackPhaseFailurePreservesJournal(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	renameErr := errors.New("rename failed")
	phaseErr := errors.New("rolling-back update failed")
	fixture.service.files.rename = func(string, string) error { return renameErr }
	fixture.journals.fail["update:rolling-back"] = phaseErr

	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, renameErr) || !errors.Is(err, phaseErr) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseRemoving {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
}

func TestUninstallRenameRaceWithExternalRemovalCommits(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.service.files.rename = func(oldPath, _ string) error {
		if err := os.RemoveAll(oldPath); err != nil {
			return err
		}
		return os.ErrNotExist
	}

	if err := fixture.service.Uninstall(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if fixture.manifests.removed.Name != "demo" || fixture.journals.found {
		t.Fatalf("removed = %+v, journal found = %v", fixture.manifests.removed, fixture.journals.found)
	}
}

func TestUninstallQuarantineCleanupFailurePreservesCommittingJournal(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.MkdirAll(fixture.target, 0o755); err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("cleanup failed")
	fixture.service.files.removeAll = func(path string) error {
		if path == fixture.journals.journal.QuarantinePath {
			return cleanupErr
		}
		return os.RemoveAll(path)
	}

	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
}

func TestUninstallJournalDeleteFailurePreservesCommittingJournal(t *testing.T) {
	fixture := newUninstallFixture(t)
	fixture.journals.fail["delete"] = errors.New("delete failed")
	err := fixture.service.Uninstall(context.Background(), fixture.request)
	if !errors.Is(err, fixture.journals.fail["delete"]) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
}

func TestUninstallCancellationBeforeCommitRollsBack(t *testing.T) {
	fixture := newUninstallFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.journals.onCreate = func(Journal) { cancel() }

	err := fixture.service.Uninstall(ctx, fixture.request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if fixture.journals.found || fixture.manifests.removed.Name != "" {
		t.Fatalf("journal found = %v, removed = %+v", fixture.journals.found, fixture.manifests.removed)
	}
}

func TestUninstallCancellationAfterCommitPreservesJournal(t *testing.T) {
	fixture := newUninstallFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.journals.onUpdate = func(journal Journal) {
		if journal.Phase == PhaseCommitting {
			cancel()
		}
	}

	err := fixture.service.Uninstall(ctx, fixture.request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
	if fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest unexpectedly removed: %+v", fixture.manifests.removed)
	}
}

var _ adapter.Adapter = (*recordingAdapter)(nil)
