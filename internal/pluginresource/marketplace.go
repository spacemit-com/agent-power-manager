package pluginresource

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/paths"
	"apm/internal/pluginartifact"
	"apm/internal/utils/jsonstrict"
)

const localPluginSourcePrefix = "./plugins/"

// sharedMarketplaceBuilder 在同一个临时 Artifact 根内顺序生成并复验两份 marketplace。
// 它先从唯一 canonical 索引生成全部 Agent manifest，再生成两份 marketplace。
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
	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if err := writeAgentManifests(pluginPath, manifest); err != nil {
		return err
	}
	if err := builder.codex.Build(ref, root, pluginPath); err != nil {
		return fmt.Errorf("build Codex marketplace: %w", err)
	}
	if err := builder.claude.Build(ref, root, pluginPath); err != nil {
		return fmt.Errorf("build Claude marketplace: %w", err)
	}
	return nil
}

func (builder sharedMarketplaceBuilder) Validate(ref domain.PowerRef, root string) error {
	if err := pluginartifact.ValidateShared(ref, root); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
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
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if err := jsonstrict.Decode(data, target, true); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	return writePrivateFile(path, func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	})
}

func writePrivateFile(path string, encode func(io.Writer) error) (resultErr error) {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("derived Plugin file %q already exists: %w", path, ErrInvalidArtifact)
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrInvalidArtifact, err)
	}
	file, err := os.CreateTemp(parent, ".apm-derived-*")
	if err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	temporary := file.Name()
	defer func() {
		if temporary != "" {
			resultErr = errors.Join(resultErr, os.Remove(temporary))
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return errors.Join(ErrInvalidArtifact, err)
	}
	encodeErr := encode(file)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		return errors.Join(ErrInvalidArtifact, encodeErr, closeErr)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("derived Plugin file %q appeared during generation: %w", path, ErrInvalidArtifact)
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if err := os.Link(temporary, path); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if err := os.Remove(temporary); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	temporary = ""
	return nil
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
