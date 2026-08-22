package lifecycle

import (
	"context"
	"errors"

	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/domain"
)

// SubmissionQueryService 实现 submission get/list 命令的查询生命周期。
type SubmissionQueryService struct {
	cloud cloud.Client
}

// NewSubmissionQuery 创建具有 Cloud 依赖的 Submission 查询生命周期。
func NewSubmissionQuery(cloudClient cloud.Client) (*SubmissionQueryService, error) {
	if isNilInterface(cloudClient) {
		return nil, errors.New("create Submission query lifecycle with nil Cloud client")
	}
	return &SubmissionQueryService{cloud: cloudClient}, nil
}

var _ cli.SubmissionQueryLifecycle = (*SubmissionQueryService)(nil)

// Get 读取单次提交详情。
func (s *SubmissionQueryService) Get(ctx context.Context, submissionID string) (cli.SubmissionQueryResult, error) {
	detail, err := s.cloud.GetSubmission(ctx, submissionID)
	if err != nil {
		return cli.SubmissionQueryResult{}, err
	}
	return cli.SubmissionQueryResult{Detail: &detail}, nil
}

// List 按筛选条件读取提交列表。
func (s *SubmissionQueryService) List(ctx context.Context, flags cli.Flags) (cli.SubmissionQueryResult, error) {
	list, err := s.cloud.ListSubmissions(ctx, domain.SubmissionListQuery{
		Status: domain.SubmissionStatus(flags.Status),
		Type:   domain.PowerType(flags.Type),
		Name:   flags.Name,
		Limit:  flags.Limit,
	})
	if err != nil {
		return cli.SubmissionQueryResult{}, err
	}
	return cli.SubmissionQueryResult{Summaries: list.Items}, nil
}
