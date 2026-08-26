package lifecycle

import (
	"errors"

	"apm/internal/adapter"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
)

// pluginTargetFacts 描述一次 Plugin 目标位置的事实与分类状态。
// marketplace 仅在 marketplace 部署模式下非空；filesystem 模式保持 nil。
type pluginTargetFacts struct {
	state       targetState
	plugin      adapter.PluginFact
	marketplace *adapter.MarketplaceFact
}

// classifyMarketplacePluginTarget 将 marketplace/Plugin 双事实与预期身份比较得到目标状态。
// wrong source/selector 是冲突；单侧受管状态是不完整；双事实齐备但缓存根缺失且清单已建立所有权时，
// 缺失的必要 source 同样归为不完整。runtimeName 为 Plugin 的版本无关实际名，expected 来自 Locate。
func classifyMarketplacePluginTarget(
	runtimeName string,
	expected pluginresource.Marketplace,
	marketplaceFact adapter.MarketplaceFact,
	pluginFact adapter.PluginFact,
	cacheExists bool,
) pluginTargetFacts {
	facts := pluginTargetFacts{plugin: pluginFact, marketplace: &marketplaceFact}
	marketplacePresent := marketplaceFact.Presence == adapter.MarketplacePresent
	marketplaceAbsent := marketplaceFact.Presence == adapter.MarketplaceAbsent
	pluginPresent := pluginFact.Presence == adapter.PluginPresent
	pluginAbsent := pluginFact.Presence == adapter.PluginAbsent

	// 错源、错名、错 selector 均为冲突。
	if marketplacePresent && (marketplaceFact.Name != expected.Name || marketplaceFact.Source != expected.Source) {
		facts.state = targetConflict
		return facts
	}
	if marketplaceAbsent && marketplaceFact.Name != expected.Name {
		facts.state = targetConflict
		return facts
	}
	if pluginPresent && (pluginFact.ActualName != runtimeName || pluginFact.Selector != runtimeName) {
		facts.state = targetConflict
		return facts
	}
	if pluginAbsent && pluginFact.ActualName != runtimeName {
		facts.state = targetConflict
		return facts
	}

	switch {
	case marketplaceAbsent && pluginAbsent:
		facts.state = targetAbsent
	case marketplacePresent && pluginPresent && cacheExists:
		facts.state = targetComplete
	case marketplacePresent && pluginPresent && !cacheExists:
		facts.state = targetIncomplete
	default:
		// marketplace-only 或 Plugin-only 的单侧受管状态。
		facts.state = targetIncomplete
	}
	return facts
}

// filesystemPluginPhysicalState 表示 filesystem Plugin 目标路径的物理状态。
type filesystemPluginPhysicalState int

const (
	filesystemPhysicalAbsent    filesystemPluginPhysicalState = iota // 物理缺失
	filesystemPhysicalDirectory                                      // 物理目录
	filesystemPhysicalUnsafe                                         // symlink、普通文件或不可安全复用
)

// probeFilesystemPluginTarget 复验 filesystem Plugin 目标路径的物理状态。
// 非物理目录（symlink/普通文件）直接以 ErrUnsafePath 失败，不在状态分类中继续决策。
func probeFilesystemPluginTarget(target string) (filesystemPluginPhysicalState, error) {
	err := transaction.ValidateFilesystemPluginTargetAbsent(target)
	switch {
	case err == nil:
		return filesystemPhysicalAbsent, nil
	case errors.Is(err, transaction.ErrUnsafePath):
		return filesystemPhysicalUnsafe, err
	case errors.Is(err, transaction.ErrIndeterminateState):
		return filesystemPhysicalDirectory, nil
	default:
		return filesystemPhysicalUnsafe, err
	}
}

// classifyFilesystemPluginTarget 将 filesystem Adapter 事实与物理路径事实映射到同一套状态。
// Adapter 报告 absent 但物理节点仍在是冲突；注册存在但目录缺失是单侧受管的不完整状态。
func classifyFilesystemPluginTarget(
	runtimeName string,
	pluginFact adapter.PluginFact,
	physical filesystemPluginPhysicalState,
) pluginTargetFacts {
	facts := pluginTargetFacts{plugin: pluginFact}
	pluginPresent := pluginFact.Presence == adapter.PluginPresent
	pluginAbsent := pluginFact.Presence == adapter.PluginAbsent

	if pluginPresent && (pluginFact.ActualName != runtimeName || pluginFact.Selector != runtimeName) {
		facts.state = targetConflict
		return facts
	}
	if pluginAbsent && pluginFact.ActualName != runtimeName {
		facts.state = targetConflict
		return facts
	}

	switch {
	case pluginAbsent && physical == filesystemPhysicalAbsent:
		facts.state = targetAbsent
	case pluginAbsent && physical == filesystemPhysicalDirectory:
		// Adapter 报告 absent 但受管目录仍在。
		facts.state = targetConflict
	case pluginAbsent && physical == filesystemPhysicalUnsafe:
		facts.state = targetConflict
	case pluginPresent && physical == filesystemPhysicalDirectory:
		facts.state = targetComplete
	case pluginPresent && physical == filesystemPhysicalAbsent:
		facts.state = targetIncomplete
	default:
		facts.state = targetConflict
	}
	return facts
}
