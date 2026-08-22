package cli

import (
	"context"

	"apm/internal/domain"
)

// SubmissionQueryResult 表示 CLI 可直接渲染的提交查询结果；get 填 Detail，list 填 Summaries。
type SubmissionQueryResult struct {
	Summaries []domain.SubmissionSummary
	Detail    *domain.Submission
}

// SubmissionQueryLifecycle 定义 submission get/list 命令唯一需要的生命周期入口。
type SubmissionQueryLifecycle interface {
	Get(context.Context, string) (SubmissionQueryResult, error)
	List(context.Context, Flags) (SubmissionQueryResult, error)
}

// SubmissionQueryLifecycleFactory 在日志初始化后创建 Submission 查询生命周期。
type SubmissionQueryLifecycleFactory func() (SubmissionQueryLifecycle, error)
