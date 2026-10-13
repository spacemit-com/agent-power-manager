package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"apm/internal/cache"
	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
)

// UpgradeService 实现 Upgrade 命令在单个全局锁区间内的全量升级生命周期。
// 它复用 Install 的 per-type 安装路径，不新增事务类型，也不放宽 journal 不变量。
type UpgradeService struct {
	locker       lock.Locker
	transactions transaction.Manager
	catalogs     CatalogManager
	registry     AdapterRegistry
	config       config.Repository
	versions     VersionSelector
	// installer 提供 per-type 安装路径；它与本服务共享同一依赖集合，但不单独获取锁。
	installer *InstallService
}

// NewUpgrade 创建持有完整共享状态区间的 Upgrade 生命周期。
// 逐条执行委托给一个共享同一依赖集合的 InstallService：upgrade 与 install 执行的是同一套
// per-type 安装路径，这里不复制安装逻辑，而是复用它。
func NewUpgrade(locker lock.Locker, transactions transaction.Manager, catalogs CatalogManager, cloud cloud.Client, cache cache.PowerCache, plugins pluginresource.Manager, registry AdapterRegistry, config config.Repository, versions VersionSelector) (*UpgradeService, error) {
	switch {
	case isNilInterface(locker):
		return nil, errors.New("create Upgrade lifecycle with nil locker")
	case isNilInterface(transactions):
		return nil, errors.New("create Upgrade lifecycle with nil transaction manager")
	case isNilInterface(catalogs):
		return nil, errors.New("create Upgrade lifecycle with nil catalog manager")
	case isNilInterface(registry):
		return nil, errors.New("create Upgrade lifecycle with nil adapter registry")
	case isNilInterface(config):
		return nil, errors.New("create Upgrade lifecycle with nil config repository")
	case isNilInterface(versions):
		return nil, errors.New("create Upgrade lifecycle with nil version selector")
	}
	installer, err := NewInstall(locker, transactions, catalogs, cloud, cache, plugins, registry, config, versions)
	if err != nil {
		return nil, fmt.Errorf("create Upgrade lifecycle: %w", err)
	}
	return &UpgradeService{
		locker:       locker,
		transactions: transactions,
		catalogs:     catalogs,
		registry:     registry,
		config:       config,
		versions:     versions,
		installer:    installer,
	}, nil
}

var _ cli.UpgradeLifecycle = (*UpgradeService)(nil)

// Upgrade 在一次全局锁持有期间恢复共享状态、规划全量升级并逐条执行。
//
// 与 install 的 fail-fast 不同：单条业务失败记为 failed 后继续下一条，因为批量升级中
// 一条与本机无关的失败不应阻断其余条目。真正需要停止的条件是「未收敛的全局事务」——
// 检测到残留 journal 时立即停止，剩余条目记 unexecuted，否则下一条事务无法安全创建。
//
// 确认询问发生在锁内、计划之后：ADR-045 要求计划与执行共用同一个 SnapshotLocked 结果，
// 把确认放到锁外拆成两阶段会让目录在两次获取之间漂移。
func (s *UpgradeService) Upgrade(ctx context.Context, flags cli.Flags, options cli.UpgradeOptions) (cli.UpgradeResult, error) {
	// Recover、快照、计划与逐条 Transaction 共同读写受保护状态，必须位于同一锁区间。
	unlock, err := s.locker.Acquire()
	if err != nil {
		return cli.UpgradeResult{}, fmt.Errorf("acquire upgrade lock: %w", err)
	}

	recovery, err := s.transactions.Recover(ctx)
	if err != nil {
		return cli.UpgradeResult{}, releaseUpgrade(unlock, fmt.Errorf("recover transaction before upgrade: %w", err))
	}
	logUpgradeRecovery(recovery)

	snapshot, err := s.registry.RefreshInstalled()
	if err != nil {
		return cli.UpgradeResult{}, releaseUpgrade(unlock, fmt.Errorf("refresh installed agents for upgrade: %w", err))
	}
	agents, err := upgradeAgents(flags, snapshot)
	if err != nil {
		return cli.UpgradeResult{}, releaseUpgrade(unlock, err)
	}

	// 先读本地清单：无任何已安装 Power 时 upgrade 已经收敛，不需要同步目录、更不访问网络。
	reportUpgradeProgress(options, cli.UpgradeProgress{Stage: cli.UpgradeStageReadingInstalled})
	groups, installedCount, err := s.readInstalledGroups(agents)
	if err != nil {
		return cli.UpgradeResult{}, releaseUpgrade(unlock, err)
	}
	if installedCount == 0 {
		if releaseErr := unlock.Release(); releaseErr != nil {
			return cli.UpgradeResult{}, fmt.Errorf("release upgrade lock: %w", releaseErr)
		}
		slog.Debug("no installed powers to upgrade")
		return cli.UpgradeResult{Items: []cli.UpgradeItem{}}, nil
	}

	// 计划与执行共用这一个快照，避免计划目标与执行时解析出的条目漂移。
	reportUpgradeProgress(options, cli.UpgradeProgress{Stage: cli.UpgradeStageSyncingCatalog})
	catalogSnapshot, catalogStatus, err := s.catalogs.SnapshotLocked(ctx)
	if err != nil {
		return cli.UpgradeResult{}, releaseUpgrade(unlock, fmt.Errorf("snapshot catalog for upgrade: %w", err))
	}
	warnStaleCatalog("upgrade", catalogStatus)

	reportUpgradeProgress(options, cli.UpgradeProgress{Stage: cli.UpgradeStagePlanning})
	plans := s.planAgents(groups, catalogSnapshot, flags)

	// --dry-run 只交付计划：不创建事务、不下载内容、不写 manifest 或 journal。
	if flags.DryRun {
		if releaseErr := unlock.Release(); releaseErr != nil {
			return cli.UpgradeResult{}, fmt.Errorf("release upgrade lock: %w", releaseErr)
		}
		result := cli.UpgradeResult{DryRun: true}
		for _, plan := range plans {
			result.Items = append(result.Items, upgradeItemFromPlan(plan, true))
		}
		s.logUpgradeSkips(result.Items)
		return result, nil
	}

	// 确认只针对真正需要动作的条目；已经全部最新时不无谓阻塞。
	if confirmed, confirmErr := confirmUpgrade(options, plans); confirmErr != nil {
		return cli.UpgradeResult{}, releaseUpgrade(unlock, confirmErr)
	} else if !confirmed {
		// 用户拒绝：不做任何变更，也不返回任何业务项。
		if releaseErr := unlock.Release(); releaseErr != nil {
			return cli.UpgradeResult{}, errors.Join(cli.ErrUpgradeDeclined, fmt.Errorf("release upgrade lock: %w", releaseErr))
		}
		slog.Debug("upgrade declined by user")
		return cli.UpgradeResult{}, cli.ErrUpgradeDeclined
	}

	items := make([]cli.UpgradeItem, 0, len(plans))
	failures := make([]cli.UpgradeFailure, 0)
	stopped := false
	executed := 0
	total := countUpgradeActions(plans)
	for index, plan := range plans {
		if stopped {
			items = append(items, upgradeUnexecutedItem(plan))
			continue
		}
		if plan.action == planActionUpgrade {
			reportUpgradeProgress(options, cli.UpgradeProgress{
				Stage:   cli.UpgradeStageExecuting,
				Agent:   plan.agent,
				Power:   plan.target,
				Current: plan.current,
				Index:   executed,
				Total:   total,
			})
		}
		item, itemErr := s.executeUpgradePlan(ctx, plan, catalogSnapshot)
		items = append(items, item)
		if plan.action == planActionUpgrade {
			executed++
		}
		if itemErr == nil {
			continue
		}
		failures = append(failures, cli.UpgradeFailure{Agent: plan.agent, Power: plan.target, Err: itemErr})
		// 残留 journal 时继续下一条会撞 ErrActiveTransaction，必须先停止并提示 apm fix。
		if s.hasResidualJournal(ctx, itemErr) {
			stopped = true
			slog.Warn(
				"upgrade stopped because a transaction is still active",
				logging.Agent(plan.agent),
				logging.Power(plan.target.Name),
				logging.Version(plan.target.Version),
				slog.Int("remaining", len(plans)-index-1),
				slog.String("hint", "run apm fix"),
			)
		}
	}

	// Release 失败时受保护状态不可确认，必须抑制全部可渲染结果并保留已发生的失败原因。
	if releaseErr := unlock.Release(); releaseErr != nil {
		return cli.UpgradeResult{}, errors.Join(
			upgradeBatchErrorOrNil(failures),
			fmt.Errorf("release upgrade lock: %w", releaseErr),
		)
	}
	result := cli.UpgradeResult{Items: items}
	if len(failures) != 0 {
		return result, &cli.UpgradeBatchError{Failures: failures}
	}
	return result, nil
}

// confirmUpgrade 在锁内、计划之后询问用户是否继续。
// Confirm 为 nil 或没有任何需要动作的条目时直接视为已确认：对已最新的系统提问只会无谓阻塞。
func confirmUpgrade(options cli.UpgradeOptions, plans []upgradePlanItem) (bool, error) {
	if options.Confirm == nil {
		return true, nil
	}
	prompt := cli.UpgradePrompt{}
	for _, plan := range plans {
		switch plan.action {
		case planActionUpgrade:
			prompt.Upgrades = append(prompt.Upgrades, upgradeItemFromPlan(plan, true))
		case planActionUpToDate:
			prompt.UpToDate++
		case planActionSkip:
			prompt.Skipped++
		}
	}
	if len(prompt.Upgrades) == 0 {
		return true, nil
	}
	confirmed, err := options.Confirm(prompt)
	if err != nil {
		return false, fmt.Errorf("confirm upgrade: %w", err)
	}
	return confirmed, nil
}

// countUpgradeActions 统计真正需要执行事务的条目数，用于进度分母。
func countUpgradeActions(plans []upgradePlanItem) int {
	total := 0
	for _, plan := range plans {
		if plan.action == planActionUpgrade {
			total++
		}
	}
	return total
}

// reportUpgradeProgress 转发一次进度事件；回调缺失即为静默。
func reportUpgradeProgress(options cli.UpgradeOptions, progress cli.UpgradeProgress) {
	if options.Progress != nil {
		options.Progress(progress)
	}
}

// readInstalledGroups 读取每个目标 Agent 的本地清单；任一清单不可读即为 fatal。
// 同时返回已安装 Power 总数，使「空清单」可以在同步目录之前判定。
func (s *UpgradeService) readInstalledGroups(agents []domain.AgentID) (map[domain.AgentID]domain.InstalledPowerGroups, int, error) {
	groups := make(map[domain.AgentID]domain.InstalledPowerGroups, len(agents))
	total := 0
	for _, agent := range agents {
		installed, err := s.config.List(agent)
		if err != nil {
			return nil, 0, fmt.Errorf("list installed powers for agent %q: %w", agent, err)
		}
		groups[agent] = installed
		for _, powers := range installed {
			total += len(powers)
		}
	}
	return groups, total, nil
}

// planAgents 按 Agent 顺序为每个目标 Agent 生成升级计划。
func (s *UpgradeService) planAgents(groups map[domain.AgentID]domain.InstalledPowerGroups, catalogSnapshot catalog.Snapshot, flags cli.Flags) []upgradePlanItem {
	types := upgradePlanPowerTypes()
	if flags.Type != "" {
		types = []domain.PowerType{domain.PowerType(flags.Type)}
	}
	plans := make([]upgradePlanItem, 0)
	// 按 agents 的原始顺序遍历，保证计划顺序与快照顺序一致。
	for _, agent := range s.orderedAgentKeys(groups) {
		plans = append(plans, planUpgrade(agent, groups[agent], catalogSnapshot, s.versions, types)...)
	}
	return plans
}

// orderedAgentKeys 以 Agent ID 字典序返回清单分组键，使计划顺序确定。
func (s *UpgradeService) orderedAgentKeys(groups map[domain.AgentID]domain.InstalledPowerGroups) []domain.AgentID {
	agents := make([]domain.AgentID, 0, len(groups))
	for agent := range groups {
		agents = append(agents, agent)
	}
	slices.Sort(agents)
	return agents
}

// executeUpgradePlan 执行单条计划：无需动作的条目直接产出结果，需升级的条目走 install 的 per-type 路径。
func (s *UpgradeService) executeUpgradePlan(ctx context.Context, plan upgradePlanItem, catalogSnapshot catalog.Snapshot) (cli.UpgradeItem, error) {
	switch plan.action {
	case planActionUpToDate:
		return upgradeItemFromPlan(plan, false), nil
	case planActionSkip:
		item := upgradeItemFromPlan(plan, false)
		logUpgradeSkip(item)
		return item, nil
	default:
		installed, err := s.installer.installForAgent(ctx, plan.agent, plan.entry, catalogSnapshot)
		if err != nil {
			item := upgradeItemFromPlan(plan, false)
			item.Outcome = cli.UpgradeOutcomeFailed
			return item, err
		}
		return cli.UpgradeItem{
			Agent:   plan.agent,
			Current: plan.current,
			Target:  installed.Power,
			Outcome: cli.UpgradeOutcomeFromInstall(installed.Outcome),
		}, nil
	}
}

// hasResidualJournal 报告一次升级失败是否留下未收敛的全局事务。
// ErrActiveTransaction 是直接判据；Inspect 作为兜底覆盖回滚失败但错误已包装的情形。
func (s *UpgradeService) hasResidualJournal(ctx context.Context, err error) bool {
	if errors.Is(err, transaction.ErrActiveTransaction) {
		return true
	}
	inspection, inspectErr := s.transactions.Inspect(ctx)
	if inspectErr != nil {
		return false
	}
	return inspection.Active
}

// logUpgradeSkips 在计划阶段一次性记录全部跳过原因（--dry-run 交付前的可读提示）。
func (s *UpgradeService) logUpgradeSkips(items []cli.UpgradeItem) {
	for _, item := range items {
		if item.Outcome == cli.UpgradeOutcomeSkipped {
			logUpgradeSkip(item)
		}
	}
}

// logUpgradeSkip 记录一条跳过原因；warnMissing 语义的降级/缺失提醒走 Warn。
func logUpgradeSkip(item cli.UpgradeItem) {
	slog.Warn("upgrade skipped power",
		logging.Agent(item.Agent),
		slog.String("type", string(item.Target.Type)),
		logging.Power(item.Target.Name),
		logging.Version(item.Current.Version),
		slog.String("reason", item.Skip),
	)
}

// upgradeItemFromPlan 把计划项映射为结果项。
// dryRun 为真时，「需要升级」的条目呈现为 planned，不声称变更已发生。
func upgradeItemFromPlan(plan upgradePlanItem, dryRun bool) cli.UpgradeItem {
	item := cli.UpgradeItem{
		Agent:   plan.agent,
		Current: plan.current,
		Target:  plan.target,
		Skip:    plan.reason,
	}
	switch plan.action {
	case planActionUpToDate:
		item.Outcome = cli.UpgradeOutcomeUpToDate
		item.Skip = ""
	case planActionSkip:
		item.Outcome = cli.UpgradeOutcomeSkipped
	default:
		if dryRun {
			item.Outcome = cli.UpgradeOutcomePlanned
		} else {
			item.Outcome = cli.UpgradeOutcomeUnexecuted
		}
		item.Skip = ""
	}
	return item
}

func upgradeUnexecutedItem(plan upgradePlanItem) cli.UpgradeItem {
	return cli.UpgradeItem{
		Agent:   plan.agent,
		Current: plan.current,
		Target:  plan.target,
		Outcome: cli.UpgradeOutcomeUnexecuted,
	}
}

func upgradeBatchErrorOrNil(failures []cli.UpgradeFailure) error {
	if len(failures) == 0 {
		return nil
	}
	return &cli.UpgradeBatchError{Failures: failures}
}

// upgradeAgents 解析具体 Agent 或 all；与 install 不同，空快照不是错误——upgrade 的请求是收敛现状。
func upgradeAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	if flags.Type == string(domain.PowerTypeExpert) && flags.Agent != "" && domain.AgentID(flags.Agent) != domain.AgentBibit {
		return nil, fmt.Errorf("upgrade power type %q only supports agent %q", domain.PowerTypeExpert, domain.AgentBibit)
	}
	target := domain.AgentID(flags.Agent)
	if flags.Type == string(domain.PowerTypeExpert) && flags.Agent == "" {
		target = domain.AgentBibit
	}
	if target == domain.AllAgents {
		return snapshot.Ordered(), nil
	}
	if !snapshot.Contains(target) {
		return nil, fmt.Errorf("upgrade powers for agent %q: unsupported or not installed%s", target, availableAgentSuffix(flags, snapshot))
	}
	return []domain.AgentID{target}, nil
}

func releaseUpgrade(unlock lock.Unlock, businessErr error) error {
	if err := unlock.Release(); err != nil {
		return errors.Join(businessErr, fmt.Errorf("release upgrade lock: %w", err))
	}
	return businessErr
}

func logUpgradeRecovery(result transaction.RecoveryResult) {
	if !result.Recovered {
		slog.Debug("no active transaction before upgrade")
		return
	}
	slog.Warn(
		"transaction recovered",
		slog.String("operation", string(result.Operation)),
		logging.Agent(result.Agent),
		logging.Power(result.Power.Name),
		logging.Version(result.Power.Version),
		logging.Phase(string(result.OriginalPhase)),
		slog.String("direction", string(result.Direction)),
		slog.Any("actions", result.Actions),
	)
}
