package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"apm/internal/domain"
	"apm/internal/paths"
)

// LocalClient 从本地目录读取能力资源。
type LocalClient struct {
	resourcesRoot string
	cacheRoot     string
	cacheTempRoot string
}

// NewLocalClient 使用绝对资源根目录创建本地客户端。
func NewLocalClient(resourcesRoot string) (*LocalClient, error) {
	if !filepath.IsAbs(resourcesRoot) {
		return nil, fmt.Errorf("resource root must be absolute: %q", resourcesRoot)
	}
	cacheRoot, err := paths.CachePath()
	if err != nil {
		return nil, fmt.Errorf("resolve cache path: %w", err)
	}
	cacheTempRoot, err := paths.CacheTempPath()
	if err != nil {
		return nil, fmt.Errorf("resolve cache temporary path: %w", err)
	}
	return &LocalClient{
		resourcesRoot: resourcesRoot,
		cacheRoot:     cacheRoot,
		cacheTempRoot: cacheTempRoot,
	}, nil
}

// List 按稳定的类型、名称、版本顺序返回所有有效的本地 Power 资源。
func (c *LocalClient) List() ([]domain.PowerRef, error) {
	if _, err := os.ReadDir(c.resourcesRoot); err != nil {
		return nil, fmt.Errorf("read resource root %q: %w", c.resourcesRoot, err)
	}
	refs, err := c.listResources("skills", "SKILL.md", domain.PowerTypeSkill)
	if err != nil {
		return nil, err
	}
	mcpRefs, err := c.listResources("mcps", "MCP.json", domain.PowerTypeMCP)
	if err != nil {
		return nil, err
	}
	refs = append(refs, mcpRefs...)
	pluginRefs, err := c.listPlugins()
	if err != nil {
		return nil, err
	}
	refs = append(refs, pluginRefs...)
	agentRefs, err := c.listAgents()
	if err != nil {
		return nil, err
	}
	refs = append(refs, agentRefs...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Type != refs[j].Type {
			return refs[i].Type < refs[j].Type
		}
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Version < refs[j].Version
	})
	return refs, nil
}

func (c *LocalClient) listResources(directory, markerName string, powerType domain.PowerType) ([]domain.PowerRef, error) {
	root := filepath.Join(c.resourcesRoot, directory)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return []domain.PowerRef{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s directory %q: %w", directory, root, err)
	}
	refs := make([]domain.PowerRef, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s resource directory %q: %w", powerType, entry.Name(), err)
		}
		for _, version := range versions {
			if !version.IsDir() {
				continue
			}
			markerPath := filepath.Join(root, entry.Name(), version.Name(), markerName)
			marker, err := os.Lstat(markerPath)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("stat %s marker %q: %w", powerType, markerPath, err)
			}
			if !marker.Mode().IsRegular() {
				continue
			}
			refs = append(refs, domain.PowerRef{Name: entry.Name(), Type: powerType, Version: version.Name()})
		}
	}
	return refs, nil
}
