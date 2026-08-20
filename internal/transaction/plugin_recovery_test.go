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
	target := filepath.Join(pluginsRoot, "demo")
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
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem, Target: adapter.PluginTarget{ActualName: "demo", Selector: "demo"}, Filesystem: &PluginFilesystemJournalPayload{
			StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", "demo"), TargetPath: target,
			TransactionDir: tx, PublishPath: filepath.Join(tx, "publish"),
		}},
	}
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentFilesystem, facts: map[string]adapter.PluginFact{
		"demo": {Presence: adapter.PluginAbsent, ActualName: "demo"},
	}, addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(&recordingManifestWriter{events: &events}, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryCommit || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:hermes-agent", "adapter:inspect:demo", "adapter:add:demo", "update:committing", "manifest:set", "delete:journal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestPluginRecoveryFilesystemAddingNewRejectsTargetAndPublishCoexistence(t *testing.T) {
	fixture := newSamePathPluginRecoveryFixture(t)
	fixture.store.journal.Phase = PhaseAddingNew
	publish := fixture.store.journal.Plugin.Filesystem.PublishPath
	if err := os.MkdirAll(publish, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publish, "version.txt"), []byte("candidate"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.Recover(context.Background())
	if !errors.Is(err, ErrRecoveryFailed) || result.Recovered {
		t.Fatalf("Recover() = %+v, %v; want fail-closed recovery", result, err)
	}
	if !fixture.store.found || fixture.store.journal.Phase != PhaseAddingNew {
		t.Fatalf("ambiguous recovery journal = found %t phase %q; want retained adding-new", fixture.store.found, fixture.store.journal.Phase)
	}
	for path, want := range map[string]string{
		filepath.Join(fixture.target, "version.txt"):                                                 "new",
		filepath.Join(fixture.store.journal.Plugin.Filesystem.PreviousQuarantinePath, "version.txt"): "old",
		filepath.Join(publish, "version.txt"):                                                        "candidate",
	} {
		contents, readErr := os.ReadFile(path)
		if readErr != nil || string(contents) != want {
			t.Fatalf("retained %q = %q, %v; want %q", path, contents, readErr, want)
		}
	}
	if len(fixture.plugin.added) != 0 || len(fixture.plugin.removed) != 0 {
		t.Fatalf("ambiguous recovery mutated adapter: added=%+v removed=%+v", fixture.plugin.added, fixture.plugin.removed)
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
	target := filepath.Join(pluginsRoot, "demo")
	tx := filepath.Join(pluginsRoot, ".apm-transactions", id)
	if err := os.MkdirAll(tx, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall, Phase: PhaseCommitting,
		Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem, Target: adapter.PluginTarget{ActualName: "demo", Selector: "demo"}, Filesystem: &PluginFilesystemJournalPayload{
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
	previousPath := filepath.Join(pluginsRoot, "demo")
	targetPath := filepath.Join(pluginsRoot, "demo")
	tx := filepath.Join(pluginsRoot, ".apm-transactions", id)
	if err := os.MkdirAll(previousPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tx, 0o700); err != nil {
		t.Fatal(err)
	}
	previousRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	previousTarget := adapter.PluginTarget{ActualName: "demo", Selector: "demo"}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall, Phase: PhaseRemovingOld,
		Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}, Previous: &previousRef,
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem,
			Target: adapter.PluginTarget{ActualName: "demo", Selector: "demo"}, Previous: &previousTarget, PreviousPresent: true,
			Filesystem: &PluginFilesystemJournalPayload{
				StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", "demo"), TargetPath: targetPath,
				PreviousTargetPath: previousPath, TransactionDir: tx, PublishPath: filepath.Join(tx, "publish"),
				PreviousQuarantinePath: filepath.Join(tx, "previous"),
			}},
	}
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentFilesystem, facts: map[string]adapter.PluginFact{
		"demo": {Presence: adapter.PluginAbsent, ActualName: "demo"},
	}, addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(&recordingManifestWriter{events: &events}, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:hermes-agent", "adapter:inspect:demo", "adapter:add:demo", "delete:journal"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestPluginRecoveryFilesystemRemovingOldPhaseWriteFailureRetryPreservesRestoredTarget(t *testing.T) {
	fixture := newSamePathPluginRecoveryFixture(t)
	filesystem := fixture.store.journal.Plugin.Filesystem
	if err := os.RemoveAll(fixture.target); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filesystem.PreviousQuarantinePath, fixture.target); err != nil {
		t.Fatal(err)
	}
	fixture.store.journal.Phase = PhaseRemovingOld
	phaseErr := errors.New("persist removing-old failed")
	fixture.store.onUpdate = func(journal Journal) {
		if journal.Phase == PhaseRollingBack {
			fixture.store.fail["update:"+string(PhaseRemovingOld)] = phaseErr
		}
	}
	activationErr := errors.New("restore activation failed")
	fixture.plugin.addAnswers = []pluginMutationAnswer{
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: activationErr},
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
	}

	if result, err := fixture.service.Recover(context.Background()); !errors.Is(err, ErrRecoveryFailed) || !errors.Is(err, activationErr) || errors.Is(err, phaseErr) || result.Recovered {
		t.Fatalf("first Recover() = %+v, %v; want activation failure without phase-write failure", result, err)
	}
	assertSamePathPreviousContents(t, fixture.target)
	if !fixture.store.found || fixture.store.journal.Phase != PhaseRemovingOld {
		t.Fatalf("failed recovery journal = found %t phase %q; want retained removing-old", fixture.store.found, fixture.store.journal.Phase)
	}
	delete(fixture.store.fail, "update:"+string(PhaseRemovingOld))
	fixture.store.onUpdate = nil

	result, err := fixture.service.Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
		t.Fatalf("second Recover() = %+v, %v; want converged rollback", result, err)
	}
	assertSamePathPreviousContents(t, fixture.target)
	if fixture.store.found {
		t.Fatal("converged retry retained journal")
	}
}

func TestPluginRecoveryFilesystemResumesAfterRestoredPreviousActivationFailure(t *testing.T) {
	fixture := newSamePathPluginRecoveryFixture(t)
	activationErr := errors.New("restore activation failed")
	fixture.plugin.addAnswers = []pluginMutationAnswer{
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationNotApplied}, err: activationErr},
		{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
	}

	if result, err := fixture.service.Recover(context.Background()); !errors.Is(err, ErrRecoveryFailed) || result.Recovered {
		t.Fatalf("first Recover() = %+v, %v; want retained recovery failure", result, err)
	}
	assertSamePathPreviousContents(t, fixture.target)
	if !fixture.store.found {
		t.Fatal("failed restore activation deleted journal")
	}

	result, err := fixture.service.Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
		t.Fatalf("second Recover() = %+v, %v; want converged rollback", result, err)
	}
	assertSamePathPreviousContents(t, fixture.target)
	if fixture.store.found {
		t.Fatal("converged rollback retained journal")
	}
}

func TestPluginRecoveryFilesystemResumesAfterRestoreRenameCompletesThenReturnsError(t *testing.T) {
	fixture := newSamePathPluginRecoveryFixture(t)
	restoreErr := errors.New("interrupted after restore")
	restored := false
	fixture.service.files.rename = func(oldPath, newPath string) error {
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		restored = true
		return restoreErr
	}

	if result, err := fixture.service.Recover(context.Background()); !errors.Is(err, ErrRecoveryFailed) || result.Recovered {
		t.Fatalf("first Recover() = %+v, %v; want retained recovery failure", result, err)
	}
	if !restored {
		t.Fatal("first recovery did not reach previous restore")
	}
	assertSamePathPreviousContents(t, fixture.target)
	fixture.service.files.rename = os.Rename

	result, err := fixture.service.Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryRollback {
		t.Fatalf("second Recover() = %+v, %v; want converged rollback", result, err)
	}
	assertSamePathPreviousContents(t, fixture.target)
	if fixture.store.found {
		t.Fatal("converged rollback retained journal")
	}
}

type samePathPluginRecoveryFixture struct {
	service *Service
	store   *recordingJournalStore
	plugin  *recordingPluginAdapter
	target  string
}

func newSamePathPluginRecoveryFixture(t *testing.T) *samePathPluginRecoveryFixture {
	t.Helper()
	events := []string{}
	t.Setenv("APM_HOME", t.TempDir())
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "plugins", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := "00112233445566778899aabbccddeeff"
	pluginsRoot := filepath.Join(t.TempDir(), "plugins")
	target := filepath.Join(pluginsRoot, "demo")
	tx := filepath.Join(pluginsRoot, ".apm-transactions", id)
	quarantine := filepath.Join(tx, "previous")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(quarantine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quarantine, "version.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	previousRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	previousTarget := adapter.PluginTarget{ActualName: "demo", Selector: "demo"}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall, Phase: PhaseRollingBack,
		Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}, Previous: &previousRef,
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem,
			Target: adapter.PluginTarget{ActualName: "demo", Selector: "demo"}, Previous: &previousTarget, PreviousPresent: true,
			Filesystem: &PluginFilesystemJournalPayload{
				StagingRoot: staging, SourcePath: filepath.Join(staging, "plugins", "demo"), TargetPath: target,
				PreviousTargetPath: target, TransactionDir: tx, PublishPath: filepath.Join(tx, "publish"), PreviousQuarantinePath: quarantine,
			}},
	}
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{
		events: &events, mode: domain.PluginDeploymentFilesystem,
		facts: map[string]adapter.PluginFact{"demo": {Presence: adapter.PluginAbsent, ActualName: "demo"}},
		removeAnswers: []pluginMutationAnswer{
			{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
			{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}},
		},
		addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}},
	}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	return &samePathPluginRecoveryFixture{
		service: newService(&recordingManifestWriter{events: &events}, store, resolver),
		store:   store,
		plugin:  plugin,
		target:  target,
	}
}

func assertSamePathPreviousContents(t *testing.T, target string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(target, "version.txt"))
	if err != nil || string(contents) != "old" {
		t.Fatalf("previous target contents = %q, %v; want preserved old version", contents, err)
	}
}

func TestPluginRecoveryFilesystemUninstallReenablesUnmovedTarget(t *testing.T) {
	events := []string{}
	id := "00112233445566778899aabbccddeeff"
	pluginsRoot := filepath.Join(t.TempDir(), "plugins")
	target := filepath.Join(pluginsRoot, "demo")
	tx := filepath.Join(pluginsRoot, ".apm-transactions", id)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tx, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationUninstall, Phase: PhaseRemoving,
		Agent: domain.AgentHermes, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentFilesystem, TargetPresent: true,
			Target:     adapter.PluginTarget{ActualName: "demo", Selector: "demo"},
			Filesystem: &PluginFilesystemJournalPayload{TargetPath: target, TransactionDir: tx, TargetQuarantinePath: filepath.Join(tx, "target")}},
	}
	store := &recordingJournalStore{journal: journal, found: true, events: &events, fail: map[string]error{}}
	plugin := &recordingPluginAdapter{events: &events, mode: domain.PluginDeploymentFilesystem, facts: map[string]adapter.PluginFact{
		"demo": {Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
	}, addAnswers: []pluginMutationAnswer{{result: adapter.PluginMutationResult{State: adapter.PluginMutationApplied}}}}
	resolver := &recordingPluginResolver{events: &events, plugin: plugin}
	service := newService(&recordingManifestWriter{events: &events}, store, resolver)
	result, err := service.Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback || !result.Recovered {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	want := []string{"resolver:hermes-agent", "adapter:inspect:demo", "update:rolling-back", "adapter:add:demo", "delete:journal"}
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
