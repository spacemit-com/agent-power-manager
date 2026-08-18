package transaction

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type marketplaceRecoveryAdapter struct {
	events          *[]string
	marketplace     adapter.MarketplaceFact
	marketplaceErr  error
	plugins         map[string]adapter.PluginFact
	pluginErrors    map[string]error
	interrupt       map[string]error
	mutationAnswers map[string][]pluginMutationAnswer
}

func (*marketplaceRecoveryAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}

func (a *marketplaceRecoveryAdapter) Inspect(_ context.Context, name string) (adapter.PluginFact, error) {
	*a.events = append(*a.events, "inspect-plugin:"+name)
	if err := a.pluginErrors[name]; err != nil {
		return adapter.PluginFact{}, err
	}
	fact, ok := a.plugins[name]
	if !ok {
		return adapter.PluginFact{}, fmt.Errorf("unexpected Plugin inspection %q", name)
	}
	return fact, nil
}

func (a *marketplaceRecoveryAdapter) Add(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	event := "add-plugin:" + target.ActualName
	*a.events = append(*a.events, event)
	answer := a.nextMutationAnswer(event)
	if answer.result.State == adapter.PluginMutationApplied {
		a.plugins[target.ActualName] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: target.ActualName, Selector: target.Selector}
	}
	return answer.result, answer.err
}

func (a *marketplaceRecoveryAdapter) Remove(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	event := "remove-plugin:" + target.ActualName
	*a.events = append(*a.events, event)
	answer := a.nextMutationAnswer(event)
	if answer.result.State == adapter.PluginMutationApplied {
		a.plugins[target.ActualName] = adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: target.ActualName}
	}
	return answer.result, answer.err
}

func (*marketplaceRecoveryAdapter) TargetPath(string) (string, error) {
	return "", adapter.ErrUnsupportedAdapter
}

func (a *marketplaceRecoveryAdapter) InspectMarketplace(_ context.Context, name string) (adapter.MarketplaceFact, error) {
	*a.events = append(*a.events, "inspect-marketplace:"+name)
	return a.marketplace, a.marketplaceErr
}

func (a *marketplaceRecoveryAdapter) RegisterMarketplace(_ context.Context, target adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	event := "register-marketplace:" + target.Source
	*a.events = append(*a.events, event)
	answer := a.nextMutationAnswer(event)
	if answer.result.State == adapter.PluginMutationApplied {
		a.marketplace = adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: target.Name, Source: target.Source}
	}
	return answer.result, answer.err
}

func (a *marketplaceRecoveryAdapter) UnregisterMarketplace(_ context.Context, target adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	event := "unregister-marketplace:" + target.Source
	*a.events = append(*a.events, event)
	answer := a.nextMutationAnswer(event)
	if answer.result.State == adapter.PluginMutationApplied {
		a.marketplace = adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: target.Name}
	}
	return answer.result, answer.err
}

func (a *marketplaceRecoveryAdapter) nextMutationAnswer(event string) pluginMutationAnswer {
	if answers := a.mutationAnswers[event]; len(answers) != 0 {
		answer := answers[0]
		a.mutationAnswers[event] = answers[1:]
		return answer
	}
	return pluginMutationAnswer{
		result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied},
		err:    a.interrupt[event],
	}
}

type marketplaceRecoveryResolver struct {
	events  *[]string
	adapter adapter.PluginAdapter
	err     error
}

func (*marketplaceRecoveryResolver) ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error) {
	return nil, nil
}

func (r *marketplaceRecoveryResolver) ResolvePlugin(agent domain.AgentID) (adapter.PluginAdapter, error) {
	*r.events = append(*r.events, "resolve:"+string(agent))
	return r.adapter, r.err
}

type marketplaceRecoveryFixture struct {
	t              *testing.T
	events         []string
	journal        Journal
	store          *recordingJournalStore
	manifests      *recordingManifestWriter
	adapter        *marketplaceRecoveryAdapter
	resolver       *marketplaceRecoveryResolver
	service        *Service
	targetSource   string
	previousSource string
	target         adapter.PluginTarget
	previous       adapter.PluginTarget
}

type statefulRecoveryManifestWriter struct {
	events      *[]string
	installed   bool
	removeCalls int
}

func (w *statefulRecoveryManifestWriter) Set(domain.AgentID, domain.PowerRef) error {
	w.installed = true
	*w.events = append(*w.events, "manifest:set")
	return nil
}

func (w *statefulRecoveryManifestWriter) Remove(domain.AgentID, domain.PowerType, string) error {
	w.removeCalls++
	if w.installed {
		*w.events = append(*w.events, "manifest:remove-present")
	} else {
		*w.events = append(*w.events, "manifest:remove-absent")
	}
	w.installed = false
	return nil
}

func newMarketplaceRecoveryFixture(t *testing.T, operation Operation, phase Phase, upgrade bool) *marketplaceRecoveryFixture {
	t.Helper()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	targetSource := filepath.Join(cacheHome, "apm", "plugins", "codex", "demo", "v2.0.0")
	previousSource := filepath.Join(cacheHome, "apm", "plugins", "codex", "demo", "v1.0.0")
	for _, source := range []string{targetSource, previousSource} {
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := adapter.PluginTarget{ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0@apm-demo"}
	previous := adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}
	journal := Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        DriverPlugin,
		Operation:     operation,
		Phase:         phase,
		Agent:         domain.AgentCodex,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"},
		Plugin: &PluginJournalPayload{
			Deployment: domain.PluginDeploymentMarketplace,
			Target:     target,
			Marketplace: &PluginMarketplaceJournalPayload{
				Name: "apm-demo", TargetSource: targetSource,
			},
		},
	}
	if operation == OperationUninstall {
		journal.Plugin.TargetPresent = true
	}
	if upgrade {
		previousRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
		journal.Previous = &previousRef
		journal.Plugin.Previous = &previous
		journal.Plugin.PreviousPresent = true
		journal.Plugin.Marketplace.PreviousPresent = true
		journal.Plugin.Marketplace.PreviousSource = previousSource
	}

	fixture := &marketplaceRecoveryFixture{t: t, journal: journal, targetSource: targetSource, previousSource: previousSource, target: target, previous: previous}
	fixture.store = &recordingJournalStore{journal: journal, found: true, events: &fixture.events, fail: map[string]error{}}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.adapter = &marketplaceRecoveryAdapter{
		events:      &fixture.events,
		marketplace: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
		plugins: map[string]adapter.PluginFact{
			target.ActualName:   {Presence: adapter.PluginAbsent, ActualName: target.ActualName},
			previous.ActualName: {Presence: adapter.PluginAbsent, ActualName: previous.ActualName},
		},
		pluginErrors:    map[string]error{},
		interrupt:       map[string]error{},
		mutationAnswers: map[string][]pluginMutationAnswer{},
	}
	fixture.resolver = &marketplaceRecoveryResolver{events: &fixture.events, adapter: fixture.adapter}
	fixture.service = newService(fixture.manifests, fixture.store, fixture.resolver)
	return fixture
}

func (f *marketplaceRecoveryFixture) recover() (RecoveryResult, error) {
	return f.service.Recover(context.Background())
}

func (f *marketplaceRecoveryFixture) setMarketplace(source string) {
	f.adapter.marketplace = adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: source}
}

func (f *marketplaceRecoveryFixture) setPlugin(target adapter.PluginTarget, presence adapter.PluginPresence) {
	fact := adapter.PluginFact{Presence: presence, ActualName: target.ActualName}
	if presence == adapter.PluginPresent {
		fact.Selector = target.Selector
	}
	f.adapter.plugins[target.ActualName] = fact
}

func TestRecoverMarketplacePluginDirection(t *testing.T) {
	tests := []struct {
		name          string
		phase         Phase
		upgrade       bool
		marketplace   string
		targetPresent bool
		wantDirection RecoveryDirection
		wantEvents    []string
	}{
		{
			name: "committing exact target continues commit", phase: PhaseCommitting, marketplace: "target", targetPresent: true,
			wantDirection: RecoveryCommit,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo-v2.0.0", "manifest:set", "delete:journal"},
		},
		{
			name: "adding exact target fixes commit", phase: PhaseAddingNewPlugin, marketplace: "target", targetPresent: true,
			wantDirection: RecoveryCommit,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo-v2.0.0", "update:committing", "manifest:set", "delete:journal"},
		},
		{
			name: "adding absent target fixes rollback", phase: PhaseAddingNewPlugin, marketplace: "target",
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo-v2.0.0", "update:rolling-back", "unregister-marketplace:"},
		},
		{
			name: "earlier install without target fixes rollback", phase: PhaseRegisteringMarketplace,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo-v2.0.0", "update:rolling-back", "delete:journal"},
		},
		{
			name: "rolling back never reverses", phase: PhaseRollingBack, marketplace: "target", targetPresent: true,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo-v2.0.0", "remove-plugin:demo-v2.0.0", "unregister-marketplace:"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, test.phase, test.upgrade)
			switch test.marketplace {
			case "target":
				fixture.setMarketplace(fixture.targetSource)
			}
			if test.targetPresent {
				fixture.setPlugin(fixture.target, adapter.PluginPresent)
			}
			result, err := fixture.recover()
			if err != nil || !result.Recovered || result.Direction != test.wantDirection {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			wantEvents := append([]string(nil), test.wantEvents...)
			if len(wantEvents) > 0 && wantEvents[len(wantEvents)-1] == "unregister-marketplace:" {
				wantEvents[len(wantEvents)-1] += fixture.targetSource
				wantEvents = append(wantEvents, "delete:journal")
			}
			if !reflect.DeepEqual(fixture.events, wantEvents) {
				t.Fatalf("events = %v, want %v", fixture.events, wantEvents)
			}
			if fixture.store.found {
				t.Fatal("journal remains after successful recovery")
			}
		})
	}
}

func TestRecoverMarketplacePluginCommittingNeverReverses(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseCommitting, false)
	fixture.setMarketplace(fixture.targetSource)
	result, err := fixture.recover()
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if !fixture.store.found || fixture.store.journal.Phase != PhaseCommitting {
		t.Fatalf("journal = found %t phase %q", fixture.store.found, fixture.store.journal.Phase)
	}
	for _, event := range fixture.events {
		if event == "update:rolling-back" || event == "manifest:set" || event == "delete:journal" {
			t.Fatalf("committing direction reversed or wrote state: %v", fixture.events)
		}
	}
}

func TestRecoverMarketplacePluginFactFailuresAreReadOnly(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*marketplaceRecoveryFixture)
	}{
		{
			name: "other marketplace source",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(filepath.Join(filepath.Dir(f.targetSource), "v9.9.9"))
			},
		},
		{
			name: "duplicate marketplace output",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.adapter.marketplaceErr = errors.New("duplicate marketplace entries")
			},
		},
		{
			name: "unreadable marketplace output",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.adapter.marketplaceErr = errors.New("truncated marketplace output")
			},
		},
		{
			name: "wrong target selector",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.adapter.plugins[f.target.ActualName] = adapter.PluginFact{
					Presence: adapter.PluginPresent, ActualName: f.target.ActualName, Selector: "demo-v2.0.0@other",
				}
			},
		},
		{
			name: "unreadable target Plugin output",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.adapter.pluginErrors[f.target.ActualName] = errors.New("truncated Plugin output")
			},
		},
		{
			name: "unreadable previous Plugin output",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.previousSource)
				f.adapter.pluginErrors[f.previous.ActualName] = errors.New("previous Plugin inspect failed")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, true)
			test.mutate(fixture)
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.store.found || fixture.store.journal.Phase != PhaseRollingBack {
				t.Fatalf("journal = found %t phase %q", fixture.store.found, fixture.store.journal.Phase)
			}
			for _, event := range fixture.events {
				if event == "manifest:set" || event == "manifest:remove" || event == "delete:journal" ||
					strings.HasPrefix(event, "add-plugin:") || strings.HasPrefix(event, "remove-plugin:") ||
					strings.HasPrefix(event, "register-marketplace:") || strings.HasPrefix(event, "unregister-marketplace:") {
					t.Fatalf("indeterminate facts caused a write: %v", fixture.events)
				}
			}
		})
	}
}

func TestRecoverMarketplacePluginRejectsUnreachableRollbackFacts(t *testing.T) {
	tests := []struct {
		name            string
		marketplace     string
		targetPresent   bool
		previousPresent bool
	}{
		{name: "target marketplace with previous Plugin", marketplace: "target", previousPresent: true},
		{name: "previous marketplace with target Plugin", marketplace: "previous", targetPresent: true},
		{name: "absent marketplace with previous Plugin", marketplace: "absent", previousPresent: true},
		{name: "absent marketplace with target Plugin", marketplace: "absent", targetPresent: true},
		{name: "both Plugins present", marketplace: "target", targetPresent: true, previousPresent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, true)
			switch test.marketplace {
			case "target":
				fixture.setMarketplace(fixture.targetSource)
			case "previous":
				fixture.setMarketplace(fixture.previousSource)
			}
			if test.targetPresent {
				fixture.setPlugin(fixture.target, adapter.PluginPresent)
			}
			if test.previousPresent {
				fixture.setPlugin(fixture.previous, adapter.PluginPresent)
			}
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.store.found || hasMarketplaceRecoveryWrite(fixture.events) {
				t.Fatalf("unreachable facts changed state: found=%t events=%v", fixture.store.found, fixture.events)
			}
		})
	}
}

func hasMarketplaceRecoveryWrite(events []string) bool {
	for _, event := range events {
		if strings.HasPrefix(event, "update:") || strings.HasPrefix(event, "add-plugin:") ||
			strings.HasPrefix(event, "remove-plugin:") || strings.HasPrefix(event, "register-marketplace:") ||
			strings.HasPrefix(event, "unregister-marketplace:") || strings.HasPrefix(event, "manifest:") ||
			event == "delete:journal" {
			return true
		}
	}
	return false
}

func TestRecoverMarketplacePluginRollbackConvergesReachableStates(t *testing.T) {
	tests := []struct {
		name            string
		upgrade         bool
		marketplace     string
		targetPresent   bool
		previousPresent bool
		wantWrites      []string
	}{
		{
			name: "upgrade full target state", upgrade: true, marketplace: "target", targetPresent: true,
			wantWrites: []string{"remove-plugin", "unregister-marketplace", "register-marketplace", "add-plugin", "delete:journal"},
		},
		{
			name: "upgrade target marketplace only", upgrade: true, marketplace: "target",
			wantWrites: []string{"unregister-marketplace", "register-marketplace", "add-plugin", "delete:journal"},
		},
		{
			name: "upgrade marketplace absent", upgrade: true, marketplace: "absent",
			wantWrites: []string{"register-marketplace", "add-plugin", "delete:journal"},
		},
		{
			name: "upgrade previous marketplace restored", upgrade: true, marketplace: "previous",
			wantWrites: []string{"add-plugin", "delete:journal"},
		},
		{
			name: "upgrade previous state complete", upgrade: true, marketplace: "previous", previousPresent: true,
			wantWrites: []string{"delete:journal"},
		},
		{
			name: "first install full target state", marketplace: "target", targetPresent: true,
			wantWrites: []string{"remove-plugin", "unregister-marketplace", "delete:journal"},
		},
		{
			name: "first install target marketplace only", marketplace: "target",
			wantWrites: []string{"unregister-marketplace", "delete:journal"},
		},
		{
			name: "first install already absent", marketplace: "absent",
			wantWrites: []string{"delete:journal"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, test.upgrade)
			setMarketplaceRecoveryState(fixture, test.marketplace, test.targetPresent, test.previousPresent)
			result, err := fixture.recover()
			if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if got := marketplaceRecoveryWrites(fixture.events); !reflect.DeepEqual(got, test.wantWrites) {
				t.Fatalf("writes = %v, want %v; events=%v", got, test.wantWrites, fixture.events)
			}
			assertMarketplaceRollbackState(t, fixture, test.upgrade)
		})
	}
}

func TestRecoverMarketplacePluginRollbackResumesAfterEveryAction(t *testing.T) {
	tests := []struct {
		name  string
		event func(*marketplaceRecoveryFixture) string
	}{
		{name: "remove target Plugin", event: func(f *marketplaceRecoveryFixture) string { return "remove-plugin:" + f.target.ActualName }},
		{name: "unregister target marketplace", event: func(f *marketplaceRecoveryFixture) string { return "unregister-marketplace:" + f.targetSource }},
		{name: "register previous marketplace", event: func(f *marketplaceRecoveryFixture) string { return "register-marketplace:" + f.previousSource }},
		{name: "restore previous Plugin", event: func(f *marketplaceRecoveryFixture) string { return "add-plugin:" + f.previous.ActualName }},
		{name: "delete journal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, true)
			setMarketplaceRecoveryState(fixture, "target", true, false)
			interrupted := errors.New("injected interruption")
			if test.event == nil {
				fixture.store.fail["delete"] = interrupted
			} else {
				fixture.adapter.interrupt[test.event(fixture)] = interrupted
			}
			first, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, interrupted) || first.Recovered || first.Direction != RecoveryRollback {
				t.Fatalf("first Recover() = %+v, %v", first, err)
			}
			if !fixture.store.found || fixture.store.journal.Phase != PhaseRollingBack {
				t.Fatalf("journal after interruption = found %t phase %q", fixture.store.found, fixture.store.journal.Phase)
			}
			fixture.adapter.interrupt = map[string]error{}
			delete(fixture.store.fail, "delete")
			fixture.events = nil
			fixture.adapter.events = &fixture.events
			fixture.store.events = &fixture.events
			fixture.manifests.events = &fixture.events
			fixture.resolver.events = &fixture.events

			second, err := fixture.recover()
			if err != nil || !second.Recovered || second.Direction != RecoveryRollback {
				t.Fatalf("second Recover() = %+v, %v", second, err)
			}
			assertMarketplaceRollbackState(t, fixture, true)
			for _, event := range fixture.events {
				if event == "update:committing" || event == "manifest:set" || event == "manifest:remove" {
					t.Fatalf("resumed rollback reversed direction: %v", fixture.events)
				}
			}
		})
	}
}

func TestRecoverMarketplacePluginRollbackMutationMatrix(t *testing.T) {
	operations := []struct {
		name    string
		prepare func(*marketplaceRecoveryFixture)
		event   func(*marketplaceRecoveryFixture) string
		write   string
	}{
		{
			name: "remove target Plugin",
			prepare: func(f *marketplaceRecoveryFixture) {
				setMarketplaceRecoveryState(f, "target", true, false)
			},
			event: func(f *marketplaceRecoveryFixture) string { return "remove-plugin:" + f.target.ActualName },
			write: "remove-plugin",
		},
		{
			name: "unregister target marketplace",
			prepare: func(f *marketplaceRecoveryFixture) {
				setMarketplaceRecoveryState(f, "target", false, false)
			},
			event: func(f *marketplaceRecoveryFixture) string { return "unregister-marketplace:" + f.targetSource },
			write: "unregister-marketplace",
		},
		{
			name:    "register previous marketplace",
			prepare: func(*marketplaceRecoveryFixture) {},
			event:   func(f *marketplaceRecoveryFixture) string { return "register-marketplace:" + f.previousSource },
			write:   "register-marketplace",
		},
		{
			name: "restore previous Plugin",
			prepare: func(f *marketplaceRecoveryFixture) {
				setMarketplaceRecoveryState(f, "previous", false, false)
			},
			event: func(f *marketplaceRecoveryFixture) string { return "add-plugin:" + f.previous.ActualName },
			write: "add-plugin",
		},
	}
	states := []struct {
		name              string
		state             adapter.PluginMutationState
		withError         bool
		wantIndeterminate bool
		wantSuccess       bool
	}{
		{name: "applied", state: adapter.PluginMutationApplied, wantSuccess: true},
		{name: "not-applied", state: adapter.PluginMutationNotApplied, withError: true},
		{name: "unknown", state: adapter.PluginMutationUnknown, withError: true, wantIndeterminate: true},
		{name: "invalid", state: adapter.PluginMutationState("invalid"), wantIndeterminate: true},
		{name: "applied-with-error", state: adapter.PluginMutationApplied, withError: true},
	}
	for _, operation := range operations {
		for _, state := range states {
			t.Run(operation.name+"/"+state.name, func(t *testing.T) {
				fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, true)
				operation.prepare(fixture)
				event := operation.event(fixture)
				mutationErr := errors.New("injected recovery mutation failure")
				answer := pluginMutationAnswer{result: adapter.PluginMutationResult{State: state.state}}
				if state.withError {
					answer.err = mutationErr
				}
				fixture.adapter.mutationAnswers[event] = []pluginMutationAnswer{answer}

				result, err := fixture.recover()
				if state.wantSuccess {
					if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
						t.Fatalf("Recover() = %+v, %v", result, err)
					}
					assertMarketplaceRollbackState(t, fixture, true)
					return
				}
				if !errors.Is(err, ErrRecoveryFailed) || result.Recovered || result.Direction != RecoveryRollback {
					t.Fatalf("Recover() = %+v, %v", result, err)
				}
				if errors.Is(err, mutationErr) != state.withError {
					t.Fatalf("mutation error membership = %t, want %t: %v", errors.Is(err, mutationErr), state.withError, err)
				}
				if errors.Is(err, ErrIndeterminateState) != state.wantIndeterminate {
					t.Fatalf("indeterminate membership = %t, want %t: %v", errors.Is(err, ErrIndeterminateState), state.wantIndeterminate, err)
				}
				if !fixture.store.found || fixture.store.journal.Phase != PhaseRollingBack {
					t.Fatalf("journal = found %t phase %q", fixture.store.found, fixture.store.journal.Phase)
				}
				if got := marketplaceRecoveryWrites(fixture.events); !reflect.DeepEqual(got, []string{operation.write}) {
					t.Fatalf("writes = %v, want only %q; events=%v", got, operation.write, fixture.events)
				}
				if fixture.events[len(fixture.events)-1] != event {
					t.Fatalf("later operation followed failed mutation: %v", fixture.events)
				}
				if fixture.manifests.setRef != (domain.PowerRef{}) || fixture.manifests.removed != (domain.PowerRef{}) {
					t.Fatalf("failed mutation changed manifest: set=%+v removed=%+v", fixture.manifests.setRef, fixture.manifests.removed)
				}

				continued, err := fixture.recover()
				if err != nil || !continued.Recovered || continued.Direction != RecoveryRollback {
					t.Fatalf("continued Recover() = %+v, %v", continued, err)
				}
				assertMarketplaceRollbackState(t, fixture, true)
			})
		}
	}
}

func setMarketplaceRecoveryState(f *marketplaceRecoveryFixture, marketplace string, targetPresent, previousPresent bool) {
	switch marketplace {
	case "target":
		f.setMarketplace(f.targetSource)
	case "previous":
		f.setMarketplace(f.previousSource)
	}
	if targetPresent {
		f.setPlugin(f.target, adapter.PluginPresent)
	}
	if previousPresent {
		f.setPlugin(f.previous, adapter.PluginPresent)
	}
}

func marketplaceRecoveryWrites(events []string) []string {
	var writes []string
	for _, event := range events {
		switch {
		case strings.HasPrefix(event, "remove-plugin:"):
			writes = append(writes, "remove-plugin")
		case strings.HasPrefix(event, "unregister-marketplace:"):
			writes = append(writes, "unregister-marketplace")
		case strings.HasPrefix(event, "register-marketplace:"):
			writes = append(writes, "register-marketplace")
		case strings.HasPrefix(event, "add-plugin:"):
			writes = append(writes, "add-plugin")
		case event == "manifest:set", event == "manifest:remove", event == "delete:journal":
			writes = append(writes, event)
		}
	}
	return writes
}

func assertMarketplaceRollbackState(t *testing.T, fixture *marketplaceRecoveryFixture, upgrade bool) {
	t.Helper()
	if fixture.store.found {
		t.Fatal("rollback left journal")
	}
	if upgrade {
		if fixture.adapter.marketplace.Presence != adapter.MarketplacePresent || fixture.adapter.marketplace.Source != fixture.previousSource ||
			fixture.adapter.plugins[fixture.previous.ActualName].Presence != adapter.PluginPresent {
			t.Fatalf("previous state not restored: marketplace=%+v plugins=%+v", fixture.adapter.marketplace, fixture.adapter.plugins)
		}
	} else if fixture.adapter.marketplace.Presence != adapter.MarketplaceAbsent {
		t.Fatalf("first-install marketplace remains: %+v", fixture.adapter.marketplace)
	}
	if fixture.adapter.plugins[fixture.target.ActualName].Presence != adapter.PluginAbsent {
		t.Fatalf("target Plugin remains: %+v", fixture.adapter.plugins[fixture.target.ActualName])
	}
	if fixture.manifests.setRef != (domain.PowerRef{}) || fixture.manifests.removed != (domain.PowerRef{}) {
		t.Fatalf("rollback changed manifest: set=%+v removed=%+v", fixture.manifests.setRef, fixture.manifests.removed)
	}
}

func TestRecoverMarketplacePluginCommitResumesIdempotently(t *testing.T) {
	tests := []struct {
		name       string
		inject     func(*marketplaceRecoveryFixture, error)
		clear      func(*marketplaceRecoveryFixture)
		wantSetRef bool
	}{
		{
			name:   "manifest write",
			inject: func(f *marketplaceRecoveryFixture, err error) { f.manifests.setErr = err },
			clear:  func(f *marketplaceRecoveryFixture) { f.manifests.setErr = nil },
		},
		{
			name:       "journal delete",
			inject:     func(f *marketplaceRecoveryFixture, err error) { f.store.fail["delete"] = err },
			clear:      func(f *marketplaceRecoveryFixture) { delete(f.store.fail, "delete") },
			wantSetRef: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseAddingNewPlugin, true)
			setMarketplaceRecoveryState(fixture, "target", true, false)
			interrupted := errors.New("injected commit interruption")
			test.inject(fixture, interrupted)
			first, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, interrupted) || first.Recovered || first.Direction != RecoveryCommit {
				t.Fatalf("first Recover() = %+v, %v", first, err)
			}
			if !fixture.store.found || fixture.store.journal.Phase != PhaseCommitting {
				t.Fatalf("journal after interruption = found %t phase %q", fixture.store.found, fixture.store.journal.Phase)
			}
			if test.wantSetRef && fixture.manifests.setRef != fixture.journal.Power {
				t.Fatalf("manifest before delete retry = %+v, want %+v", fixture.manifests.setRef, fixture.journal.Power)
			}
			test.clear(fixture)
			second, err := fixture.recover()
			if err != nil || !second.Recovered || second.Direction != RecoveryCommit {
				t.Fatalf("second Recover() = %+v, %v", second, err)
			}
			if fixture.store.found || fixture.manifests.setRef != fixture.journal.Power {
				t.Fatalf("commit did not converge: journal=%t manifest=%+v", fixture.store.found, fixture.manifests.setRef)
			}
			if fixture.adapter.marketplace.Source != fixture.targetSource || fixture.adapter.plugins[fixture.target.ActualName].Presence != adapter.PluginPresent {
				t.Fatalf("commit invented target state: marketplace=%+v plugin=%+v", fixture.adapter.marketplace, fixture.adapter.plugins[fixture.target.ActualName])
			}
		})
	}
}

func TestRecoverMarketplacePluginCommitRejectsInexactFacts(t *testing.T) {
	tests := []struct {
		name            string
		marketplace     string
		targetPresent   bool
		previousPresent bool
	}{
		{name: "marketplace absent", marketplace: "absent", targetPresent: true},
		{name: "previous marketplace selected", marketplace: "previous", targetPresent: true},
		{name: "target Plugin absent", marketplace: "target"},
		{name: "previous Plugin remains", marketplace: "target", targetPresent: true, previousPresent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseCommitting, true)
			setMarketplaceRecoveryState(fixture, test.marketplace, test.targetPresent, test.previousPresent)
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.store.found || hasMarketplaceRecoveryWrite(fixture.events) {
				t.Fatalf("inexact commit facts changed state: found=%t events=%v", fixture.store.found, fixture.events)
			}
		})
	}
}

func TestRecoverMarketplacePluginCommitDoesNotRequirePreviousCache(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseCommitting, true)
	setMarketplaceRecoveryState(fixture, "target", true, false)
	if err := os.RemoveAll(fixture.previousSource); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.recover()
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.store.found || fixture.manifests.setRef != fixture.journal.Power {
		t.Fatalf("commit did not finish: journal=%t manifest=%+v", fixture.store.found, fixture.manifests.setRef)
	}
	want := []string{
		"resolve:codex",
		"inspect-marketplace:apm-demo",
		"inspect-plugin:demo-v2.0.0",
		"inspect-plugin:demo-v1.0.0",
		"manifest:set",
		"delete:journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestRecoverMarketplacePluginRollbackValidatesOnlyRequiredCache(t *testing.T) {
	tests := []struct {
		name        string
		phase       Phase
		marketplace string
		target      bool
		previous    bool
		remove      func(*marketplaceRecoveryFixture) string
		wantError   bool
		wantWrites  []string
	}{
		{
			name: "missing previous cache blocks restoration", phase: PhaseRegisteringNewMarketplace, marketplace: "absent",
			remove: func(f *marketplaceRecoveryFixture) string { return f.previousSource }, wantError: true,
		},
		{
			name: "complete previous state does not require previous cache", phase: PhaseRollingBack, marketplace: "previous", previous: true,
			remove: func(f *marketplaceRecoveryFixture) string { return f.previousSource }, wantWrites: []string{"delete:journal"},
		},
		{
			name: "first-install rollback does not require target cache", phase: PhaseRollingBack, marketplace: "target", target: true,
			remove: func(f *marketplaceRecoveryFixture) string { return f.targetSource }, wantWrites: []string{"remove-plugin", "unregister-marketplace", "delete:journal"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upgrade := test.name != "first-install rollback does not require target cache"
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, test.phase, upgrade)
			setMarketplaceRecoveryState(fixture, test.marketplace, test.target, test.previous)
			if err := os.RemoveAll(test.remove(fixture)); err != nil {
				t.Fatal(err)
			}

			result, err := fixture.recover()
			if test.wantError {
				if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered || result.Direction != RecoveryRollback {
					t.Fatalf("Recover() = %+v, %v", result, err)
				}
				if !fixture.store.found || fixture.store.journal.Phase != PhaseRollingBack {
					t.Fatalf("journal = found %t phase %q", fixture.store.found, fixture.store.journal.Phase)
				}
				if got := marketplaceRecoveryWrites(fixture.events); len(got) != 0 {
					t.Fatalf("missing required cache caused later writes: %v", fixture.events)
				}
				if len(fixture.events) < 5 || !reflect.DeepEqual(fixture.events[:5], []string{
					"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo-v2.0.0", "inspect-plugin:demo-v1.0.0", "update:rolling-back",
				}) {
					t.Fatalf("facts/direction order = %v", fixture.events)
				}
				return
			}
			if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if got := marketplaceRecoveryWrites(fixture.events); !reflect.DeepEqual(got, test.wantWrites) {
				t.Fatalf("writes = %v, want %v; events=%v", got, test.wantWrites, fixture.events)
			}
		})
	}
}

func TestRecoverMarketplacePluginUninstallDirectionAndConvergence(t *testing.T) {
	tests := []struct {
		name          string
		phase         Phase
		pluginPresent bool
		wantDirection RecoveryDirection
		wantWrites    []string
		wantRemoved   bool
	}{
		{
			name: "removing with Plugin absent commits", phase: PhaseRemovingPlugin,
			wantDirection: RecoveryCommit, wantWrites: []string{"manifest:remove", "delete:journal"}, wantRemoved: true,
		},
		{
			name: "removing with Plugin present rolls back", phase: PhaseRemovingPlugin, pluginPresent: true,
			wantDirection: RecoveryRollback, wantWrites: []string{"delete:journal"},
		},
		{
			name: "persisted rollback keeps manifest", phase: PhaseRollingBack, pluginPresent: true,
			wantDirection: RecoveryRollback, wantWrites: []string{"delete:journal"},
		},
		{
			name: "persisted commit removes manifest", phase: PhaseCommitting,
			wantDirection: RecoveryCommit, wantWrites: []string{"manifest:remove", "delete:journal"}, wantRemoved: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationUninstall, test.phase, false)
			fixture.setMarketplace(fixture.targetSource)
			if test.pluginPresent {
				fixture.setPlugin(fixture.target, adapter.PluginPresent)
			}
			result, err := fixture.recover()
			if err != nil || !result.Recovered || result.Direction != test.wantDirection {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if got := marketplaceRecoveryWrites(fixture.events); !reflect.DeepEqual(got, test.wantWrites) {
				t.Fatalf("writes = %v, want %v; events=%v", got, test.wantWrites, fixture.events)
			}
			if fixture.store.found {
				t.Fatal("uninstall recovery left journal")
			}
			removed := fixture.manifests.removed == (domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin})
			if removed != test.wantRemoved {
				t.Fatalf("manifest removed = %t, want %t", removed, test.wantRemoved)
			}
			for _, event := range fixture.events {
				if strings.HasPrefix(event, "register-marketplace:") || strings.HasPrefix(event, "unregister-marketplace:") ||
					strings.HasPrefix(event, "add-plugin:") || strings.HasPrefix(event, "remove-plugin:") {
					t.Fatalf("uninstall recovery changed Agent state: %v", fixture.events)
				}
			}
		})
	}
}

func TestRecoverMarketplacePluginUninstallRejectsChangedState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*marketplaceRecoveryFixture)
	}{
		{name: "marketplace absent", mutate: func(*marketplaceRecoveryFixture) {}},
		{
			name: "marketplace changed",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(filepath.Join(filepath.Dir(f.targetSource), "v9.9.9"))
			},
		},
		{
			name: "rollback Plugin absent",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.journal.Phase = PhaseRollingBack
				f.store.journal.Phase = PhaseRollingBack
				f.setMarketplace(f.targetSource)
			},
		},
		{
			name: "commit Plugin present",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.journal.Phase = PhaseCommitting
				f.store.journal.Phase = PhaseCommitting
				f.setMarketplace(f.targetSource)
				f.setPlugin(f.target, adapter.PluginPresent)
			},
		},
		{
			name: "cache source missing",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				if err := os.RemoveAll(f.targetSource); err != nil {
					f.t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationUninstall, PhaseRemovingPlugin, false)
			test.mutate(fixture)
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.store.found {
				t.Fatalf("invalid uninstall state removed journal: events=%v", fixture.events)
			}
			if test.name == "cache source missing" {
				if fixture.store.journal.Phase != PhaseCommitting || len(marketplaceRecoveryWrites(fixture.events)) != 0 {
					t.Fatalf("missing cache did not stop after direction persistence: phase=%q events=%v", fixture.store.journal.Phase, fixture.events)
				}
			} else if hasMarketplaceRecoveryWrite(fixture.events) {
				t.Fatalf("invalid uninstall state caused writes: found=%t events=%v", fixture.store.found, fixture.events)
			}
		})
	}
}

func TestRecoverMarketplacePluginUninstallCommitResumes(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationUninstall, PhaseRemovingPlugin, false)
	fixture.setMarketplace(fixture.targetSource)
	interrupted := errors.New("manifest remove interrupted")
	fixture.manifests.removeErr = interrupted
	first, err := fixture.recover()
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, interrupted) || first.Direction != RecoveryCommit || first.Recovered {
		t.Fatalf("first Recover() = %+v, %v", first, err)
	}
	if !fixture.store.found || fixture.store.journal.Phase != PhaseCommitting {
		t.Fatalf("journal after interruption = found %t phase %q", fixture.store.found, fixture.store.journal.Phase)
	}
	fixture.manifests.removeErr = nil
	second, err := fixture.recover()
	if err != nil || !second.Recovered || second.Direction != RecoveryCommit || fixture.store.found {
		t.Fatalf("second Recover() = %+v, %v; journal=%t", second, err, fixture.store.found)
	}
}

func TestRecoverMarketplacePluginUninstallResumesAfterManifestRemovedBeforeJournalDelete(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationUninstall, PhaseRemovingPlugin, false)
	fixture.setMarketplace(fixture.targetSource)
	manifests := &statefulRecoveryManifestWriter{events: &fixture.events, installed: true}
	fixture.service.manifests = manifests
	deleteErr := errors.New("journal delete interrupted")
	fixture.store.fail["delete"] = deleteErr

	first, err := fixture.recover()
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, deleteErr) || first.Recovered || first.Direction != RecoveryCommit {
		t.Fatalf("first Recover() = %+v, %v", first, err)
	}
	if manifests.installed || manifests.removeCalls != 1 || !fixture.store.found || fixture.store.journal.Phase != PhaseCommitting {
		t.Fatalf("first recovery state: installed=%t removes=%d journal=%t/%q", manifests.installed, manifests.removeCalls, fixture.store.found, fixture.store.journal.Phase)
	}
	delete(fixture.store.fail, "delete")
	fixture.events = nil
	fixture.adapter.events = &fixture.events
	fixture.store.events = &fixture.events
	fixture.resolver.events = &fixture.events
	manifests.events = &fixture.events

	second, err := fixture.recover()
	if err != nil || !second.Recovered || second.Direction != RecoveryCommit {
		t.Fatalf("second Recover() = %+v, %v", second, err)
	}
	want := []string{
		"resolve:codex",
		"inspect-marketplace:apm-demo",
		"inspect-plugin:demo-v2.0.0",
		"manifest:remove-absent",
		"delete:journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("second recovery events = %v, want %v", fixture.events, want)
	}
	if fixture.store.found || manifests.installed || manifests.removeCalls != 2 {
		t.Fatalf("final state: journal=%t installed=%t removes=%d", fixture.store.found, manifests.installed, manifests.removeCalls)
	}
	if got := marketplaceRecoveryWrites(fixture.events); len(got) != 1 || got[0] != "delete:journal" {
		t.Fatalf("second recovery performed Agent writes: %v", fixture.events)
	}
}

func TestRecoverMarketplacePluginEarlierInstallPhasesRollback(t *testing.T) {
	tests := []struct {
		name            string
		phase           Phase
		marketplace     string
		previousPresent bool
		wantWrites      []string
	}{
		{
			name: "removing old before mutation", phase: PhaseRemovingOldPlugin, marketplace: "previous", previousPresent: true,
			wantWrites: []string{"delete:journal"},
		},
		{
			name: "removing old after mutation", phase: PhaseRemovingOldPlugin, marketplace: "previous",
			wantWrites: []string{"add-plugin", "delete:journal"},
		},
		{
			name: "unregistering old before mutation", phase: PhaseUnregisteringOldMarketplace, marketplace: "previous",
			wantWrites: []string{"add-plugin", "delete:journal"},
		},
		{
			name: "unregistering old after mutation", phase: PhaseUnregisteringOldMarketplace, marketplace: "absent",
			wantWrites: []string{"register-marketplace", "add-plugin", "delete:journal"},
		},
		{
			name: "registering new before mutation", phase: PhaseRegisteringNewMarketplace, marketplace: "absent",
			wantWrites: []string{"register-marketplace", "add-plugin", "delete:journal"},
		},
		{
			name: "registering new after mutation", phase: PhaseRegisteringNewMarketplace, marketplace: "target",
			wantWrites: []string{"unregister-marketplace", "register-marketplace", "add-plugin", "delete:journal"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, test.phase, true)
			setMarketplaceRecoveryState(fixture, test.marketplace, false, test.previousPresent)
			result, err := fixture.recover()
			if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if len(fixture.events) < 5 || fixture.events[4] != "update:rolling-back" {
				t.Fatalf("direction was not persisted before mutation: %v", fixture.events)
			}
			if got := marketplaceRecoveryWrites(fixture.events); !reflect.DeepEqual(got, test.wantWrites) {
				t.Fatalf("writes = %v, want %v; events=%v", got, test.wantWrites, fixture.events)
			}
			assertMarketplaceRollbackState(t, fixture, true)
		})
	}
}

func TestRecoverMarketplacePluginInspectsOnlyJournalIdentities(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, true)
	setMarketplaceRecoveryState(fixture, "previous", false, true)
	if _, err := fixture.recover(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"resolve:codex",
		"inspect-marketplace:apm-demo",
		"inspect-plugin:demo-v2.0.0",
		"inspect-plugin:demo-v1.0.0",
		"delete:journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestRecoverMarketplacePluginRejectsResolverWithoutMarketplaceCapability(t *testing.T) {
	tests := []struct {
		name    string
		resolve func(*marketplaceRecoveryFixture)
	}{
		{
			name: "resolver error",
			resolve: func(f *marketplaceRecoveryFixture) {
				f.resolver.err = errors.New("resolver failed")
			},
		},
		{
			name: "filesystem Hermes adapter",
			resolve: func(f *marketplaceRecoveryFixture) {
				f.resolver.adapter = &recordingPluginAdapter{events: &f.events, mode: domain.PluginDeploymentFilesystem}
			},
		},
		{
			name: "marketplace mode without marketplace methods",
			resolve: func(f *marketplaceRecoveryFixture) {
				f.resolver.adapter = &recordingPluginAdapter{events: &f.events, mode: domain.PluginDeploymentMarketplace}
			},
		},
		{
			name: "typed nil marketplace adapter",
			resolve: func(f *marketplaceRecoveryFixture) {
				var typedNil *marketplaceRecoveryAdapter
				f.resolver.adapter = typedNil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, false)
			test.resolve(fixture)
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, ErrIndeterminateState) {
				t.Fatalf("Recover() error = %v, want safe capability error", err)
			}
			if !fixture.store.found || hasMarketplaceRecoveryWrite(fixture.events) {
				t.Fatalf("resolver mismatch caused writes: found=%t events=%v", fixture.store.found, fixture.events)
			}
		})
	}
}

func TestRecoverMarketplacePluginRejectsUnsafeOrMissingCacheSource(t *testing.T) {
	tests := []struct {
		name      string
		phase     Phase
		upgrade   bool
		direction RecoveryDirection
		prepare   func(*marketplaceRecoveryFixture)
	}{
		{
			name: "required target source missing", phase: PhaseCommitting, direction: RecoveryCommit,
			prepare: func(f *marketplaceRecoveryFixture) {
				setMarketplaceRecoveryState(f, "target", true, false)
				if err := os.RemoveAll(f.targetSource); err != nil {
					f.t.Fatal(err)
				}
			},
		},
		{
			name: "required previous source missing", phase: PhaseRollingBack, upgrade: true, direction: RecoveryRollback,
			prepare: func(f *marketplaceRecoveryFixture) {
				if err := os.RemoveAll(f.previousSource); err != nil {
					f.t.Fatal(err)
				}
			},
		},
		{
			name: "required target source symlink", phase: PhaseCommitting, direction: RecoveryCommit,
			prepare: func(f *marketplaceRecoveryFixture) {
				setMarketplaceRecoveryState(f, "target", true, false)
				outside := filepath.Join(f.t.TempDir(), "outside")
				if err := os.MkdirAll(outside, 0o755); err != nil {
					f.t.Fatal(err)
				}
				if err := os.RemoveAll(f.targetSource); err != nil {
					f.t.Fatal(err)
				}
				if err := os.Symlink(outside, f.targetSource); err != nil {
					f.t.Fatal(err)
				}
			},
		},
		{
			name: "required previous cache ancestor symlink", phase: PhaseRollingBack, upgrade: true, direction: RecoveryRollback,
			prepare: func(f *marketplaceRecoveryFixture) {
				codexRoot := filepath.Dir(filepath.Dir(f.targetSource))
				outside := filepath.Join(f.t.TempDir(), "codex")
				for _, version := range []string{"v1.0.0", "v2.0.0"} {
					if err := os.MkdirAll(filepath.Join(outside, "demo", version), 0o755); err != nil {
						f.t.Fatal(err)
					}
				}
				if err := os.RemoveAll(codexRoot); err != nil {
					f.t.Fatal(err)
				}
				if err := os.Symlink(outside, codexRoot); err != nil {
					f.t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, test.phase, test.upgrade)
			test.prepare(fixture)
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered || result.Direction != test.direction {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.store.found || hasMarketplaceRecoveryWrite(fixture.events) {
				t.Fatalf("unsafe cache source caused writes: found=%t events=%v", fixture.store.found, fixture.events)
			}
		})
	}
}

func TestRecoverMarketplacePluginPersistsDirectionBeforeWrites(t *testing.T) {
	tests := []struct {
		name      string
		phase     Phase
		direction Phase
		prepare   func(*marketplaceRecoveryFixture)
	}{
		{
			name: "commit", phase: PhaseAddingNewPlugin, direction: PhaseCommitting,
			prepare: func(f *marketplaceRecoveryFixture) {
				setMarketplaceRecoveryState(f, "target", true, false)
			},
		},
		{
			name: "rollback", phase: PhaseRegisteringMarketplace, direction: PhaseRollingBack,
			prepare: func(*marketplaceRecoveryFixture) {},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, test.phase, false)
			test.prepare(fixture)
			updateErr := errors.New("direction update failed")
			fixture.store.fail["update:"+string(test.direction)] = updateErr
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, updateErr) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if !fixture.store.found || fixture.store.journal.Phase != test.phase {
				t.Fatalf("journal changed despite update failure: found=%t phase=%q", fixture.store.found, fixture.store.journal.Phase)
			}
			for _, event := range fixture.events {
				if strings.HasPrefix(event, "manifest:") || strings.HasPrefix(event, "add-plugin:") ||
					strings.HasPrefix(event, "remove-plugin:") || strings.HasPrefix(event, "register-marketplace:") ||
					strings.HasPrefix(event, "unregister-marketplace:") || event == "delete:journal" {
					t.Fatalf("write happened before direction persistence: %v", fixture.events)
				}
			}
		})
	}
}
