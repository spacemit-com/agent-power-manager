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
