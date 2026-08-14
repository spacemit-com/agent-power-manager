package lifecycle

import (
	"context"
	"errors"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/transaction"
)

type uninstallFixture struct {
	recovery              transaction.RecoveryResult
	snapshot              domain.AgentSnapshot
	groups                map[domain.AgentID]domain.PowerGroups
	transactionResults    map[domain.AgentID]transaction.UninstallResult
	transactionMCPResults map[domain.AgentID]transaction.UninstallResult
	mcpPresence           map[domain.AgentID]adapter.MCPPresence
	mcpInspectErrors      map[domain.AgentID]error
	mcpDefinitions        map[string]domain.MCPServer
	mcpGetErrors          map[string]error
	mcpRequests           []transaction.MCPUninstallRequest
	pluginFact            adapter.PluginFact
	pluginRequests        []transaction.PluginUninstallRequest

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

func (m *uninstallFakeTransactions) Recover(context.Context) (transaction.RecoveryResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "recover")
	return m.fixture.recovery, m.fixture.recoverErr
}

func (*uninstallFakeTransactions) InstallSkill(context.Context, transaction.SkillInstallRequest) error {
	return nil
}
func (*uninstallFakeTransactions) InstallMCP(context.Context, transaction.MCPInstallRequest) error {
	return nil
}

func (m *uninstallFakeTransactions) UninstallSkill(_ context.Context, request transaction.SkillUninstallRequest) (transaction.UninstallResult, error) {
	recordUninstallEvent(m.calls, m.fixture, "transaction:uninstall:"+string(request.Agent))
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
	return transaction.UninstallResult{}, nil
}

type uninstallFakeCloud struct {
	calls   *[]string
	fixture *uninstallFixture
}

func (*uninstallFakeCloud) List() ([]domain.PowerRef, error)    { return nil, nil }
func (*uninstallFakeCloud) DownloadSkill(domain.PowerRef) error { return nil }
func (c *uninstallFakeCloud) GetMCP(ref domain.PowerRef) (domain.MCPServer, error) {
	recordUninstallEvent(c.calls, c.fixture, "cloud:get-mcp:"+ref.Version)
	return c.fixture.mcpDefinitions[ref.Version], c.fixture.mcpGetErrors[ref.Version]
}

func (*uninstallFakeCloud) GetPlugin(domain.PowerRef) (domain.PluginDefinition, error) {
	return domain.PluginDefinition{}, nil
}
func (*uninstallFakeCloud) DownloadPlugin(domain.PowerRef) error { return nil }

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

func (r *uninstallFakeRegistry) RefreshInstalled() (domain.AgentSnapshot, error) {
	recordUninstallEvent(r.calls, r.fixture, "refresh")
	return r.fixture.snapshot, r.fixture.refreshErr
}

type uninstallFakeAdapter struct{}

func (uninstallFakeAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }

func (uninstallFakeAdapter) Install(domain.PowerRef, string) (adapter.SkillInstallResult, error) {
	return adapter.SkillInstallResult{Published: true}, nil
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

func (uninstallFakePluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}
func (uninstallFakePluginAdapter) PrepareInstall(context.Context, domain.PluginDefinition) error {
	return errors.New("unexpected PrepareInstall call")
}
func (a uninstallFakePluginAdapter) Inspect(context.Context, string) (adapter.PluginFact, error) {
	recordUninstallEvent(a.calls, a.fixture, "plugin:inspect:"+string(a.agent))
	return a.fixture.pluginFact, nil
}
func (uninstallFakePluginAdapter) Add(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (uninstallFakePluginAdapter) Remove(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (uninstallFakePluginAdapter) TargetPath(string) (string, error) { return "", nil }

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

func (c *uninstallFakeConfig) List(agent domain.AgentID) (domain.PowerGroups, error) {
	recordUninstallEvent(c.calls, c.fixture, "config:"+string(agent))
	return c.fixture.groups[agent], c.fixture.configErrors[agent]
}

func (*uninstallFakeConfig) Find(domain.AgentID, domain.PowerType, string) (domain.PowerRef, bool, error) {
	return domain.PowerRef{}, false, nil
}

func (*uninstallFakeConfig) Set(domain.AgentID, domain.PowerRef) error { return nil }

func (*uninstallFakeConfig) Remove(domain.AgentID, domain.PowerType, string) error { return nil }
