package domain

// ResultStatus 表示智能体执行的结果状态。
type ResultStatus string

const (
	// StatusCompleted 表示执行完成。
	StatusCompleted ResultStatus = "completed"
	// StatusFailed 表示执行失败。
	StatusFailed ResultStatus = "failed"
	// StatusUnexecuted 表示尚未执行。
	StatusUnexecuted ResultStatus = "unexecuted"
)

// AgentResult 表示单个智能体的执行结果。
type AgentResult struct {
	Agent  AgentID
	Status ResultStatus
	Err    error
}

// Result 表示多个智能体的执行结果集合。
type Result struct {
	Items []AgentResult
}
