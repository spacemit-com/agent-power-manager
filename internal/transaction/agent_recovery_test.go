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

type agentRecoveryFixture struct {
	t           *testing.T
	target      string
	staging     string
	journal     Journal
	journalPath string
	manifests   *recordingManifestWriter
	events      []string
}

func newAgentRecoveryFixture(t *testing.T, operation Operation, phase Phase) *agentRecoveryFixture {
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
	target := filepath.Join(t.TempDir(), "agents", "demo", "agent.yaml")
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
		Agent:         domain.AgentBibit,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "2.0.0"},
		Filesystem: &FilesystemJournalPayload{
			TargetPath:     target,
			TransactionDir: artifacts.dir,
		},
	}
	if operation == OperationInstall {
		journal.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "1.0.0"})
		journal.Filesystem.StagingRoot = staging
		journal.Filesystem.BackupPath = artifacts.backup
	} else {
		journal.Filesystem.QuarantinePath = artifacts.quarantine
	}
	fixture := &agentRecoveryFixture{t: t, target: target, staging: staging, journalPath: journalPath, journal: journal}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	return fixture
}

func (f *agentRecoveryFixture) persist() {
	f.t.Helper()
	if err := newFilesystemJournalStore(f.journalPath).Create(f.journal); err != nil {
		f.t.Fatal(err)
	}
}

func (f *agentRecoveryFixture) freshService() *Service {
	f.t.Helper()
	return newService(f.manifests, newFilesystemJournalStore(f.journalPath))
}

func (f *agentRecoveryFixture) writeFile(path, contents string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *agentRecoveryFixture) assertJournalAbsent() {
	f.t.Helper()
	if _, err := os.Lstat(f.journalPath); !errors.Is(err, os.ErrNotExist) {
		f.t.Fatalf("journal remains: %v", err)
	}
}

func TestRecoverAgentPublishingInstallWithTargetCompletesCommit(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeFile(fixture.target, "new")
	fixture.writeFile(fixture.journal.Filesystem.BackupPath, "old")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit || result.OriginalPhase != PhasePublishing {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.setRef != fixture.journal.Power {
		t.Fatalf("manifest Set = %+v", fixture.manifests.setRef)
	}
	contents, readErr := os.ReadFile(fixture.target)
	if readErr != nil || string(contents) != "new" {
		t.Fatalf("target = %q, %v", contents, readErr)
	}
	if _, err := os.Lstat(fixture.journal.Filesystem.BackupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup remains: %v", err)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverAgentPublishingInstallWithoutTargetRollsBackBackup(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeFile(fixture.journal.Filesystem.BackupPath, "old")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	contents, readErr := os.ReadFile(fixture.target)
	if readErr != nil || string(contents) != "old" {
		t.Fatalf("restored target = %q, %v", contents, readErr)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverAgentUninstallDirectionsConverge(t *testing.T) {
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
			fixture := newAgentRecoveryFixture(t, OperationUninstall, test.phase)
			if test.target {
				fixture.writeFile(fixture.target, "installed")
			}
			if test.quarantine {
				fixture.writeFile(fixture.journal.Filesystem.QuarantinePath, "installed")
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
				contents, readErr := os.ReadFile(fixture.target)
				if readErr != nil || string(contents) != "installed" {
					t.Fatalf("target not restored: %q, %v", contents, readErr)
				}
			}
			fixture.assertJournalAbsent()
		})
	}
}
