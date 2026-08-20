package pluginresource

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/paths"
)

const localPluginSourcePrefix = "./plugins/"

type pluginAuthor struct {
	Name string `json:"name"`
}

// sharedMarketplaceBuilder 在同一个临时 Artifact 根内顺序生成并复验两份 marketplace。
// 它只读取 Codex/Claude 的 JSON manifest，不解析或审计 Hermes 原生入口。
type sharedMarketplaceBuilder struct {
	codex  codexMarketplaceBuilder
	claude claudeMarketplaceBuilder
}

// New 使用默认 APM 路径和共享双 marketplace builder 创建 Plugin 资源服务。
func New(downloader Downloader) (*Service, error) {
	if isNilDependency(downloader) {
		return nil, fmt.Errorf("plugin artifact downloader: %w", ErrInvalidArtifact)
	}
	cacheRoot, err := paths.CachePath()
	if err != nil {
		return nil, err
	}
	cacheTempRoot, err := paths.CacheTempPath()
	if err != nil {
		return nil, err
	}
	stagingRoot, err := paths.StagingPath()
	if err != nil {
		return nil, err
	}
	stagingTempRoot, err := paths.StagingTempPath()
	if err != nil {
		return nil, err
	}
	return newService(cacheRoot, cacheTempRoot, stagingRoot, stagingTempRoot, downloader, builtInBuilder()), nil
}

func builtInBuilder() artifactBuilder {
	return sharedMarketplaceBuilder{}
}

func (builder sharedMarketplaceBuilder) Build(ref domain.PowerRef, root, pluginPath string) error {
	if err := builder.codex.Build(ref, root, pluginPath); err != nil {
		return fmt.Errorf("build Codex marketplace: %w", err)
	}
	if err := builder.claude.Build(ref, root, pluginPath); err != nil {
		return fmt.Errorf("build Claude marketplace: %w", err)
	}
	return nil
}

func (builder sharedMarketplaceBuilder) Validate(ref domain.PowerRef, root string) error {
	if err := builder.codex.Validate(ref, root); err != nil {
		return fmt.Errorf("validate Codex marketplace: %w", err)
	}
	if err := builder.claude.Validate(ref, root); err != nil {
		return fmt.Errorf("validate Claude marketplace: %w", err)
	}
	return nil
}

func decodeJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}

func decodePluginManifest(pluginPath, relativePath string, target any) error {
	path := filepath.Join(pluginPath, relativePath)
	info, err := os.Lstat(path)
	if err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plugin manifest %q is not a physical regular file: %w", path, ErrInvalidArtifact)
	}
	if err := decodeJSONFile(path, target); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}

// validateManifestIdentity 精确映射 APM 的 vMAJOR.MINOR.PATCH 与原生 MAJOR.MINOR.PATCH；不做 trim 或规范化。
func validateManifestIdentity(ref domain.PowerRef, actualName, nativeVersion string) error {
	wantActualName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if actualName != wantActualName || nativeVersion != strings.TrimPrefix(ref.Version, "v") {
		return fmt.Errorf("plugin manifest identity %q@%q: %w", actualName, nativeVersion, ErrInvalidArtifact)
	}
	return nil
}
