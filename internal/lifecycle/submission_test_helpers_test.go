package lifecycle

import (
	"context"

	"apm/internal/domain"
)

// recordingSubmissionClient 记录 Submit/GetSubmission/ListSubmissions 调用并返回预设结果；
// 其余 Cloud 方法复用 stubCloudClient 的空实现。
type recordingSubmissionClient struct {
	stubCloudClient
	submitCalls []domain.SubmissionRequest
	getCalls    []string
	listCalls   []domain.SubmissionListQuery
	receipt     domain.SubmissionResult
	detail      domain.Submission
	list        domain.SubmissionList
	submitErr   error
	getErr      error
	listErr     error
}

func (c *recordingSubmissionClient) Submit(_ context.Context, request domain.SubmissionRequest) (domain.SubmissionResult, error) {
	c.submitCalls = append(c.submitCalls, request)
	return c.receipt, c.submitErr
}

func (c *recordingSubmissionClient) GetSubmission(_ context.Context, id string) (domain.Submission, error) {
	c.getCalls = append(c.getCalls, id)
	return c.detail, c.getErr
}

func (c *recordingSubmissionClient) ListSubmissions(_ context.Context, query domain.SubmissionListQuery) (domain.SubmissionList, error) {
	c.listCalls = append(c.listCalls, query)
	return c.list, c.listErr
}
