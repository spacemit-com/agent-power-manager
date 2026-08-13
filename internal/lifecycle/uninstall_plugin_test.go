package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
)

func TestUninstallPluginUsesLocalActualNameWithoutCloud(t *testing.T) {
	var calls []string
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups:     map[domain.AgentID]domain.PowerGroups{domain.AgentCodex: {domain.PowerTypePlugin: {ref}}},
		pluginFact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm"},
	})
	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: string(domain.AgentCodex), Type: string(domain.PowerTypePlugin), Name: "demo"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeCompleted {
		t.Fatalf("Uninstall() = %+v, %v", result, err)
	}
	want := []string{"acquire", "recover", "refresh", "config:codex", "resolve-plugin:codex", "plugin:inspect:codex", "transaction:uninstall-plugin:codex", "release"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
