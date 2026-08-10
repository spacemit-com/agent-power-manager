package transaction

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectPathStateReportsFilesystemEntries(t *testing.T) {
	journal := validInstallJournal(t)
	if err := os.MkdirAll(journal.TargetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(journal.BackupPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal.BackupPath, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(journal.StagingRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	state, err := inspectPathState(journal)
	if err != nil {
		t.Fatal(err)
	}
	if !state.TargetExists || !state.BackupExists || !state.StagingExists || state.QuarantineExists {
		t.Fatalf("inspectPathState() = %+v", state)
	}
}

func TestRecoveryDirectionFollowsPhaseAndPathState(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
		phase     Phase
		state     PathState
		want      RecoveryDirection
		wantErr   bool
	}{
		{name: "install preparing", operation: OperationInstall, phase: PhasePreparing, state: PathState{BackupExists: true}, want: RecoveryRollback},
		{name: "install rolling back", operation: OperationInstall, phase: PhaseRollingBack, state: PathState{TargetExists: true}, want: RecoveryRollback},
		{name: "install publishing target absent", operation: OperationInstall, phase: PhasePublishing, state: PathState{BackupExists: true}, want: RecoveryRollback},
		{name: "install publishing target present", operation: OperationInstall, phase: PhasePublishing, state: PathState{TargetExists: true, BackupExists: true}, want: RecoveryCommit},
		{name: "install committing", operation: OperationInstall, phase: PhaseCommitting, state: PathState{TargetExists: true}, want: RecoveryCommit},
		{name: "install contradictory rollback", operation: OperationInstall, phase: PhaseRollingBack, state: PathState{TargetExists: true, BackupExists: true}, wantErr: true},
		{name: "uninstall removing quarantined", operation: OperationUninstall, phase: PhaseRemoving, state: PathState{QuarantineExists: true}, want: RecoveryCommit},
		{name: "uninstall removing missing", operation: OperationUninstall, phase: PhaseRemoving, state: PathState{}, want: RecoveryCommit},
		{name: "uninstall removing target", operation: OperationUninstall, phase: PhaseRemoving, state: PathState{TargetExists: true}, want: RecoveryRollback},
		{name: "uninstall rolling back", operation: OperationUninstall, phase: PhaseRollingBack, state: PathState{TargetExists: true}, want: RecoveryRollback},
		{name: "uninstall committing", operation: OperationUninstall, phase: PhaseCommitting, state: PathState{QuarantineExists: true}, want: RecoveryCommit},
		{name: "uninstall contradictory", operation: OperationUninstall, phase: PhaseRemoving, state: PathState{TargetExists: true, QuarantineExists: true}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal := Journal{Operation: test.operation, Phase: test.phase}
			got, err := recoveryDirection(journal, test.state)
			if test.wantErr {
				if !errors.Is(err, ErrIndeterminateState) {
					t.Fatalf("recoveryDirection() error = %v, want ErrIndeterminateState", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("recoveryDirection() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
