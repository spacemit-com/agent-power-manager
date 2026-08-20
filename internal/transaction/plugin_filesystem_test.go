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

func TestPluginInstallFilesystemPublicationRaceRecoversFailClosed(t *testing.T) {
	f := newPluginInstallFixture()
	t.Setenv("APM_HOME", t.TempDir())
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(staging, "plugins", "demo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.txt"), []byte("candidate"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "plugins", "demo")
	f.adapter.targets = map[string]string{"demo": target}
	f.request.StagingRoot = staging
	raceErr := errors.New("publication lost target race")
	f.service.files.rename = func(oldPath, newPath string) error {
		if oldPath != f.journals.journal.Plugin.Filesystem.PublishPath || newPath != target {
			t.Fatalf("rename(%q, %q), want publish to target", oldPath, newPath)
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "foreign.txt"), []byte("foreign"), 0o644); err != nil {
			t.Fatal(err)
		}
		return raceErr
	}

	if err := f.service.InstallPlugin(context.Background(), f.request); !errors.Is(err, raceErr) {
		t.Fatalf("InstallPlugin() error = %v, want publication race", err)
	}
	if !f.journals.found || f.journals.journal.Phase != PhaseAddingNew {
		t.Fatalf("race journal = found %t phase %q; want retained adding-new", f.journals.found, f.journals.journal.Phase)
	}
	publish := f.journals.journal.Plugin.Filesystem.PublishPath
	if _, err := os.Stat(filepath.Join(publish, "plugin.txt")); err != nil {
		t.Fatalf("candidate publish missing after race: %v", err)
	}

	resolver := &recordingPluginResolver{events: &f.events, plugin: f.adapter}
	f.service = newService(f.manifests, f.journals, resolver)
	result, err := f.service.Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v; want fail-closed recovery", result, err)
	}
	contents, readErr := os.ReadFile(filepath.Join(target, "foreign.txt"))
	if readErr != nil || string(contents) != "foreign" {
		t.Fatalf("foreign target = %q, %v; want preserved", contents, readErr)
	}
	if _, err := os.Stat(filepath.Join(publish, "plugin.txt")); err != nil {
		t.Fatalf("candidate publish changed by ambiguous recovery: %v", err)
	}
	if !f.journals.found || f.journals.journal.Phase != PhaseAddingNew {
		t.Fatalf("recovery journal = found %t phase %q; want retained adding-new", f.journals.found, f.journals.journal.Phase)
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

func TestFilesystemPluginVersionSwitchRejectsNonPhysicalManagedTarget(t *testing.T) {
	for _, test := range []struct {
		name   string
		create func(*testing.T, string)
	}{
		{
			name: "symlink",
			create: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Symlink(t.TempDir(), target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "regular file",
			create: func(t *testing.T, target string) {
				t.Helper()
				if err := os.WriteFile(target, []byte("foreign"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newPluginInstallFixture()
			t.Setenv("APM_HOME", t.TempDir())
			staging, err := paths.StagingPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(staging, "plugins", "demo"), 0o755); err != nil {
				t.Fatal(err)
			}
			pluginsRoot := filepath.Join(t.TempDir(), "plugins")
			if err := os.MkdirAll(pluginsRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(pluginsRoot, "demo")
			test.create(t, target)
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
			f.request.Previous, f.request.StagingRoot = &previous, staging
			f.adapter.targets = map[string]string{"demo": target}
			f.adapter.facts["demo"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"}
			f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}

			if err := f.service.InstallPlugin(context.Background(), f.request); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("InstallPlugin() error = %v, want ErrUnsafePath", err)
			}
			after, err := os.Lstat(target)
			if err != nil || after.Mode() != before.Mode() {
				t.Fatalf("managed target changed: before=%v after=%v err=%v", before.Mode(), after, err)
			}
			if f.journals.found {
				t.Fatal("unsafe managed target created journal")
			}
			if want := []string{"adapter:inspect:demo"}; !reflect.DeepEqual(f.events, want) {
				t.Fatalf("events = %v, want %v", f.events, want)
			}
		})
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
		"adapter:add:demo", "update:rolling-back", "update:removing-old", "adapter:add:demo", "delete:journal",
	}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
}

func TestFilesystemPluginVersionSwitchFixResumesAfterRollbackRestoreActivationFailure(t *testing.T) {
	f := newPluginInstallFixture()
	t.Setenv("APM_HOME", t.TempDir())
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
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
	enableErr := errors.New("enable new failed")
	restoreErr := errors.New("restore old activation failed")
	f.adapter.removeAnswers = []pluginMutationAnswer{
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
	}
	f.adapter.addAnswers = []pluginMutationAnswer{
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: enableErr},
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: restoreErr},
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
	}

	if err := f.service.InstallPlugin(context.Background(), f.request); !errors.Is(err, enableErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("InstallPlugin() error = %v, want enable and restore failures", err)
	}
	contents, readErr := os.ReadFile(filepath.Join(target, "version.txt"))
	if readErr != nil || string(contents) != "old" {
		t.Fatalf("restored target = %q, %v; want old contents", contents, readErr)
	}
	if !f.journals.found {
		t.Fatal("failed rollback activation deleted journal")
	}

	resolver := &recordingPluginResolver{events: &f.events, plugin: f.adapter}
	f.service = newService(f.manifests, f.journals, resolver)
	result, err := f.service.Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v; want converged rollback", result, err)
	}
	contents, readErr = os.ReadFile(filepath.Join(target, "version.txt"))
	if readErr != nil || string(contents) != "old" {
		t.Fatalf("recovered target = %q, %v; want preserved old contents", contents, readErr)
	}
}
