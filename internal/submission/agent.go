package submission

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/cloud/contentpolicy"
	"apm/internal/domain"
)

type agentSourceLoader struct{}

// Load 校验单个 agent.yaml 物理文件；构建结果保持原始字节且每次返回独立副本。
func (agentSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	raw, _, err := readPhysicalRegularFile(ctx, path, maxSingleFileSize)
	if err != nil {
		return source{}, fmt.Errorf("read Agent source: %w", err)
	}
	if len(raw) == 0 {
		return source{}, fmt.Errorf("Agent source size 0 is invalid: %w", ErrInvalid)
	}
	if contentpolicy.IsUnsafeText(raw) {
		return source{}, fmt.Errorf("Agent source contains binary or archive content: %w", ErrInvalid)
	}
	config, err := domain.DecodeAgentConfigYAML(raw)
	if err != nil {
		return source{}, errors.Join(ErrInvalid, err)
	}
	validatedRaw := append([]byte(nil), raw...)
	return source{
		validation: Validation{
			Type: domain.PowerTypeAgent, Name: config.Agent.ID, Version: config.Agent.Version,
			Description: config.Agent.Description, FileCount: 1, ContentSize: int64(len(validatedRaw)), Warnings: []string{},
		},
		contentType: "application/yaml",
		build: func() ([]byte, error) {
			return append([]byte(nil), validatedRaw...), nil
		},
	}, nil
}
