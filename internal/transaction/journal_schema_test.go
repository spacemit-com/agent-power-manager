package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
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
			j.Driver, j.Filesystem, j.AgentCLI = DriverAgentCLI, nil, &AgentCLIJournalPayload{}
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
