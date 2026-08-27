package transaction

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type recordingMarketplaceJournalStore struct {
	events  *[]string
	journal Journal
	found   bool
}

func (s *recordingMarketplaceJournalStore) Inspect() (Journal, bool, error) {
	return s.journal, s.found, nil
}

func (s *recordingMarketplaceJournalStore) Create(journal Journal) error {
	if err := validateJournal(journal); err != nil {
		return err
	}
	*s.events = append(*s.events, "create:"+string(journal.Phase))
	if s.found {
		return ErrActiveTransaction
	}
	s.journal, s.found = journal, true
	return nil
}

func (s *recordingMarketplaceJournalStore) Update(journal Journal) error {
	if err := validateJournal(journal); err != nil {
		return err
	}
	*s.events = append(*s.events, "update:"+string(journal.Phase))
	s.journal, s.found = journal, true
	return nil
}

func (s *recordingMarketplaceJournalStore) Delete(id string) error {
	*s.events = append(*s.events, "delete-journal")
	if !s.found || s.journal.TransactionID != id {
		return ErrInvalidJournal
	}
	s.found = false
	return nil
}

type recordingMarketplaceManifestWriter struct {
	events  *[]string
	setRef  domain.PowerRef
	removed domain.PowerRef
}

func (w *recordingMarketplaceManifestWriter) Set(_ domain.AgentID, power domain.InstalledPower) error {
	*w.events = append(*w.events, "set-manifest")
	w.setRef = power.Ref
	return nil
}

func (w *recordingMarketplaceManifestWriter) Remove(_ domain.AgentID, powerType domain.PowerType, name string) error {
	*w.events = append(*w.events, "remove-manifest")
	w.removed = domain.PowerRef{Name: name, Type: powerType}
	return nil
}

type recordingMarketplacePluginAdapter struct {
	events            *[]string
	pluginFacts       map[string]adapter.PluginFact
	marketplaceFact   adapter.MarketplaceFact
	addAnswers        []pluginMutationAnswer
	removeAnswers     []pluginMutationAnswer
	registerAnswers   []pluginMutationAnswer
	unregisterAnswers []pluginMutationAnswer
	added             []adapter.PluginTarget
	removed           []adapter.PluginTarget
	registered        []adapter.MarketplaceTarget
	unregistered      []adapter.MarketplaceTarget
	marketplaceNames  []string
	wantMarketplace   string
}

func (*recordingMarketplacePluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}

func (a *recordingMarketplacePluginAdapter) Inspect(_ context.Context, name string) (adapter.PluginFact, error) {
	*a.events = append(*a.events, "inspect-plugin")
	return a.pluginFacts[name], nil
}

func (a *recordingMarketplacePluginAdapter) Add(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "add-plugin")
	a.added = append(a.added, target)
	answer := a.addAnswers[0]
	a.addAnswers = a.addAnswers[1:]
	return answer.result, answer.err
}

func (a *recordingMarketplacePluginAdapter) Remove(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "remove-plugin")
	a.removed = append(a.removed, target)
	answer := a.removeAnswers[0]
	a.removeAnswers = a.removeAnswers[1:]
	return answer.result, answer.err
}

func (*recordingMarketplacePluginAdapter) TargetPath(string) (string, error) {
	return "", adapter.ErrUnsupportedAdapter
}

func (a *recordingMarketplacePluginAdapter) InspectMarketplace(_ context.Context, name string) (adapter.MarketplaceFact, error) {
	*a.events = append(*a.events, "inspect-marketplace")
	a.marketplaceNames = append(a.marketplaceNames, name)
	if name != a.wantMarketplace {
		return adapter.MarketplaceFact{}, fmt.Errorf("inspect marketplace %q, want %q", name, a.wantMarketplace)
	}
	return a.marketplaceFact, nil
}

func (a *recordingMarketplacePluginAdapter) RegisterMarketplace(_ context.Context, target adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "register-marketplace")
	a.registered = append(a.registered, target)
	answer := a.registerAnswers[0]
	a.registerAnswers = a.registerAnswers[1:]
	return answer.result, answer.err
}

func (a *recordingMarketplacePluginAdapter) UnregisterMarketplace(_ context.Context, target adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "unregister-marketplace")
	a.unregistered = append(a.unregistered, target)
	answer := a.unregisterAnswers[0]
	a.unregisterAnswers = a.unregisterAnswers[1:]
	return answer.result, answer.err
}

type marketplacePluginFixture struct {
	events    []string
	journals  *recordingMarketplaceJournalStore
	manifests *recordingMarketplaceManifestWriter
	adapter   *recordingMarketplacePluginAdapter
	service   *Service
	request   PluginInstallRequest
}

func newMarketplacePluginFixture(t *testing.T) *marketplacePluginFixture {
	t.Helper()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	targetSource := filepath.Join(cacheHome, "apm", "plugins", "demo", "v2.0.0")
	target := adapter.PluginTarget{ActualName: "demo", Selector: "demo"}

	fixture := &marketplacePluginFixture{}
	fixture.journals = &recordingMarketplaceJournalStore{events: &fixture.events}
	fixture.manifests = &recordingMarketplaceManifestWriter{events: &fixture.events}
	fixture.adapter = &recordingMarketplacePluginAdapter{
		events:          &fixture.events,
		wantMarketplace: "apm-demo",
		pluginFacts: map[string]adapter.PluginFact{
			target.ActualName: {Presence: adapter.PluginAbsent, ActualName: target.ActualName},
		},
		marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
		addAnswers:      []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}},
		registerAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}},
	}
	fixture.service = newService(fixture.manifests, fixture.journals)
	fixture.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	fixture.request = PluginInstallRequest{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"},
		Adapter: fixture.adapter,
		Target:  target,
		Marketplace: &PluginMarketplaceInstallRequest{
			Target:  adapter.MarketplaceTarget{Name: "apm-demo", Source: targetSource},
			Adapter: fixture.adapter,
		},
	}
	return fixture
}

func TestMarketplacePluginFirstInstallOrder(t *testing.T) {
	fixture := newMarketplacePluginFixture(t)
	if err := fixture.service.InstallPlugin(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"inspect-marketplace", "inspect-plugin",
		"create:registering-marketplace", "register-marketplace",
		"update:adding-new-plugin", "add-plugin",
		"update:committing", "set-manifest", "delete-journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if fixture.journals.found || fixture.manifests.setRef != fixture.request.Power {
		t.Fatalf("journal found/set ref = %v/%+v", fixture.journals.found, fixture.manifests.setRef)
	}
	if got := fixture.adapter.registered; len(got) != 1 || got[0] != fixture.request.Marketplace.Target {
		t.Fatalf("registered = %+v", got)
	}
	if got := fixture.adapter.added; len(got) != 1 || got[0] != fixture.request.Target {
		t.Fatalf("added = %+v", got)
	}
	if got := fixture.adapter.marketplaceNames; !reflect.DeepEqual(got, []string{"apm-demo"}) {
		t.Fatalf("inspected marketplace names = %v, want [apm-demo]", got)
	}
}

func TestMarketplacePluginReinstallReusesExactRetainedMarketplace(t *testing.T) {
	fixture := newMarketplacePluginFixture(t)
	fixture.adapter.marketplaceFact = adapter.MarketplaceFact{
		Presence: adapter.MarketplacePresent,
		Name:     fixture.request.Marketplace.Target.Name,
		Source:   fixture.request.Marketplace.Target.Source,
	}

	if err := fixture.service.InstallPlugin(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"inspect-marketplace", "inspect-plugin",
		"create:adding-new-plugin", "add-plugin",
		"update:committing", "set-manifest", "delete-journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if len(fixture.adapter.registered) != 0 || len(fixture.adapter.unregistered) != 0 {
		t.Fatalf("reinstall changed retained marketplace: registered=%+v unregistered=%+v", fixture.adapter.registered, fixture.adapter.unregistered)
	}
}

func TestMarketplacePluginReinstallAddFailureKeepsRetainedMarketplace(t *testing.T) {
	fixture := newMarketplacePluginFixture(t)
	fixture.adapter.marketplaceFact = adapter.MarketplaceFact{
		Presence: adapter.MarketplacePresent,
		Name:     fixture.request.Marketplace.Target.Name,
		Source:   fixture.request.Marketplace.Target.Source,
	}
	injected := errors.New("injected Plugin add failure")
	fixture.adapter.addAnswers = []pluginMutationAnswer{{
		result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied},
		err:    injected,
	}}

	if err := fixture.service.InstallPlugin(context.Background(), fixture.request); !errors.Is(err, injected) {
		t.Fatalf("InstallPlugin() error = %v, want injected failure", err)
	}
	want := []string{
		"inspect-marketplace", "inspect-plugin", "create:adding-new-plugin", "add-plugin",
		"update:rolling-back", "delete-journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if fixture.journals.found || len(fixture.adapter.registered) != 0 || len(fixture.adapter.unregistered) != 0 {
		t.Fatalf("failed reinstall changed retained marketplace: journal=%v registered=%+v unregistered=%+v", fixture.journals.found, fixture.adapter.registered, fixture.adapter.unregistered)
	}
}

func TestMarketplacePluginVersionSwitchOrder(t *testing.T) {
	fixture := newMarketplacePluginFixture(t)
	_, previousMarketplace := configureMarketplaceVersionSwitch(fixture)
	fixture.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
	fixture.adapter.unregisterAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}

	if err := fixture.service.InstallPlugin(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"inspect-marketplace", "inspect-plugin",
		"create:removing-old-plugin", "remove-plugin",
		"update:unregistering-old-marketplace", "unregister-marketplace",
		"update:registering-new-marketplace", "register-marketplace",
		"update:adding-new-plugin", "add-plugin",
		"update:committing", "set-manifest", "delete-journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if got := fixture.adapter.unregistered; len(got) != 1 || got[0] != previousMarketplace {
		t.Fatalf("unregistered = %+v", got)
	}
	if got := fixture.adapter.registered; len(got) != 1 || got[0] != fixture.request.Marketplace.Target {
		t.Fatalf("registered = %+v", got)
	}
	if previousMarketplace.Name != fixture.request.Marketplace.Target.Name || previousMarketplace.Source == fixture.request.Marketplace.Target.Source {
		t.Fatalf("marketplace identity/source = %+v -> %+v", previousMarketplace, fixture.request.Marketplace.Target)
	}
	if got := fixture.adapter.marketplaceNames; !reflect.DeepEqual(got, []string{"apm-demo"}) {
		t.Fatalf("inspected marketplace names = %v, want [apm-demo]", got)
	}
}

func configureMarketplaceVersionSwitch(fixture *marketplacePluginFixture) (adapter.PluginTarget, adapter.MarketplaceTarget) {
	previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	previousTarget := adapter.PluginTarget{ActualName: "demo", Selector: "demo"}
	previousMarketplace := adapter.MarketplaceTarget{
		Name: "apm-demo", Source: filepath.Join(filepath.Dir(fixture.request.Marketplace.Target.Source), "v1.0.0"),
	}
	fixture.request.Previous = &previous
	fixture.request.PreviousTarget = &previousTarget
	fixture.request.Marketplace.Previous = &previousMarketplace
	fixture.adapter.marketplaceFact = adapter.MarketplaceFact{
		Presence: adapter.MarketplacePresent, Name: previousMarketplace.Name, Source: previousMarketplace.Source,
	}
	fixture.adapter.pluginFacts[previousTarget.ActualName] = adapter.PluginFact{
		Presence: adapter.PluginPresent, ActualName: previousTarget.ActualName, Selector: previousTarget.Selector,
	}
	return previousTarget, previousMarketplace
}

func TestMarketplacePluginVersionSwitchMutationMatrix(t *testing.T) {
	operationErr := errors.New("CLI write failed")
	tests := []struct {
		operation         string
		state             adapter.PluginMutationState
		wantPhase         Phase
		wantFound         bool
		wantTail          []string
		wantIndeterminate bool
	}{
		{operation: "remove-old-plugin", state: adapter.PluginMutationApplied, wantPhase: PhaseUnregisteringOldMarketplace, wantFound: true, wantTail: []string{"remove-plugin", "update:unregistering-old-marketplace"}},
		{operation: "remove-old-plugin", state: adapter.PluginMutationNotApplied, wantTail: []string{"remove-plugin", "update:rolling-back", "delete-journal"}},
		{operation: "remove-old-plugin", state: adapter.PluginMutationUnknown, wantPhase: PhaseRemovingOldPlugin, wantFound: true, wantIndeterminate: true, wantTail: []string{"remove-plugin"}},
		{operation: "unregister-old-marketplace", state: adapter.PluginMutationApplied, wantPhase: PhaseRegisteringNewMarketplace, wantFound: true, wantTail: []string{"unregister-marketplace", "update:registering-new-marketplace"}},
		{operation: "unregister-old-marketplace", state: adapter.PluginMutationNotApplied, wantTail: []string{"unregister-marketplace", "update:rolling-back", "add-plugin", "delete-journal"}},
		{operation: "unregister-old-marketplace", state: adapter.PluginMutationUnknown, wantPhase: PhaseUnregisteringOldMarketplace, wantFound: true, wantIndeterminate: true, wantTail: []string{"unregister-marketplace"}},
		{operation: "register-new-marketplace", state: adapter.PluginMutationApplied, wantPhase: PhaseAddingNewPlugin, wantFound: true, wantTail: []string{"register-marketplace", "update:adding-new-plugin"}},
		{operation: "register-new-marketplace", state: adapter.PluginMutationNotApplied, wantTail: []string{"register-marketplace", "update:rolling-back", "register-marketplace", "add-plugin", "delete-journal"}},
		{operation: "register-new-marketplace", state: adapter.PluginMutationUnknown, wantPhase: PhaseRegisteringNewMarketplace, wantFound: true, wantIndeterminate: true, wantTail: []string{"register-marketplace"}},
		{operation: "add-new-plugin", state: adapter.PluginMutationApplied, wantPhase: PhaseCommitting, wantFound: true, wantTail: []string{"add-plugin", "update:committing"}},
		{operation: "add-new-plugin", state: adapter.PluginMutationNotApplied, wantTail: []string{"add-plugin", "update:rolling-back", "unregister-marketplace", "register-marketplace", "add-plugin", "delete-journal"}},
		{operation: "add-new-plugin", state: adapter.PluginMutationUnknown, wantPhase: PhaseAddingNewPlugin, wantFound: true, wantIndeterminate: true, wantTail: []string{"add-plugin"}},
	}
	for _, test := range tests {
		t.Run(test.operation+"/"+string(test.state), func(t *testing.T) {
			fixture := newMarketplacePluginFixture(t)
			previousTarget, previousMarketplace := configureMarketplaceVersionSwitch(fixture)
			applied := pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}
			selected := pluginMutationAnswer{result: adapter.PluginMutationResult{State: test.state}, err: operationErr}
			fixture.adapter.removeAnswers = []pluginMutationAnswer{applied}
			fixture.adapter.unregisterAnswers = []pluginMutationAnswer{applied}
			fixture.adapter.registerAnswers = []pluginMutationAnswer{applied}
			fixture.adapter.addAnswers = []pluginMutationAnswer{applied}
			switch test.operation {
			case "remove-old-plugin":
				fixture.adapter.removeAnswers = []pluginMutationAnswer{selected}
			case "unregister-old-marketplace":
				fixture.adapter.unregisterAnswers = []pluginMutationAnswer{selected}
				if test.state == adapter.PluginMutationNotApplied {
					fixture.adapter.addAnswers = []pluginMutationAnswer{applied}
				}
			case "register-new-marketplace":
				fixture.adapter.registerAnswers = []pluginMutationAnswer{selected}
				if test.state == adapter.PluginMutationNotApplied {
					fixture.adapter.registerAnswers = append(fixture.adapter.registerAnswers, applied)
					fixture.adapter.addAnswers = []pluginMutationAnswer{applied}
				}
			case "add-new-plugin":
				fixture.adapter.addAnswers = []pluginMutationAnswer{selected}
				if test.state == adapter.PluginMutationNotApplied {
					fixture.adapter.unregisterAnswers = append(fixture.adapter.unregisterAnswers, applied)
					fixture.adapter.registerAnswers = append(fixture.adapter.registerAnswers, applied)
					fixture.adapter.addAnswers = append(fixture.adapter.addAnswers, applied)
				}
			}

			err := fixture.service.InstallPlugin(context.Background(), fixture.request)
			if !errors.Is(err, operationErr) {
				t.Fatalf("InstallPlugin() error = %v, want operation error", err)
			}
			if errors.Is(err, ErrIndeterminateState) != test.wantIndeterminate {
				t.Fatalf("InstallPlugin() indeterminate = %v, want %v: %v", errors.Is(err, ErrIndeterminateState), test.wantIndeterminate, err)
			}
			if fixture.journals.found != test.wantFound || test.wantFound && fixture.journals.journal.Phase != test.wantPhase {
				t.Fatalf("journal = found %v, phase %q; want %v/%q", fixture.journals.found, fixture.journals.journal.Phase, test.wantFound, test.wantPhase)
			}
			got := fixture.events[len(fixture.events)-len(test.wantTail):]
			if !reflect.DeepEqual(got, test.wantTail) {
				t.Fatalf("events tail = %v, want %v; events=%v", got, test.wantTail, fixture.events)
			}
			if fixture.manifests.setRef != (domain.PowerRef{}) || fixture.manifests.removed != (domain.PowerRef{}) {
				t.Fatalf("failed switch changed manifest: set=%+v removed=%+v", fixture.manifests.setRef, fixture.manifests.removed)
			}
			if test.state == adapter.PluginMutationNotApplied {
				if got := fixture.adapter.added; test.operation != "remove-old-plugin" && (len(got) == 0 || got[len(got)-1] != previousTarget) {
					t.Fatalf("rollback added = %+v, want previous Plugin %+v", got, previousTarget)
				}
				if test.operation == "register-new-marketplace" || test.operation == "add-new-plugin" {
					if got := fixture.adapter.registered[len(fixture.adapter.registered)-1]; got != previousMarketplace {
						t.Fatalf("rollback marketplace = %+v, want %+v", got, previousMarketplace)
					}
				}
			}
		})
	}
}

func TestMarketplacePluginFirstInstallMutationMatrix(t *testing.T) {
	operationErr := errors.New("CLI write failed")
	tests := []struct {
		operation         string
		state             adapter.PluginMutationState
		wantPhase         Phase
		wantFound         bool
		wantEventsTail    []string
		wantIndeterminate bool
	}{
		{operation: "register-marketplace", state: adapter.PluginMutationApplied, wantPhase: PhaseAddingNewPlugin, wantFound: true, wantEventsTail: []string{"register-marketplace", "update:adding-new-plugin"}},
		{operation: "register-marketplace", state: adapter.PluginMutationNotApplied, wantEventsTail: []string{"register-marketplace", "update:rolling-back", "delete-journal"}},
		{operation: "register-marketplace", state: adapter.PluginMutationUnknown, wantPhase: PhaseRegisteringMarketplace, wantFound: true, wantIndeterminate: true, wantEventsTail: []string{"register-marketplace"}},
		{operation: "add-plugin", state: adapter.PluginMutationApplied, wantPhase: PhaseCommitting, wantFound: true, wantEventsTail: []string{"add-plugin", "update:committing"}},
		{operation: "add-plugin", state: adapter.PluginMutationNotApplied, wantEventsTail: []string{"add-plugin", "update:rolling-back", "unregister-marketplace", "delete-journal"}},
		{operation: "add-plugin", state: adapter.PluginMutationUnknown, wantPhase: PhaseAddingNewPlugin, wantFound: true, wantIndeterminate: true, wantEventsTail: []string{"add-plugin"}},
	}
	for _, test := range tests {
		t.Run(test.operation+"/"+string(test.state), func(t *testing.T) {
			fixture := newMarketplacePluginFixture(t)
			applied := pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}
			selected := pluginMutationAnswer{result: adapter.PluginMutationResult{State: test.state}, err: operationErr}
			fixture.adapter.registerAnswers = []pluginMutationAnswer{applied}
			fixture.adapter.addAnswers = []pluginMutationAnswer{applied}
			if test.operation == "register-marketplace" {
				fixture.adapter.registerAnswers = []pluginMutationAnswer{selected}
			} else {
				fixture.adapter.addAnswers = []pluginMutationAnswer{selected}
				if test.state == adapter.PluginMutationNotApplied {
					fixture.adapter.unregisterAnswers = []pluginMutationAnswer{applied}
				}
			}

			err := fixture.service.InstallPlugin(context.Background(), fixture.request)
			if !errors.Is(err, operationErr) {
				t.Fatalf("InstallPlugin() error = %v, want operation error", err)
			}
			if errors.Is(err, ErrIndeterminateState) != test.wantIndeterminate {
				t.Fatalf("InstallPlugin() indeterminate = %v, want %v: %v", errors.Is(err, ErrIndeterminateState), test.wantIndeterminate, err)
			}
			if fixture.journals.found != test.wantFound || test.wantFound && fixture.journals.journal.Phase != test.wantPhase {
				t.Fatalf("journal = found %v, phase %q; want %v/%q", fixture.journals.found, fixture.journals.journal.Phase, test.wantFound, test.wantPhase)
			}
			got := fixture.events[len(fixture.events)-len(test.wantEventsTail):]
			if !reflect.DeepEqual(got, test.wantEventsTail) {
				t.Fatalf("events tail = %v, want %v; events=%v", got, test.wantEventsTail, fixture.events)
			}
			if test.state == adapter.PluginMutationNotApplied && (fixture.manifests.setRef != (domain.PowerRef{}) || fixture.manifests.removed != (domain.PowerRef{})) {
				t.Fatalf("rollback changed manifest: set=%+v removed=%+v", fixture.manifests.setRef, fixture.manifests.removed)
			}
		})
	}
}

func TestMarketplacePluginRollbackRestoresJointPreStateInOrder(t *testing.T) {
	fixture := newMarketplacePluginFixture(t)
	previousTarget, previousMarketplace := configureMarketplaceVersionSwitch(fixture)
	journal, err := fixture.service.newLocalMarketplacePluginInstallJournal(fixture.request, PhaseAddingNewPlugin)
	if err != nil {
		t.Fatal(err)
	}
	applied := pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}
	fixture.adapter.removeAnswers = []pluginMutationAnswer{applied}
	fixture.adapter.unregisterAnswers = []pluginMutationAnswer{applied}
	fixture.adapter.registerAnswers = []pluginMutationAnswer{applied}
	fixture.adapter.addAnswers = []pluginMutationAnswer{applied}
	originalErr := errors.New("roll back")

	err = fixture.service.rollbackLocalMarketplacePlugin(context.Background(), journal, fixture.adapter, marketplacePluginProgress{
		previousPluginRemoved: true, previousMarketplaceRemoved: true,
		targetMarketplaceRegistered: true, targetPluginAdded: true,
	}, originalErr)
	if !errors.Is(err, originalErr) {
		t.Fatalf("rollback error = %v, want original error", err)
	}
	want := []string{
		"update:rolling-back", "remove-plugin", "unregister-marketplace",
		"register-marketplace", "add-plugin", "delete-journal",
	}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if got := fixture.adapter.removed; len(got) != 1 || got[0] != fixture.request.Target {
		t.Fatalf("removed = %+v, want target Plugin", got)
	}
	if got := fixture.adapter.unregistered; len(got) != 1 || got[0] != fixture.request.Marketplace.Target {
		t.Fatalf("unregistered = %+v, want target marketplace", got)
	}
	if got := fixture.adapter.registered; len(got) != 1 || got[0] != previousMarketplace {
		t.Fatalf("registered = %+v, want previous marketplace", got)
	}
	if got := fixture.adapter.added; len(got) != 1 || got[0] != previousTarget {
		t.Fatalf("added = %+v, want previous Plugin", got)
	}
}

func TestMarketplacePluginRollbackMutationMatrix(t *testing.T) {
	type rollbackOperation struct {
		name       string
		failureLog []string
		setAnswer  func(*recordingMarketplacePluginAdapter, pluginMutationAnswer)
	}
	type rollbackShape struct {
		name       string
		previous   bool
		progress   marketplacePluginProgress
		operations []rollbackOperation
		successLog []string
	}
	setRemoveAnswer := func(a *recordingMarketplacePluginAdapter, answer pluginMutationAnswer) { a.removeAnswers[0] = answer }
	setUnregisterAnswer := func(a *recordingMarketplacePluginAdapter, answer pluginMutationAnswer) {
		a.unregisterAnswers[0] = answer
	}
	setRegisterAnswer := func(a *recordingMarketplacePluginAdapter, answer pluginMutationAnswer) { a.registerAnswers[0] = answer }
	setAddAnswer := func(a *recordingMarketplacePluginAdapter, answer pluginMutationAnswer) { a.addAnswers[0] = answer }
	shapes := []rollbackShape{
		{
			name: "version switch", previous: true,
			progress: marketplacePluginProgress{
				previousPluginRemoved: true, previousMarketplaceRemoved: true,
				targetMarketplaceRegistered: true, targetPluginAdded: true,
			},
			operations: []rollbackOperation{
				{name: "remove target Plugin", failureLog: []string{"update:rolling-back", "remove-plugin"}, setAnswer: setRemoveAnswer},
				{name: "unregister target marketplace", failureLog: []string{"update:rolling-back", "remove-plugin", "unregister-marketplace"}, setAnswer: setUnregisterAnswer},
				{name: "register previous marketplace", failureLog: []string{"update:rolling-back", "remove-plugin", "unregister-marketplace", "register-marketplace"}, setAnswer: setRegisterAnswer},
				{name: "restore previous Plugin", failureLog: []string{"update:rolling-back", "remove-plugin", "unregister-marketplace", "register-marketplace", "add-plugin"}, setAnswer: setAddAnswer},
			},
			successLog: []string{"update:rolling-back", "remove-plugin", "unregister-marketplace", "register-marketplace", "add-plugin", "delete-journal"},
		},
		{
			name: "first install",
			progress: marketplacePluginProgress{
				targetMarketplaceRegistered: true,
			},
			operations: []rollbackOperation{
				{name: "unregister target marketplace", failureLog: []string{"update:rolling-back", "unregister-marketplace"}, setAnswer: setUnregisterAnswer},
			},
			successLog: []string{"update:rolling-back", "unregister-marketplace", "delete-journal"},
		},
	}
	states := []struct {
		name              string
		answer            pluginMutationAnswer
		wantMutationError bool
		wantIndeterminate bool
	}{
		{name: "applied", answer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}},
		{name: "not-applied", answer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: errRollbackMutation}, wantMutationError: true},
		{name: "unknown", answer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationUnknown}, err: errRollbackMutation}, wantMutationError: true, wantIndeterminate: true},
		{name: "invalid", answer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationState("invalid")}, err: errRollbackMutation}, wantMutationError: true, wantIndeterminate: true},
		{name: "applied-with-error", answer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, err: errRollbackMutation}, wantMutationError: true},
	}
	for _, shape := range shapes {
		for _, operation := range shape.operations {
			for _, state := range states {
				t.Run(shape.name+"/"+operation.name+"/"+state.name, func(t *testing.T) {
					fixture := newMarketplacePluginFixture(t)
					if shape.previous {
						configureMarketplaceVersionSwitch(fixture)
					}
					journal, err := fixture.service.newLocalMarketplacePluginInstallJournal(fixture.request, PhaseRollingBack)
					if err != nil {
						t.Fatal(err)
					}
					applied := pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}
					fixture.adapter.removeAnswers = []pluginMutationAnswer{applied}
					fixture.adapter.unregisterAnswers = []pluginMutationAnswer{applied}
					fixture.adapter.registerAnswers = []pluginMutationAnswer{applied}
					fixture.adapter.addAnswers = []pluginMutationAnswer{applied}
					operation.setAnswer(fixture.adapter, state.answer)
					originalErr := errors.New("forward mutation was not applied")
					err = fixture.service.rollbackLocalMarketplacePlugin(context.Background(), journal, fixture.adapter, shape.progress, originalErr)
					if !errors.Is(err, originalErr) {
						t.Fatalf("rollback error = %v, want original error", err)
					}
					if errors.Is(err, errRollbackMutation) != state.wantMutationError || errors.Is(err, ErrIndeterminateState) != state.wantIndeterminate {
						t.Fatalf("rollback error = %v, want mutation=%v indeterminate=%v", err, state.wantMutationError, state.wantIndeterminate)
					}
					if state.name == "applied" {
						if fixture.journals.found {
							t.Fatalf("successful rollback retained journal: %+v", fixture.journals.journal)
						}
						if !reflect.DeepEqual(fixture.events, shape.successLog) {
							t.Fatalf("events = %v, want %v", fixture.events, shape.successLog)
						}
					} else {
						if !fixture.journals.found || fixture.journals.journal.Phase != PhaseRollingBack {
							t.Fatalf("failed rollback journal = found %v phase %q", fixture.journals.found, fixture.journals.journal.Phase)
						}
						if !reflect.DeepEqual(fixture.events, operation.failureLog) {
							t.Fatalf("events = %v, want stop at %v", fixture.events, operation.failureLog)
						}
					}
					if fixture.manifests.setRef != (domain.PowerRef{}) || fixture.manifests.removed != (domain.PowerRef{}) {
						t.Fatalf("rollback changed manifest: set=%+v removed=%+v", fixture.manifests.setRef, fixture.manifests.removed)
					}
				})
			}
		}
	}
}

var errRollbackMutation = errors.New("rollback mutation failed")

func marketplacePluginUninstallRequest(fixture *marketplacePluginFixture) PluginUninstallRequest {
	return PluginUninstallRequest{
		Agent:              fixture.request.Agent,
		Power:              fixture.request.Power,
		Adapter:            fixture.adapter,
		Target:             fixture.request.Target,
		Marketplace:        &fixture.request.Marketplace.Target,
		MarketplaceAdapter: fixture.adapter,
	}
}

func TestMarketplacePluginUninstallRemovesPartialTargetsAndMarketplace(t *testing.T) {
	applied := pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}
	tests := []struct {
		name              string
		configure         func(*marketplacePluginFixture)
		wantEvents        []string
		wantTargetMissing bool
		wantRemovePlugin  bool
		wantUnregister    bool
	}{
		{
			name: "both present removes Plugin and marketplace",
			configure: func(fixture *marketplacePluginFixture) {
				fixture.adapter.marketplaceFact = adapter.MarketplaceFact{
					Presence: adapter.MarketplacePresent,
					Name:     fixture.request.Marketplace.Target.Name,
					Source:   fixture.request.Marketplace.Target.Source,
				}
				fixture.adapter.pluginFacts[fixture.request.Target.ActualName] = adapter.PluginFact{
					Presence: adapter.PluginPresent, ActualName: fixture.request.Target.ActualName, Selector: fixture.request.Target.Selector,
				}
				fixture.adapter.removeAnswers = []pluginMutationAnswer{applied}
				fixture.adapter.unregisterAnswers = []pluginMutationAnswer{applied}
			},
			wantEvents: []string{
				"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin",
				"update:unregistering-marketplace", "unregister-marketplace",
				"update:committing", "remove-manifest", "delete-journal",
			},
			wantRemovePlugin: true, wantUnregister: true,
		},
		{
			name: "marketplace only unregisters marketplace",
			configure: func(fixture *marketplacePluginFixture) {
				fixture.adapter.marketplaceFact = adapter.MarketplaceFact{
					Presence: adapter.MarketplacePresent,
					Name:     fixture.request.Marketplace.Target.Name,
					Source:   fixture.request.Marketplace.Target.Source,
				}
				fixture.adapter.unregisterAnswers = []pluginMutationAnswer{applied}
			},
			wantEvents: []string{
				"inspect-marketplace", "inspect-plugin", "create:removing-plugin",
				"update:unregistering-marketplace", "unregister-marketplace",
				"update:committing", "remove-manifest", "delete-journal",
			},
			wantTargetMissing: true, wantUnregister: true,
		},
		{
			name: "Plugin only removes Plugin",
			configure: func(fixture *marketplacePluginFixture) {
				fixture.adapter.pluginFacts[fixture.request.Target.ActualName] = adapter.PluginFact{
					Presence: adapter.PluginPresent, ActualName: fixture.request.Target.ActualName, Selector: fixture.request.Target.Selector,
				}
				fixture.adapter.removeAnswers = []pluginMutationAnswer{applied}
			},
			wantEvents: []string{
				"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin",
				"update:unregistering-marketplace",
				"update:committing", "remove-manifest", "delete-journal",
			},
			wantTargetMissing: true, wantRemovePlugin: true,
		},
		{
			name: "both absent removes only manifest",
			wantEvents: []string{
				"inspect-marketplace", "inspect-plugin", "remove-manifest",
			},
			wantTargetMissing: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplacePluginFixture(t)
			if test.configure != nil {
				test.configure(fixture)
			}
			result, err := fixture.service.UninstallPlugin(context.Background(), marketplacePluginUninstallRequest(fixture))
			if err != nil {
				t.Fatalf("UninstallPlugin() error = %v", err)
			}
			if result.TargetMissing != test.wantTargetMissing {
				t.Fatalf("TargetMissing = %t, want %t", result.TargetMissing, test.wantTargetMissing)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvents) {
				t.Fatalf("events = %v, want %v", fixture.events, test.wantEvents)
			}
			if fixture.journals.found {
				t.Fatal("journal remains after successful uninstall")
			}
			if fixture.manifests.removed != (domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin}) {
				t.Fatalf("manifest removed = %+v, want demo Plugin", fixture.manifests.removed)
			}
			if len(fixture.adapter.registered) != 0 {
				t.Fatalf("uninstall registered a marketplace: %+v", fixture.adapter.registered)
			}
			var wantRemoved []adapter.PluginTarget
			if test.wantRemovePlugin {
				wantRemoved = []adapter.PluginTarget{fixture.request.Target}
			}
			if !reflect.DeepEqual(fixture.adapter.removed, wantRemoved) {
				t.Fatalf("removed = %+v, want %+v", fixture.adapter.removed, wantRemoved)
			}
			var wantUnregistered []adapter.MarketplaceTarget
			if test.wantUnregister {
				wantUnregistered = []adapter.MarketplaceTarget{fixture.request.Marketplace.Target}
			}
			if !reflect.DeepEqual(fixture.adapter.unregistered, wantUnregistered) {
				t.Fatalf("unregistered = %+v, want %+v", fixture.adapter.unregistered, wantUnregistered)
			}
		})
	}
}

func TestMarketplacePluginUninstallRejectsWrongSourceOrSelector(t *testing.T) {
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
	}{
		{
			name:            "marketplace source conflict",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Source: "/other/cache"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent},
		},
		{
			name:            "Plugin selector conflict",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, Selector: "other"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplacePluginFixture(t)
			test.marketplaceFact.Name = fixture.request.Marketplace.Target.Name
			if test.marketplaceFact.Presence == adapter.MarketplacePresent && test.marketplaceFact.Source == "" {
				test.marketplaceFact.Source = fixture.request.Marketplace.Target.Source
			}
			test.pluginFact.ActualName = fixture.request.Target.ActualName
			if test.pluginFact.Presence == adapter.PluginPresent && test.pluginFact.Selector == "" {
				test.pluginFact.Selector = fixture.request.Target.Selector
			}
			fixture.adapter.marketplaceFact = test.marketplaceFact
			fixture.adapter.pluginFacts[fixture.request.Target.ActualName] = test.pluginFact

			result, err := fixture.service.UninstallPlugin(context.Background(), marketplacePluginUninstallRequest(fixture))
			if !errors.Is(err, ErrIndeterminateState) || result.TargetMissing {
				t.Fatalf("UninstallPlugin() = %+v, %v; want indeterminate", result, err)
			}
			if fixture.journals.found || fixture.manifests.removed != (domain.PowerRef{}) ||
				len(fixture.adapter.removed) != 0 || len(fixture.adapter.unregistered) != 0 {
				t.Fatalf("conflicting state changed resources: journal=%v manifest=%+v removed=%+v unregistered=%+v",
					fixture.journals.found, fixture.manifests.removed, fixture.adapter.removed, fixture.adapter.unregistered)
			}
		})
	}
}

func TestMarketplacePluginUninstallMutationDirections(t *testing.T) {
	operationErr := errors.New("CLI write failed")
	applied := pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}
	tests := []struct {
		name              string
		removeAnswer      pluginMutationAnswer
		unregisterAnswer  pluginMutationAnswer
		wantFound         bool
		wantPhase         Phase
		wantIndeterminate bool
		wantEvents        []string
	}{
		{
			name:         "remove Plugin not applied",
			removeAnswer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: operationErr},
			wantFound:    true, wantPhase: PhaseRemovingPlugin,
			wantEvents: []string{"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin"},
		},
		{
			name:         "remove Plugin unknown",
			removeAnswer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationUnknown}, err: operationErr},
			wantFound:    true, wantPhase: PhaseRemovingPlugin, wantIndeterminate: true,
			wantEvents: []string{"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin"},
		},
		{
			name:         "remove Plugin applied with error",
			removeAnswer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, err: operationErr},
			wantFound:    true, wantPhase: PhaseUnregisteringMarketplace,
			wantEvents: []string{"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin", "update:unregistering-marketplace"},
		},
		{
			name:             "unregister marketplace not applied",
			unregisterAnswer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: operationErr},
			wantFound:        true, wantPhase: PhaseUnregisteringMarketplace,
			wantEvents: []string{"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin", "update:unregistering-marketplace", "unregister-marketplace"},
		},
		{
			name:             "unregister marketplace unknown",
			unregisterAnswer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationUnknown}, err: operationErr},
			wantFound:        true, wantPhase: PhaseUnregisteringMarketplace, wantIndeterminate: true,
			wantEvents: []string{"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin", "update:unregistering-marketplace", "unregister-marketplace"},
		},
		{
			name:             "unregister marketplace applied with error",
			unregisterAnswer: pluginMutationAnswer{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, err: operationErr},
			wantFound:        true, wantPhase: PhaseCommitting,
			wantEvents: []string{"inspect-marketplace", "inspect-plugin", "create:removing-plugin", "remove-plugin", "update:unregistering-marketplace", "unregister-marketplace", "update:committing"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMarketplacePluginFixture(t)
			fixture.adapter.marketplaceFact = adapter.MarketplaceFact{
				Presence: adapter.MarketplacePresent, Name: fixture.request.Marketplace.Target.Name, Source: fixture.request.Marketplace.Target.Source,
			}
			fixture.adapter.pluginFacts[fixture.request.Target.ActualName] = adapter.PluginFact{
				Presence: adapter.PluginPresent, ActualName: fixture.request.Target.ActualName, Selector: fixture.request.Target.Selector,
			}
			removeAnswer := test.removeAnswer
			if removeAnswer.result.State == "" {
				removeAnswer = applied
			}
			unregisterAnswer := test.unregisterAnswer
			if unregisterAnswer.result.State == "" {
				unregisterAnswer = applied
			}
			fixture.adapter.removeAnswers = []pluginMutationAnswer{removeAnswer}
			fixture.adapter.unregisterAnswers = []pluginMutationAnswer{unregisterAnswer}

			_, err := fixture.service.UninstallPlugin(context.Background(), marketplacePluginUninstallRequest(fixture))
			if !errors.Is(err, operationErr) || errors.Is(err, ErrIndeterminateState) != test.wantIndeterminate {
				t.Fatalf("UninstallPlugin() error = %v, want operation error and indeterminate=%v", err, test.wantIndeterminate)
			}
			if fixture.journals.found != test.wantFound || test.wantFound && fixture.journals.journal.Phase != test.wantPhase {
				t.Fatalf("journal = found %v phase %q, want %v/%q", fixture.journals.found, fixture.journals.journal.Phase, test.wantFound, test.wantPhase)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvents) {
				t.Fatalf("events = %v, want %v", fixture.events, test.wantEvents)
			}
			if fixture.manifests.removed != (domain.PowerRef{}) {
				t.Fatalf("failed uninstall changed manifest: %+v", fixture.manifests.removed)
			}
		})
	}
}
