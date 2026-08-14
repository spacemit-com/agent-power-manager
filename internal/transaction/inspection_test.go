package transaction

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestInspectReportsActiveJournalWithoutMutation(t *testing.T) {
	fixture := newInstallFixture(t)
	journal := validInstallJournal(t)
	fixture.journals.journal = journal
	fixture.journals.found = true

	got, err := fixture.service.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := InspectionResult{
		Active:        true,
		TransactionID: journal.TransactionID,
		Operation:     journal.Operation,
		Agent:         journal.Agent,
		Power:         journal.Power,
		OriginalPhase: journal.Phase,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect() = %#v, want %#v", got, want)
	}
	if fixture.journals.inspectCalls != 1 || fixture.journals.updateCalls != 0 || fixture.journals.deleteCalls != 0 {
		t.Fatalf("journal calls = inspect %d update %d delete %d", fixture.journals.inspectCalls, fixture.journals.updateCalls, fixture.journals.deleteCalls)
	}
}

func TestInspectReturnsZeroResultWithoutValidActiveJournal(t *testing.T) {
	inspectErr := errors.New("inspect failed")

	tests := []struct {
		name       string
		configure  func(*testing.T, *recordingJournalStore)
		wantErr    error
		wantResult InspectionResult
	}{
		{
			name: "no journal",
			configure: func(_ *testing.T, store *recordingJournalStore) {
				store.found = false
			},
		},
		{
			name: "store error",
			configure: func(_ *testing.T, store *recordingJournalStore) {
				store.fail["inspect"] = inspectErr
			},
			wantErr: inspectErr,
		},
		{
			name: "invalid schema",
			configure: func(t *testing.T, store *recordingJournalStore) {
				store.journal = validInstallJournal(t)
				store.journal.SchemaVersion = 0
				store.found = true
			},
			wantErr: ErrInvalidJournal,
		},
		{
			name: "invalid driver",
			configure: func(t *testing.T, store *recordingJournalStore) {
				store.journal = validInstallJournal(t)
				store.journal.Driver = Driver("invalid")
				store.found = true
			},
			wantErr: ErrInvalidJournal,
		},
		{
			name: "invalid phase",
			configure: func(t *testing.T, store *recordingJournalStore) {
				store.journal = validInstallJournal(t)
				store.journal.Phase = Phase("invalid")
				store.found = true
			},
			wantErr: ErrInvalidJournal,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newInstallFixture(t)
			test.configure(t, fixture.journals)

			got, err := fixture.service.Inspect(context.Background())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Inspect() error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			if !reflect.DeepEqual(got, test.wantResult) {
				t.Fatalf("Inspect() = %#v, want %#v", got, test.wantResult)
			}
			if fixture.journals.inspectCalls != 1 || fixture.journals.updateCalls != 0 || fixture.journals.deleteCalls != 0 {
				t.Fatalf("journal calls = inspect %d update %d delete %d", fixture.journals.inspectCalls, fixture.journals.updateCalls, fixture.journals.deleteCalls)
			}
		})
	}
}
