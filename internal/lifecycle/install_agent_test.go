package lifecycle

import (
	"context"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
)

func TestInstallPluginDispatchesThroughPluginAdapterOnly(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs:  []domain.PowerRef{ref},
		selected:   ref.Version,
		pluginFact: adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
		pluginMode: domain.PluginDeploymentMarketplace,
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	assertInstallItems(t, result, []cli.InstallItem{{Agent: domain.AgentCodex, Power: ref, Outcome: cli.InstallOutcomeInstalled}})
	if containsEvent(calls, "resolve:codex") || containsEvent(calls, "transaction:install:codex") {
		t.Fatalf("calls = %v, want no Skill adapter or filesystem transaction", calls)
	}
}
