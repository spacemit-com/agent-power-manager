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

// ErrInvalidPluginIdentity 表示 Plugin 逻辑名称、类型或版本不满足规范。
var ErrInvalidPluginIdentity = errors.New("invalid plugin identity")

// ActualPluginName 将 APM 逻辑引用映射为 marketplace 中唯一的版本化实际名。
func ActualPluginName(ref PowerRef) (string, error) {
	if ref.Type != PowerTypePlugin || !isSafePluginSegment(ref.Name) {
		return "", fmt.Errorf("plugin reference %+v: %w", ref, ErrInvalidPluginIdentity)
	}
	version, err := canonicalAPMPluginVersion(ref.Version)
	if err != nil {
		return "", err
	}
	return ref.Name + "-" + version, nil
}

// PluginRuntimeName 返回目标 Agent 使用的逻辑 Plugin 名称；版本只用于身份校验。
func PluginRuntimeName(ref PowerRef) (string, error) {
	if ref.Type != PowerTypePlugin || !isSafePluginSegment(ref.Name) {
		return "", fmt.Errorf("plugin reference %+v: %w", ref, ErrInvalidPluginIdentity)
	}
	if _, err := canonicalAPMPluginVersion(ref.Version); err != nil {
		return "", err
	}
	return ref.Name, nil
}

// PluginMarketplaceName 返回逻辑 Plugin 对应的固定 marketplace 名称。
func PluginMarketplaceName(ref PowerRef) (string, error) {
	if ref.Type != PowerTypePlugin || !isSafePluginSegment(ref.Name) {
		return "", fmt.Errorf("plugin reference %+v: %w", ref, ErrInvalidPluginIdentity)
	}
	if _, err := canonicalAPMPluginVersion(ref.Version); err != nil {
		return "", err
	}
	return "apm-" + ref.Name, nil
}

func canonicalAPMPluginVersion(version string) (string, error) {
	if !strings.HasPrefix(version, "v") {
		return "", fmt.Errorf("plugin version %q must use vMAJOR.MINOR.PATCH: %w", version, ErrInvalidPluginIdentity)
	}
	native, err := canonicalNativePluginVersion(strings.TrimPrefix(version, "v"))
	if err != nil || "v"+native != version {
		return "", fmt.Errorf("plugin version %q must use vMAJOR.MINOR.PATCH: %w", version, errors.Join(ErrInvalidPluginIdentity, err))
	}
	return version, nil
}

func canonicalNativePluginVersion(version string) (string, error) {
	parsed, err := semver.StrictNewVersion(version)
	if err != nil || parsed.Prerelease() != "" || parsed.Metadata() != "" || parsed.String() != version {
		return "", fmt.Errorf("native plugin version %q must use MAJOR.MINOR.PATCH: %w", version, errors.Join(ErrInvalidPluginIdentity, err))
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
