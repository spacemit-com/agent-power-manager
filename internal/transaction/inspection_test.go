package transaction

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type failFastInspectionManifestWriter struct {
	t *testing.T
}

func (w failFastInspectionManifestWriter) Set(domain.AgentID, domain.PowerRef) error {
	w.t.Fatal("Inspect() accessed manifest Set")
	return nil
}

func (w failFastInspectionManifestWriter) Remove(domain.AgentID, domain.PowerType, string) error {
	w.t.Fatal("Inspect() accessed manifest Remove")
	return nil
}

type failFastInspectionAdapterResolver struct {
	t *testing.T
}

func (r failFastInspectionAdapterResolver) ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error) {
	r.t.Fatal("Inspect() resolved an MCP Adapter")
	return nil, nil
}

func (r failFastInspectionAdapterResolver) ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error) {
	r.t.Fatal("Inspect() resolved a Plugin Adapter")
	return nil, nil
}

func (r failFastInspectionAdapterResolver) ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error) {
	r.t.Fatal("Inspect() resolved an Agent Adapter")
	return nil, nil
}

func failFastInspectionFileOps(t *testing.T) fileOps {
	t.Helper()
	fail := func(operation string) {
		t.Fatalf("Inspect() accessed fileOps.%s", operation)
	}
	return fileOps{
		lstat: func(string) (os.FileInfo, error) {
			fail("lstat")
			return nil, nil
		},
		mkdir: func(string, os.FileMode) error {
			fail("mkdir")
			return nil
		},
		chmod: func(string, os.FileMode) error {
			fail("chmod")
			return nil
		},
		rename: func(string, string) error {
			fail("rename")
			return nil
		},
		remove: func(string) error {
			fail("remove")
			return nil
		},
		removeAll: func(string) error {
			fail("removeAll")
			return nil
		},
		readDir: func(string) ([]os.DirEntry, error) {
			fail("readDir")
			return nil, nil
		},
	}
}

func TestInspectReportsActiveJournalWithoutMutation(t *testing.T) {
	fixture := newInstallFixture(t)
	journal := validInstallJournal(t)
	fixture.journals.journal = journal
	fixture.journals.found = true
	fixture.service.manifests = failFastInspectionManifestWriter{t: t}
	resolver := failFastInspectionAdapterResolver{t: t}
	fixture.service.mcpAdapters = resolver
	fixture.service.pluginAdapters = resolver
	fixture.service.files = failFastInspectionFileOps(t)

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
	assertInspectJournalCalls(t, fixture.journals)
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
			assertInspectJournalCalls(t, fixture.journals)
		})
	}
}

func assertInspectJournalCalls(t *testing.T, store *recordingJournalStore) {
	t.Helper()
	if store.inspectCalls != 1 || store.createCalls != 0 || store.updateCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf(
			"journal calls = inspect %d create %d update %d delete %d, want 1/0/0/0",
			store.inspectCalls,
			store.createCalls,
			store.updateCalls,
			store.deleteCalls,
		)
	}
}
