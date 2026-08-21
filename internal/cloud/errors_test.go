package cloud_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/domain"
)

func TestLocalClientResolveMissingExactPowerReturnsNotFound(t *testing.T) {
	// 捕获错误变更：将精确名称或版本未命中映射为一般文件系统错误，调用方无法稳定识别未找到。
	root := t.TempDir()
	for _, version := range []string{"v0.1.0", "v0.2.0"} {
		marker := filepath.Join(root, "mcps", "filesystem", version, "MCP.json")
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte(`{"transport":"stdio","command":"npx"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Resolve(context.Background(), domain.PowerRef{
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
