package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
	"apm/internal/paths"
)

type recoveryFixture struct {
	t           *testing.T
	home        string
	target      string
	staging     string
	journal     Journal
	journalPath string
	manifests   *recordingManifestWriter
	events      []string
}

func newRecoveryFixture(t *testing.T, operation Operation, phase Phase) *recoveryFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	journalPath, err := paths.JournalPath()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "skills", "demo")
	id := "00112233445566778899aabbccddeeff"
	artifacts, err := artifactPaths(target, id)
	if err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 2,
		TransactionID: id,
		Driver:        DriverFilesystem,
		Operation:     operation,
		Phase:         phase,
		Agent:         domain.AgentCodex,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "2.0.0"},
		Filesystem: &FilesystemJournalPayload{
			TargetPath:     target,
			TransactionDir: artifacts.dir,
		},
	}
	if operation == OperationInstall {
		journal.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"})
		journal.Filesystem.StagingRoot = staging
		journal.Filesystem.BackupPath = artifacts.backup
	} else {
		journal.Filesystem.QuarantinePath = artifacts.quarantine
	}
	fixture := &recoveryFixture{t: t, home: home, target: target, staging: staging, journalPath: journalPath, journal: journal}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	return fixture
}

func (f *recoveryFixture) persist() {
	f.t.Helper()
	if err := newFilesystemJournalStore(f.journalPath).Create(f.journal); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryFixture) freshService() *Service {
	f.t.Helper()
	return newService(f.manifests, newFilesystemJournalStore(f.journalPath))
}

func (f *recoveryFixture) writePath(path, contents string) {
	f.t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "value"), []byte(contents), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryFixture) assertJournalAbsent() {
	f.t.Helper()
	if _, err := os.Lstat(f.journalPath); !errors.Is(err, os.ErrNotExist) {
		f.t.Fatalf("journal remains: %v", err)
	}
}

func TestRecoverWithoutJournalReturnsZeroResult(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePreparing)
	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
}

func TestRecoverPublishingInstallWithTargetCompletesCommitWithoutAdapter(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writePath(fixture.target, "new")
	fixture.writePath(fixture.journal.Filesystem.BackupPath, "old")
	fixture.writePath(fixture.staging, "staging")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit || result.OriginalPhase != PhasePublishing {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.setRef != fixture.journal.Power {
		t.Fatalf("manifest Set = %+v", fixture.manifests.setRef)
	}
	if _, err := os.Stat(fixture.target); err != nil {
		t.Fatalf("target missing: %v", err)
	}
	if _, err := os.Lstat(fixture.journal.Filesystem.BackupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup remains: %v", err)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverPublishingInstallWithoutTargetRollsBackBackup(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writePath(fixture.journal.Filesystem.BackupPath, "old")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "value"))
	if readErr != nil || string(contents) != "old" {
		t.Fatalf("restored target = %q, %v", contents, readErr)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverCommittingInstallCompletesCommit(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhaseCommitting)
	fixture.writePath(fixture.target, "new")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.setRef != fixture.journal.Power {
		t.Fatalf("manifest Set = %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverInstallPreparingAndRollingBackConverge(t *testing.T) {
	for _, phase := range []Phase{PhasePreparing, PhaseRollingBack} {
		t.Run(string(phase), func(t *testing.T) {
			fixture := newRecoveryFixture(t, OperationInstall, phase)
			fixture.writePath(fixture.target, "old")
			fixture.persist()
			result, err := fixture.freshService().Recover(context.Background())
			if err != nil || result.Direction != RecoveryRollback {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			fixture.assertJournalAbsent()
		})
	}
}

func TestRecoverUninstallDirectionsConverge(t *testing.T) {
	tests := []struct {
		name               string
		phase              Phase
		target             bool
		quarantine         bool
		wantDirection      RecoveryDirection
		wantManifestRemove bool
	}{
		{name: "removing quarantine commits", phase: PhaseRemoving, quarantine: true, wantDirection: RecoveryCommit, wantManifestRemove: true},
		{name: "removing missing target commits", phase: PhaseRemoving, wantDirection: RecoveryCommit, wantManifestRemove: true},
		{name: "removing target rolls back", phase: PhaseRemoving, target: true, wantDirection: RecoveryRollback},
		{name: "rolling back quarantine restores", phase: PhaseRollingBack, quarantine: true, wantDirection: RecoveryRollback},
		{name: "committing completes", phase: PhaseCommitting, quarantine: true, wantDirection: RecoveryCommit, wantManifestRemove: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRecoveryFixture(t, OperationUninstall, test.phase)
			if test.target {
				fixture.writePath(fixture.target, "installed")
			}
			if test.quarantine {
				fixture.writePath(fixture.journal.Filesystem.QuarantinePath, "installed")
			}
			fixture.persist()

			result, err := fixture.freshService().Recover(context.Background())
			if err != nil || result.Direction != test.wantDirection {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if got := fixture.manifests.removed.Name != ""; got != test.wantManifestRemove {
				t.Fatalf("manifest removed = %v, want %v", got, test.wantManifestRemove)
			}
			if test.phase == PhaseRollingBack {
				if _, err := os.Stat(fixture.target); err != nil {
					t.Fatalf("target not restored: %v", err)
				}
			}
			fixture.assertJournalAbsent()
		})
	}
}

func TestRecoverContradictoryPathsFailsClosed(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationUninstall, PhaseRemoving)
	fixture.writePath(fixture.target, "external")
	fixture.writePath(fixture.journal.Filesystem.QuarantinePath, "installed")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if result.Recovered || result.TransactionID != fixture.journal.TransactionID {
		t.Fatalf("result completion/metadata = %+v", result)
	}
	if _, statErr := os.Stat(fixture.target); statErr != nil {
		t.Fatalf("target changed: %v", statErr)
	}
}

func TestRecoverCancellationPreservesJournalBeforeMutation(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writePath(fixture.journal.Filesystem.BackupPath, "old")
	fixture.persist()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := fixture.freshService().Recover(ctx)
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if _, statErr := os.Stat(fixture.journal.Filesystem.BackupPath); statErr != nil {
		t.Fatalf("backup changed: %v", statErr)
	}
	if _, statErr := os.Stat(fixture.journalPath); statErr != nil {
		t.Fatalf("journal missing: %v", statErr)
	}
}

func TestRecoverCorruptJournalFailsClosed(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePreparing)
	if err := os.MkdirAll(filepath.Dir(fixture.journalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.journalPath, []byte(`{"schema_version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrInvalidJournal) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.setRef.Name != "" || fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest mutated: set=%+v remove=%+v", fixture.manifests.setRef, fixture.manifests.removed)
	}
}

func TestRecoverSymlinkTransactionRootFailsClosed(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationUninstall, PhaseCommitting)
	external := t.TempDir()
	root := filepath.Dir(fixture.journal.Filesystem.TransactionDir)
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, root); err != nil {
		t.Fatal(err)
	}
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrUnsafePath) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest unexpectedly removed: %+v", fixture.manifests.removed)
	}
}

type failOnceDeleteJournalStore struct {
	JournalStore
	err error
}

func (s *failOnceDeleteJournalStore) Delete(transactionID string) error {
	if s.err != nil {
		err := s.err
		s.err = nil
		return err
	}
	return s.JournalStore.Delete(transactionID)
}

func TestRecoverDeleteFailureCanRetryWithFreshManager(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writePath(fixture.target, "new")
	fixture.writePath(fixture.journal.Filesystem.BackupPath, "old")
	fixture.persist()
	store := newFilesystemJournalStore(fixture.journalPath)
	deleteErr := errors.New("delete failed")
	first := newService(fixture.manifests, &failOnceDeleteJournalStore{JournalStore: store, err: deleteErr})

	result, err := first.Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, deleteErr) || result.Recovered {
		t.Fatalf("first Recover() = %+v, %v", result, err)
	}
	if _, statErr := os.Stat(fixture.journalPath); statErr != nil {
		t.Fatalf("journal missing after delete failure: %v", statErr)
	}

	result, err = fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.OriginalPhase != PhaseCommitting {
		t.Fatalf("second Recover() = %+v, %v", result, err)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverFailureReturnsCompletedActions(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writePath(fixture.target, "new")
	fixture.writePath(fixture.journal.Filesystem.BackupPath, "old")
	fixture.persist()
	service := fixture.freshService()
	cleanupErr := errors.New("remove backup failed")
	service.files.removeAll = func(path string) error {
		if path == fixture.journal.Filesystem.BackupPath {
			return cleanupErr
		}
		return os.RemoveAll(path)
	}

	result, err := service.Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, cleanupErr) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Kind != RecoveryActionSetManifest {
		t.Fatalf("completed actions = %+v", result.Actions)
	}
	if _, statErr := os.Stat(fixture.journalPath); statErr != nil {
		t.Fatalf("journal missing after partial recovery: %v", statErr)
	}
}

func TestRecoverDirectionUpdateFailurePreservesArtifacts(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writePath(fixture.journal.Filesystem.BackupPath, "old")
	fixture.persist()
	events := []string{}
	recording := &recordingJournalStore{
		journal: fixture.journal,
		found:   true,
		events:  &events,
		fail:    map[string]error{"update:rolling-back": errors.New("update failed")},
	}
	service := newService(fixture.manifests, recording)

	result, err := service.Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, recording.fail["update:rolling-back"]) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if _, statErr := os.Stat(fixture.journal.Filesystem.BackupPath); statErr != nil {
		t.Fatalf("backup changed after phase update failure: %v", statErr)
	}
}

func TestNewManagerUsesCentralJournal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	events := []string{}
	resolver := &recordingMCPResolver{events: &events, err: errors.New("unused")}
	manager, err := NewManager(&recordingManifestWriter{events: &events}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := any(manager).(Manager); !ok {
		t.Fatal("NewManager result does not implement Manager")
	}
}

func TestNewManagerRejectsNilManifestWriter(t *testing.T) {
	if _, err := NewManager(nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("NewManager(nil) error = %v", err)
	}
}
