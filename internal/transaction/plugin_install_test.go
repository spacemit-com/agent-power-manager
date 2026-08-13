package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

type pluginMutationAnswer struct {
	result adapter.PluginMutationResult
	err    error
}

type recordingPluginAdapter struct {
	events        *[]string
	mode          domain.PluginDeploymentMode
	facts         map[string]adapter.PluginFact
	inspectErr    error
	addAnswers    []pluginMutationAnswer
	removeAnswers []pluginMutationAnswer
	added         []adapter.PluginTarget
	removed       []adapter.PluginTarget
	targets       map[string]string
}

func (a *recordingPluginAdapter) DeploymentMode() domain.PluginDeploymentMode { return a.mode }
func (*recordingPluginAdapter) PrepareInstall(context.Context, domain.PluginDefinition) error {
	return nil
}
func (a *recordingPluginAdapter) Inspect(_ context.Context, name string) (adapter.PluginFact, error) {
	*a.events = append(*a.events, "adapter:inspect:"+name)
	return a.facts[name], a.inspectErr
}
func (a *recordingPluginAdapter) Add(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "adapter:add:"+target.ActualName)
	a.added = append(a.added, target)
	answer := a.addAnswers[0]
	a.addAnswers = a.addAnswers[1:]
	return answer.result, answer.err
}
func (a *recordingPluginAdapter) Remove(_ context.Context, target adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	*a.events = append(*a.events, "adapter:remove:"+target.ActualName)
	a.removed = append(a.removed, target)
	answer := a.removeAnswers[0]
	a.removeAnswers = a.removeAnswers[1:]
	return answer.result, answer.err
}
func (a *recordingPluginAdapter) TargetPath(name string) (string, error) {
	if target, ok := a.targets[name]; ok {
		return target, nil
	}
	return "", adapter.ErrUnsupportedAdapter
}

type pluginInstallFixture struct {
	events    []string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   *recordingPluginAdapter
	service   *Service
	request   PluginInstallRequest
}

func newPluginInstallFixture() *pluginInstallFixture {
	f := &pluginInstallFixture{}
	f.journals = &recordingJournalStore{events: &f.events, fail: map[string]error{}}
	f.manifests = &recordingManifestWriter{events: &f.events}
	f.adapter = &recordingPluginAdapter{
		events: &f.events, mode: domain.PluginDeploymentMarketplace,
		facts:      map[string]adapter.PluginFact{"demo-v2.0.0": {Presence: adapter.PluginAbsent, ActualName: "demo-v2.0.0"}},
		addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}},
	}
	f.service = newService(f.manifests, f.journals)
	f.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	f.request = PluginInstallRequest{
		Agent: domain.AgentCodex, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"},
		Definition: domain.PluginDefinition{ActualName: "demo-v2.0.0", Marketplace: domain.MarketplaceDefinition{Name: "apm", Source: "/marketplace"}},
		Adapter:    f.adapter,
	}
	return f
}

func TestPluginInstallMarketplaceFirstInstallOrder(t *testing.T) {
	f := newPluginInstallFixture()
	if err := f.service.InstallPlugin(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	want := []string{"adapter:inspect:demo-v2.0.0", "create:adding-new", "adapter:add:demo-v2.0.0", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
	if got := f.adapter.added[0].Selector; got != "demo-v2.0.0@apm" {
		t.Fatalf("selector = %q", got)
	}
}

func TestPluginInstallMarketplaceUpdateRemovesExactPreviousSelector(t *testing.T) {
	f := newPluginInstallFixture()
	previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	f.request.Previous = &previous
	f.adapter.facts["demo-v1.0.0"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@custom"}
	f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
	if err := f.service.InstallPlugin(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	want := []string{"adapter:inspect:demo-v2.0.0", "adapter:inspect:demo-v1.0.0", "create:removing-old", "adapter:remove:demo-v1.0.0", "update:adding-new", "adapter:add:demo-v2.0.0", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
	if f.adapter.removed[0].Selector != "demo-v1.0.0@custom" {
		t.Fatalf("removed = %#v", f.adapter.removed[0])
	}
}

func TestPluginInstallSameVersionAbsentRepairs(t *testing.T) {
	f := newPluginInstallFixture()
	previous := f.request.Power
	f.request.Previous = &previous
	if err := f.service.InstallPlugin(context.Background(), f.request); err != nil {
		t.Fatalf("same-version repair error = %v", err)
	}
	if len(f.adapter.added) != 1 {
		t.Fatalf("add calls = %d", len(f.adapter.added))
	}
}

func TestPluginInstallMarketplaceRejectsUnmanagedTarget(t *testing.T) {
	f := newPluginInstallFixture()
	f.adapter.facts["demo-v2.0.0"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0@other"}
	if err := f.service.InstallPlugin(context.Background(), f.request); !errors.Is(err, adapter.ErrPluginConflict) {
		t.Fatalf("error = %v", err)
	}
	if f.journals.found {
		t.Fatal("conflict created journal")
	}
}

func TestPluginInstallFilesystemPublishesThenEnables(t *testing.T) {
	f := newPluginInstallFixture()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(staging, "plugins", "demo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.txt"), []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "plugins", "demo-v2.0.0")
	f.adapter.mode = domain.PluginDeploymentFilesystem
	f.adapter.targets = map[string]string{"demo-v2.0.0": target}
	f.request.StagingRoot = staging
	if err := f.service.InstallPlugin(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(target, "plugin.txt"))
	if err != nil || string(contents) != "plugin" {
		t.Fatalf("published contents = %q, %v", contents, err)
	}
	want := []string{"adapter:inspect:demo-v2.0.0", "create:adding-new", "adapter:add:demo-v2.0.0", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestPluginInstallFilesystemUpdateQuarantinesPrevious(t *testing.T) {
	f := newPluginInstallFixture()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, _ := paths.StagingPath()
	source := filepath.Join(staging, "plugins", "demo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginsRoot := filepath.Join(t.TempDir(), "plugins")
	oldTarget := filepath.Join(pluginsRoot, "demo-v1.0.0")
	newTarget := filepath.Join(pluginsRoot, "demo-v2.0.0")
	if err := os.MkdirAll(oldTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	f.request.Previous, f.request.StagingRoot = &previous, staging
	f.adapter.mode = domain.PluginDeploymentFilesystem
	f.adapter.targets = map[string]string{"demo-v1.0.0": oldTarget, "demo-v2.0.0": newTarget}
	f.adapter.facts["demo-v1.0.0"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0"}
	f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
	if err := f.service.InstallPlugin(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old target remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newTarget, "new.txt")); err != nil {
		t.Fatalf("new target missing: %v", err)
	}
	want := []string{"adapter:inspect:demo-v2.0.0", "adapter:inspect:demo-v1.0.0", "create:removing-old", "adapter:remove:demo-v1.0.0", "update:adding-new", "adapter:add:demo-v2.0.0", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
}

func TestPluginInstallFilesystemEnableNotAppliedRollsBackPublishedTarget(t *testing.T) {
	f := newPluginInstallFixture()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, _ := paths.StagingPath()
	source := filepath.Join(staging, "plugins", "demo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "plugins", "demo-v2.0.0")
	f.adapter.mode = domain.PluginDeploymentFilesystem
	f.adapter.targets = map[string]string{"demo-v2.0.0": target}
	f.adapter.addAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: errors.New("enable failed")}}
	f.request.StagingRoot = staging
	if err := f.service.InstallPlugin(context.Background(), f.request); err == nil {
		t.Fatal("InstallPlugin() error = nil")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back target remains: %v", err)
	}
	if f.journals.found {
		t.Fatal("rolled-back journal remains")
	}
}
