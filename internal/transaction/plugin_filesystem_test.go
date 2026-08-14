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
