package transaction

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type mcpMutationAnswer struct {
	result adapter.MCPMutationResult
	err    error
}

type recordingMCPAdapter struct {
	events        *[]string
	presence      adapter.MCPPresence
	inspectErr    error
	onInspect     func()
	onRemove      func()
	addAnswers    []mcpMutationAnswer
	removeAnswers []mcpMutationAnswer
	addServers    []domain.MCPServer
	removeRefs    []domain.PowerRef
}

func (a *recordingMCPAdapter) Inspect(context.Context, string) (adapter.MCPPresence, error) {
	*a.events = append(*a.events, "adapter:inspect")
	if a.onInspect != nil {
		a.onInspect()
	}
	return a.presence, a.inspectErr
}

func (a *recordingMCPAdapter) Add(_ context.Context, _ domain.PowerRef, server domain.MCPServer) (adapter.MCPMutationResult, error) {
	*a.events = append(*a.events, "adapter:add")
	a.addServers = append(a.addServers, server.Clone())
	answer := a.addAnswers[0]
	a.addAnswers = a.addAnswers[1:]
	return answer.result, answer.err
}

func (a *recordingMCPAdapter) Remove(_ context.Context, ref domain.PowerRef) (adapter.MCPMutationResult, error) {
	*a.events = append(*a.events, "adapter:remove")
	a.removeRefs = append(a.removeRefs, ref)
	answer := a.removeAnswers[0]
	a.removeAnswers = a.removeAnswers[1:]
	if a.onRemove != nil {
		a.onRemove()
	}
	return answer.result, answer.err
}

type mcpInstallFixture struct {
	events    []string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   *recordingMCPAdapter
	service   *Service
	request   MCPInstallRequest
}

func newMCPInstallFixture(t *testing.T) *mcpInstallFixture {
	t.Helper()
	fixture := &mcpInstallFixture{}
	fixture.journals = &recordingJournalStore{events: &fixture.events, fail: map[string]error{}}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.adapter = &recordingMCPAdapter{
		events:   &fixture.events,
		presence: adapter.MCPAbsent,
		addAnswers: []mcpMutationAnswer{{
			result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied},
		}},
	}
	fixture.service = newService(fixture.manifests, fixture.journals)
	fixture.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	server := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"}
	fixture.request = MCPInstallRequest{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "2.0.0"},
		Server:  &server,
		Adapter: fixture.adapter,
	}
	return fixture
}

func TestMCPInstallFirstInstallOrderAndClonesServer(t *testing.T) {
	fixture := newMCPInstallFixture(t)
	wantServer := fixture.request.Server.Clone()
	if err := fixture.service.InstallMCP(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if got := fixture.journals.journal.SchemaVersion; got != 1 {
		t.Fatalf("journal schema = %d, want 1", got)
	}
	wantEvents := []string{"adapter:inspect", "create:adding-new", "adapter:add", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(fixture.events, wantEvents) {
		t.Fatalf("events = %v, want %v", fixture.events, wantEvents)
	}
	if fixture.journals.found || fixture.manifests.setRef != fixture.request.Power {
		t.Fatalf("journal found/set ref = %v/%+v", fixture.journals.found, fixture.manifests.setRef)
	}
	if fixture.journals.journal.Driver != DriverAgentCLI || fixture.journals.journal.AgentCLI.Server == nil ||
		!reflect.DeepEqual(*fixture.journals.journal.AgentCLI.Server, wantServer) {
		t.Fatalf("journal = %+v", fixture.journals.journal)
	}
	fixture.request.Server.URL = "https://mutated.example/mcp"
	if fixture.journals.journal.AgentCLI.Server.URL != wantServer.URL {
		t.Fatal("journal retained caller MCP server pointer")
	}
}

func TestMCPInstallPreflightBranchesDoNotCreateJournal(t *testing.T) {
	t.Run("unmanaged conflict", func(t *testing.T) {
		fixture := newMCPInstallFixture(t)
		fixture.adapter.presence = adapter.MCPPresent
		fixture.request.Server = nil
		if err := fixture.service.InstallMCP(context.Background(), fixture.request); !errors.Is(err, adapter.ErrMCPConflict) {
			t.Fatalf("InstallMCP() error = %v, want ErrMCPConflict", err)
		}
		if fixture.journals.found || len(fixture.adapter.addServers) != 0 {
			t.Fatal("conflict path changed state")
		}
	})
	t.Run("missing definition", func(t *testing.T) {
		fixture := newMCPInstallFixture(t)
		fixture.request.Server = nil
		if err := fixture.service.InstallMCP(context.Background(), fixture.request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("InstallMCP() error = %v, want ErrInvalidRequest", err)
		}
		if fixture.journals.found || len(fixture.adapter.addServers) != 0 {
			t.Fatal("invalid path changed state")
		}
	})
}

func TestMCPInstallFirstAddMutationStates(t *testing.T) {
	operationErr := errors.New("add failed")
	tests := []struct {
		name       string
		answer     mcpMutationAnswer
		wantPhase  Phase
		wantFound  bool
		wantErr    error
		wantEvents []string
	}{
		{
			name: "not applied", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationNotApplied}, err: operationErr},
			wantFound: false, wantErr: operationErr,
			wantEvents: []string{"adapter:inspect", "create:adding-new", "adapter:add", "update:rolling-back", "delete:journal"},
		},
		{
			name: "unknown", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationUnknown}, err: operationErr},
			wantFound: true, wantPhase: PhaseAddingNew, wantErr: ErrIndeterminateState,
			wantEvents: []string{"adapter:inspect", "create:adding-new", "adapter:add"},
		},
		{
			name: "applied plus error", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, err: operationErr},
			wantFound: true, wantPhase: PhaseCommitting, wantErr: operationErr,
			wantEvents: []string{"adapter:inspect", "create:adding-new", "adapter:add", "update:committing"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPInstallFixture(t)
			fixture.adapter.addAnswers = []mcpMutationAnswer{test.answer}
			err := fixture.service.InstallMCP(context.Background(), fixture.request)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("InstallMCP() error = %v, want %v", err, test.wantErr)
			}
			if fixture.journals.found != test.wantFound || test.wantFound && fixture.journals.journal.Phase != test.wantPhase {
				t.Fatalf("journal = found %v, phase %q", fixture.journals.found, fixture.journals.journal.Phase)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvents) {
				t.Fatalf("events = %v, want %v", fixture.events, test.wantEvents)
			}
		})
	}
}

func TestMCPInstallUpdateOrder(t *testing.T) {
	fixture := newMCPInstallFixture(t)
	fixture.adapter.presence = adapter.MCPPresent
	fixture.adapter.removeAnswers = []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}}}
	old := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "old", Args: []string{"--serve"}}
	fixture.request.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "1.0.0"})
	fixture.request.PreviousServer = &old

	if err := fixture.service.InstallMCP(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	want := []string{"adapter:inspect", "create:removing-old", "adapter:remove", "update:adding-new", "adapter:add", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if got := fixture.adapter.removeRefs; len(got) != 1 || got[0] != *fixture.request.Previous {
		t.Fatalf("remove refs = %+v", got)
	}
}

func TestMCPInstallUpdateStaleManifestRemovesOnlyMatchingEntry(t *testing.T) {
	fixture := newMCPInstallFixture(t)
	fixture.request.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "1.0.0"})
	fixture.request.Server = nil
	err := fixture.service.InstallMCP(context.Background(), fixture.request)
	if !errors.Is(err, ErrStaleManifest) {
		t.Fatalf("InstallMCP() error = %v, want ErrStaleManifest", err)
	}
	if fixture.journals.found || fixture.manifests.removedAgent != domain.AgentCodex ||
		fixture.manifests.removed != (domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP}) {
		t.Fatalf("journal/removed = %v/%+v", fixture.journals.found, fixture.manifests.removed)
	}
	want := []string{"adapter:inspect", "manifest:remove"}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestMCPInstallUpdateFailureDirections(t *testing.T) {
	operationErr := errors.New("CLI failed")
	t.Run("remove not applied", func(t *testing.T) {
		fixture := mcpUpdateFixture(t)
		fixture.adapter.removeAnswers = []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationNotApplied}, err: operationErr}}
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, operationErr) || fixture.journals.found || len(fixture.adapter.addServers) != 0 {
			t.Fatalf("error/found/adds = %v/%v/%d", err, fixture.journals.found, len(fixture.adapter.addServers))
		}
	})
	t.Run("remove unknown", func(t *testing.T) {
		fixture := mcpUpdateFixture(t)
		fixture.adapter.removeAnswers = []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationUnknown}, err: operationErr}}
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, ErrIndeterminateState) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseRemovingOld {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
	t.Run("new add not applied restores old", func(t *testing.T) {
		fixture := mcpUpdateFixture(t)
		fixture.adapter.addAnswers = []mcpMutationAnswer{
			{result: adapter.MCPMutationResult{State: adapter.MCPMutationNotApplied}, err: operationErr},
			{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}},
		}
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, operationErr) || fixture.journals.found || len(fixture.adapter.addServers) != 2 {
			t.Fatalf("error/found/adds = %v/%v/%d", err, fixture.journals.found, len(fixture.adapter.addServers))
		}
		if !reflect.DeepEqual(fixture.adapter.addServers[1], *fixture.request.PreviousServer) {
			t.Fatalf("restored server = %+v", fixture.adapter.addServers[1])
		}
	})
	t.Run("new add unknown", func(t *testing.T) {
		fixture := mcpUpdateFixture(t)
		fixture.adapter.addAnswers = []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationUnknown}, err: operationErr}}
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, ErrIndeterminateState) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseAddingNew {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
	t.Run("old restore applied plus error keeps rollback direction", func(t *testing.T) {
		fixture := mcpUpdateFixture(t)
		restoreErr := errors.New("restore cleanup failed")
		fixture.adapter.addAnswers = []mcpMutationAnswer{
			{result: adapter.MCPMutationResult{State: adapter.MCPMutationNotApplied}, err: operationErr},
			{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, err: restoreErr},
		}
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, restoreErr) || errors.Is(err, ErrIndeterminateState) ||
			!fixture.journals.found || fixture.journals.journal.Phase != PhaseRollingBack {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
	t.Run("remove applied plus error advances direction", func(t *testing.T) {
		fixture := mcpUpdateFixture(t)
		fixture.adapter.removeAnswers = []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, err: operationErr}}
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, operationErr) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseAddingNew {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
}

func TestMCPInstallCommitFailuresPreserveRecoverableJournal(t *testing.T) {
	t.Run("phase update", func(t *testing.T) {
		fixture := newMCPInstallFixture(t)
		phaseErr := errors.New("phase update failed")
		fixture.journals.fail["update:committing"] = phaseErr
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, phaseErr) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseAddingNew {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
	t.Run("manifest", func(t *testing.T) {
		fixture := newMCPInstallFixture(t)
		fixture.manifests.setErr = errors.New("manifest failed")
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, fixture.manifests.setErr) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
	t.Run("journal delete", func(t *testing.T) {
		fixture := newMCPInstallFixture(t)
		fixture.journals.fail["delete"] = errors.New("delete failed")
		err := fixture.service.InstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, fixture.journals.fail["delete"]) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
}

func mcpUpdateFixture(t *testing.T) *mcpInstallFixture {
	t.Helper()
	fixture := newMCPInstallFixture(t)
	fixture.adapter.presence = adapter.MCPPresent
	fixture.adapter.removeAnswers = []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}}}
	old := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "old", Args: []string{"--serve"}}
	fixture.request.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "1.0.0"})
	fixture.request.PreviousServer = &old
	return fixture
}
