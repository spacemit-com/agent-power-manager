package submission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"apm/internal/cloud/contentpolicy"
	"apm/internal/domain"
)

// validatedAgentSource 保存一次校验后的 Agent 身份与原始字节，
// 使 Prepare 能与 Validate 共用同一份已校验数据，避免校验后再次读取不同字节。
type validatedAgentSource struct {
	validation Validation
	raw        []byte
}

// validateAgentSource 校验单个 agent.yaml 物理文件并返回其身份与原始字节。
// 版本只来自 agent.yaml；显式传入 --version 会在任何文件读取前被拒绝。
// 原始字节保持不变（不 tar、不 JCS、不规范化），供调用方按字节发布。
func validateAgentSource(ctx context.Context, request Request) (validatedAgentSource, error) {
	if err := ctx.Err(); err != nil {
		return validatedAgentSource{}, err
	}
	if strings.TrimSpace(request.Version) != "" {
		return validatedAgentSource{}, fmt.Errorf("agent version comes from agent.yaml; --version is not allowed: %w", ErrInvalid)
	}
	info, err := os.Lstat(request.Path)
	if err != nil {
		return validatedAgentSource{}, fmt.Errorf("stat Agent source: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return validatedAgentSource{}, fmt.Errorf("Agent source is not a physical regular file: %w", ErrInvalid)
	}
	if info.Size() <= 0 || info.Size() > maxSingleFileSize {
		return validatedAgentSource{}, fmt.Errorf("Agent source size %d is invalid: %w", info.Size(), ErrInvalid)
	}
	raw, err := os.ReadFile(request.Path)
	if err != nil {
		return validatedAgentSource{}, err
	}
	if contentpolicy.IsUnsafeText(raw) {
		return validatedAgentSource{}, fmt.Errorf("Agent source contains binary or archive content: %w", ErrInvalid)
	}
	config, err := domain.DecodeAgentConfigYAML(raw)
	if err != nil {
		return validatedAgentSource{}, errors.Join(ErrInvalid, err)
	}
	return validatedAgentSource{validation: Validation{
		Type: domain.PowerTypeAgent, Name: config.Agent.ID, Version: config.Agent.Version,
		Description: config.Agent.Description, ChangeSummary: request.ChangeSummary,
		FileCount: 1, ContentSize: int64(len(raw)), Warnings: []string{},
	}, raw: raw}, nil
}
