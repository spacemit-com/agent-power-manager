package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// PluginDeploymentMode 表示 Plugin 由 marketplace 或本地文件系统部署。
type PluginDeploymentMode string

const (
	// PluginDeploymentMarketplace 表示由目标 Agent 的 marketplace 安装插件。
	PluginDeploymentMarketplace PluginDeploymentMode = "marketplace"
	// PluginDeploymentFilesystem 表示由 APM 发布插件目录。
	PluginDeploymentFilesystem PluginDeploymentMode = "filesystem"
)

// ErrInvalidPluginDefinition 表示 Plugin 名称、版本或 Cloud 定义不满足约束。
var ErrInvalidPluginDefinition = errors.New("invalid plugin definition")

// MarketplaceDefinition 描述目标 Agent 注册 Cloud marketplace 所需的身份和来源。
type MarketplaceDefinition struct {
	Name   string
	Source string
}

// PluginDefinition 描述精确 Plugin 版本的 Agent 实际名称和 marketplace。
type PluginDefinition struct {
	ActualName  string
	Marketplace MarketplaceDefinition
}

// ActualPluginName 将 APM 逻辑引用映射为 marketplace 中唯一的版本化实际名。
func ActualPluginName(ref PowerRef) (string, error) {
	if ref.Type != PowerTypePlugin || !isSafePluginSegment(ref.Name) {
		return "", fmt.Errorf("plugin reference %+v: %w", ref, ErrInvalidPluginDefinition)
	}
	version, err := canonicalAPMPluginVersion(ref.Version)
	if err != nil {
		return "", err
	}
	return ref.Name + "-" + version, nil
}

// PluginRefFromMarketplace 将原生 marketplace 条目还原为 APM 逻辑引用。
func PluginRefFromMarketplace(actualName, nativeVersion string) (PowerRef, error) {
	version, err := canonicalNativePluginVersion(nativeVersion)
	if err != nil {
		return PowerRef{}, err
	}
	suffix := "-v" + version
	if !strings.HasSuffix(actualName, suffix) {
		return PowerRef{}, fmt.Errorf("plugin actual name %q does not end with %q: %w", actualName, suffix, ErrInvalidPluginDefinition)
	}
	name := strings.TrimSuffix(actualName, suffix)
	if !isSafePluginSegment(name) || !isSafePluginSegment(actualName) {
		return PowerRef{}, fmt.Errorf("plugin actual name %q: %w", actualName, ErrInvalidPluginDefinition)
	}
	return PowerRef{Name: name, Type: PowerTypePlugin, Version: "v" + version}, nil
}

// Validate 校验 Cloud 返回的安装定义，不清洗或改写原始值。
func (definition PluginDefinition) Validate() error {
	if !isSafePluginSegment(definition.ActualName) ||
		strings.TrimSpace(definition.Marketplace.Name) == "" ||
		strings.TrimSpace(definition.Marketplace.Source) == "" {
		return fmt.Errorf("plugin definition %+v: %w", definition, ErrInvalidPluginDefinition)
	}
	return nil
}

func canonicalAPMPluginVersion(version string) (string, error) {
	if !strings.HasPrefix(version, "v") {
		return "", fmt.Errorf("plugin version %q must use vMAJOR.MINOR.PATCH: %w", version, ErrInvalidPluginDefinition)
	}
	native, err := canonicalNativePluginVersion(strings.TrimPrefix(version, "v"))
	if err != nil || "v"+native != version {
		return "", fmt.Errorf("plugin version %q must use vMAJOR.MINOR.PATCH: %w", version, errors.Join(ErrInvalidPluginDefinition, err))
	}
	return version, nil
}

func canonicalNativePluginVersion(version string) (string, error) {
	parsed, err := semver.StrictNewVersion(version)
	if err != nil || parsed.Prerelease() != "" || parsed.Metadata() != "" || parsed.String() != version {
		return "", fmt.Errorf("native plugin version %q must use MAJOR.MINOR.PATCH: %w", version, errors.Join(ErrInvalidPluginDefinition, err))
	}
	return parsed.String(), nil
}

func isSafePluginSegment(name string) bool {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return false
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\\`) {
		return false
	}
	return filepath.Clean(name) == name
}
