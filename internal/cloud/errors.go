package cloud

import "errors"

var (
	// ErrNotFound 表示 Cloud 中不存在请求的精确能力标识。
	ErrNotFound = errors.New("cloud power not found")
	// ErrYanked 表示请求的精确能力版本已被提供方撤回。
	ErrYanked = errors.New("cloud power yanked")
	// ErrInvalidResponse 表示提供方返回的数据不满足能力协议。
	ErrInvalidResponse = errors.New("cloud invalid response")
	// ErrIntegrity 表示提供方数据未通过完整性校验。
	ErrIntegrity = errors.New("cloud integrity failure")
	// ErrUnsafeContent 表示提供方内容违反本地安全边界。
	ErrUnsafeContent = errors.New("cloud unsafe content")
	// ErrProviderConfig 表示提供方配置缺失或无效。
	ErrProviderConfig = errors.New("cloud provider configuration error")
	// ErrSubmissionUnsupported 表示 provider 不支持提交。
	ErrSubmissionUnsupported = errors.New("cloud submission unsupported")
	// ErrSubmissionConflict 表示提交与活动申请、版本或审批状态冲突。
	ErrSubmissionConflict = errors.New("cloud submission conflict")
	// ErrAuth 表示 API Key 无效或无权操作。
	ErrAuth = errors.New("cloud authentication failure")
	// ErrUnavailable 表示依赖服务（官网认证等）不可用。
	ErrUnavailable = errors.New("cloud service unavailable")
)
