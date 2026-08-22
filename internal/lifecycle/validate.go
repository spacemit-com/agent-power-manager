package lifecycle

import (
	"context"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/submission"
)

// ValidateService 实现 validate 命令的离线完整校验生命周期。
type ValidateService struct{}

// NewValidate 创建无外部依赖的 Validate 生命周期。
func NewValidate() (*ValidateService, error) {
	return &ValidateService{}, nil
}

var _ cli.ValidateLifecycle = (*ValidateService)(nil)

// Validate 对指定路径执行离线完整校验，不读取 API Key、不访问网络。
func (s *ValidateService) Validate(ctx context.Context, flags cli.Flags, args []string) (cli.ValidateResult, error) {
	validation, err := submission.Validate(ctx, submission.Request{
		Type:    domain.PowerType(flags.Type),
		Path:    args[0],
		Version: flags.Version,
	})
	if err != nil {
		return cli.ValidateResult{}, err
	}
	return cli.ValidateResult{
		Type:        string(validation.Type),
		Name:        validation.Name,
		Version:     validation.Version,
		Description: validation.Description,
		FileCount:   validation.FileCount,
		ContentSize: validation.ContentSize,
		Warnings:    validation.Warnings,
	}, nil
}
