package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
)

type installFixture struct {
	recovery       transaction.RecoveryResult
	snapshot       domain.AgentSnapshot
	cloudRefs      []domain.PowerRef
	selected       string
	installed      map[domain.AgentID]domain.PowerRef
	cacheResponses map[domain.AgentID][]installCacheResponse

	acquireErr               error
	releaseErr               error
	recoverErr               error
	refreshErr               error
	cloudListErr             error
	cloudGetErr              error
	resolveErrors            map[domain.AgentID]error
	configFindErrors         map[domain.AgentID]error
	transactionInstallErr    map[domain.AgentID]error
	transactionMCPInstallErr map[domain.AgentID]error
	mcpPresence              map[domain.AgentID]adapter.MCPPresence
	mcpInspectErrors         map[domain.AgentID]error
	mcpDefinitions           map[string]domain.MCPServer
	mcpGetErrors             map[string]error
	mcpRequests              []transaction.MCPInstallRequest
	pluginFact               adapter.PluginFact
	pluginFacts              map[string]adapter.PluginFact
	pluginInspectErrors      map[string]error
	marketplaceFacts         map[string]adapter.MarketplaceFact
	marketplaceInspectErrors map[string]error
	pluginMode               domain.PluginDeploymentMode
	pluginRequests           []transaction.PluginInstallRequest
	pluginRequestSink        *[]transaction.PluginInstallRequest
	pluginResources          *fakePluginResources
	configRemoveErr          error
	versionErr               error

	currentAgent domain.AgentID
	cacheCalls   map[domain.AgentID]int

	beforeRefresh func()
	beforeRelease func()
}

type fakePluginResources struct {
	calls       *[]string
	artifacts   map[string]pluginresource.Artifact
	ensureErr   map[string]error
	lookupErr   map[string]error
	stageRoot   string
	stageErr    error
	stageCalls  int
	ensureCalls int
	lookupCalls int
}

type installFakePluginResources = fakePluginResources

func pluginArtifactKey(ref domain.PowerRef) string {
	return ref.Name + ":" + ref.Version
}

func fakePluginArtifact(ref domain.PowerRef) pluginresource.Artifact {
	logicalName, _ := domain.PluginRuntimeName(ref)
	root := "/cache/plugins/" + ref.Name + "/" + ref.Version
	artifact := pluginresource.Artifact{
		Power: ref, Root: root, PluginPath: root + "/plugins/" + logicalName,
		CodexMarketplaceSource: root, ClaudeMarketplaceSource: root,
	}
	return artifact
}

func (resources *fakePluginResources) Ensure(_ context.Context, ref domain.PowerRef) (pluginresource.Artifact, error) {
	resources.ensureCalls++
	if resources.calls != nil {
		*resources.calls = append(*resources.calls, "resource:ensure:"+ref.Version)
	}
	key := pluginArtifactKey(ref)
	if err := resources.ensureErr[key]; err != nil {
		return pluginresource.Artifact{}, err
	}
	if artifact, ok := resources.artifacts[key]; ok {
		return artifact, nil
	}
	return fakePluginArtifact(ref), nil
}

func (resources *fakePluginResources) Lookup(ref domain.PowerRef) (pluginresource.Artifact, error) {
	resources.lookupCalls++
	if resources.calls != nil {
		*resources.calls = append(*resources.calls, "resource:lookup:"+ref.Version)
	}
	key := pluginArtifactKey(ref)
	if err := resources.lookupErr[key]; err != nil {
		return pluginresource.Artifact{}, err
	}
	if artifact, ok := resources.artifacts[key]; ok {
		return artifact, nil
	}
	return fakePluginArtifact(ref), nil
}

func (resources *fakePluginResources) StageFilesystem(ref domain.PowerRef) (string, error) {
	resources.stageCalls++
	if resources.calls != nil {
		*resources.calls = append(*resources.calls, "resource:stage-filesystem:"+ref.Version)
	}
	if resources.stageErr != nil {
		return "", resources.stageErr
	}
	if resources.stageRoot != "" {
		return resources.stageRoot, nil
	}
	return "/staging", nil
}

type installCacheResponse struct {
	staging string
	err     error
}

type installFakeUnlock struct {
	calls   *[]string
	fixture *installFixture
}

func (u *installFakeUnlock) Release() error {
	if u.fixture.beforeRelease != nil {
		u.fixture.beforeRelease()
	}
	*u.calls = append(*u.calls, "release")
	return u.fixture.releaseErr
}

type installFakeLocker struct {
	calls   *[]string
	fixture *installFixture
}

func (l *installFakeLocker) Acquire() (lock.Unlock, error) {
	*l.calls = append(*l.calls, "acquire")
	if l.fixture.acquireErr != nil {
		return nil, l.fixture.acquireErr
	}
	return &installFakeUnlock{calls: l.calls, fixture: l.fixture}, nil
}

type installFakeTransactions struct {
	calls   *[]string
	fixture *installFixture
}

func (m *installFakeTransactions) Inspect(context.Context) (transaction.InspectionResult, error) {
	*m.calls = append(*m.calls, "inspect")
	return transaction.InspectionResult{}, errors.New("Install lifecycle called Inspect before Recover")
}

func (m *installFakeTransactions) Recover(context.Context) (transaction.RecoveryResult, error) {
	*m.calls = append(*m.calls, "recover")
	return m.fixture.recovery, m.fixture.recoverErr
}

func (m *installFakeTransactions) InstallSkill(_ context.Context, request transaction.SkillInstallRequest) error {
	*m.calls = append(*m.calls, "transaction:install:"+string(request.Agent))
	return m.fixture.transactionInstallErr[request.Agent]
}

func (m *installFakeTransactions) InstallMCP(_ context.Context, request transaction.MCPInstallRequest) error {
	*m.calls = append(*m.calls, "transaction:install-mcp:"+string(request.Agent))
	m.fixture.mcpRequests = append(m.fixture.mcpRequests, request)
	return m.fixture.transactionMCPInstallErr[request.Agent]
}

func (*installFakeTransactions) UninstallSkill(context.Context, transaction.SkillUninstallRequest) (transaction.UninstallResult, error) {
	return transaction.UninstallResult{}, nil
}

func (*installFakeTransactions) UninstallMCP(context.Context, transaction.MCPUninstallRequest) (transaction.UninstallResult, error) {
	return transaction.UninstallResult{}, nil
}
func (m *installFakeTransactions) InstallPlugin(_ context.Context, request transaction.PluginInstallRequest) error {
	*m.calls = append(*m.calls, "transaction:install-plugin:"+string(request.Agent))
	m.fixture.pluginRequests = append(m.fixture.pluginRequests, request)
	if m.fixture.pluginRequestSink != nil {
		*m.fixture.pluginRequestSink = append(*m.fixture.pluginRequestSink, request)
	}
	return m.fixture.transactionInstallErr[request.Agent]
}
func (*installFakeTransactions) UninstallPlugin(context.Context, transaction.PluginUninstallRequest) (transaction.UninstallResult, error) {
	return transaction.UninstallResult{}, errors.New("unexpected UninstallPlugin call")
}

type installFakeCloud struct {
	calls   *[]string
	fixture *installFixture
}

func (c *installFakeCloud) List() ([]domain.PowerRef, error) {
	*c.calls = append(*c.calls, "cloud:list")
	return c.fixture.cloudRefs, c.fixture.cloudListErr
}

func (c *installFakeCloud) DownloadSkill(domain.PowerRef) error {
	*c.calls = append(*c.calls, "cloud:download-skill")
	return c.fixture.cloudGetErr
}

func (c *installFakeCloud) GetMCP(ref domain.PowerRef) (domain.MCPServer, error) {
	*c.calls = append(*c.calls, "cloud:get-mcp:"+ref.Version)
	return c.fixture.mcpDefinitions[ref.Version], c.fixture.mcpGetErrors[ref.Version]
}

func (c *installFakeCloud) DownloadPlugin(context.Context, domain.PowerRef, string) error {
	*c.calls = append(*c.calls, "cloud:unexpected-download-plugin")
	return errors.New("unexpected direct Plugin Cloud download")
}

type installFakeCache struct {
	calls   *[]string
	fixture *installFixture
}

func (c *installFakeCache) StageSkill(domain.PowerRef) (string, error) {
	return c.stage("skill")
}

func (c *installFakeCache) stage(power string) (string, error) {
	agent := c.fixture.currentAgent
	*c.calls = append(*c.calls, "cache:stage-"+power+":"+string(agent))
	if c.fixture.cacheCalls == nil {
		c.fixture.cacheCalls = make(map[domain.AgentID]int)
	}
	call := c.fixture.cacheCalls[agent]
	c.fixture.cacheCalls[agent] = call + 1
	responses := c.fixture.cacheResponses[agent]
	if call < len(responses) {
		return responses[call].staging, responses[call].err
	}
	return "/staging/" + string(agent), nil
}

type installFakeRegistry struct {
	calls   *[]string
	fixture *installFixture
}

func (r *installFakeRegistry) ResolveSkill(agent domain.AgentID) (adapter.SkillAdapter, error) {
	r.fixture.currentAgent = agent
	*r.calls = append(*r.calls, "resolve:"+string(agent))
	return installFakeAdapter{}, r.fixture.resolveErrors[agent]
}

func (r *installFakeRegistry) ResolveMCP(agent domain.AgentID) (adapter.MCPAdapter, error) {
	r.fixture.currentAgent = agent
	*r.calls = append(*r.calls, "resolve-mcp:"+string(agent))
	return installFakeMCPAdapter{agent: agent, calls: r.calls, fixture: r.fixture}, r.fixture.resolveErrors[agent]
}
func (r *installFakeRegistry) ResolvePlugin(agent domain.AgentID) (adapter.PluginAdapter, error) {
	r.fixture.currentAgent = agent
	*r.calls = append(*r.calls, "resolve-plugin:"+string(agent))
	return installFakePluginAdapter{agent: agent, calls: r.calls, fixture: r.fixture}, r.fixture.resolveErrors[agent]
}

func (r *installFakeRegistry) RefreshInstalled() (domain.AgentSnapshot, error) {
	if r.fixture.beforeRefresh != nil {
		r.fixture.beforeRefresh()
	}
	*r.calls = append(*r.calls, "refresh")
	return r.fixture.snapshot, r.fixture.refreshErr
}

type installFakeAdapter struct{}

func (installFakeAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }

func (installFakeAdapter) Install(domain.PowerRef, string) (adapter.SkillInstallResult, error) {
	return adapter.SkillInstallResult{Published: true}, nil
}

type installFakeMCPAdapter struct {
	agent   domain.AgentID
	calls   *[]string
	fixture *installFixture
}

type installFakePluginAdapter struct {
	agent   domain.AgentID
	calls   *[]string
	fixture *installFixture
}

func (a installFakePluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return a.fixture.pluginMode
}
func (a installFakePluginAdapter) Inspect(_ context.Context, actualName string) (adapter.PluginFact, error) {
	*a.calls = append(*a.calls, "plugin:inspect:"+string(a.agent)+":"+actualName)
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

func (a installFakePluginAdapter) InspectMarketplace(_ context.Context, name string) (adapter.MarketplaceFact, error) {
	*a.calls = append(*a.calls, "marketplace:inspect:"+string(a.agent)+":"+name)
	if err := a.fixture.marketplaceInspectErrors[name]; err != nil {
		return adapter.MarketplaceFact{}, err
	}
	if fact, ok := a.fixture.marketplaceFacts[name]; ok {
		return fact, nil
	}
	return adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: name}, nil
}

func (installFakePluginAdapter) RegisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, nil
}

func (installFakePluginAdapter) UnregisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, nil
}
func (installFakePluginAdapter) Add(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, nil
}
func (installFakePluginAdapter) Remove(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{State: adapter.PluginMutationApplied}, nil
}
func (installFakePluginAdapter) TargetPath(string) (string, error) { return "/plugins/demo", nil }

func (a installFakeMCPAdapter) Inspect(context.Context, string) (adapter.MCPPresence, error) {
	*a.calls = append(*a.calls, "mcp:inspect:"+string(a.agent))
	return a.fixture.mcpPresence[a.agent], a.fixture.mcpInspectErrors[a.agent]
}

func (installFakeMCPAdapter) Add(context.Context, domain.PowerRef, domain.MCPServer) (adapter.MCPMutationResult, error) {
	return adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, nil
}

func (installFakeMCPAdapter) Remove(context.Context, domain.PowerRef) (adapter.MCPMutationResult, error) {
	return adapter.MCPMutationResult{State: adapter.MCPMutationApplied}, nil
}

type installFakeConfig struct {
	calls   *[]string
	fixture *installFixture
}

func (c *installFakeConfig) Find(agent domain.AgentID, _ domain.PowerType, _ string) (domain.PowerRef, bool, error) {
	*c.calls = append(*c.calls, "config:find:"+string(agent))
	ref, ok := c.fixture.installed[agent]
	return ref, ok, c.fixture.configFindErrors[agent]
}

func (*installFakeConfig) List(domain.AgentID) (domain.PowerGroups, error) { return nil, nil }

func (*installFakeConfig) Set(domain.AgentID, domain.PowerRef) error { return nil }

func (c *installFakeConfig) Remove(agent domain.AgentID, powerType domain.PowerType, name string) error {
	*c.calls = append(*c.calls, "config:remove:"+string(agent)+":"+string(powerType)+":"+name)
	return c.fixture.configRemoveErr
}

type installFakeVersions struct {
	selected string
	err      error
}

func (s *installFakeVersions) Select([]string, string) (string, error) { return s.selected, s.err }

func newInstallService(t *testing.T, calls *[]string, fixture installFixture) *InstallService {
	t.Helper()
	pluginResources := fixture.pluginResources
	if pluginResources == nil {
		pluginResources = &fakePluginResources{}
	}
	pluginResources.calls = calls
	service, err := NewInstall(
		&installFakeLocker{calls: calls, fixture: &fixture},
		&installFakeTransactions{calls: calls, fixture: &fixture},
		&installFakeCloud{calls: calls, fixture: &fixture},
		&installFakeCache{calls: calls, fixture: &fixture},
		pluginResources,
		&installFakeRegistry{calls: calls, fixture: &fixture},
		&installFakeConfig{calls: calls, fixture: &fixture},
		&installFakeVersions{selected: fixture.selected, err: fixture.versionErr},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func installTarget() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}
}

func targetRefs(target domain.PowerRef) []domain.PowerRef {
	return []domain.PowerRef{target}
}

func installTargetFixture() installFixture {
	target := installTarget()
	return installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs: targetRefs(target),
		selected:  target.Version,
	}
}

func installTargetFlags(agent string) cli.Flags {
	return cli.Flags{Agent: agent, Type: "skill", Name: "demo"}
}

func assertZeroInstallResult(t *testing.T, result cli.InstallResult) {
	t.Helper()
	if result.Items != nil {
		t.Fatalf("Install() result = %#v, want zero result", result)
	}
}

func assertNoInstallBatchError(t *testing.T, err error) {
	t.Helper()
	var batch *cli.InstallBatchError
	if errors.As(err, &batch) {
		t.Fatalf("Install() error = %v, want no InstallBatchError", err)
	}
}

func assertInstallBatchError(t *testing.T, err error, agent domain.AgentID, cause error) {
	t.Helper()
	var batch *cli.InstallBatchError
	if !errors.As(err, &batch) {
		t.Fatalf("Install() error = %v, want InstallBatchError", err)
	}
	if batch.Agent != agent {
		t.Fatalf("InstallBatchError agent = %q, want %q", batch.Agent, agent)
	}
	if !errors.Is(batch, cause) {
		t.Fatalf("InstallBatchError = %v, want cause %v", batch, cause)
	}
}

func assertEventSubsequence(t *testing.T, events, want []string) {
	t.Helper()
	next := 0
	for _, event := range events {
		if next < len(want) && event == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("events = %v, want subsequence %v", events, want)
	}
}

func assertInstallItems(t *testing.T, result cli.InstallResult, want []cli.InstallItem) {
	t.Helper()
	if result.Items == nil {
		t.Fatal("Install() returned a nil Items slice")
	}
	if !reflect.DeepEqual(result.Items, want) {
		t.Fatalf("Install().Items = %#v, want %#v", result.Items, want)
	}
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
