package transaction

import (
	"errors"
	"testing"
)

func TestDecodeJournalConvertsLegacyFilesystemSchemaToV2(t *testing.T) {
	legacy := validInstallJournal(t)
	got, err := decodeJournal([]byte(legacyJournalJSON(legacy)))
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 2 || got.Driver != DriverFilesystem || got.Filesystem == nil || got.AgentCLI != nil {
		t.Fatalf("decoded legacy journal = %+v, want v2 filesystem payload", got)
	}
	if got.Filesystem.StagingRoot != legacy.Filesystem.StagingRoot || got.Filesystem.TargetPath != legacy.Filesystem.TargetPath ||
		got.Filesystem.TransactionDir != legacy.Filesystem.TransactionDir || got.Filesystem.BackupPath != legacy.Filesystem.BackupPath {
		t.Fatalf("filesystem payload = %+v, want legacy paths", got.Filesystem)
	}
}

func TestValidateJournalAcceptsOnlyOneMatchingV2DriverPayload(t *testing.T) {
	legacy := validInstallJournal(t)
	valid := Journal{
		SchemaVersion: 2,
		TransactionID: legacy.TransactionID,
		Driver:        DriverFilesystem,
		Operation:     legacy.Operation,
		Phase:         legacy.Phase,
		Agent:         legacy.Agent,
		Power:         legacy.Power,
		Previous:      legacy.Previous,
		Filesystem: &FilesystemJournalPayload{
			StagingRoot:    legacy.Filesystem.StagingRoot,
			TargetPath:     legacy.Filesystem.TargetPath,
			TransactionDir: legacy.Filesystem.TransactionDir,
			BackupPath:     legacy.Filesystem.BackupPath,
		},
	}
	if err := validateJournal(valid); err != nil {
		t.Fatalf("validateJournal(valid v2) error = %v", err)
	}

	tests := map[string]func(*Journal){
		"unknown schema":  func(j *Journal) { j.SchemaVersion = 3 },
		"missing driver":  func(j *Journal) { j.Driver = "" },
		"unknown driver":  func(j *Journal) { j.Driver = "database" },
		"missing payload": func(j *Journal) { j.Filesystem = nil },
		"both payloads":   func(j *Journal) { j.AgentCLI = &AgentCLIJournalPayload{} },
		"wrong payload": func(j *Journal) {
			j.Driver, j.Filesystem, j.AgentCLI = DriverAgentCLI, nil, &AgentCLIJournalPayload{}
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

func TestDecodeJournalRejectsUnknownV2FieldsAndTrailingJSON(t *testing.T) {
	for _, contents := range []string{
		`{"schema_version":2,"unknown":true}`,
		`{"schema_version":2} {}`,
	} {
		if _, err := decodeJournal([]byte(contents)); !errors.Is(err, ErrInvalidJournal) {
			t.Fatalf("decodeJournal(%q) error = %v, want ErrInvalidJournal", contents, err)
		}
	}
}
