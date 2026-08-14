package transaction

import (
	"bytes"
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
		SchemaVersion: 1,
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
	contents := []byte(legacyFlatJournalJSON(fixture.journal))
	if err := os.WriteFile(fixture.journalPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrInvalidJournal) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.setRef.Name != "" || fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest mutated: set=%+v remove=%+v", fixture.manifests.setRef, fixture.manifests.removed)
	}
	after, readErr := os.ReadFile(fixture.journalPath)
	if readErr != nil || !bytes.Equal(after, contents) {
		t.Fatalf("corrupt journal changed: %v, %q", readErr, after)
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
