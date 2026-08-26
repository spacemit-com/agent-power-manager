package lifecycle

// manifestState 表示 manifest 中记录的已安装 Power 状态。
type manifestState int

const (
	manifestAbsent           manifestState = iota // manifest 中无记录
	manifestSameVersion                           // manifest 记录与目标版本一致
	manifestDifferentVersion                      // manifest 记录与目标版本不同
)

// targetState 表示目标位置的现状。
type targetState int

const (
	targetAbsent     targetState = iota // 目标位置不存在
	targetComplete                      // 目标位置完整
	targetIncomplete                    // 目标位置不完整
	targetConflict                      // 目标位置存在冲突
)

// lifecycleAction 表示状态决策后应采取的动作。
type lifecycleAction int

const (
	actionUnchanged       lifecycleAction = iota // 保持现状
	actionInstallNew                             // 全新安装
	actionReplace                                // 原地替换
	actionCleanAndInstall                        // 清理后安装
	actionUninstall                              // 卸载
	actionCleanOnly                              // 仅清理残留
	actionUnexecuted                             // 从未执行
	actionRejectConflict                         // 因目标冲突而拒绝
	actionRejectState                            // 因状态不符而拒绝
)

// stateDecision 是一次状态决策的结果，描述应执行的动作及是否需要告警缺失。
type stateDecision struct {
	action      lifecycleAction
	warnMissing bool
}

// decideInstallState 根据 manifest 状态与目标现状决定安装动作。
// 纯函数：不执行任何 I/O，仅做状态映射。同版本输入在任何目标状态下都直接返回
// actionUnchanged。
func decideInstallState(manifest manifestState, target targetState) stateDecision {
	if manifest == manifestSameVersion {
		return stateDecision{action: actionUnchanged}
	}
	switch manifest {
	case manifestAbsent:
		if target == targetAbsent {
			return stateDecision{action: actionInstallNew}
		}
		return stateDecision{action: actionRejectConflict}
	case manifestDifferentVersion:
		switch target {
		case targetComplete:
			return stateDecision{action: actionReplace}
		case targetAbsent, targetIncomplete:
			return stateDecision{action: actionCleanAndInstall, warnMissing: true}
		default:
			return stateDecision{action: actionRejectState}
		}
	default:
		return stateDecision{action: actionRejectState}
	}
}

// decideUninstallState 根据是否已有 manifest 记录与目标现状决定卸载动作。
// 纯函数：不执行任何 I/O，仅做状态映射。
func decideUninstallState(hasManifest bool, target targetState) stateDecision {
	if !hasManifest {
		return stateDecision{action: actionUnexecuted}
	}
	switch target {
	case targetComplete:
		return stateDecision{action: actionUninstall}
	case targetAbsent, targetIncomplete:
		return stateDecision{action: actionCleanOnly, warnMissing: true}
	default:
		return stateDecision{action: actionRejectState}
	}
}
