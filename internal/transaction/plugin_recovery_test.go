package transaction

import (
	"context"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type recordingPluginResolver struct {
	events *[]string
	plugin adapter.PluginAdapter
}

func (*recordingPluginResolver) ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error) {
	return nil, nil
}
func (r *recordingPluginResolver) ResolvePlugin(agent domain.AgentID) (adapter.PluginAdapter, error) {
	*r.events = append(*r.events, "resolver:"+string(agent))
	return r.plugin, nil
}

func TestPluginRecoveryMarketplaceAddingNewPresentCommits(t *testing.T) {
	events := []string{}
	journal := validPluginMarketplaceJournal()
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	manifests := &recordingManifestWriter{events: &events}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentMarketplace, facts: map[string]adapter.PluginFact{
		"demo-v1.0.0": {Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm"},
	}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(manifests, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryCommit || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:codex", "adapter:inspect:demo-v1.0.0", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestPluginRecoveryMarketplaceAddingNewAbsentRollsBack(t *testing.T) {
	events := []string{}
	journal := validPluginMarketplaceJournal()
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentMarketplace, facts: map[string]adapter.PluginFact{
		"demo-v1.0.0": {Presence: adapter.PluginAbsent, ActualName: "demo-v1.0.0"},
	}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(&recordingManifestWriter{events: &events}, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:codex", "adapter:inspect:demo-v1.0.0", "update:rolling-back", "delete:journal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestPluginRecoveryMarketplaceUninstallAbsentCommits(t *testing.T) {
	events := []string{}
	journal := validPluginMarketplaceJournal()
	journal.Operation, journal.Phase = OperationUninstall, PhaseRemoving
	journal.Plugin.TargetPresent = true
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentMarketplace, facts: map[string]adapter.PluginFact{
		"demo-v1.0.0": {Presence: adapter.PluginAbsent, ActualName: "demo-v1.0.0"},
	}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(&recordingManifestWriter{events: &events}, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryCommit || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:codex", "adapter:inspect:demo-v1.0.0", "update:committing", "manifest:remove", "delete:journal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
