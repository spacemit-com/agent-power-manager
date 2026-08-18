package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/utils/filesystem"
)

type pluginMarketplaceDocument struct {
	Name    string                   `json:"name"`
	Plugins []pluginMarketplaceEntry `json:"plugins"`
}

type pluginMarketplaceEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

type localPluginRecord struct {
	ref        domain.PowerRef
	definition domain.PluginDefinition
	sourcePath string
}

// PluginPackageDownloader 按 Agent 下载原始 Plugin 包到调用方控制的空目录。
// 该过渡接口不生成 marketplace，也不持有 APM cache 或 staging。
// 复制中途失败可能留下部分目标，由创建该目标的调用方负责清理。
type PluginPackageDownloader interface {
	DownloadPluginTo(context.Context, domain.AgentID, domain.PowerRef, string) error
}

// DownloadPluginTo 复制精确 Agent/ref 的原始 Plugin 包；目标必须不存在或为空物理目录。
// 复制失败时目标可能是半成品，其生命周期和清理由调用方负责。
func (c *LocalClient) DownloadPluginTo(ctx context.Context, agent domain.AgentID, ref domain.PowerRef, destination string) error {
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

// GetPlugin 从 Claude marketplace 权威索引读取精确版本的 Plugin 安装定义。
func (c *LocalClient) GetPlugin(ref domain.PowerRef) (domain.PluginDefinition, error) {
	if ref.Type != domain.PowerTypePlugin || ref.Name == "" || ref.Version == "" {
		return domain.PluginDefinition{}, ErrNotFound
	}
	records, err := c.pluginRecords()
	if err != nil {
		return domain.PluginDefinition{}, err
	}
	for _, record := range records {
		if record.ref == ref {
			return record.definition, nil
		}
	}
	return domain.PluginDefinition{}, ErrNotFound
}

func (c *LocalClient) listPlugins() ([]domain.PowerRef, error) {
	records, err := c.pluginRecords()
	if err != nil {
		return nil, err
	}
	refs := make([]domain.PowerRef, 0, len(records))
	for _, record := range records {
		refs = append(refs, record.ref)
	}
	return refs, nil
}

// DownloadPlugin 将指定版本的文件型 Plugin 原子复制到本地缓存。
func (c *LocalClient) DownloadPlugin(ref domain.PowerRef) (resultErr error) {
	if ref.Version == "" {
		return errors.New("power version is required")
	}
	if ref.Type != domain.PowerTypePlugin {
		return ErrNotFound
	}
	records, err := c.pluginRecords()
	if err != nil {
		return err
	}
	var source string
	for _, record := range records {
		if record.ref == ref {
			source = record.sourcePath
			break
		}
	}
	if source == "" {
		return ErrNotFound
	}
	if err := os.RemoveAll(c.cacheTempRoot); err != nil {
		return fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err)
	}
	defer func() {
		if err := os.RemoveAll(c.cacheTempRoot); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err))
		}
	}()

	final := filepath.Join(c.cacheRoot, "plugins", ref.Name, ref.Version)
	temporary := filepath.Join(c.cacheTempRoot, "plugins", ref.Name, ref.Version)
	if info, err := os.Stat(final); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("cached Plugin %q is not a directory", final)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat cached Plugin %q: %w", final, err)
	}
	if err := filesystem.CopyDir(source, temporary); err != nil {
		return fmt.Errorf("copy source Plugin %q: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create Plugin cache parent %q: %w", filepath.Dir(final), err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("commit cached Plugin %q: %w", final, err)
	}
	return nil
}

func (c *LocalClient) pluginRecords() ([]localPluginRecord, error) {
	marketplaceRoot := filepath.Join(c.resourcesRoot, "plugins")
	indexPath := filepath.Join(marketplaceRoot, ".claude-plugin", "marketplace.json")
	info, err := os.Lstat(indexPath)
	if errors.Is(err, fs.ErrNotExist) {
		return []localPluginRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat Plugin marketplace %q: %w", indexPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Plugin marketplace %q is not a regular file", indexPath)
	}
	contents, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("read Plugin marketplace %q: %w", indexPath, err)
	}
	document, err := decodePluginMarketplace(contents)
	if err != nil {
		return nil, fmt.Errorf("decode Plugin marketplace %q: %w", indexPath, err)
	}
	if document.Name == "" {
		return nil, fmt.Errorf("Plugin marketplace %q has empty name", indexPath)
	}

	physicalRoot, err := filesystem.ResolvePhysicalPath(marketplaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve Plugin marketplace root %q: %w", marketplaceRoot, err)
	}
	records := make([]localPluginRecord, 0, len(document.Plugins))
	seen := make(map[domain.PowerRef]struct{}, len(document.Plugins))
	for index, entry := range document.Plugins {
		ref, err := domain.PluginRefFromMarketplace(entry.Name, entry.Version)
		if err != nil {
			return nil, fmt.Errorf("Plugin marketplace entry %d: %w", index, err)
		}
		if _, exists := seen[ref]; exists {
			return nil, fmt.Errorf("Plugin marketplace contains duplicate ref %+v", ref)
		}
		seen[ref] = struct{}{}

		sourcePath, err := validateLocalPluginSource(physicalRoot, marketplaceRoot, entry.Source)
		if err != nil {
			return nil, fmt.Errorf("Plugin marketplace entry %d source %q: %w", index, entry.Source, err)
		}
		definition := domain.PluginDefinition{
			ActualName: entry.Name,
			Marketplace: domain.MarketplaceDefinition{
				Name:   document.Name,
				Source: marketplaceRoot,
			},
		}
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("Plugin marketplace entry %d: %w", index, err)
		}
		records = append(records, localPluginRecord{ref: ref, definition: definition, sourcePath: sourcePath})
	}
	return records, nil
}

func validateLocalPluginSource(physicalRoot, marketplaceRoot, source string) (string, error) {
	if source == "" || filepath.IsAbs(source) {
		return "", errors.New("source must be a non-empty relative path")
	}
	lexical := filepath.Clean(filepath.Join(marketplaceRoot, source))
	within, err := filesystem.PathEqualOrWithin(marketplaceRoot, lexical)
	if err != nil || !within {
		return "", fmt.Errorf("source escapes marketplace root: %w", err)
	}
	info, err := os.Lstat(lexical)
	if err != nil {
		return "", fmt.Errorf("stat source %q: %w", lexical, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("source %q is not a physical directory", lexical)
	}
	physicalSource, err := filesystem.ResolvePhysicalPath(lexical)
	if err != nil {
		return "", fmt.Errorf("resolve source %q: %w", lexical, err)
	}
	within, err = filesystem.PathEqualOrWithin(physicalRoot, physicalSource)
	if err != nil || !within {
		return "", fmt.Errorf("physical source %q escapes marketplace root: %w", physicalSource, err)
	}
	return lexical, nil
}

func decodePluginMarketplace(contents []byte) (pluginMarketplaceDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var document pluginMarketplaceDocument
	if err := decoder.Decode(&document); err != nil {
		return pluginMarketplaceDocument{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return pluginMarketplaceDocument{}, fmt.Errorf("decode trailing JSON: %w", err)
	}
	return document, nil
}
