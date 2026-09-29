package submission

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

// maxUploadSize 是 APC 的同步接收边界（20 MiB）；打包后在 Prepare 校验。
const (
	maxUploadSize           = 20 << 20
	maxPackedExpansionRatio = int64(100)
)

// Prepare 校验源并确定性构建各资源内容，计算 MD5 并组装 Content；
// Skill/Plugin/Agent 执行确定性归档，MCP 执行 JCS 规范化，全程不写临时文件。
func Prepare(ctx context.Context, request Request) (Content, error) {
	message := strings.TrimSpace(request.Message)
	if message == "" {
		return Content{}, fmt.Errorf("submit requires --message: %w", ErrInvalid)
	}
	loaded, err := loadSource(ctx, request.Type, request.Path)
	if err != nil {
		return Content{}, err
	}
	data, err := loaded.build()
	if err != nil {
		return Content{}, err
	}
	if loaded.contentType == "application/gzip" {
		if err := validatePackedExpansionRatio(loaded.validation.ContentSize, int64(len(data))); err != nil {
			return Content{}, err
		}
	}
	if len(data) > maxUploadSize {
		return Content{}, fmt.Errorf("packed content size %d exceeds %d bytes: %w", len(data), maxUploadSize, ErrInvalid)
	}
	sum := md5.Sum(data)
	return Content{
		Type:          loaded.validation.Type,
		SchemaVersion: loaded.validation.SchemaVersion,
		Name:          loaded.validation.Name,
		Version:       loaded.validation.Version,
		Description:   loaded.validation.Description,
		NameZH:        loaded.validation.NameZH,
		NameEN:        loaded.validation.NameEN,
		DescriptionZH: loaded.validation.DescriptionZH,
		Message:       message,
		Definition:    loaded.definition,
		ContentType:   loaded.contentType,
		Bytes:         append([]byte(nil), data...),
		MD5:           hex.EncodeToString(sum[:]),
	}, nil
}

// validatePackedExpansionRatio 以商和余数比较解压倍率，避免 packedSize*100 溢出。
func validatePackedExpansionRatio(sourceSize, packedSize int64) error {
	if sourceSize < 0 || packedSize <= 0 {
		return fmt.Errorf("invalid source or packed size for decompression ratio: %w", ErrInvalid)
	}
	quotient, remainder := sourceSize/packedSize, sourceSize%packedSize
	if quotient > maxPackedExpansionRatio || quotient == maxPackedExpansionRatio && remainder > 0 {
		return fmt.Errorf("source size %d exceeds %dx packed size %d decompression ratio: %w", sourceSize, maxPackedExpansionRatio, packedSize, ErrInvalid)
	}
	return nil
}
