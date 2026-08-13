package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

func TestDecodeJournalAcceptsOnlyCurrentSchemaV1Envelope(t *testing.T) {
	valid := validInstallJournal(t)
	valid.SchemaVersion = 1
	contents, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeJournal(contents)
	if err != nil || !reflect.DeepEqual(got, valid) {
		t.Fatalf("decodeJournal(valid) = %+v, %v", got, err)
	}

	legacy := legacyFlatJournalJSON(valid)
	invalid := map[string][]byte{
		"missing version": bytes.Replace(contents, []byte(`"schema_version":1,`), nil, 1),
		"version zero":    bytes.Replace(contents, []byte(`"schema_version":1`), []byte(`"schema_version":0`), 1),
		"version two":     bytes.Replace(contents, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1),
		"version three":   bytes.Replace(contents, []byte(`"schema_version":1`), []byte(`"schema_version":3`), 1),
		"legacy flat":     []byte(legacy),
		"unknown field":   bytes.Replace(contents, []byte(`"phase"`), []byte(`"unknown":true,"phase"`), 1),
		"trailing JSON":   append(append([]byte(nil), contents...), []byte(` {}`)...),
	}
	for name, document := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeJournal(document); !errors.Is(err, ErrInvalidJournal) {
				t.Fatalf("decodeJournal() error = %v, want ErrInvalidJournal", err)
			}
		})
	}
}

func TestValidatePluginJournalEnvelope(t *testing.T) {
	valid := validPluginMarketplaceJournal()
	if err := validateJournal(valid); err != nil {
		t.Fatalf("validate marketplace plugin journal: %v", err)
	}
	invalid := map[string]func(*Journal){
		"wrong power":       func(j *Journal) { j.Power.Type = domain.PowerTypeSkill },
		"missing payload":   func(j *Journal) { j.Plugin = nil },
		"filesystem too":    func(j *Journal) { j.Filesystem = &FilesystemJournalPayload{} },
		"agent cli too":     func(j *Journal) { j.AgentCLI = &AgentCLIJournalPayload{} },
		"unknown deploy":    func(j *Journal) { j.Plugin.Deployment = "unknown" },
		"marketplace paths": func(j *Journal) { j.Plugin.Filesystem = &PluginFilesystemJournalPayload{} },
		"unsafe target":     func(j *Journal) { j.Plugin.Target.ActualName = "../demo" },
		"illegal phase":     func(j *Journal) { j.Phase = PhasePreparing },
		"first previous": func(j *Journal) {
			j.Plugin.Previous = &adapter.PluginTarget{ActualName: "demo-v0.9.0", Selector: "demo-v0.9.0@apm"}
		},
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			journal := valid
			payload := *valid.Plugin
			journal.Plugin = &payload
			mutate(&journal)
			if err := validateJournal(journal); !errors.Is(err, ErrInvalidJournal) {
				t.Fatalf("validateJournal() error = %v, want ErrInvalidJournal", err)
			}
		})
	}
}

func TestValidatePluginFilesystemJournals(t *testing.T) {
	staging, _ := validationPaths(t)
	id := "00112233445566778899aabbccddeeff"
	pluginsRoot := filepath.Join(t.TempDir(), "plugins")
	transactionDir := filepath.Join(pluginsRoot, ".apm-transactions", id)
	previousRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	previousTarget := adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0"}
	update := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall,
		Phase: PhaseRemovingOld, Agent: domain.AgentHermes,
		Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}, Previous: &previousRef,
		Plugin: &PluginJournalPayload{
			Deployment: domain.PluginDeploymentFilesystem,
			Target:     adapter.PluginTarget{ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0"},
			Previous:   &previousTarget, PreviousPresent: true,
			Filesystem: &PluginFilesystemJournalPayload{
				StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", "demo"),
				TargetPath: filepath.Join(pluginsRoot, "demo-v2.0.0"), PreviousTargetPath: filepath.Join(pluginsRoot, "demo-v1.0.0"),
				TransactionDir: transactionDir, PublishPath: filepath.Join(transactionDir, "publish"),
				PreviousQuarantinePath: filepath.Join(transactionDir, "previous"),
			},
		},
	}
	if err := validateJournal(update); err != nil {
		t.Fatalf("validate filesystem update: %v", err)
	}
	uninstall := update
	uninstall.Operation, uninstall.Phase, uninstall.Previous = OperationUninstall, PhaseRemoving, nil
	uninstall.Power = previousRef
	uninstall.Plugin = &PluginJournalPayload{
		Deployment: domain.PluginDeploymentFilesystem, Target: previousTarget, TargetPresent: true,
		Filesystem: &PluginFilesystemJournalPayload{
			TargetPath: filepath.Join(pluginsRoot, "demo-v1.0.0"), TransactionDir: transactionDir,
			TargetQuarantinePath: filepath.Join(transactionDir, "target"),
		},
	}
	if err := validateJournal(uninstall); err != nil {
		t.Fatalf("validate filesystem uninstall: %v", err)
	}
}

func validPluginMarketplaceJournal() Journal {
	return Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        DriverPlugin,
		Operation:     OperationInstall,
		Phase:         PhaseAddingNew,
		Agent:         domain.AgentCodex,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		Plugin: &PluginJournalPayload{
			Deployment: domain.PluginDeploymentMarketplace,
			Target:     adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm"},
		},
	}
}

func TestValidateJournalAcceptsOnlyOneMatchingV1DriverPayload(t *testing.T) {
	valid := validInstallJournal(t)
	if err := validateJournal(valid); err != nil {
		t.Fatalf("validateJournal(valid v1) error = %v", err)
	}

	tests := map[string]func(*Journal){
		"unknown schema":  func(j *Journal) { j.SchemaVersion = 3 },
		"missing driver":  func(j *Journal) { j.Driver = "" },
		"unknown driver":  func(j *Journal) { j.Driver = "database" },
		"missing payload": func(j *Journal) { j.Filesystem = nil },
		"both payloads":   func(j *Journal) { j.AgentCLI = &AgentCLIJournalPayload{} },
		"wrong payload": func(j *Journal) {
			j.Filesystem, j.AgentCLI = nil, &AgentCLIJournalPayload{}
		},
		"illegal phase": func(j *Journal) { j.Phase = PhaseRemovingOld },
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

func legacyFlatJournalJSON(journal Journal) string {
	return `{"schema_version":1,"transaction_id":"` + journal.TransactionID +
		`","operation":"install","phase":"preparing","agent":"codex",` +
		`"power":{"Name":"demo","Type":"skill","Version":"2.0.0"},` +
		`"staging_root":"` + journal.Filesystem.StagingRoot +
		`","target_path":"` + journal.Filesystem.TargetPath +
		`","transaction_dir":"` + journal.Filesystem.TransactionDir +
		`","backup_path":"` + journal.Filesystem.BackupPath + `"}`
}
