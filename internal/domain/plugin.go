package domain

import (
	"errors"
	"fmt"
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

// IsCanonicalPowerName 校验 APC Power 的 1-64 字节小写 kebab 名称；连字符只能分隔非空字母数字段。
func IsCanonicalPowerName(name string) bool {
	if len(name) < 1 || len(name) > 64 || name[0] == '-' || name[len(name)-1] == '-' || strings.Contains(name, "--") {
		return false
	}
	for _, character := range []byte(name) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return false
	}
	return true
}

func isSafePluginSegment(name string) bool {
	return IsCanonicalPowerName(name)
}
