package transaction

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

func TestValidatePowerAcceptsAgent(t *testing.T) {
	if err := validatePower(domain.PowerRef{Name: "a", Type: domain.PowerTypeExpert, Version: "v1.0.0"}, ErrInvalidRequest); err != nil {
		t.Fatalf("validatePower(agent) error = %v", err)
	}
}

func TestValidateAgentInstallRequest(t *testing.T) {
	valid := AgentInstallRequest{
		Agent:       domain.AgentBibit,
		Power:       domain.PowerRef{Name: "a", Type: domain.PowerTypeExpert, Version: "v1.0.0"},
		StagingRoot: agentStagingRoot(t),
		Adapter:     &agentValidationAdapter{},
	}
	if err := validateAgentInstallRequest(valid); err != nil {
		t.Fatalf("validateAgentInstallRequest() error = %v", err)
	}
	tests := map[string]func(*AgentInstallRequest){
		"empty agent":   func(r *AgentInstallRequest) { r.Agent = "" },
		"all agents":    func(r *AgentInstallRequest) { r.Agent = domain.AllAgents },
		"empty name":    func(r *AgentInstallRequest) { r.Power.Name = "" },
		"skill power":   func(r *AgentInstallRequest) { r.Power.Type = domain.PowerTypeSkill },
		"empty version": func(r *AgentInstallRequest) { r.Power.Version = "" },
		"empty staging": func(r *AgentInstallRequest) { r.StagingRoot = "" },
		"nil adapter":   func(r *AgentInstallRequest) { r.Adapter = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateAgentInstallRequest(request); err == nil {
				t.Fatalf("validateAgentInstallRequest() error = nil")
			}
		})
	}
}

type agentValidationAdapter struct{}

func (a *agentValidationAdapter) TargetPath(domain.PowerRef) (string, error) {
	return "/tmp/agent.yaml", nil
}
func (a *agentValidationAdapter) ValidateArtifact(domain.PowerRef, string) error {
	return nil
}
func (a *agentValidationAdapter) Install(domain.PowerRef, string) (adapter.AgentInstallResult, error) {
	return adapter.AgentInstallResult{Published: true}, nil
}

// agentStagingRoot 返回与 paths.StagingPath 一致的隔离 staging 绝对路径。
func agentStagingRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("APM_HOME", t.TempDir())
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	return staging
}

// agentTestAvatarPNG 是 128x128 有效 PNG，满足 avatar 校验（签名、正方形与尺寸）。
var agentTestAvatarPNG = func() []byte {
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, 128, 128))); err != nil {
		panic("encode test agent avatar PNG: " + err.Error())
	}
	return output.Bytes()
}()

// agentFixtureYAML 返回与 ref 身份一致、avatar 指向 assets/avatar.png 的有效 Agent fixture。
func agentFixtureYAML(ref domain.PowerRef) string {
	return "schema_version: 1\nagent: {id: " + ref.Name + ", name: " + ref.Name + ", version: " + ref.Version + ", description: demo, soul: demo, metadata: {avatar: assets/avatar.png}}\n"
}

// writeAgentDirectory 在 root 下写入 agent.yaml 与 assets/avatar.png，
// 构成一个完整且有效的 Agent 目录。
func writeAgentDirectory(t *testing.T, root string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "avatar.png"), agentTestAvatarPNG, 0o644); err != nil {
		t.Fatal(err)
	}
}

// walkAgentDir 收集 root 下所有文件的规范相对路径到正文的映射。
func walkAgentDir(t *testing.T, root string) map[string][]byte {
	t.Helper()
	tree := make(map[string][]byte)
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." || entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(relative)] = body
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// agentTransactionFixture 提供使用真实 Bibit Adapter 的 Agent 安装/卸载事务测试环境。
type agentTransactionFixture struct {
	t         *testing.T
	events    []string
	root      string
	staging   string
	journals  *recordingJournalStore
	manifests *recordingManifestWriter
	adapter   adapter.AgentAdapter
	service   *Service
}

func newAgentTransactionFixture(t *testing.T) *agentTransactionFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	root := filepath.Join(t.TempDir(), ".bianbu-agent")
	t.Setenv("BIANBU_AGENT_HOME", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	registry := adapter.NewRegistry()
	agentAdapter, err := registry.ResolveAgent(domain.AgentBibit)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &agentTransactionFixture{t: t, root: root, staging: staging, adapter: agentAdapter}
	fixture.journals = &recordingJournalStore{events: &fixture.events, fail: map[string]error{}}
	fixture.manifests = &recordingManifestWriter{events: &fixture.events}
	fixture.service = newService(fixture.manifests, fixture.journals)
	fixture.service.newID = func() (string, error) { return "00112233445566778899aabbccddeeff", nil }
	return fixture
}

func TestAgentInstallFirstInstallPublishesCompleteDirectory(t *testing.T) {
	fixture := newAgentTransactionFixture(t)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeAgentDirectory(t, filepath.Join(fixture.staging, "agents", ref.Name), []byte(agentFixtureYAML(ref)))

	request := AgentInstallRequest{
		Agent:       domain.AgentBibit,
		Power:       ref,
		StagingRoot: fixture.staging,
		Adapter:     fixture.adapter,
	}
	if err := fixture.service.InstallAgent(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := fixture.journals.journal.SchemaVersion; got != 1 {
		t.Fatalf("journal schema = %d, want 1", got)
	}
	if got := fixture.journals.journal.Driver; got != DriverFilesystem {
		t.Fatalf("journal driver = %q, want filesystem", got)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after successful install")
	}
	if fixture.manifests.setRef != ref {
		t.Fatalf("Set ref = %+v", fixture.manifests.setRef)
	}
	target := filepath.Join(fixture.root, "agents", ref.Name)
	want := map[string][]byte{
		"agent.yaml":        []byte(agentFixtureYAML(ref)),
		"assets/avatar.png": agentTestAvatarPNG,
	}
	if got := walkAgentDir(t, target); !reflect.DeepEqual(got, want) {
		t.Fatalf("installed tree = %v, want %v", got, want)
	}
	if _, err := os.Lstat(fixture.staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestAgentInstallReplacementBacksUpOldTarget(t *testing.T) {
	fixture := newAgentTransactionFixture(t)
	old := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v2.0.0"}
	writeAgentDirectory(t, filepath.Join(fixture.root, "agents", ref.Name), []byte(agentFixtureYAML(old)))
	writeAgentDirectory(t, filepath.Join(fixture.staging, "agents", ref.Name), []byte(agentFixtureYAML(ref)))

	request := AgentInstallRequest{
		Agent:       domain.AgentBibit,
		Power:       ref,
		Previous:    &old,
		StagingRoot: fixture.staging,
		Adapter:     fixture.adapter,
	}
	if err := fixture.service.InstallAgent(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(fixture.root, "agents", ref.Name)
	want := map[string][]byte{
		"agent.yaml":        []byte(agentFixtureYAML(ref)),
		"assets/avatar.png": agentTestAvatarPNG,
	}
	if got := walkAgentDir(t, target); !reflect.DeepEqual(got, want) {
		t.Fatalf("installed tree = %v, want %v", got, want)
	}
	if fixture.manifests.setRef != ref {
		t.Fatalf("Set ref = %+v", fixture.manifests.setRef)
	}
	assertEventSubsequence(t, fixture.events, []string{"create:preparing", "update:publishing", "update:committing", "manifest:set", "delete:journal"})
}

func TestAgentInstallRejectsUnmanagedTarget(t *testing.T) {
	fixture := newAgentTransactionFixture(t)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeAgentDirectory(t, filepath.Join(fixture.root, "agents", ref.Name), []byte(agentFixtureYAML(ref)))
	writeAgentDirectory(t, filepath.Join(fixture.staging, "agents", ref.Name), []byte(agentFixtureYAML(ref)))

	request := AgentInstallRequest{
		Agent:       domain.AgentBibit,
		Power:       ref,
		StagingRoot: fixture.staging,
		Adapter:     fixture.adapter,
	}
	err := fixture.service.InstallAgent(context.Background(), request)
	if !errors.Is(err, adapter.ErrTargetConflict) {
		t.Fatalf("InstallAgent() error = %v, want ErrTargetConflict", err)
	}
	if fixture.journals.found || len(fixture.events) != 0 {
		t.Fatalf("journal/events = %v, %v", fixture.journals.found, fixture.events)
	}
}

func TestAgentUninstallQuarantinesAndPrunesEmptyAgentsParent(t *testing.T) {
	fixture := newAgentTransactionFixture(t)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeAgentDirectory(t, filepath.Join(fixture.root, "agents", ref.Name), []byte(agentFixtureYAML(ref)))

	request := AgentUninstallRequest{Agent: domain.AgentBibit, Power: ref, Adapter: fixture.adapter}
	result, err := fixture.service.UninstallAgent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetMissing {
		t.Fatalf("UninstallAgent() result = %+v, want TargetMissing false", result)
	}
	if got := fixture.journals.journal.SchemaVersion; got != 1 {
		t.Fatalf("journal schema = %d, want 1", got)
	}
	if got := fixture.journals.journal.Driver; got != DriverFilesystem {
		t.Fatalf("journal driver = %q, want filesystem", got)
	}
	if fixture.journals.found {
		t.Fatal("journal remains after successful uninstall")
	}
	if fixture.manifests.removed.Name != "demo" || fixture.manifests.removed.Type != domain.PowerTypeExpert {
		t.Fatalf("removed manifest = %+v", fixture.manifests.removed)
	}
	if _, err := os.Lstat(filepath.Join(fixture.root, "agents", ref.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.root, "agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty agents parent not pruned: %v", err)
	}
	if info, err := os.Stat(fixture.root); err != nil || !info.IsDir() {
		t.Fatalf("bibit root removed or missing: %v", err)
	}
}

func TestAgentUninstallKeepsAgentsParentWhenNotEmpty(t *testing.T) {
	fixture := newAgentTransactionFixture(t)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	other := domain.PowerRef{Name: "other", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeAgentDirectory(t, filepath.Join(fixture.root, "agents", ref.Name), []byte(agentFixtureYAML(ref)))
	writeAgentDirectory(t, filepath.Join(fixture.root, "agents", other.Name), []byte(agentFixtureYAML(other)))

	request := AgentUninstallRequest{Agent: domain.AgentBibit, Power: ref, Adapter: fixture.adapter}
	if _, err := fixture.service.UninstallAgent(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "agents", other.Name)); err != nil {
		t.Fatalf("sibling agent removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "agents")); err != nil {
		t.Fatalf("agents parent removed while non-empty: %v", err)
	}
}

func TestAgentUninstallReportsInitiallyMissingTarget(t *testing.T) {
	fixture := newAgentTransactionFixture(t)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}

	request := AgentUninstallRequest{Agent: domain.AgentBibit, Power: ref, Adapter: fixture.adapter}
	result, err := fixture.service.UninstallAgent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.TargetMissing {
		t.Fatalf("UninstallAgent() result = %+v, want TargetMissing true", result)
	}
	if fixture.manifests.removed.Name != "demo" || fixture.journals.found {
		t.Fatalf("removed = %+v, journal found = %v", fixture.manifests.removed, fixture.journals.found)
	}
}
