package cli

import "context"

// ValidateResult 表示 CLI 可直接渲染的校验结果。
type ValidateResult struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description"`
	FileCount   int      `json:"file_count"`
	ContentSize int64    `json:"content_size"`
	Warnings    []string `json:"warnings"`
}

// ValidateLifecycle 定义 validate 命令唯一需要的生命周期入口。
type ValidateLifecycle interface {
	Validate(context.Context, Flags, []string) (ValidateResult, error)
}

// ValidateLifecycleFactory 在日志初始化后创建 Validate 生命周期。
type ValidateLifecycleFactory func() (ValidateLifecycle, error)
