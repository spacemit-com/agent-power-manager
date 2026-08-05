package transaction

// PathState 表示事务相关路径的存在状态。
type PathState struct {
	StagingExists    bool
	TargetExists     bool
	BackupExists     bool
	QuarantineExists bool
}

// InspectPathState 检查事务日志对应路径的状态。
func InspectPathState(Journal) (PathState, error) {
	return PathState{}, nil
}
