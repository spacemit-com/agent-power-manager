package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
	"apm/internal/version"
)

// newUpgradeService 复用 install 的 fake 体系构造 Upgrade 生命周期，避免第二套 fake。
func newUpgradeService(t *testing.T, calls *[]string, fixture installFixture) *UpgradeService {
	t.Helper()
	pluginResources := fixture.pluginResources
	if pluginResources == nil {
		pluginResources = &fakePluginResources{}
	}
	pluginResources.calls = calls
	service, err := NewUpgrade(
		&installFakeLocker{calls: calls, fixture: &fixture},
		&installFakeTransactions{calls: calls, fixture: &fixture},
		&installFakeCatalogManager{calls: calls, fixture: &fixture},
		&installFakeCloud{calls: calls, fixture: &fixture},
		&installFakeCache{calls: calls, fixture: &fixture},
		pluginResources,
		&installFakeRegistry{calls: calls, fixture: &fixture},
		&installFakeConfig{calls: calls, fixture: &fixture},
		// 计划器测试覆盖选择器失败；服务层用真实选择器，验证端到端的 latest 选择。
		version.NewSelector(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// upgradeFixture 构造「codex 装了 demo v0.0.1、目录有 v0.1.0」的最小升级场景。
// 显式给出 catalogEntries 以绕开 fake 的「自动并入已安装版本」回退，便于精确控制目录内容。
func upgradeFixture() installFixture {
	installed := skillRef("demo", "v0.0.1")
	return installFixture{
		snapshot:         domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		catalogEntries:   catalogEntries(skillRef("demo", "v0.1.0")),
		installed:        map[domain.AgentID]domain.PowerRef{domain.AgentCodex: installed},
		installedGroups:  map[domain.AgentID]domain.InstalledPowerGroups{domain.AgentCodex: groupsWith(domain.PowerTypeSkill, installed)},
		targetPresence:   map[domain.AgentID]transaction.TargetPresence{domain.AgentCodex: transaction.TargetPresent},
		inspectAllowed:   true,
		pluginMode:       domain.PluginDeploymentFilesystem,
		configListErrors: map[domain.AgentID]error{},
	}
}

func TestUpgradeInstallsLatestVersionForInstalledPower(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	want := []cli.UpgradeItem{{
		Agent:   domain.AgentCodex,
		Current: skillRef("demo", "v0.0.1"),
		Target:  skillRef("demo", "v0.1.0"),
		Outcome: cli.UpgradeOutcomeUpgraded,
	}}
	if !reflect.DeepEqual(result.Items, want) {
		t.Fatalf("Upgrade() = %#v, want %#v", result.Items, want)
	}
}

func TestUpgradeReportsUpToDateWithoutTouchingContentBoundaries(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	current := skillRef("demo", "v0.1.0")
	fixture.installed = map[domain.AgentID]domain.PowerRef{domain.AgentCodex: current}
	fixture.installedGroups = map[domain.AgentID]domain.InstalledPowerGroups{
		domain.AgentCodex: groupsWith(domain.PowerTypeSkill, current),
	}
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != cli.UpgradeOutcomeUpToDate {
		t.Fatalf("Upgrade() = %#v, want a single up-to-date item", result.Items)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "transaction:") || strings.HasPrefix(call, "resource:ensure") {
			t.Fatalf("up-to-date upgrade touched %q; want no transaction or content work", call)
		}
	}
}

func TestUpgradeSkipsAndWarnsWhenCatalogHasNoCandidate(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	fixture.catalogEntries = catalogEntries(skillRef("other", "v0.1.0"))
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("Upgrade() = %#v, want one item", result.Items)
	}
	item := result.Items[0]
	if item.Outcome != cli.UpgradeOutcomeSkipped || item.Skip != cli.UpgradeSkipNotInCatalog {
		t.Fatalf("Upgrade() item = %#v, want skipped/not-in-catalog", item)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "transaction:") {
			t.Fatalf("skipped upgrade ran %q; want no transaction", call)
		}
	}
}

func TestUpgradeSkipsNewerLocalVersionWithoutDowngrading(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	local := skillRef("demo", "v0.2.0")
	fixture.installed = map[domain.AgentID]domain.PowerRef{domain.AgentCodex: local}
	fixture.installedGroups = map[domain.AgentID]domain.InstalledPowerGroups{
		domain.AgentCodex: groupsWith(domain.PowerTypeSkill, local),
	}
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	item := result.Items[0]
	if item.Outcome != cli.UpgradeOutcomeSkipped || item.Skip != cli.UpgradeSkipNewerLocal {
		t.Fatalf("Upgrade() item = %#v, want skipped/newer-local", item)
	}
	if item.Target.Version != "v0.2.0" {
		t.Fatalf("Upgrade() target = %q, want the unchanged local version", item.Target.Version)
	}
}

func TestUpgradeDryRunPlansWithoutTouchingLocalState(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents), DryRun: true})
	if err != nil {
		t.Fatalf("Upgrade(dry-run) error = %v", err)
	}
	if !result.DryRun {
		t.Fatal("Upgrade(dry-run) result = DryRun false, want true")
	}
	if len(result.Items) != 1 {
		t.Fatalf("Upgrade(dry-run) = %#v, want one planned item", result.Items)
	}
	// 计划阶段不得声称变更为 upgraded。
	if result.Items[0].Outcome != cli.UpgradeOutcomePlanned {
		t.Fatalf("Upgrade(dry-run) outcome = %q, want planned", result.Items[0].Outcome)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "transaction:") || strings.HasPrefix(call, "resource:ensure") {
			t.Fatalf("dry-run touched %q; want a pure plan", call)
		}
	}
}

func TestUpgradeContinuesAfterSingleFailure(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	alpha := skillRef("alpha", "v0.0.1")
	demo := skillRef("demo", "v0.0.1")
	fixture.installedGroups = map[domain.AgentID]domain.InstalledPowerGroups{
		domain.AgentCodex: groupsWith(domain.PowerTypeSkill, alpha, demo),
	}
	fixture.catalogEntries = catalogEntries(skillRef("alpha", "v0.1.0"), skillRef("demo", "v0.1.0"))
	cause := errors.New("staging copy failed")
	fixture.transactionInstallErrByPower = map[string]error{"alpha": cause}
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err == nil {
		t.Fatal("Upgrade() error = nil, want batch failure")
	}
	var batch *cli.UpgradeBatchError
	if !errors.As(err, &batch) || !errors.Is(err, cause) {
		t.Fatalf("Upgrade() error = %v, want UpgradeBatchError wrapping cause", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("Upgrade() = %#v, want two items", result.Items)
	}
	if result.Items[0].Outcome != cli.UpgradeOutcomeFailed {
		t.Fatalf("first item = %#v, want failed", result.Items[0])
	}
	// 关键：一条失败之后仍继续执行下一条，而不是 fail-fast。
	if result.Items[1].Outcome != cli.UpgradeOutcomeUpgraded {
		t.Fatalf("second item = %#v, want upgraded after the first failure", result.Items[1])
	}
}

func TestUpgradeStopsImmediatelyOnResidualJournal(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	alpha := skillRef("alpha", "v0.0.1")
	demo := skillRef("demo", "v0.0.1")
	zeta := skillRef("zeta", "v0.0.1")
	fixture.installedGroups = map[domain.AgentID]domain.InstalledPowerGroups{
		domain.AgentCodex: groupsWith(domain.PowerTypeSkill, alpha, demo, zeta),
	}
	fixture.catalogEntries = catalogEntries(
		skillRef("alpha", "v0.1.0"), skillRef("demo", "v0.1.0"), skillRef("zeta", "v0.1.0"),
	)
	fixture.transactionInstallErrByPower = map[string]error{"alpha": transaction.ErrActiveTransaction}
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err == nil {
		t.Fatal("Upgrade() error = nil, want batch failure")
	}
	if len(result.Items) != 3 {
		t.Fatalf("Upgrade() = %#v, want three items", result.Items)
	}
	if result.Items[0].Outcome != cli.UpgradeOutcomeFailed {
		t.Fatalf("first item = %#v, want failed", result.Items[0])
	}
	// 残留 journal 时后续条目不能再执行，必须记 unexecuted。
	for index, item := range result.Items[1:] {
		if item.Outcome != cli.UpgradeOutcomeUnexecuted {
			t.Fatalf("item[%d] = %#v, want unexecuted after residual journal", index+1, item)
		}
	}
	// 第三条不得真正执行事务。
	for _, call := range calls {
		if call == "transaction:install:codex" {
			// alpha 与 demo 都可能出现在此；用计数断言只执行了一次。
			continue
		}
	}
	executions := 0
	for _, call := range calls {
		if call == "transaction:install:codex" {
			executions++
		}
	}
	if executions != 1 {
		t.Fatalf("transaction executions = %d, want exactly 1 before stopping", executions)
	}
}

func TestUpgradeStopsWhenInspectReportsActiveJournal(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	alpha := skillRef("alpha", "v0.0.1")
	demo := skillRef("demo", "v0.0.1")
	fixture.installedGroups = map[domain.AgentID]domain.InstalledPowerGroups{
		domain.AgentCodex: groupsWith(domain.PowerTypeSkill, alpha, demo),
	}
	fixture.catalogEntries = catalogEntries(skillRef("alpha", "v0.1.0"), skillRef("demo", "v0.1.0"))
	// 普通业务失败 + Inspect 报告仍有活动事务 → 必须停止。
	fixture.transactionInstallErrByPower = map[string]error{"alpha": errors.New("staging copy failed")}
	fixture.inspection = transaction.InspectionResult{Active: true}
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err == nil {
		t.Fatal("Upgrade() error = nil, want batch failure")
	}
	if len(result.Items) != 2 || result.Items[1].Outcome != cli.UpgradeOutcomeUnexecuted {
		t.Fatalf("Upgrade() = %#v, want the second item unexecuted", result.Items)
	}
}

func TestUpgradeEmptySnapshotSucceedsWithEmptyResult(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	fixture.snapshot = domain.AgentSnapshot{}
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err != nil {
		t.Fatalf("Upgrade(empty) error = %v, want success", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("Upgrade(empty) = %#v, want no items", result.Items)
	}
	// 空收敛不应获取目录快照之外的任何写入。
	for _, call := range calls {
		if strings.HasPrefix(call, "transaction:") {
			t.Fatalf("empty upgrade ran %q; want no transaction", call)
		}
	}
}

func TestUpgradeRejectsUnknownConcreteAgent(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	fixture.snapshot = domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentHermes}}
	service := newUpgradeService(t, &calls, fixture)

	_, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AgentCodex)})
	if err == nil || !strings.Contains(err.Error(), "unsupported or not installed") {
		t.Fatalf("Upgrade(unknown agent) error = %v, want unsupported agent error", err)
	}
}

func TestUpgradeRejectsExpertForNonBibitAgent(t *testing.T) {
	var calls []string
	service := newUpgradeService(t, &calls, upgradeFixture())

	_, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AgentCodex), Type: string(domain.PowerTypeExpert)})
	if err == nil || !strings.Contains(err.Error(), "only supports agent") {
		t.Fatalf("Upgrade(expert/codex) error = %v, want expert agent restriction", err)
	}
}

func TestUpgradeFailsFastWhenAgentManifestIsUnreadable(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	cause := errors.New("manifest is corrupt")
	fixture.configListErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
	service := newUpgradeService(t, &calls, fixture)

	_, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err == nil || !errors.Is(err, cause) {
		t.Fatalf("Upgrade(corrupt manifest) error = %v, want cause %v", err, cause)
	}
}

func TestUpgradeSuppressesResultWhenReleaseFails(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	fixture.releaseErr = errors.New("unlock failed")
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err == nil || !strings.Contains(err.Error(), "release upgrade lock") {
		t.Fatalf("Upgrade(release failure) error = %v, want release error", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("Upgrade(release failure) = %#v, want suppressed result", result.Items)
	}
}

func TestUpgradeMultiAgentProcessesInSnapshotOrder(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	codex := skillRef("demo", "v0.0.1")
	claude := skillRef("demo", "v0.0.1")
	fixture.snapshot = domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}}
	fixture.installed = map[domain.AgentID]domain.PowerRef{
		domain.AgentCodex: codex, domain.AgentClaudeCode: claude,
	}
	fixture.installedGroups = map[domain.AgentID]domain.InstalledPowerGroups{
		domain.AgentCodex:      groupsWith(domain.PowerTypeSkill, codex),
		domain.AgentClaudeCode: groupsWith(domain.PowerTypeSkill, claude),
	}
	fixture.targetPresence = map[domain.AgentID]transaction.TargetPresence{
		domain.AgentCodex: transaction.TargetPresent, domain.AgentClaudeCode: transaction.TargetPresent,
	}
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents)})
	if err != nil {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("Upgrade() = %#v, want two items", result.Items)
	}
	if result.Items[0].Agent != domain.AgentClaudeCode || result.Items[1].Agent != domain.AgentCodex {
		t.Fatalf("Upgrade() agent order = %q, %q; want claude-code then codex",
			result.Items[0].Agent, result.Items[1].Agent)
	}
}

func TestNewUpgradeRejectsNilDependencies(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	locker := &installFakeLocker{calls: &calls, fixture: &fixture}
	transactions := &installFakeTransactions{calls: &calls, fixture: &fixture}
	catalogs := &installFakeCatalogManager{calls: &calls, fixture: &fixture}
	cloudClient := &installFakeCloud{calls: &calls, fixture: &fixture}
	powerCache := &installFakeCache{calls: &calls, fixture: &fixture}
	pluginResources := &fakePluginResources{}
	registry := &installFakeRegistry{calls: &calls, fixture: &fixture}
	repository := &installFakeConfig{calls: &calls, fixture: &fixture}
	selector := &installFakeVersions{}

	tests := []struct {
		name string
		call func() (*UpgradeService, error)
		want string
	}{
		{name: "nil locker", want: "nil locker", call: func() (*UpgradeService, error) {
			return NewUpgrade(nil, transactions, catalogs, cloudClient, powerCache, pluginResources, registry, repository, selector)
		}},
		{name: "nil transactions", want: "nil transaction manager", call: func() (*UpgradeService, error) {
			return NewUpgrade(locker, nil, catalogs, cloudClient, powerCache, pluginResources, registry, repository, selector)
		}},
		{name: "nil catalogs", want: "nil catalog manager", call: func() (*UpgradeService, error) {
			return NewUpgrade(locker, transactions, nil, cloudClient, powerCache, pluginResources, registry, repository, selector)
		}},
		{name: "nil selector", want: "nil version selector", call: func() (*UpgradeService, error) {
			return NewUpgrade(locker, transactions, catalogs, cloudClient, powerCache, pluginResources, registry, repository, nil)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.call()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewUpgrade() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestUpgradeTypeFilterOnlyProcessesRequestedType(t *testing.T) {
	var calls []string
	fixture := upgradeFixture()
	skill := skillRef("demo", "v0.0.1")
	mcp := domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.0.1"}
	fixture.installedGroups = map[domain.AgentID]domain.InstalledPowerGroups{
		domain.AgentCodex: {
			domain.PowerTypeSkill: {domain.InstalledPower{Ref: skill}},
			domain.PowerTypeMCP:   {domain.InstalledPower{Ref: mcp}},
		},
	}
	fixture.catalogEntries = catalogEntries(
		skillRef("demo", "v0.1.0"),
		domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.1.0"},
	)
	service := newUpgradeService(t, &calls, fixture)

	result, err := service.Upgrade(context.Background(), cli.Flags{Agent: string(domain.AllAgents), Type: string(domain.PowerTypeMCP)})
	if err != nil {
		t.Fatalf("Upgrade(mcp only) error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Target.Type != domain.PowerTypeMCP {
		t.Fatalf("Upgrade(mcp only) = %#v, want only the MCP item", result.Items)
	}
}
