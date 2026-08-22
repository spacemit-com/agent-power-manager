package local

import (
	"context"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var _ cloud.Submission = (*Client)(nil)

// Submit 空实现：Local provider 只做 fixture 只读，不提供上传，fail loud 返回 ErrSubmissionUnsupported。
func (c *Client) Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionReceipt, error) {
	return domain.SubmissionReceipt{}, cloud.ErrSubmissionUnsupported
}

// GetSubmission 空实现：Local provider 只读，不提供提交查询。
func (c *Client) GetSubmission(context.Context, string) (domain.Submission, error) {
	return domain.Submission{}, cloud.ErrSubmissionUnsupported
}

// ListSubmissions 空实现：Local provider 只读，不提供提交列表。
func (c *Client) ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error) {
	return domain.SubmissionList{}, cloud.ErrSubmissionUnsupported
}
