package transaction

import (
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

func (*nilValidationAdapter) DetectAgent() (bool, error)                 { return false, nil }
func (*nilValidationAdapter) TargetPath(domain.PowerRef) (string, error) { return "", nil }
func (*nilValidationAdapter) Install(domain.PowerRef, string) (adapter.InstallResult, error) {
	return adapter.InstallResult{}, nil
}

func (a validationAdapter) DetectAgent() (bool, error) { return true, nil }
func (a validationAdapter) TargetPath(domain.PowerRef) (string, error) {
	return a.target, nil
}
func (a validationAdapter) Install(domain.PowerRef, string) (adapter.InstallResult, error) {
	return adapter.InstallResult{Published: true}, nil
}

func TestValidateInstallRequest(t *testing.T) {
	staging, target := validationPaths(t)
	valid := InstallRequest{
		Agent:       domain.AgentCodex,
		Power:       domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "2.0.0"},
		Previous:    powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}),
		StagingRoot: staging,
		Adapter:     validationAdapter{target: target},
	}
	if err := validateInstallRequest(valid); err != nil {
		t.Fatalf("validateInstallRequest(valid) error = %v", err)
	}

	tests := map[string]func(*InstallRequest){
		"empty agent":       func(r *InstallRequest) { r.Agent = "" },
		"all agents":        func(r *InstallRequest) { r.Agent = domain.AllAgents },
		"empty name":        func(r *InstallRequest) { r.Power.Name = "" },
		"empty type":        func(r *InstallRequest) { r.Power.Type = "" },
		"empty version":     func(r *InstallRequest) { r.Power.Version = "" },
		"empty staging":     func(r *InstallRequest) { r.StagingRoot = "" },
		"nil adapter":       func(r *InstallRequest) { r.Adapter = nil },
		"typed nil adapter": func(r *InstallRequest) { r.Adapter = (*nilValidationAdapter)(nil) },
		"previous name":     func(r *InstallRequest) { r.Previous.Name = "other" },
		"previous type":     func(r *InstallRequest) { r.Previous.Type = domain.PowerTypePlugin },
		"previous version":  func(r *InstallRequest) { r.Previous.Version = "" },
		"plugin power": func(r *InstallRequest) {
			r.Power.Type, r.Previous.Type = domain.PowerTypePlugin, domain.PowerTypePlugin
		},
		"MCP power": func(r *InstallRequest) {
			r.Power.Type, r.Previous.Type = domain.PowerTypeMCP, domain.PowerTypeMCP
		},
		"noncanonical stage": func(r *InstallRequest) { r.StagingRoot += string(filepath.Separator) + ".." },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid
			previous := *valid.Previous
			request.Previous = &previous
			mutate(&request)
			if err := validateInstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateInstallRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestValidateUninstallRequest(t *testing.T) {
	_, target := validationPaths(t)
	valid := UninstallRequest{
		Agent:   domain.AgentHermes,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"},
		Adapter: validationAdapter{target: target},
	}
	if err := validateUninstallRequest(valid); err != nil {
		t.Fatalf("validateUninstallRequest(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*UninstallRequest){
		"nil adapter":  func(r *UninstallRequest) { r.Adapter = nil },
		"plugin power": func(r *UninstallRequest) { r.Power.Type = domain.PowerTypePlugin },
		"MCP power":    func(r *UninstallRequest) { r.Power.Type = domain.PowerTypeMCP },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateUninstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateUninstallRequest(nil adapter) error = %v", err)
			}
		})
	}
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
