package transaction

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type recordingMCPResolver struct {
	events  *[]string
	adapter adapter.MCPAdapter
	err     error
	agents  []domain.AgentID
}

func (r *recordingMCPResolver) ResolveMCP(agent domain.AgentID) (adapter.MCPAdapter, error) {
	*r.events = append(*r.events, "resolver:"+string(agent))
	r.agents = append(r.agents, agent)
	return r.adapter, r.err
}

type mcpRecoveryFixture struct {
	events    []string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   *recordingMCPAdapter
	resolver  *recordingMCPResolver
	service   *Service
	journal   Journal
}

func newMCPRecoveryFixture(t *testing.T, operation Operation, phase Phase, previous bool, presence adapter.MCPPresence) *mcpRecoveryFixture {
	t.Helper()
	fixture := &mcpRecoveryFixture{}
	server := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"}
	fixture.journal = Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        DriverAgentCLI,
		Operation:     operation,
		Phase:         phase,
		Agent:         domain.AgentClaudeCode,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "2.0.0"},
		AgentCLI:      &AgentCLIJournalPayload{Server: server},
	}
	if previous {
		old := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "old", Args: []string{"--serve"}}
		fixture.journal.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "1.0.0"})
		fixture.journal.AgentCLI.PreviousServer = &old
	}
	fixture.journals = &recordingJournalStore{journal: fixture.journal, found: true, events: &fixture.events, fail: map[string]error{}}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.adapter = &recordingMCPAdapter{
		events:        &fixture.events,
		presence:      presence,
		addAnswers:    []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}}, {result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}}},
		removeAnswers: []mcpMutationAnswer{{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}}},
	}
	fixture.resolver = &recordingMCPResolver{events: &fixture.events, adapter: fixture.adapter}
	fixture.service = newService(fixture.manifests, fixture.journals, fixture.resolver)
	return fixture
}

func TestMCPRecoveryPhaseTable(t *testing.T) {
	tests := []struct {
		name          string
		operation     Operation
		phase         Phase
		previous      bool
		presence      adapter.MCPPresence
		wantDirection RecoveryDirection
		wantEvents    []string
	}{
		{
			name: "first add present commits", operation: OperationInstall, phase: PhaseAddingNew, presence: adapter.MCPPresent,
			wantDirection: RecoveryCommit,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:committing", "manifest:set", "delete:journal"},
		},
		{
			name: "first add absent rolls back", operation: OperationInstall, phase: PhaseAddingNew, presence: adapter.MCPAbsent,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:rolling-back", "delete:journal"},
		},
		{
			name: "update remove old present rolls back", operation: OperationInstall, phase: PhaseRemovingOld, previous: true, presence: adapter.MCPPresent,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:rolling-back", "delete:journal"},
		},
		{
			name: "update remove old absent restores", operation: OperationInstall, phase: PhaseRemovingOld, previous: true, presence: adapter.MCPAbsent,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:rolling-back", "adapter:add", "delete:journal"},
		},
		{
			name: "update add new present commits", operation: OperationInstall, phase: PhaseAddingNew, previous: true, presence: adapter.MCPPresent,
			wantDirection: RecoveryCommit,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:committing", "manifest:set", "delete:journal"},
		},
		{
			name: "update add new absent restores", operation: OperationInstall, phase: PhaseAddingNew, previous: true, presence: adapter.MCPAbsent,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:rolling-back", "adapter:add", "delete:journal"},
		},
		{
			name: "uninstall remove absent commits", operation: OperationUninstall, phase: PhaseRemoving, presence: adapter.MCPAbsent,
			wantDirection: RecoveryCommit,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:committing", "manifest:remove", "delete:journal"},
		},
		{
			name: "uninstall remove present rolls back", operation: OperationUninstall, phase: PhaseRemoving, presence: adapter.MCPPresent,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "update:rolling-back", "delete:journal"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, test.operation, test.phase, test.previous, test.presence)
			result, err := fixture.service.Recover(context.Background())
			if err != nil || !result.Recovered || result.Direction != test.wantDirection || result.OriginalPhase != test.phase {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if fixture.journals.found || !reflect.DeepEqual(fixture.events, test.wantEvents) {
				t.Fatalf("journal/events = %v/%v, want %v", fixture.journals.found, fixture.events, test.wantEvents)
			}
		})
	}
}

func TestMCPRecoveryCommittingDoesNotRequireCLI(t *testing.T) {
	for _, operation := range []Operation{OperationInstall, OperationUninstall} {
		t.Run(string(operation), func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, operation, PhaseCommitting, false, adapter.MCPAbsent)
			fixture.resolver.err = errors.New("CLI unavailable")
			result, err := fixture.service.Recover(context.Background())
			if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			wantAction := RecoveryActionSetManifest
			if operation == OperationUninstall {
				wantAction = RecoveryActionRemoveManifest
			}
			if len(result.Actions) != 1 || result.Actions[0].Kind != wantAction || len(fixture.resolver.agents) != 0 {
				t.Fatalf("actions/resolver = %+v/%v", result.Actions, fixture.resolver.agents)
			}
		})
	}
}

func TestMCPRecoveryRollingBackRestoresRequiredOldState(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
		previous  bool
		presence  adapter.MCPPresence
		wantWrite string
	}{
		{name: "update absent adds old", operation: OperationInstall, previous: true, presence: adapter.MCPAbsent, wantWrite: "adapter:add"},
		{name: "uninstall absent adds removed", operation: OperationUninstall, presence: adapter.MCPAbsent, wantWrite: "adapter:add"},
		{name: "first install present removes new", operation: OperationInstall, presence: adapter.MCPPresent, wantWrite: "adapter:remove"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, test.operation, PhaseRollingBack, test.previous, test.presence)
			result, err := fixture.service.Recover(context.Background())
			if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			found := false
			for _, event := range fixture.events {
				found = found || event == test.wantWrite
			}
			if !found {
				t.Fatalf("events = %v, want %q", fixture.events, test.wantWrite)
			}
		})
	}
}

func TestMCPRecoveryInspectAndResolverFailuresRetainJournal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*mcpRecoveryFixture) error
	}{
		{name: "inspect", mutate: func(f *mcpRecoveryFixture) error {
			f.adapter.inspectErr = errors.New("inspect failed")
			return f.adapter.inspectErr
		}},
		{name: "resolver", mutate: func(f *mcpRecoveryFixture) error {
			f.resolver.err = errors.New("resolver failed")
			return f.resolver.err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, OperationInstall, PhaseAddingNew, false, adapter.MCPAbsent)
			cause := test.mutate(fixture)
			result, err := fixture.service.Recover(context.Background())
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, cause) || result.Recovered || !fixture.journals.found {
				t.Fatalf("Recover() = %+v, %v, journal=%v", result, err, fixture.journals.found)
			}
		})
	}
}

func TestCrossTypeRecoveryKeepsFilesystemDriverIndependentOfMCPResolver(t *testing.T) {
	fixture := newRecoveryFixture(t, OperationInstall, PhasePreparing)
	fixture.persist()
	events := []string{}
	resolver := &recordingMCPResolver{events: &events, err: errors.New("must not resolve MCP")}
	service := newService(fixture.manifests, newFilesystemJournalStore(fixture.journalPath), resolver)
	result, err := service.Recover(context.Background())
	if err != nil || !result.Recovered || len(resolver.agents) != 0 {
		t.Fatalf("Recover() = %+v, %v, resolver=%v", result, err, resolver.agents)
	}
}
