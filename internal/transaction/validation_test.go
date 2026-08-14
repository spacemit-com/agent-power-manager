package transaction

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

type validationAdapter struct {
	target string
}

type nilValidationAdapter struct{}

type nilPluginValidationAdapter struct{}

func (*nilPluginValidationAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}
func (*nilPluginValidationAdapter) PrepareInstall(context.Context, domain.PluginDefinition) error {
	return nil
}
func (*nilPluginValidationAdapter) Inspect(context.Context, string) (adapter.PluginFact, error) {
	return adapter.PluginFact{}, nil
}
func (*nilPluginValidationAdapter) Add(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (*nilPluginValidationAdapter) Remove(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (*nilPluginValidationAdapter) TargetPath(string) (string, error) { return "", nil }

func (*nilValidationAdapter) TargetPath(domain.PowerRef) (string, error) { return "", nil }
func (*nilValidationAdapter) Install(domain.PowerRef, string) (adapter.SkillInstallResult, error) {
	return adapter.SkillInstallResult{}, nil
}

func (a validationAdapter) TargetPath(domain.PowerRef) (string, error) {
	return a.target, nil
}
func (a validationAdapter) Install(domain.PowerRef, string) (adapter.SkillInstallResult, error) {
	return adapter.SkillInstallResult{Published: true}, nil
}

func TestValidateJournalRejectsUnsafeFields(t *testing.T) {
	valid := validInstallJournal(t)
	tests := map[string]func(*Journal){
		"unknown schema":    func(j *Journal) { j.SchemaVersion = 3 },
		"empty transaction": func(j *Journal) { j.TransactionID = "" },
		"relative target":   func(j *Journal) { j.Filesystem.TargetPath = "relative" },
		"dirty target":      func(j *Journal) { j.Filesystem.TargetPath += string(filepath.Separator) + ".." },
		"wrong artifact id": func(j *Journal) {
			j.Filesystem.TransactionDir = filepath.Join(filepath.Dir(j.Filesystem.TargetPath), ".apm-transactions", "other")
		},
		"uninstall publishing": func(j *Journal) { j.Operation, j.Phase = OperationUninstall, PhasePublishing },
		"install quarantine": func(j *Journal) {
			j.Filesystem.QuarantinePath = filepath.Join(j.Filesystem.TransactionDir, "quarantine")
		},
		"filesystem plugin": func(j *Journal) {
			j.Power.Type, j.Previous.Type = domain.PowerTypePlugin, domain.PowerTypePlugin
		},
		"filesystem MCP": func(j *Journal) {
			j.Power.Type, j.Previous.Type = domain.PowerTypeMCP, domain.PowerTypeMCP
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			journal := valid
			filesystem := *valid.Filesystem
			journal.Filesystem = &filesystem
			mutate(&journal)
			if err := validateJournal(journal); !errors.Is(err, ErrInvalidJournal) {
				t.Fatalf("validateJournal() error = %v, want ErrInvalidJournal", err)
			}
		})
	}
}

func TestTransactionContractErrorsAreStable(t *testing.T) {
	for _, err := range []error{
		ErrActiveTransaction,
		ErrInvalidRequest,
		ErrInvalidJournal,
		ErrUnsafePath,
		ErrIndeterminateState,
		ErrRecoveryFailed,
	} {
		if err == nil {
			t.Fatal("transaction sentinel is nil")
		}
	}
}

func validationPaths(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "skills", "demo")
	return staging, target
}

func validInstallJournal(t *testing.T) Journal {
	t.Helper()
	staging, target := validationPaths(t)
	id := "00112233445566778899aabbccddeeff"
	dir := filepath.Join(filepath.Dir(target), ".apm-transactions", id)
	return Journal{
		SchemaVersion: 1,
		TransactionID: id,
		Driver:        DriverFilesystem,
		Operation:     OperationInstall,
		Phase:         PhasePreparing,
		Agent:         domain.AgentCodex,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "2.0.0"},
		Previous:      powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}),
		Filesystem: &FilesystemJournalPayload{
			StagingRoot:    staging,
			TargetPath:     target,
			TransactionDir: dir,
			BackupPath:     filepath.Join(dir, "backup"),
		},
	}
}

func powerRefPointer(ref domain.PowerRef) *domain.PowerRef { return &ref }
