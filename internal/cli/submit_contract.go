package cli

import "context"

// SubmitResult 表示 CLI 可直接渲染的提交结果。
type SubmitResult struct {
	SubmissionID string `json:"submission_id"`
	Status       string `json:"status"`
}

// SubmitLifecycle 定义 submit 命令唯一需要的生命周期入口。
type SubmitLifecycle interface {
	Submit(context.Context, Flags, []string) (SubmitResult, error)
}

// SubmitLifecycleFactory 在日志初始化后创建 Submit 生命周期。
type SubmitLifecycleFactory func() (SubmitLifecycle, error)
