package transaction

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type mcpUninstallFixture struct {
	events    []string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   *recordingMCPAdapter
	service   *Service
	request   MCPUninstallRequest
}

func newMCPUninstallFixture(t *testing.T) *mcpUninstallFixture {
	t.Helper()
	fixture := &mcpUninstallFixture{}
	fixture.journals = &recordingJournalStore{events: &fixture.events, fail: map[string]error{}}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.adapter = &recordingMCPAdapter{
		events:   &fixture.events,
		presence: adapter.MCPPresent,
		removeAnswers: []mcpMutationAnswer{{
			result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied},
		}},
	}
	fixture.service = newService(fixture.manifests, fixture.journals)
	fixture.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	server := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://old.example/mcp"}
	fixture.request = MCPUninstallRequest{
		Agent:   domain.AgentHermes,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "1.0.0"},
		Server:  &server,
		Adapter: fixture.adapter,
	}
	return fixture
}

func TestMCPUninstallPresentOrder(t *testing.T) {
	fixture := newMCPUninstallFixture(t)
	result, err := fixture.service.UninstallMCP(context.Background(), fixture.request)
	if err != nil || result.TargetMissing {
		t.Fatalf("UninstallMCP() = %+v, %v", result, err)
	}
	if got := fixture.journals.journal.SchemaVersion; got != 1 {
		t.Fatalf("journal schema = %d, want 1", got)
	}
	want := []string{"adapter:inspect", "create:removing", "adapter:remove", "update:committing", "manifest:remove", "delete:journal"}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if fixture.journals.found || fixture.manifests.removedAgent != domain.AgentHermes ||
		fixture.manifests.removed != (domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP}) {
		t.Fatalf("journal/removed = %v/%+v", fixture.journals.found, fixture.manifests.removed)
	}
}

func TestMCPUninstallAbsentRemovesOnlyManifestEntry(t *testing.T) {
	fixture := newMCPUninstallFixture(t)
	fixture.adapter.presence = adapter.MCPAbsent
	fixture.request.Server = nil
	result, err := fixture.service.UninstallMCP(context.Background(), fixture.request)
	if err != nil || !result.TargetMissing {
		t.Fatalf("UninstallMCP() = %+v, %v", result, err)
	}
	if fixture.journals.found || len(fixture.adapter.removeRefs) != 0 ||
		fixture.manifests.removed != (domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP}) {
		t.Fatalf("journal/removes/manifest = %v/%v/%+v", fixture.journals.found, fixture.adapter.removeRefs, fixture.manifests.removed)
	}
	want := []string{"adapter:inspect", "manifest:remove"}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestMCPUninstallAbsentPreservesFactWithManifestError(t *testing.T) {
	fixture := newMCPUninstallFixture(t)
	fixture.adapter.presence = adapter.MCPAbsent
	fixture.manifests.removeErr = errors.New("manifest failed")
	result, err := fixture.service.UninstallMCP(context.Background(), fixture.request)
	if !errors.Is(err, fixture.manifests.removeErr) || !result.TargetMissing || fixture.journals.found {
		t.Fatalf("UninstallMCP() = %+v, %v, journal=%v", result, err, fixture.journals.found)
	}
}

func TestMCPUninstallInspectFailureHasNoSideEffects(t *testing.T) {
	fixture := newMCPUninstallFixture(t)
	fixture.adapter.inspectErr = errors.New("inspect failed")
	result, err := fixture.service.UninstallMCP(context.Background(), fixture.request)
	if !errors.Is(err, fixture.adapter.inspectErr) || result.TargetMissing || fixture.journals.found ||
		fixture.manifests.removed.Name != "" || len(fixture.adapter.removeRefs) != 0 {
		t.Fatalf("UninstallMCP() = %+v, %v with side effects", result, err)
	}
}

func TestMCPUninstallMutationDirections(t *testing.T) {
	operationErr := errors.New("remove failed")
	tests := []struct {
		name       string
		answer     mcpMutationAnswer
		wantFound  bool
		wantPhase  Phase
		wantErr    error
		wantEvents []string
	}{
		{
			name: "not applied", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationNotApplied}, err: operationErr},
			wantErr:    operationErr,
			wantEvents: []string{"adapter:inspect", "create:removing", "adapter:remove", "update:rolling-back", "delete:journal"},
		},
		{
			name: "unknown", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationUnknown}, err: operationErr},
			wantFound: true, wantPhase: PhaseRemoving, wantErr: ErrIndeterminateState,
			wantEvents: []string{"adapter:inspect", "create:removing", "adapter:remove"},
		},
		{
			name: "applied plus error", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, err: operationErr},
			wantFound: true, wantPhase: PhaseCommitting, wantErr: operationErr,
			wantEvents: []string{"adapter:inspect", "create:removing", "adapter:remove", "update:committing"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPUninstallFixture(t)
			fixture.adapter.removeAnswers = []mcpMutationAnswer{test.answer}
			_, err := fixture.service.UninstallMCP(context.Background(), fixture.request)
			if !errors.Is(err, test.wantErr) || fixture.journals.found != test.wantFound ||
				test.wantFound && fixture.journals.journal.Phase != test.wantPhase {
				t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvents) {
				t.Fatalf("events = %v, want %v", fixture.events, test.wantEvents)
			}
		})
	}
}

func TestMCPUninstallCommitFailuresPreserveJournal(t *testing.T) {
	t.Run("manifest", func(t *testing.T) {
		fixture := newMCPUninstallFixture(t)
		fixture.manifests.removeErr = errors.New("manifest failed")
		_, err := fixture.service.UninstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, fixture.manifests.removeErr) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
	t.Run("delete", func(t *testing.T) {
		fixture := newMCPUninstallFixture(t)
		fixture.journals.fail["delete"] = errors.New("delete failed")
		_, err := fixture.service.UninstallMCP(context.Background(), fixture.request)
		if !errors.Is(err, fixture.journals.fail["delete"]) || !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting {
			t.Fatalf("error/journal = %v/%+v", err, fixture.journals.journal)
		}
	})
}

func TestMCPUninstallContextCancelledBeforeInspect(t *testing.T) {
	fixture := newMCPUninstallFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.UninstallMCP(ctx, fixture.request); !errors.Is(err, context.Canceled) {
		t.Fatalf("UninstallMCP() error = %v", err)
	}
	if len(fixture.events) != 0 {
		t.Fatalf("events = %v, want none", fixture.events)
	}
}
