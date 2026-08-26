package transaction

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
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

func (*marketplaceRecoveryResolver) ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error) {
	return nil, nil
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
	targetSource := filepath.Join(cacheHome, "apm", "plugins", "demo", "v2.0.0")
	previousSource := filepath.Join(cacheHome, "apm", "plugins", "demo", "v1.0.0")
	targetRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}
	previousRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	writeValidMarketplaceRecoveryArtifact(t, targetSource, targetRef)
	writeValidMarketplaceRecoveryArtifact(t, previousSource, previousRef)
	target := adapter.PluginTarget{ActualName: "demo", Selector: "demo"}
	previous := adapter.PluginTarget{ActualName: "demo", Selector: "demo"}
	journal := Journal{
		SchemaVersion: 1,
		TransactionID: "00112233445566778899aabbccddeeff",
		Driver:        DriverPlugin,
		Operation:     operation,
		Phase:         phase,
		Agent:         domain.AgentCodex,
		Power:         targetRef,
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
		journal.Plugin.Marketplace.TargetPresent = true
	}
	if upgrade {
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
			target.ActualName: {Presence: adapter.PluginAbsent, ActualName: target.ActualName},
		},
		pluginErrors:    map[string]error{},
		interrupt:       map[string]error{},
		mutationAnswers: map[string][]pluginMutationAnswer{},
	}
	fixture.resolver = &marketplaceRecoveryResolver{events: &fixture.events, adapter: fixture.adapter}
	fixture.service = newService(fixture.manifests, fixture.store, fixture.resolver)
	return fixture
}

func writeValidMarketplaceRecoveryArtifact(t *testing.T, root string, ref domain.PowerRef) {
	t.Helper()
	actualName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		t.Fatal(err)
	}
	nativeVersion := strings.TrimPrefix(ref.Version, "v")
	files := map[string]string{
		filepath.Join("plugins", actualName, "apc-plugin.json"):                     fmt.Sprintf(`{"schema_version":1,"name":%q,"version":%q,"description":"Demo","skills":[%q]}`, actualName, ref.Version, "skills/"+actualName),
		filepath.Join("plugins", actualName, "skills", actualName, "SKILL.md"):      "# Demo\n",
		filepath.Join("plugins", actualName, "skills", actualName, "skill-card.md"): "# Skill Card\n",
		filepath.Join("plugins", actualName, ".codex-plugin", "plugin.json"):        fmt.Sprintf(`{"name":%q,"version":%q,"description":"Demo","skills":"./skills/"}`, actualName, nativeVersion),
		filepath.Join("plugins", actualName, ".claude-plugin", "plugin.json"):       fmt.Sprintf(`{"name":%q,"version":%q,"description":"Demo","skills":["./skills/"]}`, actualName, nativeVersion),
		filepath.Join("plugins", actualName, ".hermes-plugin", "plugin.yaml"):       fmt.Sprintf("name: %s\nversion: %s\ndescription: Demo\nauthor: APM\n", actualName, nativeVersion),
		filepath.Join("plugins", actualName, ".hermes-plugin", "__init__.py"):       "def register(ctx):\n    return None\n",
		filepath.Join(".agents", "plugins", "marketplace.json"): fmt.Sprintf(`{
  "name":"apm-%s",
  "interface":{"displayName":%q},
  "plugins":[{"name":%q,"source":{"source":"local","path":%q},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Coding"}]
		}`, ref.Name, ref.Name, actualName, "./plugins/"+actualName),
		filepath.Join(".claude-plugin", "marketplace.json"): fmt.Sprintf(`{
  "name":"apm-%s",
  "owner":{"name":"APM"},
  "metadata":{"description":"Demo","version":%q},
  "plugins":[{"name":%q,"source":%q,"description":"Demo"}]
		}`, ref.Name, nativeVersion, actualName, "./plugins/"+actualName),
	}
	for relative, contents := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
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
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo", "manifest:set", "delete:journal"},
		},
		{
			name: "adding exact target fixes commit", phase: PhaseAddingNewPlugin, marketplace: "target", targetPresent: true,
			wantDirection: RecoveryCommit,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo", "update:committing", "manifest:set", "delete:journal"},
		},
		{
			name: "adding absent target fixes rollback", phase: PhaseAddingNewPlugin, marketplace: "target",
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo", "update:rolling-back", "unregister-marketplace:"},
		},
		{
			name: "earlier install without target fixes rollback", phase: PhaseRegisteringMarketplace,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo", "update:rolling-back", "delete:journal"},
		},
		{
			name: "rolling back never reverses", phase: PhaseRollingBack, marketplace: "target", targetPresent: true,
			wantDirection: RecoveryRollback,
			wantEvents:    []string{"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo", "remove-plugin:demo", "unregister-marketplace:"},
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
					Presence: adapter.PluginPresent, ActualName: f.target.ActualName, Selector: "other",
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
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, true)
	fixture.setPlugin(fixture.target, adapter.PluginPresent)
	result, err := fixture.recover()
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if !fixture.store.found || hasMarketplaceRecoveryWrite(fixture.events) {
		t.Fatalf("unreachable facts changed state: found=%t events=%v", fixture.store.found, fixture.events)
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

func TestRecoverRetainedMarketplaceReinstallRollbackLeavesPresentSourceUntouched(t *testing.T) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		for _, phase := range []Phase{PhaseAddingNewPlugin, PhaseRollingBack} {
			t.Run(string(agent)+"/"+string(phase), func(t *testing.T) {
				fixture := newMarketplaceRecoveryFixture(t, OperationInstall, phase, false)
				fixture.journal.Agent = agent
				fixture.journal.Plugin.Marketplace.PreviousSource = fixture.targetSource
				fixture.journal.Plugin.Marketplace.PreviousPresent = true
				fixture.store.journal = fixture.journal
				fixture.setMarketplace(fixture.targetSource)

				// 若实现错误地注销 retained marketplace，本注入会使恢复失败并暴露多余写操作。
				fixture.adapter.interrupt["unregister-marketplace:"+fixture.targetSource] = errors.New("retained marketplace must not be unregistered")
				result, err := fixture.recover()
				if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
					t.Fatalf("Recover() = %+v, %v", result, err)
				}
				if fixture.store.found || fixture.adapter.marketplace.Presence != adapter.MarketplacePresent || fixture.adapter.marketplace.Source != fixture.targetSource {
					t.Fatalf("recovery state = journal %v marketplace %+v", fixture.store.found, fixture.adapter.marketplace)
				}
				for _, event := range fixture.events {
					if strings.HasPrefix(event, "unregister-marketplace:") || strings.HasPrefix(event, "register-marketplace:") {
						t.Fatalf("retained marketplace rollback mutated marketplace: events=%v", fixture.events)
					}
				}
			})
		}
	}
}

func TestRecoverRetainedMarketplaceReinstallCommitLeavesPresentSourceUntouched(t *testing.T) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		t.Run(string(agent), func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseCommitting, false)
			fixture.journal.Agent = agent
			fixture.journal.Plugin.Marketplace.PreviousSource = fixture.targetSource
			fixture.journal.Plugin.Marketplace.PreviousPresent = true
			fixture.store.journal = fixture.journal
			fixture.setMarketplace(fixture.targetSource)
			fixture.setPlugin(fixture.target, adapter.PluginPresent)

			result, err := fixture.recover()
			if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			for _, event := range fixture.events {
				if strings.HasPrefix(event, "unregister-marketplace:") || strings.HasPrefix(event, "register-marketplace:") {
					t.Fatalf("retained marketplace commit mutated marketplace: events=%v", fixture.events)
				}
			}
		})
	}
}

func TestRecoverRetainedMarketplaceReinstallFixedRollbackRemovesOnlyPlugin(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, false)
	fixture.journal.Plugin.Marketplace.PreviousSource = fixture.targetSource
	fixture.journal.Plugin.Marketplace.PreviousPresent = true
	fixture.store.journal = fixture.journal
	fixture.setMarketplace(fixture.targetSource)
	fixture.setPlugin(fixture.target, adapter.PluginPresent)

	result, err := fixture.recover()
	if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.adapter.marketplace.Presence != adapter.MarketplacePresent || fixture.adapter.marketplace.Source != fixture.targetSource {
		t.Fatalf("retained marketplace changed: %+v", fixture.adapter.marketplace)
	}
	if fact := fixture.adapter.plugins[fixture.target.ActualName]; fact.Presence != adapter.PluginAbsent {
		t.Fatalf("rolled-back Plugin remains: %+v", fact)
	}
	writes := marketplaceRecoveryWrites(fixture.events)
	want := []string{"remove-plugin", "delete:journal"}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("rollback writes = %v, want %v; events=%v", writes, want, fixture.events)
	}
}

func TestRecoverRetainedMarketplaceReinstallAbsentSourceRequiresFixedRollback(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseAddingNewPlugin, false)
	fixture.journal.Plugin.Marketplace.PreviousSource = fixture.targetSource
	fixture.journal.Plugin.Marketplace.PreviousPresent = true
	fixture.store.journal = fixture.journal

	result, err := fixture.recover()
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v; want fail closed", result, err)
	}
	if !fixture.store.found || fixture.store.journal.Phase != PhaseAddingNewPlugin {
		t.Fatalf("journal = found %v phase %q, want unchanged adding-new-plugin", fixture.store.found, fixture.store.journal.Phase)
	}
	for _, event := range fixture.events {
		if strings.HasPrefix(event, "unregister-marketplace:") || strings.HasPrefix(event, "register-marketplace:") {
			t.Fatalf("unfixed retained recovery mutated marketplace: events=%v", fixture.events)
		}
	}
}

func TestRecoverRetainedMarketplaceReinstallAbsentSourceValidatesOfflineBeforeRegister(t *testing.T) {
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, false)
	fixture.journal.Plugin.Marketplace.PreviousSource = fixture.targetSource
	fixture.journal.Plugin.Marketplace.PreviousPresent = true
	fixture.store.journal = fixture.journal
	if err := os.Remove(filepath.Join(fixture.targetSource, ".agents", "plugins", "marketplace.json")); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.recover()
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v; want offline artifact rejection", result, err)
	}
	if !fixture.store.found || fixture.store.journal.Phase != PhaseRollingBack {
		t.Fatalf("journal = found %v phase %q", fixture.store.found, fixture.store.journal.Phase)
	}
	for _, event := range fixture.events {
		if strings.HasPrefix(event, "register-marketplace:") {
			t.Fatalf("invalid offline artifact reached register: events=%v", fixture.events)
		}
	}
}

func TestRecoverRetainedMarketplaceReinstallAbsentSourceRegisterRetries(t *testing.T) {
	states := []struct {
		name         string
		answer       pluginMutationAnswer
		firstPresent bool
	}{
		{
			name: "applied with error",
			answer: pluginMutationAnswer{
				result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied},
			},
			firstPresent: true,
		},
		{
			name: "not applied",
			answer: pluginMutationAnswer{
				result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied},
			},
		},
		{
			name: "unknown",
			answer: pluginMutationAnswer{
				result: adapter.PluginMutationResult{State: adapter.PluginMutationUnknown},
			},
		},
	}
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		for _, test := range states {
			t.Run(string(agent)+"/"+test.name, func(t *testing.T) {
				fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, false)
				fixture.journal.Agent = agent
				fixture.journal.Plugin.Marketplace.PreviousSource = fixture.targetSource
				fixture.journal.Plugin.Marketplace.PreviousPresent = true
				fixture.store.journal = fixture.journal

				injected := errors.New("injected retained marketplace register failure")
				test.answer.err = injected
				register := "register-marketplace:" + fixture.targetSource
				fixture.adapter.mutationAnswers[register] = []pluginMutationAnswer{test.answer}
				first, err := fixture.recover()
				if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, injected) || first.Recovered || first.Direction != RecoveryRollback {
					t.Fatalf("first Recover() = %+v, %v", first, err)
				}
				if !fixture.store.found || fixture.store.journal.Phase != PhaseRollingBack {
					t.Fatalf("first journal = found %v phase %q", fixture.store.found, fixture.store.journal.Phase)
				}
				if got := fixture.adapter.marketplace.Presence == adapter.MarketplacePresent; got != test.firstPresent {
					t.Fatalf("marketplace present after first recovery = %v, want %v", got, test.firstPresent)
				}

				fixture.events = nil
				fixture.adapter.events = &fixture.events
				fixture.store.events = &fixture.events
				fixture.manifests.events = &fixture.events
				fixture.resolver.events = &fixture.events
				second, err := fixture.recover()
				if err != nil || !second.Recovered || second.Direction != RecoveryRollback {
					t.Fatalf("second Recover() = %+v, %v", second, err)
				}
				if fixture.store.found || fixture.adapter.marketplace.Presence != adapter.MarketplacePresent || fixture.adapter.marketplace.Source != fixture.targetSource {
					t.Fatalf("second recovery state = journal %v marketplace %+v", fixture.store.found, fixture.adapter.marketplace)
				}
				registers := 0
				for _, event := range fixture.events {
					if strings.HasPrefix(event, "unregister-marketplace:") {
						t.Fatalf("retained retry unregistered marketplace: events=%v", fixture.events)
					}
					if strings.HasPrefix(event, "register-marketplace:") {
						registers++
					}
				}
				wantRegisters := 1
				if test.firstPresent {
					wantRegisters = 0
				}
				if registers != wantRegisters {
					t.Fatalf("second recovery register calls = %d, want %d; events=%v", registers, wantRegisters, fixture.events)
				}
			})
		}
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
	} else if fixture.adapter.marketplace.Presence != adapter.MarketplaceAbsent ||
		fixture.adapter.plugins[fixture.target.ActualName].Presence != adapter.PluginAbsent {
		t.Fatalf("first-install state remains: marketplace=%+v plugins=%+v", fixture.adapter.marketplace, fixture.adapter.plugins)
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
		"inspect-plugin:demo",
		"manifest:set",
		"delete:journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestRecoverMarketplacePluginRequiresCompleteSharedArtifact(t *testing.T) {
	// 捕获错误变更：恢复只校验 journal Agent，忽略同一共享 artifact 的另一份派生 marketplace 损坏。
	tests := []struct {
		name              string
		agent             domain.AgentID
		unrelatedRelative string
		old               string
		replacement       string
	}{
		{
			name: "Codex rejects corrupted Claude marketplace", agent: domain.AgentCodex,
			unrelatedRelative: filepath.Join(".claude-plugin", "marketplace.json"),
			old:               `"name":"apm-demo"`, replacement: `"name":"apm-other"`,
		},
		{
			name: "Claude rejects corrupted Codex marketplace", agent: domain.AgentClaudeCode,
			unrelatedRelative: filepath.Join(".agents", "plugins", "marketplace.json"),
			old:               `"name":"apm-demo"`, replacement: `"name":"apm-other"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseCommitting, false)
			fixture.journal.Agent = test.agent
			fixture.store.journal.Agent = test.agent
			setMarketplaceRecoveryState(fixture, "target", true, false)
			path := filepath.Join(fixture.targetSource, test.unrelatedRelative)
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			corrupt := strings.Replace(string(contents), test.old, test.replacement, 1)
			if corrupt == string(contents) {
				t.Fatalf("fixture %q does not contain %q", path, test.old)
			}
			if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
				t.Fatal(err)
			}

			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered || result.Direction != RecoveryCommit {
				t.Fatalf("Recover() = %+v, %v, want indeterminate commit", result, err)
			}
			assertRequiredArtifactFailureRetainedState(t, fixture, PhaseCommitting)
		})
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
				if len(fixture.events) < 4 || !reflect.DeepEqual(fixture.events[:4], []string{
					"resolve:codex", "inspect-marketplace:apm-demo", "inspect-plugin:demo", "update:rolling-back",
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

func TestRecoverMarketplacePluginUninstallConvergesToRemoval(t *testing.T) {
	tests := []struct {
		name       string
		phase      Phase
		configure  func(*marketplaceRecoveryFixture)
		wantWrites []string
	}{
		{
			name:  "pre-remove removes Plugin and marketplace",
			phase: PhaseRemovingPlugin,
			configure: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.setPlugin(f.target, adapter.PluginPresent)
			},
			wantWrites: []string{"remove-plugin", "unregister-marketplace", "manifest:remove", "delete:journal"},
		},
		{
			name:  "Plugin removed unregisters marketplace",
			phase: PhaseRemovingPlugin,
			configure: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
			},
			wantWrites: []string{"unregister-marketplace", "manifest:remove", "delete:journal"},
		},
		{
			name:       "marketplace removed commits",
			phase:      PhaseUnregisteringMarketplace,
			wantWrites: []string{"manifest:remove", "delete:journal"},
		},
		{
			name:       "committing removes manifest",
			phase:      PhaseCommitting,
			wantWrites: []string{"manifest:remove", "delete:journal"},
		},
		{
			name:  "marketplace only unregisters marketplace",
			phase: PhaseRemovingPlugin,
			configure: func(f *marketplaceRecoveryFixture) {
				f.journal.Plugin.TargetPresent = false
				f.store.journal.Plugin.TargetPresent = false
				f.setMarketplace(f.targetSource)
			},
			wantWrites: []string{"unregister-marketplace", "manifest:remove", "delete:journal"},
		},
		{
			name:  "Plugin only removes Plugin",
			phase: PhaseRemovingPlugin,
			configure: func(f *marketplaceRecoveryFixture) {
				f.journal.Plugin.Marketplace.TargetPresent = false
				f.store.journal.Plugin.Marketplace.TargetPresent = false
				f.setPlugin(f.target, adapter.PluginPresent)
			},
			wantWrites: []string{"remove-plugin", "manifest:remove", "delete:journal"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationUninstall, test.phase, false)
			if test.configure != nil {
				test.configure(fixture)
			}
			result, err := fixture.recover()
			if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if got := marketplaceRecoveryWrites(fixture.events); !reflect.DeepEqual(got, test.wantWrites) {
				t.Fatalf("writes = %v, want %v; events=%v", got, test.wantWrites, fixture.events)
			}
			if fixture.store.found {
				t.Fatal("uninstall recovery left journal")
			}
			if fixture.manifests.removed != (domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin}) {
				t.Fatalf("manifest removed = %+v, want demo Plugin", fixture.manifests.removed)
			}
			for _, event := range fixture.events {
				if strings.HasPrefix(event, "register-marketplace:") || strings.HasPrefix(event, "add-plugin:") {
					t.Fatalf("uninstall recovery added Agent state: %v", fixture.events)
				}
			}
		})
	}
}

func TestRecoverMarketplacePluginUninstallRejectsUnreachableState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*marketplaceRecoveryFixture)
	}{
		{
			name: "marketplace changed source",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(filepath.Join(filepath.Dir(f.targetSource), "v9.9.9"))
			},
		},
		{
			name: "Plugin selector conflict",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.setPlugin(adapter.PluginTarget{ActualName: "demo", Selector: "other"}, adapter.PluginPresent)
			},
		},
		{
			name: "Plugin reappeared without recorded presence",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.journal.Plugin.TargetPresent = false
				f.store.journal.Plugin.TargetPresent = false
				f.setPlugin(f.target, adapter.PluginPresent)
			},
		},
		{
			name: "marketplace reappeared without recorded presence",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.journal.Plugin.Marketplace.TargetPresent = false
				f.store.journal.Plugin.Marketplace.TargetPresent = false
				f.setMarketplace(f.targetSource)
			},
		},
		{
			name: "marketplace disappeared before Plugin removal",
			mutate: func(f *marketplaceRecoveryFixture) {
				f.setPlugin(f.target, adapter.PluginPresent)
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
			if !fixture.store.found || hasMarketplaceRecoveryWrite(fixture.events) {
				t.Fatalf("unreachable uninstall state caused writes: found=%t events=%v", fixture.store.found, fixture.events)
			}
		})
	}
}

func TestRecoverMarketplacePluginUninstallMutationMatrix(t *testing.T) {
	operationErr := errors.New("CLI write failed")
	tests := []struct {
		name              string
		configure         func(*marketplaceRecoveryFixture)
		wantIndeterminate bool
	}{
		{
			name: "remove Plugin not applied",
			configure: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.setPlugin(f.target, adapter.PluginPresent)
				f.adapter.mutationAnswers["remove-plugin:demo"] = []pluginMutationAnswer{
					{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: operationErr},
				}
			},
		},
		{
			name: "remove Plugin unknown",
			configure: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.setPlugin(f.target, adapter.PluginPresent)
				f.adapter.mutationAnswers["remove-plugin:demo"] = []pluginMutationAnswer{
					{result: adapter.PluginMutationResult{State: adapter.PluginMutationUnknown}, err: operationErr},
				}
			},
			wantIndeterminate: true,
		},
		{
			name: "unregister marketplace not applied",
			configure: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.adapter.mutationAnswers["unregister-marketplace:"+f.targetSource] = []pluginMutationAnswer{
					{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: operationErr},
				}
			},
		},
		{
			name: "unregister marketplace unknown",
			configure: func(f *marketplaceRecoveryFixture) {
				f.setMarketplace(f.targetSource)
				f.adapter.mutationAnswers["unregister-marketplace:"+f.targetSource] = []pluginMutationAnswer{
					{result: adapter.PluginMutationResult{State: adapter.PluginMutationUnknown}, err: operationErr},
				}
			},
			wantIndeterminate: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationUninstall, PhaseRemovingPlugin, false)
			test.configure(fixture)
			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, operationErr) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if errors.Is(err, ErrIndeterminateState) != test.wantIndeterminate {
				t.Fatalf("Recover() indeterminate = %t, want %t; err=%v", errors.Is(err, ErrIndeterminateState), test.wantIndeterminate, err)
			}
			if !fixture.store.found {
				t.Fatal("failed uninstall mutation dropped journal")
			}
			if fixture.manifests.removed != (domain.PowerRef{}) {
				t.Fatalf("failed uninstall mutation removed manifest: %+v", fixture.manifests.removed)
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
		"inspect-plugin:demo",
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
			if len(fixture.events) < 4 || fixture.events[3] != "update:rolling-back" {
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
		"inspect-plugin:demo",
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

func TestRecoverMarketplacePluginRejectsIncompleteRequiredTargetArtifactBeforeWrites(t *testing.T) {
	// 捕获错误变更：Recovery 只检查 source 目录链，随后用缺失/损坏的不可变 artifact 提交 manifest。
	tests := []struct {
		name   string
		mutate func(*marketplaceRecoveryFixture) string
	}{
		{
			name: "missing generated marketplace",
			mutate: func(f *marketplaceRecoveryFixture) string {
				if err := os.Remove(filepath.Join(f.targetSource, ".agents", "plugins", "marketplace.json")); err != nil {
					f.t.Fatal(err)
				}
				return ""
			},
		},
		{
			name: "corrupt generated marketplace",
			mutate: func(f *marketplaceRecoveryFixture) string {
				path := filepath.Join(f.targetSource, ".agents", "plugins", "marketplace.json")
				if err := os.WriteFile(path, []byte(`{"name":"apm-demo","plugins":[]}`), 0o600); err != nil {
					f.t.Fatal(err)
				}
				return ""
			},
		},
		{
			name: "changed native manifest identity",
			mutate: func(f *marketplaceRecoveryFixture) string {
				path := filepath.Join(f.targetSource, "plugins", f.target.ActualName, ".codex-plugin", "plugin.json")
				if err := os.WriteFile(path, []byte(`{"name":"other-v2.0.0","version":"2.0.0"}`), 0o600); err != nil {
					f.t.Fatal(err)
				}
				return ""
			},
		},
		{
			name: "extra capability directory",
			mutate: func(f *marketplaceRecoveryFixture) string {
				path := filepath.Join(f.targetSource, "plugins", f.target.ActualName, "commands", "demo.md")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					f.t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("command\n"), 0o600); err != nil {
					f.t.Fatal(err)
				}
				return ""
			},
		},
		{
			name: "extra native file",
			mutate: func(f *marketplaceRecoveryFixture) string {
				path := filepath.Join(f.targetSource, "plugins", f.target.ActualName, ".hermes-plugin", "debug.py")
				if err := os.WriteFile(path, []byte("debug\n"), 0o600); err != nil {
					f.t.Fatal(err)
				}
				return ""
			},
		},
		{
			name: "extra marketplace file",
			mutate: func(f *marketplaceRecoveryFixture) string {
				path := filepath.Join(f.targetSource, ".agents", "plugins", "extra.json")
				if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
					f.t.Fatal(err)
				}
				return ""
			},
		},
		{
			name: "nested symlink",
			mutate: func(f *marketplaceRecoveryFixture) string {
				sentinel := filepath.Join(f.t.TempDir(), "outside-sentinel")
				if err := os.WriteFile(sentinel, []byte("outside unchanged"), 0o600); err != nil {
					f.t.Fatal(err)
				}
				link := filepath.Join(f.targetSource, "plugins", f.target.ActualName, "linked-payload")
				if err := os.Symlink(sentinel, link); err != nil {
					f.t.Fatal(err)
				}
				return sentinel
			},
		},
		{
			name: "nested special file",
			mutate: func(f *marketplaceRecoveryFixture) string {
				path := filepath.Join(f.targetSource, "plugins", f.target.ActualName, "payload.fifo")
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					f.t.Fatal(err)
				}
				return ""
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseCommitting, false)
			setMarketplaceRecoveryState(fixture, "target", true, false)
			sentinel := test.mutate(fixture)

			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered || result.Direction != RecoveryCommit {
				t.Fatalf("Recover() = %+v, %v, want indeterminate commit", result, err)
			}
			assertRequiredArtifactFailureRetainedState(t, fixture, PhaseCommitting)
			if sentinel != "" {
				contents, readErr := os.ReadFile(sentinel)
				if readErr != nil || string(contents) != "outside unchanged" {
					t.Fatalf("external sentinel = %q, %v, want unchanged", contents, readErr)
				}
			}
		})
	}
}

func TestRecoverMarketplacePluginRejectsCodexKnownFieldTypeCorruptionBeforeWrites(t *testing.T) {
	// 捕获错误变更：恢复层的共享 schema 忽略 builder 已知字段类型损坏，并使用损坏的不可变 artifact 收敛事务。
	tests := []struct {
		name     string
		manifest string
	}{
		{
			name:     "description changed to object",
			manifest: `{"name":"demo","version":"2.0.0","description":{"text":"Demo"},"skills":"./skills/"}`,
		},
		{
			name:     "skills changed to array",
			manifest: `{"name":"demo","version":"2.0.0","description":"Demo","skills":["./skills/"]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseCommitting, false)
			setMarketplaceRecoveryState(fixture, "target", true, false)
			manifestPath := filepath.Join(fixture.targetSource, "plugins", fixture.target.ActualName, ".codex-plugin", "plugin.json")
			if err := os.WriteFile(manifestPath, []byte(test.manifest), 0o600); err != nil {
				t.Fatal(err)
			}

			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered || result.Direction != RecoveryCommit {
				t.Fatalf("Recover() = %+v, %v, want indeterminate commit", result, err)
			}
			assertRequiredArtifactFailureRetainedState(t, fixture, PhaseCommitting)
			contents, readErr := os.ReadFile(manifestPath)
			if readErr != nil || string(contents) != test.manifest {
				t.Fatalf("recovery manifest = %q, %v, want unchanged corruption", contents, readErr)
			}
		})
	}
}

func TestRecoverMarketplacePluginRollbackValidatesCompletePreviousArtifactBeforeWrites(t *testing.T) {
	// 捕获错误变更：需要恢复旧 Plugin 时仅校验 previous source 目录存在便开始注册和安装。
	fixture := newMarketplaceRecoveryFixture(t, OperationInstall, PhaseRollingBack, true)
	manifest := filepath.Join(fixture.previousSource, "plugins", fixture.previous.ActualName, ".codex-plugin", "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"other-v1.0.0","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.recover()
	if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || result.Recovered || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v, want indeterminate rollback", result, err)
	}
	assertRequiredArtifactFailureRetainedState(t, fixture, PhaseRollingBack)
}

func TestRecoverMarketplacePluginUninstallDoesNotValidateArtifact(t *testing.T) {
	// 捕获错误变更：卸载恢复复验或依赖不可变共享 artifact，而卸载只移除运行时事实，绝不依赖缓存内容。
	fixture := newMarketplaceRecoveryFixture(t, OperationUninstall, PhaseCommitting, false)
	if err := os.RemoveAll(fixture.targetSource); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.recover()
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
		t.Fatalf("Recover() = %+v, %v, want removal commit without artifact", result, err)
	}
	if fixture.store.found {
		t.Fatal("uninstall recovery left journal")
	}
	if fixture.manifests.removed != (domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin}) {
		t.Fatalf("manifest removed = %+v, want demo Plugin", fixture.manifests.removed)
	}
}

func TestRecoverMarketplacePluginUnknownCollectionRetainsDirectionAndState(t *testing.T) {
	// 捕获错误变更：malformed Plugin list 的 unknown 事实仍固定方向或触发 Agent/manifest 写入。
	tests := []struct {
		name      string
		operation Operation
		phase     Phase
	}{
		{name: "install before direction", operation: OperationInstall, phase: PhaseAddingNewPlugin},
		{name: "install persisted rollback", operation: OperationInstall, phase: PhaseRollingBack},
		{name: "install persisted commit", operation: OperationInstall, phase: PhaseCommitting},
		{name: "uninstall before direction", operation: OperationUninstall, phase: PhaseRemovingPlugin},
		{name: "uninstall persisted unregistering", operation: OperationUninstall, phase: PhaseUnregisteringMarketplace},
		{name: "uninstall persisted commit", operation: OperationUninstall, phase: PhaseCommitting},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplaceRecoveryFixture(t, test.operation, test.phase, false)
			fixture.setMarketplace(fixture.targetSource)
			fixture.adapter.pluginErrors[fixture.target.ActualName] = adapter.ErrPluginStateUnknown

			result, err := fixture.recover()
			if !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, ErrIndeterminateState) || !errors.Is(err, adapter.ErrPluginStateUnknown) || result.Recovered {
				t.Fatalf("Recover() = %+v, %v, want retained unknown state", result, err)
			}
			assertRequiredArtifactFailureRetainedState(t, fixture, test.phase)
		})
	}
}

func assertRequiredArtifactFailureRetainedState(t *testing.T, fixture *marketplaceRecoveryFixture, phase Phase) {
	t.Helper()
	if !fixture.store.found || fixture.store.journal.Phase != phase {
		t.Fatalf("journal = found %t phase %q, want retained %q", fixture.store.found, fixture.store.journal.Phase, phase)
	}
	if fixture.manifests.setRef != (domain.PowerRef{}) || fixture.manifests.removed != (domain.PowerRef{}) {
		t.Fatalf("manifest changed: set=%+v removed=%+v", fixture.manifests.setRef, fixture.manifests.removed)
	}
	for _, event := range fixture.events {
		if strings.HasPrefix(event, "add-plugin:") || strings.HasPrefix(event, "remove-plugin:") ||
			strings.HasPrefix(event, "register-marketplace:") || strings.HasPrefix(event, "unregister-marketplace:") {
			t.Fatalf("required artifact failure caused Agent write: %v", fixture.events)
		}
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
