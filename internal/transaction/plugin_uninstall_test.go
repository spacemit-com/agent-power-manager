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
)

func TestPluginUninstallFilesystemDisablesAndQuarantines(t *testing.T) {
	f := newPluginInstallFixture()
	target := filepath.Join(t.TempDir(), "plugins", "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	f.adapter.mode = domain.PluginDeploymentFilesystem
	f.adapter.targets = map[string]string{"demo": target}
	f.adapter.facts["demo"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"}
	f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
	request := PluginUninstallRequest{Agent: domain.AgentHermes, Power: f.request.Power, Target: adapter.PluginTarget{ActualName: "demo", Selector: "demo"}, Adapter: f.adapter}
	result, err := f.service.UninstallPlugin(context.Background(), request)
	if err != nil || result.TargetMissing {
		t.Fatalf("UninstallPlugin() = %+v, %v", result, err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target remains: %v", err)
	}
	want := []string{"adapter:inspect:demo", "create:removing", "adapter:remove:demo", "update:committing", "manifest:remove", "delete:journal"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
}

func TestPluginUninstallFilesystemRejectsNonPhysicalManagedTarget(t *testing.T) {
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
			f.adapter.targets = map[string]string{"demo": target}
			f.adapter.facts["demo"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"}
			f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
			request := PluginUninstallRequest{
				Agent: domain.AgentHermes, Power: f.request.Power,
				Target: adapter.PluginTarget{ActualName: "demo", Selector: "demo"}, Adapter: f.adapter,
			}

			result, err := f.service.UninstallPlugin(context.Background(), request)
			if !errors.Is(err, ErrUnsafePath) || result.TargetMissing {
				t.Fatalf("UninstallPlugin() = %+v, %v; want ErrUnsafePath", result, err)
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
