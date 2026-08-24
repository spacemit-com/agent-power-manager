package submission

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

// maxUploadSize 是 APC 的同步接收边界（20 MiB）；打包后在 Prepare 校验。
const maxUploadSize = 20 << 20

// Prepare 校验源并确定性构建各资源内容，计算 MD5 并组装 Content；
// Agent 保留原始 YAML，其余类型执行确定性归档或 JCS 规范化，全程不写临时文件。
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
	if len(data) > maxUploadSize {
		return Content{}, fmt.Errorf("packed content size %d exceeds %d bytes: %w", len(data), maxUploadSize, ErrInvalid)
	}
	sum := md5.Sum(data)
	return Content{
		Type:        loaded.validation.Type,
		Name:        loaded.validation.Name,
		Version:     loaded.validation.Version,
		Description: loaded.validation.Description,
		Message:     message,
		ContentType: loaded.contentType,
		Bytes:       append([]byte(nil), data...),
		MD5:         hex.EncodeToString(sum[:]),
	}, nil
}
