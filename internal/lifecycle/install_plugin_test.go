package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
)

func TestInstallPluginMarketplacePreparesBeforeInspectAndSkipsCache(t *testing.T) {
	var calls []string
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}
	service := newInstallService(t, &calls, installFixture{
		pluginDefinition: domain.PluginDefinition{ActualName: "demo-v2.0.0", Marketplace: domain.MarketplaceDefinition{Name: "apm", Source: "/marketplace"}},
		pluginFact:       adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo-v2.0.0"},
		pluginMode:       domain.PluginDeploymentMarketplace,
	})
	item, err := service.installForAgent(context.Background(), domain.AgentCodex, ref)
	if err != nil || item.Outcome != cli.InstallOutcomeInstalled {
		t.Fatalf("installForAgent() = %+v, %v", item, err)
	}
	want := []string{"resolve-plugin:codex", "config:find:codex", "cloud:get-plugin", "plugin:prepare:codex", "plugin:inspect:codex", "transaction:install-plugin:codex"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
