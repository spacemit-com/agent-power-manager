package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

// lifecycleContractCase 描述四类 Power 面对同一 manifest/target 入口状态时的一致宏观行为。
// manifest 与 target 使用与共享状态策略一致的字符串标签；wantInstall/wantUninstall 只在对应
// 方向被断言，wantWarning 是期望出现的唯一 WARN 消息，wantPublicError 是期望的公共业务 sentinel。
type lifecycleContractCase struct {
	name            string
	manifest        string
	target          string
	wantInstall     cli.InstallOutcome
	wantUninstall   cli.UninstallOutcome
	wantWarning     string
	wantPublicError error
}

const (
	contractName            = "demo"
	contractTargetVersion   = "v2.0.0"
	contractPreviousVersion = "v1.0.0"
	contractRemovedVersion  = "v1.2.3"
)

// contractPower 描述一种 Power 类型在契约表中的目标 Agent 与引用。
type contractPower struct {
	powerType domain.PowerType
	agent     domain.AgentID
}

// contractPowers 是跨类型契约测试统一运行的四种能力；Plugin 的 incomplete 状态并入缺失行，
// 不构成独立例外。
var contractPowers = []contractPower{
	{powerType: domain.PowerTypeSkill, agent: domain.AgentCodex},
	{powerType: domain.PowerTypeExpert, agent: domain.AgentBibit},
	{powerType: domain.PowerTypeMCP, agent: domain.AgentCodex},
	{powerType: domain.PowerTypePlugin, agent: domain.AgentCodex},
}

// TestLifecycleStateContract 以同一 install/uninstall 入口矩阵驱动四类 Power，证明宏观行为一致。
func TestLifecycleStateContract(t *testing.T) {
	installCases := []lifecycleContractCase{
		{name: "absent over absent installs", manifest: "absent", target: "absent", wantInstall: cli.InstallOutcomeInstalled},
		{name: "absent over present rejects with only target conflict", manifest: "absent", target: "present", wantInstall: cli.InstallOutcomeFailed, wantPublicError: adapter.ErrTargetConflict},
		{name: "same version any target is unchanged with zero probe", manifest: "same", target: "any", wantInstall: cli.InstallOutcomeUnchanged},
		{name: "different version over complete upgrades", manifest: "different", target: "complete", wantInstall: cli.InstallOutcomeUpgraded},
		{name: "different version over absent warns and installs", manifest: "different", target: "absent", wantInstall: cli.InstallOutcomeInstalled, wantWarning: "installed power target is missing"},
	}
	uninstallCases := []lifecycleContractCase{
		{name: "absent manifest any target warns and stays unexecuted", manifest: "absent", target: "any", wantUninstall: cli.UninstallOutcomeUnexecuted, wantWarning: "power is not installed"},
		{name: "present manifest complete target completes", manifest: "present", target: "complete", wantUninstall: cli.UninstallOutcomeCompleted},
		{name: "present manifest absent target warns and completes", manifest: "present", target: "absent", wantUninstall: cli.UninstallOutcomeCompleted, wantWarning: "installed power target is missing"},
	}

	for _, power := range contractPowers {
		power := power
		t.Run(string(power.powerType), func(t *testing.T) {
			t.Run("install", func(t *testing.T) {
				cases := installCases
				if power.powerType == domain.PowerTypePlugin {
					cases = append(cases, lifecycleContractCase{
						name: "different version over incomplete warns and installs", manifest: "different", target: "incomplete",
						wantInstall: cli.InstallOutcomeInstalled, wantWarning: "installed power target is missing",
					})
				}
				if power.powerType == domain.PowerTypeExpert {
					// 目录型 Agent 的文件级缺陷（缺 agent.yaml / 缺被引用 avatar / 多余
					// 或不一致资源）都收敛为同一个"缺失/不完整受管目标"：
					// 一次 target-missing 告警 + 首次安装语义 publish，outcome 仍为 installed，
					// 绝不产生第二条告警或降级为 missing。
					cases = append(cases,
						lifecycleContractCase{name: "different version over missing agent.yaml warns and installs", manifest: "different", target: "incomplete",
							wantInstall: cli.InstallOutcomeInstalled, wantWarning: "installed power target is missing"},
						lifecycleContractCase{name: "different version over missing referenced avatar warns and installs", manifest: "different", target: "incomplete",
							wantInstall: cli.InstallOutcomeInstalled, wantWarning: "installed power target is missing"},
						lifecycleContractCase{name: "different version over extra inconsistent assets warns and installs", manifest: "different", target: "incomplete",
							wantInstall: cli.InstallOutcomeInstalled, wantWarning: "installed power target is missing"},
					)
				}
				for _, tc := range cases {
					tc := tc
					t.Run(tc.name, func(t *testing.T) {
						runContractInstallCase(t, power, tc)
					})
				}
			})
			t.Run("uninstall", func(t *testing.T) {
				cases := uninstallCases
				if power.powerType == domain.PowerTypePlugin {
					cases = append(cases, lifecycleContractCase{
						name: "present manifest incomplete target warns and completes", manifest: "present", target: "incomplete",
						wantUninstall: cli.UninstallOutcomeCompleted, wantWarning: "installed power target is missing",
					})
				}
				if power.powerType == domain.PowerTypeExpert {
					// 与 install 方向一致：目录型缺陷在卸载方向同样只产生一次
					// target-missing 告警，并清理 manifest 后以 completed 收敛。
					cases = append(cases,
						lifecycleContractCase{name: "present manifest missing agent.yaml warns and completes", manifest: "present", target: "incomplete",
							wantUninstall: cli.UninstallOutcomeCompleted, wantWarning: "installed power target is missing"},
						lifecycleContractCase{name: "present manifest missing referenced avatar warns and completes", manifest: "present", target: "incomplete",
							wantUninstall: cli.UninstallOutcomeCompleted, wantWarning: "installed power target is missing"},
						lifecycleContractCase{name: "present manifest extra inconsistent assets warns and completes", manifest: "present", target: "incomplete",
							wantUninstall: cli.UninstallOutcomeCompleted, wantWarning: "installed power target is missing"},
					)
				}
				for _, tc := range cases {
					tc := tc
					t.Run(tc.name, func(t *testing.T) {
						runContractUninstallCase(t, power, tc)
					})
				}
			})
		})
	}
}

// runContractInstallCase 以契约表一行驱动完整 Install，并断言 outcome、warning 与公共错误。
func runContractInstallCase(t *testing.T, power contractPower, tc lifecycleContractCase) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	contractTestLogger(t, &stdout, &stderr)
	var calls []string
	service := newInstallService(t, &calls, contractInstallFixture(power, tc.manifest, tc.target))

	result, err := service.Install(context.Background(), cli.Flags{
		Agent: string(power.agent), Type: string(power.powerType), Name: contractName,
	})

	if tc.wantPublicError != nil {
		if err == nil {
			t.Fatalf("Install() error = nil, want %v", tc.wantPublicError)
		}
		if !errors.Is(err, tc.wantPublicError) {
			t.Fatalf("Install() error = %v, want %v", err, tc.wantPublicError)
		}
		assertContractSingleOutcome(t, result.Items, cli.InstallOutcomeFailed)
		assertContractForbiddenSentinels(t, err)
		assertContractWarnings(t, stderr.String(), "")
		return
	}
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	assertContractSingleOutcome(t, result.Items, tc.wantInstall)
	assertContractWarnings(t, stderr.String(), tc.wantWarning)
	if tc.manifest == "same" {
		assertZeroProbe(t, calls)
	}
}

// runContractUninstallCase 以契约表一行驱动完整 Uninstall，并断言 outcome 与 warning。
func runContractUninstallCase(t *testing.T, power contractPower, tc lifecycleContractCase) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	contractTestLogger(t, &stdout, &stderr)
	var calls []string
	service := newUninstallService(t, &calls, contractUninstallFixture(power, tc.manifest, tc.target))

	result, err := service.Uninstall(context.Background(), cli.Flags{
		Agent: string(power.agent), Type: string(power.powerType), Name: contractName,
	})
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	assertContractSingleUninstallOutcome(t, result.Items, tc.wantUninstall)
	assertContractWarnings(t, stderr.String(), tc.wantWarning)
	if tc.manifest == "absent" {
		assertZeroProbe(t, calls)
	}
}

// contractInstallFixture 根据类型、manifest 与 target 标签构建 Install 契约 fixture。
func contractInstallFixture(power contractPower, manifest, target string) installFixture {
	targetRef := domain.PowerRef{Name: contractName, Type: power.powerType, Version: contractTargetVersion}
	previousRef := domain.PowerRef{Name: contractName, Type: power.powerType, Version: contractPreviousVersion}
	fixture := installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{power.agent}},
		cloudRefs: []domain.PowerRef{targetRef},
		selected:  contractTargetVersion,
	}
	switch manifest {
	case "same":
		fixture.installed = map[domain.AgentID]domain.PowerRef{power.agent: targetRef}
	case "different":
		fixture.installed = map[domain.AgentID]domain.PowerRef{power.agent: previousRef}
	}

	switch power.powerType {
	case domain.PowerTypeSkill, domain.PowerTypeExpert:
		// 目录型目标（Skill 的 SKILL.md、Agent 的 agent.yaml+assets）在生命周期层
		// 只探测位置存在性；文件级缺陷（缺清单/缺 avatar/多余资源）一律建模为
		// TargetAbsent，与共享状态策略的 incomplete 分支收敛为同一条缺失告警路径。
		switch target {
		case "present", "complete":
			fixture.targetPresence = map[domain.AgentID]transaction.TargetPresence{power.agent: transaction.TargetPresent}
		default:
			fixture.targetPresence = map[domain.AgentID]transaction.TargetPresence{power.agent: transaction.TargetAbsent}
		}
	case domain.PowerTypeMCP:
		fixture.mcpDefinitions = map[string]domain.MCPServer{
			contractPreviousVersion: {Transport: domain.MCPTransportStdio, Command: contractName},
			contractTargetVersion:   {Transport: domain.MCPTransportStdio, Command: contractName},
		}
		if target == "present" || target == "complete" {
			fixture.mcpPresence = map[domain.AgentID]adapter.MCPPresence{power.agent: adapter.MCPPresent}
		} else {
			fixture.mcpPresence = map[domain.AgentID]adapter.MCPPresence{power.agent: adapter.MCPAbsent}
		}
	case domain.PowerTypePlugin:
		fixture.pluginMode = domain.PluginDeploymentMarketplace
		classifyVersion := contractTargetVersion
		if manifest == "different" {
			classifyVersion = contractPreviousVersion
		}
		source := contractPluginSource(classifyVersion)
		switch target {
		case "present":
			fixture.pluginFacts = map[string]adapter.PluginFact{
				contractName: {Presence: adapter.PluginPresent, ActualName: contractName, Selector: contractName + "@" + contractMarketplaceName},
			}
		case "complete":
			fixture.marketplaceFacts = map[string]adapter.MarketplaceFact{
				contractMarketplaceName: {Presence: adapter.MarketplacePresent, Name: contractMarketplaceName, Source: source},
			}
			fixture.pluginFacts = map[string]adapter.PluginFact{
				contractName: {Presence: adapter.PluginPresent, ActualName: contractName, Selector: contractName + "@" + contractMarketplaceName},
			}
		case "incomplete":
			fixture.marketplaceFacts = map[string]adapter.MarketplaceFact{
				contractMarketplaceName: {Presence: adapter.MarketplacePresent, Name: contractMarketplaceName, Source: source},
			}
		}
	}
	return fixture
}

// contractUninstallFixture 根据类型、manifest 与 target 标签构建 Uninstall 契约 fixture。
func contractUninstallFixture(power contractPower, manifest, target string) uninstallFixture {
	ref := domain.PowerRef{Name: contractName, Type: power.powerType, Version: contractRemovedVersion}
	fixture := uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{power.agent}},
	}
	if manifest == "present" {
		fixture.groups = map[domain.AgentID]domain.InstalledPowerGroups{
			power.agent: {power.powerType: {domain.InstalledPower{Ref: ref}}},
		}
		// 目录型缺陷（缺 agent.yaml / 缺 avatar / 多余资源）与位置缺失一样
		// 建模为 TargetMissing，卸载方向只产生一次告警并收敛为 completed。
		targetMissing := target == "absent" || target == "incomplete"
		switch power.powerType {
		case domain.PowerTypeMCP:
			presence := adapter.MCPPresent
			if targetMissing {
				presence = adapter.MCPAbsent
			}
			fixture.mcpPresence = map[domain.AgentID]adapter.MCPPresence{power.agent: presence}
			fixture.transactionMCPResults = map[domain.AgentID]transaction.UninstallResult{
				power.agent: {TargetMissing: targetMissing},
			}
		case domain.PowerTypePlugin:
			fixture.transactionResults = map[domain.AgentID]transaction.UninstallResult{
				power.agent: {TargetMissing: targetMissing},
			}
			switch target {
			case "complete":
				fixture.marketplaceFacts = map[string]adapter.MarketplaceFact{
					contractMarketplaceName: {Presence: adapter.MarketplacePresent, Name: contractMarketplaceName, Source: contractPluginSource(contractRemovedVersion)},
				}
				fixture.pluginFact = adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: contractName, Selector: contractName + "@" + contractMarketplaceName}
			case "incomplete":
				fixture.marketplaceFacts = map[string]adapter.MarketplaceFact{
					contractMarketplaceName: {Presence: adapter.MarketplacePresent, Name: contractMarketplaceName, Source: contractPluginSource(contractRemovedVersion)},
				}
			}
		default:
			fixture.transactionResults = map[domain.AgentID]transaction.UninstallResult{
				power.agent: {TargetMissing: targetMissing},
			}
		}
	}
	return fixture
}

const contractMarketplaceName = "apm-demo"

func contractPluginSource(version string) string {
	return "/cache/plugins/" + contractName + "/" + version
}

// assertContractSingleOutcome 断言 Install 恰好返回一个指定 outcome 的条目。
func assertContractSingleOutcome(t *testing.T, items []cli.InstallItem, want cli.InstallOutcome) {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("Install().Items = %#v, want exactly one item", items)
	}
	if items[0].Outcome != want {
		t.Fatalf("Install() outcome = %q, want %q", items[0].Outcome, want)
	}
}

// assertContractSingleUninstallOutcome 断言 Uninstall 恰好返回一个指定 outcome 的条目。
func assertContractSingleUninstallOutcome(t *testing.T, items []cli.UninstallItem, want cli.UninstallOutcome) {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("Uninstall().Items = %#v, want exactly one item", items)
	}
	if items[0].Outcome != want {
		t.Fatalf("Uninstall() outcome = %q, want %q", items[0].Outcome, want)
	}
}

// assertContractWarnings 断言 stderr 恰好包含期望的一条 WARN（wantWarning 为空时断言无 WARN）。
func assertContractWarnings(t *testing.T, stderr, wantWarning string) {
	t.Helper()
	if wantWarning == "" {
		if strings.Contains(stderr, "level=WARN") {
			t.Fatalf("stderr = %q, want no warning", stderr)
		}
		return
	}
	if strings.Count(stderr, "level=WARN") != 1 {
		t.Fatalf("stderr = %q, want exactly one warning %q", stderr, wantWarning)
	}
	if !strings.Contains(stderr, wantWarning) {
		t.Fatalf("stderr = %q, want warning %q", stderr, wantWarning)
	}
}

// assertContractForbiddenSentinels 断言冲突错误只暴露公共 adapter.ErrTargetConflict，
// 不额外暴露 MCP/Plugin 专属 conflict sentinel。
func assertContractForbiddenSentinels(t *testing.T, err error) {
	t.Helper()
	for _, forbidden := range []error{adapter.ErrMCPConflict, adapter.ErrPluginConflict, adapter.ErrMarketplaceConflict} {
		if errors.Is(err, forbidden) {
			t.Fatalf("error = %v, must not expose %v", err, forbidden)
		}
	}
}

// assertZeroProbe 断言调用序列没有发生任何目标探测（filesystem inspect、MCP Inspect 或 Plugin/marketplace Inspect）。
func assertZeroProbe(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(call, "inspect") {
			t.Fatalf("calls = %v, must not contain any probe event", calls)
		}
	}
}

func contractTestLogger(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	original := slog.Default()
	slog.SetDefault(logging.NewLogger(stdout, stderr, false))
	t.Cleanup(func() { slog.SetDefault(original) })
}
