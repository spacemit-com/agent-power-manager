package transaction

import (
	"context"
	"errors"
	"fmt"
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

func (r *recordingMCPResolver) ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error) {
	return nil, r.err
}

func (r *recordingMCPResolver) ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error) {
	return nil, r.err
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
	fixture.journal = Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        DriverAgentCLI,
		Operation:     operation,
		Phase:         phase,
		Agent:         domain.AgentClaudeCode,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "2.0.0"},
		AgentCLI:      &AgentCLIJournalPayload{},
	}
	if operation == OperationInstall {
		server := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"}
		fixture.journal.AgentCLI.Server = &server
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
			name: "uninstall remove present continues removal", operation: OperationUninstall, phase: PhaseRemoving, presence: adapter.MCPPresent,
			wantDirection: RecoveryCommit,
			wantEvents:    []string{"resolver:claude-code", "adapter:inspect", "adapter:remove", "update:committing", "manifest:remove", "delete:journal"},
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

func TestMCPInstallRecoveryCommittingDoesNotRequireCLI(t *testing.T) {
	fixture := newMCPRecoveryFixture(t, OperationInstall, PhaseCommitting, false, adapter.MCPAbsent)
	fixture.resolver.err = errors.New("CLI unavailable")
	result, err := fixture.service.Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Kind != RecoveryActionSetManifest || len(fixture.resolver.agents) != 0 {
		t.Fatalf("actions/resolver = %+v/%v", result.Actions, fixture.resolver.agents)
	}
}

func TestMCPInstallRecoveryRollingBackRestoresRequiredOldState(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
		previous  bool
		presence  adapter.MCPPresence
		wantWrite string
	}{
		{name: "update absent adds old", operation: OperationInstall, previous: true, presence: adapter.MCPAbsent, wantWrite: "adapter:add"},
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

func TestMCPUninstallRecoveryCrashWindowsConvergeToAbsent(t *testing.T) {
	tests := []struct {
		name       string
		phase      Phase
		presence   adapter.MCPPresence
		wantEvents []string
	}{
		{
			name: "before remove", phase: PhaseRemoving, presence: adapter.MCPPresent,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "adapter:remove", "update:committing", "manifest:remove", "delete:journal"},
		},
		{
			name: "after applied remove before phase update", phase: PhaseRemoving, presence: adapter.MCPAbsent,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "update:committing", "manifest:remove", "delete:journal"},
		},
		{
			name: "after rollback phase write", phase: PhaseRollingBack, presence: adapter.MCPPresent,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "update:removing", "adapter:remove", "update:committing", "manifest:remove", "delete:journal"},
		},
		{
			name: "rollback phase with absent target", phase: PhaseRollingBack, presence: adapter.MCPAbsent,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "update:committing", "manifest:remove", "delete:journal"},
		},
		{
			name: "after phase update target present", phase: PhaseCommitting, presence: adapter.MCPPresent,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "adapter:remove", "manifest:remove", "delete:journal"},
		},
		{
			name: "after phase update target absent", phase: PhaseCommitting, presence: adapter.MCPAbsent,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "manifest:remove", "delete:journal"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, OperationUninstall, test.phase, false, test.presence)
			result, err := fixture.service.Recover(context.Background())
			if err != nil || !result.Recovered || result.Direction != RecoveryCommit || fixture.journals.found {
				t.Fatalf("Recover() = %+v, %v, journal=%v", result, err, fixture.journals.found)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvents) {
				t.Fatalf("events = %v, want %v", fixture.events, test.wantEvents)
			}
			if len(fixture.adapter.addServers) != 0 {
				t.Fatalf("uninstall recovery called Add: %+v", fixture.adapter.addServers)
			}
		})
	}
}

func TestMCPUninstallRecoveryCancellationAfterInspectPreservesJournalAndManifest(t *testing.T) {
	for _, phase := range []Phase{PhaseRemoving, PhaseRollingBack, PhaseCommitting} {
		for _, presence := range []adapter.MCPPresence{adapter.MCPPresent, adapter.MCPAbsent} {
			t.Run(string(phase)+"/"+string(presence), func(t *testing.T) {
				fixture := newMCPRecoveryFixture(t, OperationUninstall, phase, false, presence)
				ctx, cancel := context.WithCancel(context.Background())
				fixture.adapter.onInspect = cancel

				result, err := fixture.service.Recover(ctx)
				if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, context.Canceled) || result.Recovered {
					t.Fatalf("Recover() = %+v, %v", result, err)
				}
				if !fixture.journals.found || fixture.journals.journal.Phase != phase || fixture.manifests.removed.Name != "" {
					t.Fatalf("journal/manifest = %v/%+v/%+v", fixture.journals.found, fixture.journals.journal, fixture.manifests.removed)
				}
				if want := []string{"resolver:claude-code", "adapter:inspect"}; !reflect.DeepEqual(fixture.events, want) {
					t.Fatalf("events = %v, want %v", fixture.events, want)
				}
			})
		}
	}
}

func TestMCPUninstallRecoveryCancellationAfterDirectionPersistPreservesNewPhase(t *testing.T) {
	tests := []struct {
		name       string
		phase      Phase
		presence   adapter.MCPPresence
		wantPhase  Phase
		wantEvents []string
	}{
		{
			name: "rolling-back present persists removing", phase: PhaseRollingBack, presence: adapter.MCPPresent, wantPhase: PhaseRemoving,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "update:removing"},
		},
		{
			name: "rolling-back absent persists committing", phase: PhaseRollingBack, presence: adapter.MCPAbsent, wantPhase: PhaseCommitting,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "update:committing"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, OperationUninstall, test.phase, false, test.presence)
			ctx, cancel := context.WithCancel(context.Background())
			fixture.journals.onUpdate = func(journal Journal) {
				if journal.Phase == test.wantPhase {
					cancel()
				}
			}

			result, err := fixture.service.Recover(ctx)
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, context.Canceled) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.journals.found || fixture.journals.journal.Phase != test.wantPhase || fixture.manifests.removed.Name != "" {
				t.Fatalf("journal/manifest = %v/%+v/%+v", fixture.journals.found, fixture.journals.journal, fixture.manifests.removed)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvents) || len(fixture.adapter.removeRefs) != 0 {
				t.Fatalf("events/removes = %v/%v, want %v/none", fixture.events, fixture.adapter.removeRefs, test.wantEvents)
			}
		})
	}
}

func TestMCPUninstallRecoveryDirectionUpdateFailurePreservesPhaseAndErrorIdentity(t *testing.T) {
	tests := []struct {
		name       string
		phase      Phase
		presence   adapter.MCPPresence
		update     Phase
		wantEvents []string
	}{
		{
			name: "rolling-back to removing", phase: PhaseRollingBack, presence: adapter.MCPPresent, update: PhaseRemoving,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "update:removing"},
		},
		{
			name: "absent to committing", phase: PhaseRemoving, presence: adapter.MCPAbsent, update: PhaseCommitting,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "update:committing"},
		},
	}
	for _, test := range tests {
		for _, cancelDuringUpdate := range []bool{false, true} {
			t.Run(test.name+"/cancel="+fmt.Sprint(cancelDuringUpdate), func(t *testing.T) {
				fixture := newMCPRecoveryFixture(t, OperationUninstall, test.phase, false, test.presence)
				phaseErr := errors.New("phase update failed")
				fixture.journals.fail["update:"+string(test.update)] = phaseErr
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if cancelDuringUpdate {
					fixture.journals.beforeUpdate = func(journal Journal) {
						if journal.Phase == test.update {
							cancel()
						}
					}
				}

				result, err := fixture.service.Recover(ctx)
				if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, phaseErr) || result.Recovered {
					t.Fatalf("Recover() = %+v, %v", result, err)
				}
				if cancelDuringUpdate && !errors.Is(err, context.Canceled) {
					t.Fatalf("Recover() error = %v, want joined context.Canceled", err)
				}
				if !fixture.journals.found || fixture.journals.journal.Phase != test.phase || fixture.manifests.removed.Name != "" {
					t.Fatalf("journal/manifest = %v/%+v/%+v", fixture.journals.found, fixture.journals.journal, fixture.manifests.removed)
				}
				if !reflect.DeepEqual(fixture.events, test.wantEvents) || len(fixture.adapter.removeRefs) != 0 {
					t.Fatalf("events/removes = %v/%v, want %v/none", fixture.events, fixture.adapter.removeRefs, test.wantEvents)
				}
			})
		}
	}
}

func TestMCPUninstallRecoveryRemoveMutationStates(t *testing.T) {
	operationErr := errors.New("remove failed")
	tests := []struct {
		name       string
		answer     mcpMutationAnswer
		wantPhase  Phase
		wantErr    error
		wantEvents []string
	}{
		{
			name: "applied plus error", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, err: operationErr},
			wantPhase: PhaseCommitting, wantErr: operationErr,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "adapter:remove", "update:committing"},
		},
		{
			name: "not applied", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationNotApplied}, err: operationErr},
			wantPhase: PhaseRemoving, wantErr: operationErr,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "adapter:remove"},
		},
		{
			name: "unknown", answer: mcpMutationAnswer{result: adapter.MCPMutationResult{State: adapter.MCPMutationUnknown}, err: operationErr},
			wantPhase: PhaseRemoving, wantErr: ErrIndeterminateState,
			wantEvents: []string{"resolver:claude-code", "adapter:inspect", "adapter:remove"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, OperationUninstall, PhaseRemoving, false, adapter.MCPPresent)
			fixture.adapter.removeAnswers = []mcpMutationAnswer{test.answer}

			result, err := fixture.service.Recover(context.Background())
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, test.wantErr) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.journals.found || fixture.journals.journal.Phase != test.wantPhase || fixture.manifests.removed.Name != "" {
				t.Fatalf("journal/manifest = %v/%+v/%+v", fixture.journals.found, fixture.journals.journal, fixture.manifests.removed)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvents) {
				t.Fatalf("events = %v, want %v", fixture.events, test.wantEvents)
			}
		})
	}
}

func TestMCPUninstallRecoveryCancellationAfterCommittingUpdatePreservesManifest(t *testing.T) {
	fixture := newMCPRecoveryFixture(t, OperationUninstall, PhaseRemoving, false, adapter.MCPPresent)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.journals.onUpdate = func(journal Journal) {
		if journal.Phase == PhaseCommitting {
			cancel()
		}
	}

	result, err := fixture.service.Recover(ctx)
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, context.Canceled) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if !fixture.journals.found || fixture.journals.journal.Phase != PhaseCommitting || fixture.manifests.removed.Name != "" {
		t.Fatalf("journal/manifest = %v/%+v/%+v", fixture.journals.found, fixture.journals.journal, fixture.manifests.removed)
	}
	if want := []string{"resolver:claude-code", "adapter:inspect", "adapter:remove", "update:committing"}; !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestMCPUninstallRecoveryRetriesManifestAndJournalCommitIdempotently(t *testing.T) {
	for _, failure := range []string{"manifest", "delete"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newMCPRecoveryFixture(t, OperationUninstall, PhaseCommitting, false, adapter.MCPAbsent)
			cause := errors.New(failure + " failed")
			if failure == "manifest" {
				fixture.manifests.removeErr = cause
			} else {
				fixture.journals.fail["delete"] = cause
			}
			if _, err := fixture.service.Recover(context.Background()); !errors.Is(err, cause) || !fixture.journals.found {
				t.Fatalf("first Recover() error/journal = %v/%v", err, fixture.journals.found)
			}
			fixture.manifests.removeErr = nil
			delete(fixture.journals.fail, "delete")
			fixture.events = nil
			result, err := fixture.service.Recover(context.Background())
			if err != nil || !result.Recovered || result.Direction != RecoveryCommit || fixture.journals.found {
				t.Fatalf("second Recover() = %+v, %v, journal=%v", result, err, fixture.journals.found)
			}
			if want := []string{"resolver:claude-code", "adapter:inspect", "manifest:remove", "delete:journal"}; !reflect.DeepEqual(fixture.events, want) {
				t.Fatalf("retry events = %v, want %v", fixture.events, want)
			}
		})
	}
}

func TestMCPUninstallRecoveryUnknownRemoveKeepsRecoverableJournal(t *testing.T) {
	fixture := newMCPRecoveryFixture(t, OperationUninstall, PhaseRemoving, false, adapter.MCPPresent)
	fixture.adapter.removeAnswers = []mcpMutationAnswer{{
		result: adapter.MCPMutationResult{State: adapter.MCPMutationUnknown}, err: errors.New("ambiguous remove"),
	}}
	result, err := fixture.service.Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered ||
		!fixture.journals.found || fixture.journals.journal.Phase != PhaseRemoving {
		t.Fatalf("Recover() = %+v, %v, journal=%+v", result, err, fixture.journals.journal)
	}
	if len(fixture.adapter.addServers) != 0 {
		t.Fatalf("unknown uninstall recovery called Add: %+v", fixture.adapter.addServers)
	}
}

func TestMCPUninstallRecoveryCommittingPhaseWriteFailureCanRetry(t *testing.T) {
	fixture := newMCPRecoveryFixture(t, OperationUninstall, PhaseRemoving, false, adapter.MCPPresent)
	fixture.journals.fail["update:committing"] = errors.New("phase write failed")
	if _, err := fixture.service.Recover(context.Background()); !errors.Is(err, fixture.journals.fail["update:committing"]) ||
		!fixture.journals.found || fixture.journals.journal.Phase != PhaseRemoving {
		t.Fatalf("first Recover() error/journal = %v/%+v", err, fixture.journals.journal)
	}
	delete(fixture.journals.fail, "update:committing")
	fixture.adapter.presence = adapter.MCPAbsent
	fixture.events = nil
	result, err := fixture.service.Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit || fixture.journals.found {
		t.Fatalf("second Recover() = %+v, %v, journal=%v", result, err, fixture.journals.found)
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
