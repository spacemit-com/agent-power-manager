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
	target := filepath.Join(t.TempDir(), "plugins", "demo")
	f.adapter.mode = domain.PluginDeploymentFilesystem
	f.adapter.targets = map[string]string{"demo": target}
	f.request.StagingRoot = staging
	if err := f.service.InstallPlugin(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(target, "plugin.txt"))
	if err != nil || string(contents) != "plugin" {
		t.Fatalf("published contents = %q, %v", contents, err)
	}
	want := []string{"adapter:inspect:demo", "create:adding-new", "adapter:add:demo", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestFilesystemPluginVersionSwitchQuarantinesSameLogicalTargetBeforePublish(t *testing.T) {
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
	target := filepath.Join(pluginsRoot, "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	f.request.Previous, f.request.StagingRoot = &previous, staging
	f.adapter.mode = domain.PluginDeploymentFilesystem
	f.adapter.targets = map[string]string{"demo": target}
	f.adapter.facts["demo"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"}
	f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
	if err := f.service.InstallPlugin(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old contents remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "new.txt")); err != nil {
		t.Fatalf("new target missing: %v", err)
	}
	want := []string{"adapter:inspect:demo", "create:removing-old", "adapter:remove:demo", "update:adding-new", "adapter:add:demo", "update:committing", "manifest:set", "delete:journal"}
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
	target := filepath.Join(t.TempDir(), "plugins", "demo")
	f.adapter.mode = domain.PluginDeploymentFilesystem
	f.adapter.targets = map[string]string{"demo": target}
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

func TestFilesystemPluginVersionSwitchRollbackRestoresSameLogicalTarget(t *testing.T) {
	f := newPluginInstallFixture()
	t.Setenv("APM_HOME", t.TempDir())
	staging, _ := paths.StagingPath()
	source := filepath.Join(staging, "plugins", "demo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "version.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "plugins", "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	f.request.Previous, f.request.StagingRoot = &previous, staging
	f.adapter.targets = map[string]string{"demo": target}
	f.adapter.facts["demo"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"}
	f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
	f.adapter.addAnswers = []pluginMutationAnswer{
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: errors.New("enable failed")},
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
	}

	if err := f.service.InstallPlugin(context.Background(), f.request); err == nil {
		t.Fatal("InstallPlugin() error = nil")
	}
	contents, err := os.ReadFile(filepath.Join(target, "version.txt"))
	if err != nil || string(contents) != "old" {
		t.Fatalf("rolled-back target = %q, %v; want old contents", contents, err)
	}
	if f.journals.found {
		t.Fatal("rolled-back journal remains")
	}
	want := []string{
		"adapter:inspect:demo", "create:removing-old", "adapter:remove:demo", "update:adding-new",
		"adapter:add:demo", "update:rolling-back", "adapter:add:demo", "delete:journal",
	}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
}
