package cloud_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalClientResolveMissingExactPowerReturnsNotFound(t *testing.T) {
	// 捕获错误变更：将精确名称或版本未命中映射为一般文件系统错误，调用方无法稳定识别未找到。
	client := newMCPTestClient(t, writeTwoVersionRoot(t))

	_, err := client.Resolve(context.Background(), domain.PowerRef{
		Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v9.0.0",
	})
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrNotFound", err)
	}
}

func TestCloudErrorSentinelsClassifyWrappedErrors(t *testing.T) {
	for _, want := range []error{
		cloud.ErrNotFound,
		cloud.ErrYanked,
		cloud.ErrInvalidResponse,
		cloud.ErrIntegrity,
		cloud.ErrUnsafeContent,
		cloud.ErrProviderConfig,
	} {
		if err := fmt.Errorf("provider request: %w", want); !errors.Is(err, want) {
			t.Fatalf("errors.Is(%v, %v) = false", err, want)
		}
	}
}
