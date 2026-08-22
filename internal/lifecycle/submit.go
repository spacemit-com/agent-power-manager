package lifecycle

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/submission"
)

// SubmitService 实现 submit 命令的离线校验、确定性打包与上传生命周期。
type SubmitService struct {
	cloud cloud.Client
}

// NewSubmit 创建具有 Cloud 依赖的 Submit 生命周期。
func NewSubmit(cloudClient cloud.Client) (*SubmitService, error) {
	if isNilInterface(cloudClient) {
		return nil, errors.New("create Submit lifecycle with nil Cloud client")
	}
	return &SubmitService{cloud: cloudClient}, nil
}

var _ cli.SubmitLifecycle = (*SubmitService)(nil)

// Submit 先离线校验与确定性打包，再生成 Idempotency-Key 并上传。
func (s *SubmitService) Submit(ctx context.Context, flags cli.Flags, args []string) (cli.SubmitResult, error) {
	content, err := submission.Prepare(ctx, submission.Request{
		Type:          domain.PowerType(flags.Type),
		Path:          args[0],
		Version:       flags.Version,
		ChangeSummary: flags.ChangeSummary,
	})
	if err != nil {
		return cli.SubmitResult{}, err
	}
	idempotencyKey, err := newIdempotencyKey()
	if err != nil {
		return cli.SubmitResult{}, err
	}
	receipt, err := s.cloud.Submit(ctx, domain.SubmissionRequest{
		Type:           content.Type,
		Name:           content.Name,
		Version:        content.Version,
		Description:    content.Description,
		ChangeSummary:  content.ChangeSummary,
		ContentType:    content.ContentType,
		Content:        content.Bytes,
		ContentMD5:     content.MD5,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return cli.SubmitResult{}, err
	}
	return cli.SubmitResult{SubmissionID: receipt.SubmissionID, Status: string(receipt.Status)}, nil
}

// newIdempotencyKey 生成 RFC 4122 UUID v4 字符串，作为每次逻辑提交的幂等键。
func newIdempotencyKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
