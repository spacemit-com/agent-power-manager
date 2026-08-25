package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/cloud"
	"apm/internal/domain"
)

// DownloadPlugin 复制指定版本的完整共享 Plugin 包；Cloud 不生成 marketplace，也不按 Agent 裁剪入口。
// 目标必须不存在或为空物理目录，发布失败时仅清理本次调用创建的临时目录。
func (c *Client) DownloadPlugin(ctx context.Context, entry domain.CatalogEntry, destination string) error {
	if _, err := domain.PluginRuntimeName(entry.Ref); err != nil {
		return cloud.ErrNotFound
	}
	content, err := c.loadMatchingCatalogContent(ctx, entry, domain.PowerTypePlugin)
	if err != nil {
		return err
	}
	source, err := c.catalogSourcePath(entry.Ref)
	if err != nil {
		return err
	}
	resolvedDestination, err := absoluteDestination(destination)
	if err != nil {
		return err
	}
	if err := validateTreeDestination(resolvedDestination); err != nil {
		return err
	}
	if err := validateCopyBoundary(source, resolvedDestination); err != nil {
		return err
	}
	if content.tree == nil {
		return unsafeContent("Local Plugin catalog content has no captured tree")
	}
	return copyTreeAtomically(ctx, content.tree, resolvedDestination, "Plugin", c.beforeTreePublish)
}

// listPlugins 根据 `<logical>/<version>` 共享原始包目录发现逻辑 Plugin，不审计各 Agent 入口完整性。
func (c *Client) listPlugins(ctx context.Context) ([]domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !logical.IsDir() {
			continue
		}
		logicalRoot := filepath.Join(root, logical.Name())
		versions, err := os.ReadDir(logicalRoot)
		if err != nil {
			return nil, fmt.Errorf("read Plugin resource %q: %w", logicalRoot, err)
		}
		for _, version := range versions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !version.IsDir() {
				continue
			}
			if _, valid := canonicalPowerVersion(version.Name()); !valid {
				continue
			}
			ref := domain.PowerRef{Name: logical.Name(), Type: domain.PowerTypePlugin, Version: version.Name()}
			if _, err := domain.PluginRuntimeName(ref); err != nil {
				continue
			}
			refs = append(refs, ref)
		}
	}
	return refs, nil
}
