package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"apm/internal/domain"
)

// UpgradeOutcome 表示单条已安装 Power 的一次升级可见结果。
type UpgradeOutcome string

const (
	// UpgradeOutcomeInstalled 表示目标位置此前已被删除，本次按首次安装语义补齐。
	// 它与 upgraded 分开：升级路径发现受管目标缺失时会走 repair 语义，如实报告可避免
	// 声称做过并未发生的版本替换。
	UpgradeOutcomeInstalled UpgradeOutcome = "installed"
	// UpgradeOutcomeUpgraded 表示从旧版本升级成功。
	UpgradeOutcomeUpgraded UpgradeOutcome = "upgraded"
	// UpgradeOutcomeDowngraded 表示安装路径实际执行了降级。
	// 计划器只升不降，因此它只在执行期事实与计划不一致时出现，仍如实报告而非改写为 upgraded。
	UpgradeOutcomeDowngraded UpgradeOutcome = "downgraded"
	// UpgradeOutcomePlanned 表示 --dry-run 计划要升级该条，但未做任何变更。
	// 它与 upgraded 分开，避免计划输出声称已发生的变更。
	UpgradeOutcomePlanned UpgradeOutcome = "planned"
	// UpgradeOutcomeUpToDate 表示本地版本已是目录最新版本，无需动作。
	UpgradeOutcomeUpToDate UpgradeOutcome = "up-to-date"
	// UpgradeOutcomeSkipped 表示计划有意不动该条，原因见 UpgradeItem.Skip。
	UpgradeOutcomeSkipped UpgradeOutcome = "skipped"
	// UpgradeOutcomeFailed 表示该条升级失败。
	UpgradeOutcomeFailed UpgradeOutcome = "failed"
	// UpgradeOutcomeUnexecuted 表示该条尚未执行（前序失败或检测到残留事务后停止）。
	UpgradeOutcomeUnexecuted UpgradeOutcome = "unexecuted"
)

// UpgradeOutcomeFromInstall 把安装路径的既有结果如实映射为升级结果。
// 已知成功结果各有明确语义（首次安装/升级/降级），不合并为单一 upgraded。
func UpgradeOutcomeFromInstall(outcome InstallOutcome) UpgradeOutcome {
	switch outcome {
	case InstallOutcomeInstalled:
		return UpgradeOutcomeInstalled
	case InstallOutcomeDowngraded:
		return UpgradeOutcomeDowngraded
	case InstallOutcomeUnchanged:
		return UpgradeOutcomeUpToDate
	default:
		return UpgradeOutcomeUpgraded
	}
}

// 跳过原因是 UpgradeItem.Skip 的稳定取值，进入 --json 契约。
const (
	// UpgradeSkipNotInCatalog 表示目录中已无该能力的候选版本（或本地当前版本已下架）。
	UpgradeSkipNotInCatalog = "not-in-catalog"
	// UpgradeSkipNewerLocal 表示本地版本高于目录最新版本，升级不做降级。
	UpgradeSkipNewerLocal = "newer-local"
)

// UpgradeItem 表示一个 Agent 上一条已安装 Power 的升级结果。
// Current 来自本地清单，Target 来自同一目录快照的计划目标版本。
type UpgradeItem struct {
	Agent   domain.AgentID
	Current domain.PowerRef
	Target  domain.PowerRef
	Outcome UpgradeOutcome
	// Skip 仅在 Outcome 为 skipped 时非空，取值为 UpgradeSkipNotInCatalog 或 UpgradeSkipNewerLocal。
	Skip string
}

// UpgradeResult 表示 CLI 可直接渲染的升级业务结果集合。
// DryRun 为真时 Items 只表达计划，未对本地状态做任何变更。
type UpgradeResult struct {
	Items  []UpgradeItem
	DryRun bool
}

// UpgradeFailure 表示一条 Power 的升级失败及底层原因；单条失败不中断整批。
type UpgradeFailure struct {
	Agent domain.AgentID
	Power domain.PowerRef
	Err   error
}

// UpgradeBatchError 表示本批存在一条或多条失败。
type UpgradeBatchError struct {
	Failures []UpgradeFailure
}

func (e *UpgradeBatchError) Error() string {
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, fmt.Sprintf(
			"%s/%s/%s@%s: %v",
			failure.Agent, failure.Power.Type, failure.Power.Name, failure.Power.Version, failure.Err,
		))
	}
	return fmt.Sprintf("upgrade failed for %d power(s): %s", len(e.Failures), strings.Join(parts, "; "))
}

// Unwrap 返回全部底层失败原因，使 errors.Is 可匹配其中任一原因。
func (e *UpgradeBatchError) Unwrap() []error {
	causes := make([]error, 0, len(e.Failures))
	for _, failure := range e.Failures {
		causes = append(causes, failure.Err)
	}
	return causes
}

// UpgradeLifecycle 定义 upgrade 命令唯一需要的生命周期入口。
//
// 第二个参数承载 Flags 无法表达的副作用边界（确认与进度）：Flags 是参与 == 比较的值类型，
// 放入函数字段会使其不可比较。
type UpgradeLifecycle interface {
	Upgrade(context.Context, Flags, UpgradeOptions) (UpgradeResult, error)
}

// UpgradeLifecycleFactory 在日志初始化后创建 Upgrade 生命周期。
type UpgradeLifecycleFactory func() (UpgradeLifecycle, error)

// UpgradeStage 表示升级过程中的一个可见阶段，用于在长时间无输出时提示进度。
type UpgradeStage string

const (
	// UpgradeStageReadingInstalled 表示正在读取本地已安装清单。
	UpgradeStageReadingInstalled UpgradeStage = "reading-installed"
	// UpgradeStageSyncingCatalog 表示正在同步云端目录快照。
	UpgradeStageSyncingCatalog UpgradeStage = "syncing-catalog"
	// UpgradeStagePlanning 表示正在计算升级计划。
	UpgradeStagePlanning UpgradeStage = "planning"
	// UpgradeStageExecuting 表示正在逐条执行升级。
	UpgradeStageExecuting UpgradeStage = "executing"
)

// UpgradeProgress 是一次阶段进度事件。
// executing 阶段携带当前目标、对应版本与 (Index, Total)，使长批次可见进度。
type UpgradeProgress struct {
	Stage   UpgradeStage
	Agent   domain.AgentID
	Power   domain.PowerRef
	Current domain.PowerRef
	Index   int
	Total   int
}

// UpgradePrompt 是确认询问所需的全部事实：只包含将要升级的条目与计数。
type UpgradePrompt struct {
	Upgrades []UpgradeItem
	UpToDate int
	Skipped  int
}

// UpgradeOptions 承载 Flags 无法表达的副作用边界。
// Confirm 为 nil 表示无需确认（由 CLI 依 -y/--dry-run/--json 与交互性判定）；
// Progress 为 nil 表示静默（--json 机器模式）。
type UpgradeOptions struct {
	Confirm  func(UpgradePrompt) (bool, error)
	Progress func(UpgradeProgress)
}

// ErrUpgradeDeclined 表示用户在确认询问中拒绝继续。
// 该错误不做任何变更，CLI 以非零状态退出且不渲染业务结果。
var ErrUpgradeDeclined = errors.New("upgrade aborted by user")
