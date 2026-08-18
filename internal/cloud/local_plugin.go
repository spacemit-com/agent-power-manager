package cloud

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/utils/filesystem"
)

// DownloadPlugin 复制精确 Agent/ref 的原始 Plugin 包；Cloud 不生成 marketplace，也不写入 APM 缓存。
// 目标必须不存在或为空物理目录，复制失败后的半成品由调用方清理。
func (c *LocalClient) DownloadPlugin(ctx context.Context, agent domain.AgentID, ref domain.PowerRef, destination string) error {
	if !supportedPluginPackageAgent(agent) {
		return ErrNotFound
	}
	if _, err := domain.ActualPluginName(ref); err != nil {
		return ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	source := filepath.Join(c.resourcesRoot, "plugins", ref.Name, ref.Version, string(agent))
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat raw Plugin package %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("raw Plugin package %q is not a physical directory", source)
	}
	if err := validatePluginSourcePackage(c.resourcesRoot, source); err != nil {
		return err
	}
	if destination == "" {
		return errors.New("raw Plugin destination is empty")
	}
	resolvedDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve raw Plugin destination %q: %w", destination, err)
	}
	resolvedDestination = filepath.Clean(resolvedDestination)
	if err := validatePluginDownloadDestination(resolvedDestination); err != nil {
		return err
	}
	if err := validatePluginCopyBoundary(source, resolvedDestination); err != nil {
		return err
	}
	if err := filesystem.CopyDir(source, resolvedDestination); err != nil {
		return fmt.Errorf("copy raw Plugin package %q to %q: %w", source, resolvedDestination, err)
	}
	return nil
}

// validatePluginSourcePackage 在 CopyDir 打开 payload 前校验可信资源根、祖先组件和完整后代树。
func validatePluginSourcePackage(resourcesRoot, source string) error {
	physicalRoot, err := filesystem.ResolvePhysicalPath(resourcesRoot)
	if err != nil {
		return fmt.Errorf("resolve physical resource root %q: %w", resourcesRoot, err)
	}
	physicalSource, err := filesystem.ResolvePhysicalPath(source)
	if err != nil {
		return fmt.Errorf("resolve physical raw Plugin package %q: %w", source, err)
	}
	insideRoot, err := filesystem.PathEqualOrWithin(physicalRoot, physicalSource)
	if err != nil {
		return fmt.Errorf("compare physical resource root %q and raw Plugin package %q: %w", physicalRoot, physicalSource, err)
	}
	if !insideRoot {
		return fmt.Errorf("raw Plugin package %q escapes physical resource root %q", source, physicalRoot)
	}

	relative, err := filepath.Rel(resourcesRoot, source)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.Join(fmt.Errorf("raw Plugin package %q escapes resource root %q", source, resourcesRoot), err)
	}
	current := resourcesRoot
	components := append([]string{""}, strings.Split(relative, string(filepath.Separator))...)
	for _, component := range components {
		if component != "" && component != "." {
			current = filepath.Join(current, component)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("lstat raw Plugin package ancestor %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("raw Plugin package ancestor %q is not a physical directory", current)
		}
	}
	if err := pluginartifact.ValidatePhysicalTree(source); err != nil {
		return fmt.Errorf("validate raw Plugin package %q: %w", source, err)
	}
	return nil
}

// listPlugins 只根据 `<logical>/<version>/<agent>` 原始包目录发现逻辑 Plugin；多个 Agent 包合并为同一引用。
func (c *LocalClient) listPlugins() ([]domain.PowerRef, error) {
	root := filepath.Join(c.resourcesRoot, "plugins")
	logicalEntries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return []domain.PowerRef{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Plugin resources %q: %w", root, err)
	}

	refs := make([]domain.PowerRef, 0)
	for _, logical := range logicalEntries {
		if !logical.IsDir() {
			continue
		}
		logicalRoot := filepath.Join(root, logical.Name())
		versions, err := os.ReadDir(logicalRoot)
		if err != nil {
			return nil, fmt.Errorf("read Plugin resource %q: %w", logicalRoot, err)
		}
		for _, version := range versions {
			if !version.IsDir() {
				continue
			}
			ref := domain.PowerRef{Name: logical.Name(), Type: domain.PowerTypePlugin, Version: version.Name()}
			if _, err := domain.ActualPluginName(ref); err != nil {
				continue
			}
			hasPackage, err := hasSupportedPluginPackage(filepath.Join(logicalRoot, version.Name()))
			if err != nil {
				return nil, err
			}
			if hasPackage {
				refs = append(refs, ref)
			}
		}
	}
	return refs, nil
}

func hasSupportedPluginPackage(versionRoot string) (bool, error) {
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes} {
		path := filepath.Join(versionRoot, string(agent))
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("stat raw Plugin package %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			return true, nil
		}
	}
	return false, nil
}

func validatePluginCopyBoundary(source, destination string) error {
	physicalSource, err := filesystem.ResolvePhysicalPath(source)
	if err != nil {
		return fmt.Errorf("resolve raw Plugin package %q: %w", source, err)
	}
	physicalDestination, err := filesystem.ResolvePhysicalPath(destination)
	if err != nil {
		return fmt.Errorf("resolve raw Plugin destination %q: %w", destination, err)
	}
	insideSource, err := filesystem.PathEqualOrWithin(physicalSource, physicalDestination)
	if err != nil {
		return fmt.Errorf("compare raw Plugin package %q and destination %q: %w", physicalSource, physicalDestination, err)
	}
	if insideSource {
		return fmt.Errorf("raw Plugin destination %q must not equal or be contained by source %q", destination, source)
	}
	return nil
}

func supportedPluginPackageAgent(agent domain.AgentID) bool {
	switch agent {
	case domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes:
		return true
	default:
		return false
	}
}

func validatePluginDownloadDestination(destination string) error {
	info, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return validatePluginDestinationAncestors(destination)
	}
	if err != nil {
		return fmt.Errorf("stat raw Plugin destination %q: %w", destination, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("raw Plugin destination %q is not a physical directory", destination)
	}
	if err := validatePluginDestinationAncestors(destination); err != nil {
		return err
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("read raw Plugin destination %q: %w", destination, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("raw Plugin destination %q is not empty", destination)
	}
	return nil
}

func validatePluginDestinationAncestors(destination string) error {
	volume := filepath.VolumeName(destination)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(destination, current)
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stat raw Plugin destination ancestor %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("raw Plugin destination ancestor %q is not a physical directory", current)
		}
	}
	return nil
}
