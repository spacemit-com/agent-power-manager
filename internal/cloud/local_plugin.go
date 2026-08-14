package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

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
