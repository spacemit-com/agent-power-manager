package cloud

import (
	"context"

	"apm/internal/domain"
)

// Submission 定义向 Cloud 提交资源申请与查询本人提交的边界；只面向 APC，Local provider 不提供。
type Submission interface {
	Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionResult, error)
	GetSubmission(context.Context, string) (domain.Submission, error)
	ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error)
}
