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

type recordingPluginResolver struct {
	events *[]string
	plugin adapter.PluginAdapter
}

func TestPluginRecoveryFilesystemPublishedTargetEnablesAndCommits(t *testing.T) {
	events := []string{}
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, _ := paths.StagingPath()
	if err := os.MkdirAll(filepath.Join(staging, "plugins", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	pluginsRoot := filepath.Join(t.TempDir(), "plugins")
	target := filepath.Join(pluginsRoot, "demo-v1.0.0")
	tx := filepath.Join(pluginsRoot, ".apm-transactions", id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tx, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall, Phase: PhaseAddingNew,
		Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem, Target: adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0"}, Filesystem: &PluginFilesystemJournalPayload{
			StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", "demo"), TargetPath: target,
			TransactionDir: tx, PublishPath: filepath.Join(tx, "publish"),
		}},
	}
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentFilesystem, facts: map[string]adapter.PluginFact{
		"demo-v1.0.0": {Presence: adapter.PluginAbsent, ActualName: "demo-v1.0.0"},
	}, addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(&recordingManifestWriter{events: &events}, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryCommit || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:hermes-agent", "adapter:inspect:demo-v1.0.0", "adapter:add:demo-v1.0.0", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestPluginRecoveryFilesystemCommittingRejectsMissingInstallTarget(t *testing.T) {
	events := []string{}
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, _ := paths.StagingPath()
	if err := os.MkdirAll(filepath.Join(staging, "plugins", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	pluginsRoot := filepath.Join(t.TempDir(), "plugins")
	target := filepath.Join(pluginsRoot, "demo-v1.0.0")
	tx := filepath.Join(pluginsRoot, ".apm-transactions", id)
	if err := os.MkdirAll(tx, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall, Phase: PhaseCommitting,
		Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem, Target: adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0"}, Filesystem: &PluginFilesystemJournalPayload{
			StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", "demo"), TargetPath: target,
			TransactionDir: tx, PublishPath: filepath.Join(tx, "publish"),
		}},
	}
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	service := newService(&recordingManifestWriter{events: &events}, store)
	result, err := service.Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || result.Recovered || !store.found {
		t.Fatalf("Recover() = %+v, %v; journal found=%t", result, err, store.found)
	}
	if len(events) != 0 {
		t.Fatalf("missing target recovery changed state: %v", events)
	}
}

func TestPluginRecoveryFilesystemRemovingOldReenablesUnmovedPreviousTarget(t *testing.T) {
	events := []string{}
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, _ := paths.StagingPath()
	if err := os.MkdirAll(filepath.Join(staging, "plugins", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	pluginsRoot := filepath.Join(t.TempDir(), "plugins")
	previousPath := filepath.Join(pluginsRoot, "demo-v1.0.0")
	targetPath := filepath.Join(pluginsRoot, "demo-v2.0.0")
	tx := filepath.Join(pluginsRoot, ".apm-transactions", id)
	if err := os.MkdirAll(previousPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tx, 0o700); err != nil {
		t.Fatal(err)
	}
	previousRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	previousTarget := adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0"}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall, Phase: PhaseRemovingOld,
		Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}, Previous: &previousRef,
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem,
			Target: adapter.PluginTarget{ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0"}, Previous: &previousTarget, PreviousPresent: true,
			Filesystem: &PluginFilesystemJournalPayload{
				StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", "demo"), TargetPath: targetPath,
				PreviousTargetPath: previousPath, TransactionDir: tx, PublishPath: filepath.Join(tx, "publish"),
				PreviousQuarantinePath: filepath.Join(tx, "previous"),
			}},
	}
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentFilesystem, facts: map[string]adapter.PluginFact{
		"demo-v2.0.0": {Presence: adapter.PluginAbsent, ActualName: "demo-v2.0.0"},
	}, addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(&recordingManifestWriter{events: &events}, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:hermes-agent", "adapter:inspect:demo-v2.0.0", "update:rolling-back", "adapter:add:demo-v1.0.0", "delete:journal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
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
