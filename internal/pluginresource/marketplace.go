package pluginresource

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/domain"
	"apm/internal/paths"
)

const localPluginSourcePrefix = "./plugins/"

type pluginAuthor struct {
	Name string `json:"name"`
}

type hermesArtifactBuilder struct{}

// New 使用默认 APM 路径和全部内建 Agent builder 创建 Plugin 资源服务。
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
	return newService(cacheRoot, cacheTempRoot, stagingRoot, stagingTempRoot, downloader, builtInBuilders()), nil
}

func builtInBuilders() map[domain.AgentID]artifactBuilder {
	return map[domain.AgentID]artifactBuilder{
		domain.AgentCodex:      codexMarketplaceBuilder{},
		domain.AgentClaudeCode: claudeMarketplaceBuilder{},
		domain.AgentHermes:     hermesArtifactBuilder{},
	}
}

func (hermesArtifactBuilder) Build(agent domain.AgentID, _ domain.PowerRef, _, _ string) error {
	if agent != domain.AgentHermes {
		return fmt.Errorf("build Hermes plugin artifact for agent %q: %w", agent, ErrUnsupportedAgent)
	}
	return nil
}

func (hermesArtifactBuilder) Validate(agent domain.AgentID, ref domain.PowerRef, root string) error {
	if agent != domain.AgentHermes {
		return fmt.Errorf("validate Hermes plugin artifact for agent %q: %w", agent, ErrUnsupportedAgent)
	}
	actualName, err := domain.ActualPluginName(ref)
	if err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if err := validatePhysicalTree(filepath.Join(root, "plugins", actualName)); err != nil {
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
	if err := rejectCloudMarketplaces(pluginPath); err != nil {
		return err
	}
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

// Cloud 只能提供原始 Plugin 包；任一 Agent marketplace 都必须由 APM 本地生成。
func rejectCloudMarketplaces(pluginPath string) error {
	for _, relativePath := range []string{codexMarketplacePath, claudeMarketplacePath} {
		path := filepath.Join(pluginPath, relativePath)
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("Cloud plugin package contains marketplace %q: %w", relativePath, ErrInvalidArtifact)
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.Join(ErrInvalidArtifact, err)
		}
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
	wantActualName, err := domain.ActualPluginName(ref)
	if err != nil {
		return err
	}
	gotRef, err := domain.PluginRefFromMarketplace(actualName, nativeVersion)
	if err != nil || gotRef != ref || actualName != wantActualName {
		return fmt.Errorf("plugin manifest identity %q@%q: %w", actualName, nativeVersion, ErrInvalidArtifact)
	}
	return nil
}
