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
	ChangeSummary  string
	ContentType    string // "application/gzip" 或 "application/json"
	Content        []byte
	ContentMD5     string
	IdempotencyKey string
}

// SubmissionReceipt 表示 APC 对 202 Accepted 的返回。
type SubmissionReceipt struct {
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
	Status           SubmissionStatus
	ContentAvailable bool
	ValidationErrors []string
	ReviewComment    string
	PublishedVersion string
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

// SubmissionListQuery 表示提交列表的筛选与分页条件。
type SubmissionListQuery struct {
	Status SubmissionStatus
	Type   PowerType
	Name   string
	Cursor string
	Limit  int
}

// SubmissionList 表示提交列表结果与下一页游标。
type SubmissionList struct {
	Items      []SubmissionSummary
	NextCursor string
}
