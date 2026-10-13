package lifecycle

import (
	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/domain"

	"github.com/Masterminds/semver/v3"
)

// upgradePlanAction 表示计划对一条已安装 Power 采取的本地动作。
type upgradePlanAction int

const (
	// planUpgrade 表示需要把本地版本替换为计划目标版本。
	planActionUpgrade upgradePlanAction = iota
	// planActionUpToDate 表示本地版本已是目录最新版本，无需任何动作。
	planActionUpToDate
	// planActionSkip 表示计划有意不动该条，原因见 upgradePlanItem.reason。
	planActionSkip
)

// upgradePlanItem 是一次全量升级计划中的一条。
// entry 仅在 action 为 planUpgrade 时有效，并且与执行期交给 Adapter/Transaction 的是同一份内容。
type upgradePlanItem struct {
	agent   domain.AgentID
	current domain.PowerRef
	target  domain.PowerRef
	entry   domain.CatalogEntry
	action  upgradePlanAction
	reason  string
}

// upgradePlanPowerTypes 固定计划遍历顺序，使输出与 --dry-run 结果可复现。
// 顺序与本地清单分组顺序一致：skill、plugin、mcp、expert。
func upgradePlanPowerTypes() []domain.PowerType {
	return []domain.PowerType{
		domain.PowerTypeSkill,
		domain.PowerTypePlugin,
		domain.PowerTypeMCP,
		domain.PowerTypeExpert,
	}
}

// planUpgrade 是纯函数：不执行任何 I/O，仅把「本地清单事实」与「已验证的不可变目录快照」映射成有序升级计划。
//
// 决策规则：
//   - 目录中已无同名候选 → skip(not-in-catalog)：没有可升的目标版本。
//   - 目录最新版本等于本地版本 → up-to-date。
//   - 目录最新版本低于本地版本 → skip(newer-local)；升级命令不做降级。
//   - 目录最新版本高于本地版本 → upgrade，并携带该版本的完整 CatalogEntry。
//
// types 为空时处理全部类别；组内保持清单原序，同名重复条目各自成条。
func planUpgrade(agent domain.AgentID, groups domain.InstalledPowerGroups, catalogSnapshot catalog.Snapshot, selector VersionSelector, types []domain.PowerType) []upgradePlanItem {
	if len(types) == 0 {
		types = upgradePlanPowerTypes()
	}
	plans := make([]upgradePlanItem, 0)
	for _, powerType := range types {
		candidatesByType := catalogSnapshot.List(powerType)
		for _, installed := range groups[powerType] {
			plans = append(plans, planUpgradePower(agent, installed.Ref, candidatesByType, catalogSnapshot, selector))
		}
	}
	return plans
}

// planUpgradePower 为单条已安装引用生成计划项；无法安全升级的情况一律降级为可读的跳过原因。
func planUpgradePower(agent domain.AgentID, current domain.PowerRef, candidates []domain.CatalogEntry, catalogSnapshot catalog.Snapshot, selector VersionSelector) upgradePlanItem {
	item := upgradePlanItem{agent: agent, current: current, target: current}

	versions := make([]string, 0, len(candidates))
	for _, entry := range candidates {
		if entry.Ref.Name == current.Name {
			versions = append(versions, entry.Ref.Version)
		}
	}
	if len(versions) == 0 {
		item.action = planActionSkip
		item.reason = cli.UpgradeSkipNotInCatalog
		return item
	}

	latest, err := selector.Select(versions, "")
	if err != nil {
		// 目录候选已在解码时校验过规范版本，选择失败视为目录状态异常，按不可升处理。
		item.action = planActionSkip
		item.reason = cli.UpgradeSkipNotInCatalog
		return item
	}
	if latest == current.Version {
		item.action = planActionUpToDate
		return item
	}

	// 跳过时 target 保持 current：它表示「本次执行后本地会停留的版本」，
	// 与成功升级时的目标版本语义一致。
	if !isVersionGreater(latest, current.Version) {
		// 只升不降：本地领先时保留现状并交由调用方告警。
		item.action = planActionSkip
		item.reason = cli.UpgradeSkipNewerLocal
		return item
	}

	// 目录已下架本地当前版本时，只有 MCP 的版本切换会因此失败：installMCP 在替换路径上需要旧版本
	// metadata 以支持回滚（见 installMCP 的 actionReplace）。其余类型的替换不依赖旧目录条目，
	// 因此仍可正常升级；把这条限制限制在真正需要它的类型上，避免无谓地阻断升级。
	if !containsVersion(versions, current.Version) && requiresPreviousCatalogEntry(current.Type) {
		item.action = planActionSkip
		item.reason = cli.UpgradeSkipNotInCatalog
		return item
	}

	target := domain.PowerRef{Name: current.Name, Type: current.Type, Version: latest}
	entry, err := catalogSnapshot.Resolve(target)
	if err != nil {
		item.action = planActionSkip
		item.reason = cli.UpgradeSkipNotInCatalog
		return item
	}
	item.target = target
	item.entry = entry
	item.action = planActionUpgrade
	return item
}

// requiresPreviousCatalogEntry 报告某类型的版本切换是否必须能读到旧版本的目录条目。
// 只有 MCP 如此：installMCP 在替换路径上调用 snapshot.Metadata(previous.Ref) 取旧定义用于回滚。
func requiresPreviousCatalogEntry(powerType domain.PowerType) bool {
	return powerType == domain.PowerTypeMCP
}

func containsVersion(versions []string, version string) bool {
	for _, candidate := range versions {
		if candidate == version {
			return true
		}
	}
	return false
}

// isVersionGreater 报告 left 是否严格高于 right。
// 两侧版本都已分别由目录解码与清单读取校验为规范 vMAJOR.MINOR.PATCH，故与 install 的分类路径
// 保持一致直接解析，不再自建比较器。
func isVersionGreater(left, right string) bool {
	return semver.MustParse(left).GreaterThan(semver.MustParse(right))
}
