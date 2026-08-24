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

type submissionListOutput struct {
	Items []submissionSummaryOutput `json:"items"`
}

type submissionSummaryOutput struct {
	SubmissionID string `json:"submission_id"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

type submissionDetailOutput struct {
	SubmissionID     string                  `json:"submission_id"`
	Type             string                  `json:"type"`
	Name             string                  `json:"name"`
	Version          string                  `json:"version"`
	Description      string                  `json:"description"`
	Message          string                  `json:"message"`
	Status           string                  `json:"status"`
	CreatedAt        string                  `json:"created_at"`
	UpdatedAt        string                  `json:"updated_at"`
	ContentAvailable bool                    `json:"content_available"`
	ValidationErrors []string                `json:"validation_errors"`
	ReviewComment    string                  `json:"review_comment"`
	PublishedVersion string                  `json:"published_version"`
	Timeline         []submissionEventOutput `json:"timeline"`
}

type submissionEventOutput struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	StatusFrom string `json:"status_from"`
	StatusTo   string `json:"status_to"`
	Timestamp  string `json:"timestamp"`
	Message    string `json:"message"`
}

func newSubmissionListOutput(summaries []domain.SubmissionSummary) submissionListOutput {
	items := make([]submissionSummaryOutput, 0, len(summaries))
	for _, summary := range summaries {
		items = append(items, submissionSummaryOutput{
			SubmissionID: summary.SubmissionID,
			Type:         string(summary.Type),
			Name:         summary.Name,
			Version:      summary.Version,
			Status:       string(summary.Status),
			CreatedAt:    summary.CreatedAt,
		})
	}
	return submissionListOutput{Items: items}
}

func newSubmissionDetailOutput(detail domain.Submission) submissionDetailOutput {
	timeline := make([]submissionEventOutput, 0, len(detail.Timeline))
	for _, event := range detail.Timeline {
		timeline = append(timeline, submissionEventOutput{
			EventID:    event.EventID,
			EventType:  event.EventType,
			StatusFrom: string(event.StatusFrom),
			StatusTo:   string(event.StatusTo),
			Timestamp:  event.Timestamp,
			Message:    event.Message,
		})
	}
	return submissionDetailOutput{
		SubmissionID:     detail.SubmissionID,
		Type:             string(detail.Type),
		Name:             detail.Name,
		Version:          detail.Version,
		Description:      detail.Description,
		Message:          detail.Message,
		Status:           string(detail.Status),
		CreatedAt:        detail.CreatedAt,
		UpdatedAt:        detail.UpdatedAt,
		ContentAvailable: detail.ContentAvailable,
		ValidationErrors: append([]string{}, detail.ValidationErrors...),
		ReviewComment:    detail.ReviewComment,
		PublishedVersion: detail.PublishedVersion,
		Timeline:         timeline,
	}
}
