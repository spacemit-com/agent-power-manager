package pluginresource

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/pluginartifact"
	filesystemutil "apm/internal/utils/filesystem"
)

// validatePhysicalTree 拒绝 symlink 和非目录根，确保后续读取不会逃逸 artifact 边界。
func validatePhysicalTree(root string) error {
	if err := pluginartifact.ValidatePhysicalTree(root); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	return nil
}

// validatePhysicalAncestors 校验可信根以下的既存路径组件，拒绝经祖先 symlink 逃逸。
// 配置根本身由 lifecycle 全局锁协作管理；可移植 stdlib 边界不承诺阻止非协作进程的替换竞态。
func validatePhysicalAncestors(trustedRoot, target string) error {
	relative, err := filepath.Rel(trustedRoot, target)
	if err != nil {
		return fmt.Errorf("resolve plugin artifact path %q below trusted root %q: %w", target, trustedRoot, err)
	}
	if relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("plugin artifact path %q escapes trusted root %q: %w", target, trustedRoot, ErrInvalidArtifact)
	}
	current := trustedRoot
	for _, component := range splitPath(relative) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lstat plugin artifact ancestor %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("plugin artifact ancestor %q is not a physical directory: %w", current, ErrInvalidArtifact)
		}
	}
	return nil
}

func splitPath(path string) []string {
	components := make([]string, 0, 4)
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component != "" && component != "." {
			components = append(components, component)
		}
	}
	return components
}

// validateStagingDisjoint 在任何递归清理前解析物理路径，拒绝 staging 与共享 cache/source 双向包含。
// pluginSource 必须是本次要物化的落盘插件叶子，避免只比较 canonical 叶子而漏掉落盘子树的别名。
func validateStagingDisjoint(cacheRoot string, artifact Artifact, pluginSource, stagingRoot, stagingTempRoot string) error {
	type namedPath struct {
		name     string
		logical  string
		physical string
	}
	resolve := func(name, path string) (namedPath, error) {
		physical, err := filesystemutil.ResolvePhysicalPath(path)
		if err != nil {
			return namedPath{}, fmt.Errorf("resolve physical %s path %q: %w", name, path, errors.Join(ErrInvalidArtifact, err))
		}
		return namedPath{name: name, logical: path, physical: physical}, nil
	}

	sources := make([]namedPath, 0, 3)
	for _, path := range []struct {
		name string
		path string
	}{
		{name: "cache root", path: cacheRoot},
		{name: "artifact root", path: artifact.Root},
		{name: "Plugin source", path: pluginSource},
	} {
		resolved, err := resolve(path.name, path.path)
		if err != nil {
			return err
		}
		sources = append(sources, resolved)
	}
	targets := make([]namedPath, 0, 2)
	for _, path := range []struct {
		name string
		path string
	}{
		{name: "staging root", path: stagingRoot},
		{name: "staging temporary root", path: stagingTempRoot},
	} {
		resolved, err := resolve(path.name, path.path)
		if err != nil {
			return err
		}
		targets = append(targets, resolved)
	}
	// 两个写入根也必须物理分离；否则临时根的前置清理可能经直接或 symlink 别名删除旧 staging。
	for leftIndex := 0; leftIndex < len(targets); leftIndex++ {
		for rightIndex := leftIndex + 1; rightIndex < len(targets); rightIndex++ {
			left, right := targets[leftIndex], targets[rightIndex]
			rightWithinLeft, err := filesystemutil.PathEqualOrWithin(left.physical, right.physical)
			if err != nil {
				return fmt.Errorf("compare physical %s and %s paths: %w", left.name, right.name, errors.Join(ErrInvalidArtifact, err))
			}
			leftWithinRight, err := filesystemutil.PathEqualOrWithin(right.physical, left.physical)
			if err != nil {
				return fmt.Errorf("compare physical %s and %s paths: %w", right.name, left.name, errors.Join(ErrInvalidArtifact, err))
			}
			if rightWithinLeft || leftWithinRight {
				return fmt.Errorf(
					"physical %s path %q overlaps %s path %q: %w",
					right.name, right.logical, left.name, left.logical, ErrInvalidArtifact,
				)
			}
		}
	}

	for _, source := range sources {
		for _, target := range targets {
			targetWithinSource, err := filesystemutil.PathEqualOrWithin(source.physical, target.physical)
			if err != nil {
				return fmt.Errorf("compare physical %s and %s paths: %w", source.name, target.name, errors.Join(ErrInvalidArtifact, err))
			}
			sourceWithinTarget, err := filesystemutil.PathEqualOrWithin(target.physical, source.physical)
			if err != nil {
				return fmt.Errorf("compare physical %s and %s paths: %w", target.name, source.name, errors.Join(ErrInvalidArtifact, err))
			}
			if targetWithinSource || sourceWithinTarget {
				return fmt.Errorf(
					"physical %s path %q overlaps %s path %q: %w",
					target.name, target.logical, source.name, source.logical, ErrInvalidArtifact,
				)
			}
		}
	}
	return nil
}
