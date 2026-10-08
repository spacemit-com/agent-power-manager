package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

type recoveryAgentAdapter struct {
	calls []struct {
		ref  domain.PowerRef
		path string
	}
	err error
}

func (a *recoveryAgentAdapter) TargetPath(domain.PowerRef) (string, error) {
	return "", errors.New("unexpected TargetPath call during recovery")
}

func (a *recoveryAgentAdapter) ValidateArtifact(ref domain.PowerRef, path string) error {
	a.calls = append(a.calls, struct {
		ref  domain.PowerRef
		path string
	}{ref, path})
	return a.err
}

func (a *recoveryAgentAdapter) Install(domain.PowerRef, string) (adapter.AgentInstallResult, error) {
	return adapter.AgentInstallResult{}, errors.New("unexpected Install call during recovery")
}

type recoveryAgentResolver struct {
	adapter adapter.AgentAdapter
	err     error
	agents  []domain.AgentID
}

func (r *recoveryAgentResolver) ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error) {
	return nil, errors.New("unexpected MCP resolve during agent recovery")
}

func (r *recoveryAgentResolver) ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error) {
	return nil, errors.New("unexpected plugin resolve during agent recovery")
}

func (r *recoveryAgentResolver) ResolveAgent(agent domain.AgentID) (adapter.AgentAdapter, error) {
	r.agents = append(r.agents, agent)
	return r.adapter, r.err
}

type agentRecoveryFixture struct {
	t           *testing.T
	target      string
	staging     string
	journal     Journal
	journalPath string
	manifests   *recordingManifestWriter
	adapter     *recoveryAgentAdapter
	resolver    *recoveryAgentResolver
	events      []string
}

func newAgentRecoveryFixture(t *testing.T, operation Operation, phase Phase) *agentRecoveryFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	journalPath, err := paths.JournalPath()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "agents", "demo")
	id := "00112233445566778899aabbccddeeff"
	artifacts, err := artifactPaths(target, id)
	if err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1,
		TransactionID: id,
		Driver:        DriverFilesystem,
		Operation:     operation,
		Phase:         phase,
		Agent:         domain.AgentBibit,
		Power:         domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v2.0.0"},
		Filesystem: &FilesystemJournalPayload{
			TargetPath:     target,
			TransactionDir: artifacts.dir,
		},
	}
	if operation == OperationInstall {
		journal.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"})
		journal.Filesystem.StagingRoot = staging
		journal.Filesystem.BackupPath = artifacts.backup
	} else {
		journal.Filesystem.QuarantinePath = artifacts.quarantine
	}
	fixture := &agentRecoveryFixture{t: t, target: target, staging: staging, journalPath: journalPath, journal: journal}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.adapter = &recoveryAgentAdapter{}
	fixture.resolver = &recoveryAgentResolver{adapter: fixture.adapter}
	return fixture
}

func (f *agentRecoveryFixture) persist() {
	f.t.Helper()
	if err := newFilesystemJournalStore(f.journalPath).Create(f.journal); err != nil {
		f.t.Fatal(err)
	}
}

func (f *agentRecoveryFixture) freshService() *Service {
	f.t.Helper()
	return newService(f.manifests, newFilesystemJournalStore(f.journalPath), f.resolver)
}

// writeDir 在 path 下写入与 ref 身份一致的完整 Agent 目录（agent.yaml + assets/avatar.png）。
func (f *agentRecoveryFixture) writeDir(path string, ref domain.PowerRef) {
	f.t.Helper()
	writeAgentDirectory(f.t, path, []byte(agentFixtureYAML(ref)))
}

// writeCorruptDir 在 path 下写入 avatar 无效的 Agent 目录，必须被目录校验拒绝。
func (f *agentRecoveryFixture) writeCorruptDir(path string, ref domain.PowerRef) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "assets"), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "agent.yaml"), []byte(agentFixtureYAML(ref)), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "assets", "avatar.png"), []byte("not a png"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// writeIncompleteDir 在 path 下创建缺少 agent.yaml 的不完整目录，必须被目录校验拒绝。
func (f *agentRecoveryFixture) writeIncompleteDir(path string) {
	f.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// useRealBibitAdapter 用真实 Bibit Adapter 替换 recording mock，验证目录级校验。
func (f *agentRecoveryFixture) useRealBibitAdapter() {
	f.t.Helper()
	registry := adapter.NewRegistry()
	real, err := registry.ResolveAgent(domain.AgentBibit)
	if err != nil {
		f.t.Fatal(err)
	}
	f.resolver.adapter = real
}

func (f *agentRecoveryFixture) assertJournalAbsent() {
	f.t.Helper()
	if _, err := os.Lstat(f.journalPath); !errors.Is(err, os.ErrNotExist) {
		f.t.Fatalf("journal remains: %v", err)
	}
}

func (f *agentRecoveryFixture) assertValidated(ref domain.PowerRef, path string) {
	f.t.Helper()
	if len(f.adapter.calls) != 1 {
		f.t.Fatalf("ValidateArtifact calls = %+v, want exactly 1", f.adapter.calls)
	}
	call := f.adapter.calls[0]
	if call.ref != ref || call.path != path {
		f.t.Fatalf("ValidateArtifact call = ref %+v path %q, want ref %+v path %q", call.ref, call.path, ref, path)
	}
}

func (f *agentRecoveryFixture) assertNotValidated() {
	f.t.Helper()
	if len(f.adapter.calls) != 0 {
		f.t.Fatalf("ValidateArtifact calls = %+v, want none", f.adapter.calls)
	}
}

func (f *agentRecoveryFixture) assertJournalPresent() {
	f.t.Helper()
	if _, err := os.Lstat(f.journalPath); err != nil {
		f.t.Fatalf("journal missing: %v", err)
	}
}

func TestRecoverAgentPublishingInstallWithTargetCompletesCommit(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.target, fixture.journal.Power)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit || result.OriginalPhase != PhasePublishing {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.setRef != fixture.journal.Power {
		t.Fatalf("manifest Set = %+v", fixture.manifests.setRef)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
	if readErr != nil || string(contents) != agentFixtureYAML(fixture.journal.Power) {
		t.Fatalf("target agent.yaml = %q, %v", contents, readErr)
	}
	if _, err := os.Lstat(fixture.journal.Filesystem.BackupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup remains: %v", err)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverAgentPublishingInstallWithoutTargetRollsBackBackup(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
	if readErr != nil || string(contents) != agentFixtureYAML(*fixture.journal.Previous) {
		t.Fatalf("restored target agent.yaml = %q, %v", contents, readErr)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalAbsent()
}

func TestRecoverAgentUninstallDirectionsConverge(t *testing.T) {
	tests := []struct {
		name               string
		phase              Phase
		target             bool
		quarantine         bool
		wantDirection      RecoveryDirection
		wantManifestRemove bool
	}{
		{name: "removing quarantine commits", phase: PhaseRemoving, quarantine: true, wantDirection: RecoveryCommit, wantManifestRemove: true},
		{name: "removing missing target commits", phase: PhaseRemoving, wantDirection: RecoveryCommit, wantManifestRemove: true},
		{name: "removing target rolls back", phase: PhaseRemoving, target: true, wantDirection: RecoveryRollback},
		{name: "rolling back quarantine restores", phase: PhaseRollingBack, quarantine: true, wantDirection: RecoveryRollback},
		{name: "committing completes", phase: PhaseCommitting, quarantine: true, wantDirection: RecoveryCommit, wantManifestRemove: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentRecoveryFixture(t, OperationUninstall, test.phase)
			if test.target {
				fixture.writeDir(fixture.target, fixture.journal.Power)
			}
			if test.quarantine {
				fixture.writeDir(fixture.journal.Filesystem.QuarantinePath, fixture.journal.Power)
			}
			fixture.persist()

			result, err := fixture.freshService().Recover(context.Background())
			if err != nil || result.Direction != test.wantDirection {
				t.Fatalf("Recover() = %+v, %v", result, err)
			}
			if got := fixture.manifests.removed.Name != ""; got != test.wantManifestRemove {
				t.Fatalf("manifest removed = %v, want %v", got, test.wantManifestRemove)
			}
			if test.phase == PhaseRollingBack {
				contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
				if readErr != nil || string(contents) != agentFixtureYAML(fixture.journal.Power) {
					t.Fatalf("target not restored: %q, %v", contents, readErr)
				}
			}
			fixture.assertJournalAbsent()
		})
	}
}

func TestAgentRecoveryInstallCommitValidatesTarget(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.target, fixture.journal.Power)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	fixture.assertValidated(fixture.journal.Power, fixture.target)
	if fixture.manifests.setRef != fixture.journal.Power {
		t.Fatalf("manifest Set = %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalAbsent()
}

func TestAgentRecoveryInstallCommitRejectsMismatch(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.target, fixture.journal.Power)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.adapter.err = errors.New("artifact identity mismatch")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) || !errors.Is(err, fixture.adapter.err) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState wrapping %v", err, fixture.adapter.err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	fixture.assertValidated(fixture.journal.Power, fixture.target)
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
	if readErr != nil || string(contents) != agentFixtureYAML(fixture.journal.Power) {
		t.Fatalf("target agent.yaml = %q, %v", contents, readErr)
	}
	backup, readErr := os.ReadFile(filepath.Join(fixture.journal.Filesystem.BackupPath, "agent.yaml"))
	if readErr != nil || string(backup) != agentFixtureYAML(*fixture.journal.Previous) {
		t.Fatalf("backup agent.yaml = %q, %v", backup, readErr)
	}
}

func TestAgentRecoveryInstallRollbackValidatesBackup(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	fixture.assertValidated(*fixture.journal.Previous, fixture.journal.Filesystem.BackupPath)
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
	if readErr != nil || string(contents) != agentFixtureYAML(*fixture.journal.Previous) {
		t.Fatalf("restored target agent.yaml = %q, %v", contents, readErr)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalAbsent()
}

func TestAgentRecoveryInstallRollbackRejectsMismatch(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.adapter.err = errors.New("artifact identity mismatch")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) || !errors.Is(err, fixture.adapter.err) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState wrapping %v", err, fixture.adapter.err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	fixture.assertValidated(*fixture.journal.Previous, fixture.journal.Filesystem.BackupPath)
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target unexpectedly restored: %v", statErr)
	}
	backup, readErr := os.ReadFile(filepath.Join(fixture.journal.Filesystem.BackupPath, "agent.yaml"))
	if readErr != nil || string(backup) != agentFixtureYAML(*fixture.journal.Previous) {
		t.Fatalf("backup agent.yaml = %q, %v", backup, readErr)
	}
}

func TestAgentRecoveryInstallRollbackRejectsMissingPrevious(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.journal.Previous = nil
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, fixture.journal.Power)
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState", err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	fixture.assertNotValidated()
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target unexpectedly restored: %v", statErr)
	}
	backup, readErr := os.ReadFile(filepath.Join(fixture.journal.Filesystem.BackupPath, "agent.yaml"))
	if readErr != nil || string(backup) != agentFixtureYAML(fixture.journal.Power) {
		t.Fatalf("backup agent.yaml = %q, %v", backup, readErr)
	}
}

func TestAgentRecoveryUninstallRollbackValidatesQuarantine(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationUninstall, PhaseRollingBack)
	fixture.writeDir(fixture.journal.Filesystem.QuarantinePath, fixture.journal.Power)
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || result.Direction != RecoveryRollback {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	fixture.assertValidated(fixture.journal.Power, fixture.journal.Filesystem.QuarantinePath)
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
	if readErr != nil || string(contents) != agentFixtureYAML(fixture.journal.Power) {
		t.Fatalf("restored target agent.yaml = %q, %v", contents, readErr)
	}
	fixture.assertJournalAbsent()
}

func TestAgentRecoveryUninstallRollbackRejectsMismatch(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationUninstall, PhaseRollingBack)
	fixture.writeDir(fixture.journal.Filesystem.QuarantinePath, fixture.journal.Power)
	fixture.adapter.err = errors.New("artifact identity mismatch")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) || !errors.Is(err, fixture.adapter.err) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState wrapping %v", err, fixture.adapter.err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest unexpectedly removed: %+v", fixture.manifests.removed)
	}
	fixture.assertJournalPresent()
	fixture.assertValidated(fixture.journal.Power, fixture.journal.Filesystem.QuarantinePath)
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target unexpectedly restored: %v", statErr)
	}
	quarantine, readErr := os.ReadFile(filepath.Join(fixture.journal.Filesystem.QuarantinePath, "agent.yaml"))
	if readErr != nil || string(quarantine) != agentFixtureYAML(fixture.journal.Power) {
		t.Fatalf("quarantine agent.yaml = %q, %v", quarantine, readErr)
	}
}

func TestAgentRecoveryUninstallCommitSkipsValidation(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationUninstall, PhaseCommitting)
	fixture.writeDir(fixture.journal.Filesystem.QuarantinePath, fixture.journal.Power)
	fixture.adapter.err = errors.New("must not validate on uninstall commit")
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	fixture.assertNotValidated()
	if fixture.manifests.removed.Name == "" {
		t.Fatalf("manifest not removed: %+v", fixture.manifests.removed)
	}
	fixture.assertJournalAbsent()
}

func TestAgentRecoveryResolverErrorFailsClosed(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.target, fixture.journal.Power)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	resolveErr := errors.New("resolve failed")
	fixture.resolver.err = resolveErr
	fixture.resolver.adapter = nil
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) || !errors.Is(err, resolveErr) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState wrapping %v", err, resolveErr)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	fixture.assertNotValidated()
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
	if readErr != nil || string(contents) != agentFixtureYAML(fixture.journal.Power) {
		t.Fatalf("target agent.yaml = %q, %v", contents, readErr)
	}
}

func TestAgentRecoveryTypedNilAdapterFailsClosed(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.target, fixture.journal.Power)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.resolver.adapter = (*recoveryAgentAdapter)(nil)
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState", err)
	}
	if strings.Contains(err.Error(), "%!w(<nil>)") {
		t.Fatalf("Recover() error = %v, must not render %%!w(<nil>)", err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if len(fixture.resolver.agents) != 1 || fixture.resolver.agents[0] != fixture.journal.Agent {
		t.Fatalf("resolver agents = %v, want [%s]", fixture.resolver.agents, fixture.journal.Agent)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	contents, readErr := os.ReadFile(filepath.Join(fixture.target, "agent.yaml"))
	if readErr != nil || string(contents) != agentFixtureYAML(fixture.journal.Power) {
		t.Fatalf("target agent.yaml = %q, %v", contents, readErr)
	}
	backup, readErr := os.ReadFile(filepath.Join(fixture.journal.Filesystem.BackupPath, "agent.yaml"))
	if readErr != nil || string(backup) != agentFixtureYAML(*fixture.journal.Previous) {
		t.Fatalf("backup agent.yaml = %q, %v", backup, readErr)
	}
}

func TestAgentRecoveryInstallCommitCompletesWithValidDirectories(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeDir(fixture.target, fixture.journal.Power)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.useRealBibitAdapter()
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if err != nil || !result.Recovered || result.Direction != RecoveryCommit {
		t.Fatalf("Recover() = %+v, %v", result, err)
	}
	if fixture.manifests.setRef != fixture.journal.Power {
		t.Fatalf("manifest Set = %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalAbsent()
}

func TestAgentRecoveryInstallCommitRejectsCorruptTarget(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeCorruptDir(fixture.target, fixture.journal.Power)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.useRealBibitAdapter()
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState", err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	if _, statErr := os.Lstat(fixture.target); statErr != nil {
		t.Fatalf("corrupt target removed: %v", statErr)
	}
	if _, statErr := os.Lstat(fixture.journal.Filesystem.BackupPath); statErr != nil {
		t.Fatalf("backup removed: %v", statErr)
	}
}

func TestAgentRecoveryInstallRollbackRejectsCorruptBackup(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeCorruptDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.useRealBibitAdapter()
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState", err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target unexpectedly restored: %v", statErr)
	}
	if _, statErr := os.Lstat(fixture.journal.Filesystem.BackupPath); statErr != nil {
		t.Fatalf("corrupt backup removed: %v", statErr)
	}
}

func TestAgentRecoveryUninstallRollbackRejectsCorruptQuarantine(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationUninstall, PhaseRollingBack)
	fixture.writeCorruptDir(fixture.journal.Filesystem.QuarantinePath, fixture.journal.Power)
	fixture.useRealBibitAdapter()
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState", err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.removed.Name != "" {
		t.Fatalf("manifest unexpectedly removed: %+v", fixture.manifests.removed)
	}
	fixture.assertJournalPresent()
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target unexpectedly restored: %v", statErr)
	}
	if _, statErr := os.Lstat(fixture.journal.Filesystem.QuarantinePath); statErr != nil {
		t.Fatalf("corrupt quarantine removed: %v", statErr)
	}
}

func TestAgentRecoveryInstallCommitRejectsIncompleteTarget(t *testing.T) {
	fixture := newAgentRecoveryFixture(t, OperationInstall, PhasePublishing)
	fixture.writeIncompleteDir(fixture.target)
	fixture.writeDir(fixture.journal.Filesystem.BackupPath, *fixture.journal.Previous)
	fixture.useRealBibitAdapter()
	fixture.persist()

	result, err := fixture.freshService().Recover(context.Background())
	if !errors.Is(err, ErrIndeterminateState) {
		t.Fatalf("Recover() error = %v, want ErrIndeterminateState", err)
	}
	if result.Recovered {
		t.Fatalf("Recover() recovered = %+v", result)
	}
	if fixture.manifests.setRef.Name != "" {
		t.Fatalf("manifest unexpectedly set: %+v", fixture.manifests.setRef)
	}
	fixture.assertJournalPresent()
	if _, statErr := os.Lstat(fixture.target); statErr != nil {
		t.Fatalf("incomplete target removed: %v", statErr)
	}
}
