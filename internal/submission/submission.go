package submission

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	"apm/internal/domain"
)

// maxUploadSize 是 APC 的同步接收边界（20 MiB）；打包后在 Prepare 校验。
const maxUploadSize = 20 << 20

// Prepare 校验源并确定性打包（Skill/Plugin）或 JCS 规范化（MCP），
// 计算 MD5 并组装 Content；全程内存内，不写临时文件。
func Prepare(ctx context.Context, request Request) (Content, error) {
	switch request.Type {
	case domain.PowerTypeAgent:
		if strings.TrimSpace(request.ChangeSummary) == "" {
			return Content{}, fmt.Errorf("agent submit requires --change-summary: %w", ErrInvalid)
		}
		source, err := validateAgentSource(ctx, request)
		if err != nil {
			return Content{}, err
		}
		sum := md5.Sum(source.raw)
		return Content{
			Type:          source.validation.Type,
			Name:          source.validation.Name,
			Version:       source.validation.Version,
			Description:   source.validation.Description,
			ChangeSummary: source.validation.ChangeSummary,
			ContentType:   "application/yaml",
			Bytes:         source.raw,
			MD5:           hex.EncodeToString(sum[:]),
		}, nil
	case domain.PowerTypeSkill:
		if strings.TrimSpace(request.Version) == "" {
			return Content{}, fmt.Errorf("skill submit requires --version: %w", ErrInvalid)
		}
		if strings.TrimSpace(request.ChangeSummary) == "" {
			return Content{}, fmt.Errorf("skill submit requires --change-summary: %w", ErrInvalid)
		}
	case domain.PowerTypePlugin:
		if strings.TrimSpace(request.ChangeSummary) == "" {
			return Content{}, fmt.Errorf("plugin submit requires --change-summary: %w", ErrInvalid)
		}
	}
	validation, err := Validate(ctx, request)
	if err != nil {
		return Content{}, err
	}
	var contentType string
	var data []byte
	switch request.Type {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		contentType = "application/gzip"
		data, err = BuildTarGz(request.Path)
	case domain.PowerTypeMCP:
		contentType = "application/json"
		submission, err := readMCPSubmission(request.Path)
		if err != nil {
			return Content{}, err
		}
		data, err = CanonicalizeJSON(submission.Definition)
	default:
		return Content{}, fmt.Errorf("unsupported type %q: %w", request.Type, ErrInvalid)
	}
	if err != nil {
		return Content{}, err
	}
	if len(data) > maxUploadSize {
		return Content{}, fmt.Errorf("packed content size %d exceeds %d bytes: %w", len(data), maxUploadSize, ErrInvalid)
	}
	sum := md5.Sum(data)
	return Content{
		Type:          validation.Type,
		Name:          validation.Name,
		Version:       validation.Version,
		Description:   validation.Description,
		ChangeSummary: validation.ChangeSummary,
		ContentType:   contentType,
		Bytes:         data,
		MD5:           hex.EncodeToString(sum[:]),
	}, nil
}
