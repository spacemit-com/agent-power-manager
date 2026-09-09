package domain

// SubmissionStatus 表示一次资源提交所处的流程状态，与 APC 公开状态机一致。
type SubmissionStatus string

const (
	// SubmissionReceived 表示内容已完整接收并通过同步初检。
	SubmissionReceived SubmissionStatus = "received"
	// SubmissionValidating 表示 APC 正在执行最低验收。
	SubmissionValidating SubmissionStatus = "validating"
	// SubmissionValidationFailed 表示最低验收失败，不创建工单。
	SubmissionValidationFailed SubmissionStatus = "validation_failed"
	// SubmissionReviewPending 表示已创建内网工单，等待决定。
	SubmissionReviewPending SubmissionStatus = "review_pending"
	// SubmissionRejected 表示审核者拒绝。
	SubmissionRejected SubmissionStatus = "rejected"
	// SubmissionApproved 表示审核决定已通过，尚未完成技术发布。
	SubmissionApproved SubmissionStatus = "approved"
	// SubmissionPublishing 表示正在发布内容和目录。
	SubmissionPublishing SubmissionStatus = "publishing"
	// SubmissionPublishFailed 表示技术发布失败，保留通过决定并等待重试。
	SubmissionPublishFailed SubmissionStatus = "publish_failed"
	// SubmissionPublished 表示内容和目录均已公开可用。
	SubmissionPublished SubmissionStatus = "published"
)

// SubmissionRequest 表示一次提交上传请求；Content 是确定性打包或规范化后的字节。
type SubmissionRequest struct {
	Type           PowerType
	Name           string
	Version        string
	Description    string
	Message        string
	Definition     *MCPServer // 仅 MCP 提交携带；其余类型为 nil，不进入 metadata
	ContentType    string     // "application/gzip" 或 "application/json"
	Content        []byte
	ContentMD5     string
	IdempotencyKey string
}

// IsActiveSubmissionStatus 仅识别 APC 配额和查询契约定义的活动提交状态。
func IsActiveSubmissionStatus(status SubmissionStatus) bool {
	switch status {
	case SubmissionReceived, SubmissionValidating, SubmissionReviewPending, SubmissionApproved, SubmissionPublishing:
		return true
	default:
		return false
	}
}

// SubmissionOutcome 表示一次提交请求的最终处理结果类别，用于区分新创建的提交与幂等重放的提交。
type SubmissionOutcome string

const (
	// SubmissionAccepted 表示提交已被接收，并创建了新的 Submission。
	SubmissionAccepted SubmissionOutcome = "accepted"
	// SubmissionReplayed 表示请求被幂等重放，返回的是已存在的 Submission。
	SubmissionReplayed SubmissionOutcome = "replayed"
)

// SubmissionResult 表示提交请求的统一结果，同时承载处理结果类别与对应的 Submission 标识；
// Outcome 必须与 Status 语义一致（如 accepted 对应已接收、replayed 对应已存在）。
type SubmissionResult struct {
	Outcome      SubmissionOutcome
	SubmissionID string
	Status       SubmissionStatus
	StatusURL    string
}

// Submission 表示一次提交的详情，供 submission get 展示。
type Submission struct {
	SubmissionID     string
	Type             PowerType
	Name             string
	Version          string
	Description      string
	Message          string
	Status           SubmissionStatus
	CreatedAt        string
	UpdatedAt        string
	ContentAvailable bool
	ValidationErrors []string
	ReviewComment    string
	PublishedVersion string
	Timeline         []SubmissionEvent
}

// SubmissionEvent 表示 APC 原样返回的一次状态演进事件，时间线顺序由服务端保证。
type SubmissionEvent struct {
	EventID    string
	EventType  string
	StatusFrom SubmissionStatus
	StatusTo   SubmissionStatus
	Timestamp  string
	Message    string
}

// SubmissionSummary 表示提交列表中的一条摘要。
type SubmissionSummary struct {
	SubmissionID string
	Type         PowerType
	Name         string
	Version      string
	Status       SubmissionStatus
	CreatedAt    string
}

// SubmissionListQuery 表示当前 API Key 名下活动提交的筛选条件，不支持分页。
type SubmissionListQuery struct {
	Status SubmissionStatus
	Type   PowerType
	Name   string
}

// SubmissionList 表示至多二十条活动提交的列表结果。
type SubmissionList struct {
	Items []SubmissionSummary
}
