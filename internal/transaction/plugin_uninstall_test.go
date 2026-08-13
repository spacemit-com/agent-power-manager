package transaction

import (
	"context"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

func TestPluginUninstallMarketplaceUsesInspectedSelector(t *testing.T) {
	f := newPluginInstallFixture()
	f.adapter.facts["demo-v2.0.0"] = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0@custom"}
	f.adapter.removeAnswers = []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}
	request := PluginUninstallRequest{Agent: domain.AgentCodex, Power: f.request.Power, ActualName: "demo-v2.0.0", Adapter: f.adapter}
	result, err := f.service.UninstallPlugin(context.Background(), request)
	if err != nil || result.TargetMissing {
		t.Fatalf("UninstallPlugin() = %+v, %v", result, err)
	}
	want := []string{"adapter:inspect:demo-v2.0.0", "create:removing", "adapter:remove:demo-v2.0.0", "update:committing", "manifest:remove", "delete:journal"}
	if !reflect.DeepEqual(f.events, want) || f.adapter.removed[0].Selector != "demo-v2.0.0@custom" {
		t.Fatalf("events/removed = %v/%#v", f.events, f.adapter.removed)
	}
}

func TestPluginUninstallMarketplaceMissingRemovesOnlyManifest(t *testing.T) {
	f := newPluginInstallFixture()
	request := PluginUninstallRequest{Agent: domain.AgentCodex, Power: f.request.Power, ActualName: "demo-v2.0.0", Adapter: f.adapter}
	result, err := f.service.UninstallPlugin(context.Background(), request)
	if err != nil || !result.TargetMissing {
		t.Fatalf("UninstallPlugin() = %+v, %v", result, err)
	}
	want := []string{"adapter:inspect:demo-v2.0.0", "manifest:remove"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v", f.events)
	}
}
