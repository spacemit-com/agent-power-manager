package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

type uninstallFixture struct {
	recovery              transaction.RecoveryResult
	snapshot              domain.AgentSnapshot
	groups                map[domain.AgentID]domain.InstalledPowerGroups
	transactionResults    map[domain.AgentID]transaction.UninstallResult
	transactionMCPResults map[domain.AgentID]transaction.UninstallResult
	mcpPresence           map[domain.AgentID]adapter.MCPPresence
	mcpInspectErrors      map[domain.AgentID]error
	mcpRequests           []transaction.MCPUninstallRequest
	pluginFact            adapter.PluginFact
	pluginFacts           map[string]adapter.PluginFact
	pluginInspectErrors   map[string]error
	pluginTargets         map[string]string
	marketplaceFacts      map[string]adapter.MarketplaceFact
	marketplaceErrors     map[string]error
	pluginMode            domain.PluginDeploymentMode
	pluginRequests        []transaction.PluginUninstallRequest
	pluginRequestSink     *[]transaction.PluginUninstallRequest
	pluginResources       *fakePluginResources
	configRemoveErr       error

	acquireErr           error
	recoverErr           error
	refreshErr           error
	releaseErr           error
	configErrors         map[domain.AgentID]error
	resolveErrors        map[domain.AgentID]error
	transactionErrors    map[domain.AgentID]error
	transactionMCPErrors map[domain.AgentID]error

	beforeRelease func()
	onEvent       func(string)
}

func recordUninstallEvent(calls *[]string, fixture *uninstallFixture, event string) {
	*calls = append(*calls, event)
	if fixture.onEvent != nil {
		fixture.onEvent(event)
	}
}

type uninstallFakeUnlock struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (u *uninstallFakeUnlock) Release() error {
	if u.fixture.beforeRelease != nil {
		u.fixture.beforeRelease()
	}
	recordUninstallEvent(u.calls, u.fixture, "release")
	return u.fixture.releaseErr
}

type uninstallFakeLocker struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (l *uninstallFakeLocker) Acquire() (lock.Unlock, error) {
	recordUninstallEvent(l.calls, l.fixture, "acquire")
	if l.fixture.acquireErr != nil {
		return nil, l.fixture.acquireErr
	}
	return &uninstallFakeUnlock{calls: l.calls, fixture: l.fixture}, nil
}

type uninstallFakeTransactions struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (m *uninstallFakeTransactions) Inspect(context.Context) (transaction.InspectionResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "inspect")
	return transaction.InspectionResult{}, errors.New("Uninstall lifecycle called Inspect before Recover")
}

func (m *uninstallFakeTransactions) Recover(context.Context) (transaction.RecoveryResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "recover")
	return m.fixture.recovery, m.fixture.recoverErr
}

func (m *uninstallFakeTransactions) InspectFilesystemTarget(ref domain.PowerRef, _ transaction.FilesystemTarget) (transaction.TargetPresence, error) {
	recordUninstallEvent(m.calls, m.fixture, "transaction:inspect-target:"+string(ref.Type))
	return transaction.TargetAbsent, nil
}

func (*uninstallFakeTransactions) InstallSkill(context.Context, transaction.SkillInstallRequest) error {
	return nil
}
func (*uninstallFakeTransactions) InstallAgent(context.Context, transaction.AgentInstallRequest) error {
	return nil
}
func (*uninstallFakeTransactions) InstallMCP(context.Context, transaction.MCPInstallRequest) error {
	return nil
}

func (m *uninstallFakeTransactions) UninstallSkill(_ context.Context, request transaction.SkillUninstallRequest) (transaction.UninstallResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "transaction:uninstall:"+string(request.Agent))
	return m.fixture.transactionResults[request.Agent], m.fixture.transactionErrors[request.Agent]
}
func (m *uninstallFakeTransactions) UninstallAgent(_ context.Context, request transaction.AgentUninstallRequest) (transaction.UninstallResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "transaction:uninstall-agent:"+string(request.Agent))
	return m.fixture.transactionResults[request.Agent], m.fixture.transactionErrors[request.Agent]
}
func (m *uninstallFakeTransactions) UninstallMCP(_ context.Context, request transaction.MCPUninstallRequest) (transaction.UninstallResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "transaction:uninstall-mcp:"+string(request.Agent))
	m.fixture.mcpRequests = append(m.fixture.mcpRequests, request)
	return m.fixture.transactionMCPResults[request.Agent], m.fixture.transactionMCPErrors[request.Agent]
}
func (*uninstallFakeTransactions) InstallPlugin(context.Context, transaction.PluginInstallRequest) error {
	return errors.New("unexpected InstallPlugin call")
}
func (m *uninstallFakeTransactions) UninstallPlugin(_ context.Context, request transaction.PluginUninstallRequest) (transaction.UninstallResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "transaction:uninstall-plugin:"+string(request.Agent))
	m.fixture.pluginRequests = append(m.fixture.pluginRequests, request)
	if m.fixture.pluginRequestSink != nil {
		*m.fixture.pluginRequestSink = append(*m.fixture.pluginRequestSink, request)
	}
	return m.fixture.transactionResults[request.Agent], m.fixture.transactionErrors[request.Agent]
}

type uninstallFakeRegistry struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (r *uninstallFakeRegistry) ResolveSkill(agent domain.AgentID) (adapter.SkillAdapter, error) {
	recordUninstallEvent(r.calls, r.fixture, "resolve:"+string(agent))
	return uninstallFakeAdapter{}, r.fixture.resolveErrors[agent]
}
func (r *uninstallFakeRegistry) ResolveMCP(agent domain.AgentID) (adapter.MCPAdapter, error) {
	recordUninstallEvent(r.calls, r.fixture, "resolve-mcp:"+string(agent))
	return uninstallFakeMCPAdapter{agent: agent, calls: r.calls, fixture: r.fixture}, r.fixture.resolveErrors[agent]
}
func (r *uninstallFakeRegistry) ResolvePlugin(agent domain.AgentID) (adapter.PluginAdapter, error) {
	recordUninstallEvent(r.calls, r.fixture, "resolve-plugin:"+string(agent))
	return uninstallFakePluginAdapter{agent: agent, calls: r.calls, fixture: r.fixture}, r.fixture.resolveErrors[agent]
}
func (r *uninstallFakeRegistry) ResolveAgent(agent domain.AgentID) (adapter.AgentAdapter, error) {
	recordUninstallEvent(r.calls, r.fixture, "resolve-agent:"+string(agent))
	return uninstallFakeAgentAdapter{}, r.fixture.resolveErrors[agent]
}

func (r *uninstallFakeRegistry) RefreshInstalled() (domain.AgentSnapshot, error) {
	recordUninstallEvent(r.calls, r.fixture, "refresh")
	return r.fixture.snapshot, r.fixture.refreshErr
}

type uninstallFakeAdapter struct{}

func (uninstallFakeAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }

func (uninstallFakeAdapter) Install(domain.PowerRef, string) (adapter.SkillInstallResult, error) {
	return adapter.SkillInstallResult{Published: true}, nil
}

type uninstallFakeAgentAdapter struct{}

func (uninstallFakeAgentAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }

func (uninstallFakeAgentAdapter) ValidateArtifact(domain.PowerRef, string) error { return nil }

func (uninstallFakeAgentAdapter) Install(domain.PowerRef, string) (adapter.AgentInstallResult, error) {
	return adapter.AgentInstallResult{Published: true}, nil
}

type uninstallFakeMCPAdapter struct {
	agent   domain.AgentID
	calls   *[]string
	fixture *uninstallFixture
}

type uninstallFakePluginAdapter struct {
	agent   domain.AgentID
	calls   *[]string
	fixture *uninstallFixture
}

func (adapter uninstallFakePluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	if adapter.fixture.pluginMode != "" {
		return adapter.fixture.pluginMode
	}
	return domain.PluginDeploymentMarketplace
}
func (a uninstallFakePluginAdapter) Inspect(_ context.Context, actualName string) (adapter.PluginFact, error) {
	recordUninstallEvent(a.calls, a.fixture, "plugin:inspect:"+string(a.agent)+":"+actualName)
	if err := a.fixture.pluginInspectErrors[actualName]; err != nil {
		return adapter.PluginFact{}, err
	}
	if fact, ok := a.fixture.pluginFacts[actualName]; ok {
		return fact, nil
	}
	if a.fixture.pluginFact.ActualName == "" {
		return adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: actualName}, nil
	}
	return a.fixture.pluginFact, nil
}
func (a uninstallFakePluginAdapter) InspectMarketplace(_ context.Context, name string) (adapter.MarketplaceFact, error) {
	recordUninstallEvent(a.calls, a.fixture, "marketplace:inspect:"+string(a.agent)+":"+name)
	if err := a.fixture.marketplaceErrors[name]; err != nil {
		return adapter.MarketplaceFact{}, err
	}
	if fact, ok := a.fixture.marketplaceFacts[name]; ok {
		return fact, nil
	}
	return adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: name}, nil
}
func (uninstallFakePluginAdapter) RegisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, nil
}
func (uninstallFakePluginAdapter) UnregisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, nil
}
func (uninstallFakePluginAdapter) Add(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (uninstallFakePluginAdapter) Remove(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (a uninstallFakePluginAdapter) TargetPath(name string) (string, error) {
	recordUninstallEvent(a.calls, a.fixture, "plugin:target-path:"+string(a.agent)+":"+name)
	if target, ok := a.fixture.pluginTargets[name]; ok {
		return target, nil
	}
	return "", adapter.ErrUnsupportedAdapter
}

func (a uninstallFakeMCPAdapter) Inspect(context.Context, string) (adapter.MCPPresence, error) {
	recordUninstallEvent(a.calls, a.fixture, "mcp:inspect:"+string(a.agent))
	return a.fixture.mcpPresence[a.agent], a.fixture.mcpInspectErrors[a.agent]
}
func (uninstallFakeMCPAdapter) Add(context.Context, domain.PowerRef, domain.MCPServer) (adapter.MCPMutationResult, error) {
	return adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, nil
}
func (uninstallFakeMCPAdapter) Remove(context.Context, domain.PowerRef) (adapter.MCPMutationResult, error) {
	return adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, nil
}

type uninstallFakeConfig struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (c *uninstallFakeConfig) List(agent domain.AgentID) (domain.InstalledPowerGroups, error) {
	recordUninstallEvent(c.calls, c.fixture, "config:"+string(agent))
	return c.fixture.groups[agent], c.fixture.configErrors[agent]
}

func (*uninstallFakeConfig) Find(domain.AgentID, domain.PowerType, string) (domain.InstalledPower, bool, error) {
	return domain.InstalledPower{}, false, nil
}

func (*uninstallFakeConfig) Set(domain.AgentID, domain.InstalledPower) error { return nil }

func (c *uninstallFakeConfig) Remove(agent domain.AgentID, powerType domain.PowerType, name string) error {
	recordUninstallEvent(c.calls, c.fixture, "config:remove:"+string(agent)+":"+string(powerType)+":"+name)
	return c.fixture.configRemoveErr
}

func newUninstallService(t *testing.T, calls *[]string, fixture uninstallFixture) *UninstallService {
	t.Helper()
	pluginResources := fixture.pluginResources
	if pluginResources == nil {
		pluginResources = &fakePluginResources{}
	}
	pluginResources.calls = calls
	service, err := NewUninstall(
		&uninstallFakeLocker{calls: calls, fixture: &fixture},
		&uninstallFakeTransactions{calls: calls, fixture: &fixture},
		pluginResources,
		&uninstallFakeRegistry{calls: calls, fixture: &fixture},
		&uninstallFakeConfig{calls: calls, fixture: &fixture},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func uninstallTarget() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.2.3"}
}

func uninstallTargetFixture() uninstallFixture {
	target := uninstallTarget()
	return uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups: map[domain.AgentID]domain.InstalledPowerGroups{
			domain.AgentCodex: {domain.PowerTypeSkill: {domain.InstalledPower{Ref: target}}},
		},
	}
}

func uninstallTargetFlags(agent string) cli.Flags {
	return cli.Flags{Agent: agent, Type: "skill", Name: "demo"}
}

func powerPointer(ref domain.PowerRef) *domain.PowerRef {
	copied := ref
	return &copied
}

func assertUninstallItems(t *testing.T, result cli.UninstallResult, want []cli.UninstallItem) {
	t.Helper()
	if result.Items == nil {
		t.Fatal("Uninstall() returned a nil Items slice")
	}
	if !reflect.DeepEqual(result.Items, want) {
		t.Fatalf("Uninstall().Items = %#v, want %#v", result.Items, want)
	}
}

func assertZeroUninstallResult(t *testing.T, result cli.UninstallResult) {
	t.Helper()
	if result.Items != nil {
		t.Fatalf("Uninstall() result = %#v, want zero result", result)
	}
}

func assertNoUninstallBatchError(t *testing.T, err error) {
	t.Helper()
	var batch *cli.UninstallBatchError
	if errors.As(err, &batch) {
		t.Fatalf("Uninstall() error = %v, want no UninstallBatchError", err)
	}
}

func assertUninstallBatchError(t *testing.T, err error, agent domain.AgentID, cause error) {
	t.Helper()
	var batch *cli.UninstallBatchError
	if !errors.As(err, &batch) {
		t.Fatalf("Uninstall() error = %v, want UninstallBatchError", err)
	}
	if batch.Agent != agent {
		t.Fatalf("UninstallBatchError agent = %q, want %q", batch.Agent, agent)
	}
	if !errors.Is(batch, cause) {
		t.Fatalf("UninstallBatchError = %v, want cause %v", batch, cause)
	}
}

func uninstallTestLogger(t *testing.T, stdout, stderr *bytes.Buffer, verbose bool) {
	t.Helper()
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
}

func assertContainsAll(t *testing.T, value string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(value, want) {
			t.Fatalf("value = %q, want it to contain %q", value, want)
		}
	}
}

func rejectLaterUninstallEvents(t *testing.T, agents []domain.AgentID) func(string) {
	t.Helper()
	forbidden := make(map[string]bool, len(agents)*3)
	for _, agent := range agents {
		forbidden["config:"+string(agent)] = true
		forbidden["resolve:"+string(agent)] = true
		forbidden["transaction:uninstall:"+string(agent)] = true
	}
	return func(event string) {
		if forbidden[event] {
			t.Fatalf("Uninstall() accessed later Agent after first failure: %s", event)
		}
	}
}
