package cloud

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"apm/internal/domain"
	"apm/internal/paths"

	"github.com/Masterminds/semver/v3"
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

// List 按类型、名称和 SemVer 升序返回有效的本地 Power 资源；枚举期间必须响应取消。
func (c *LocalClient) List(ctx context.Context) ([]domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.ReadDir(c.resourcesRoot); err != nil {
		return nil, fmt.Errorf("read resource root %q: %w", c.resourcesRoot, err)
	}
	refs, err := c.listResources(ctx, "skills", "SKILL.md", domain.PowerTypeSkill)
	if err != nil {
		return nil, err
	}
	mcpRefs, err := c.listResources(ctx, "mcps", "MCP.json", domain.PowerTypeMCP)
	if err != nil {
		return nil, err
	}
	refs = append(refs, mcpRefs...)
	pluginRefs, err := c.listPlugins(ctx)
	if err != nil {
		return nil, err
	}
	refs = append(refs, pluginRefs...)
	agentRefs, err := c.listAgents(ctx)
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
		return semver.MustParse(refs[i].Version).LessThan(semver.MustParse(refs[j].Version))
	})
	return refs, nil
}

// Resolve 返回同类型同名能力的精确版本或最大 SemVer，且不改变 List 返回的引用切片。
func (c *LocalClient) Resolve(ctx context.Context, query domain.PowerRef) (domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return domain.PowerRef{}, err
	}
	if !safePowerIdentity(query) {
		return domain.PowerRef{}, ErrNotFound
	}
	refs, err := c.List(ctx)
	if err != nil {
		return domain.PowerRef{}, err
	}
	var latest domain.PowerRef
	found := false
	for _, ref := range refs {
		if ref.Type != query.Type || ref.Name != query.Name {
			continue
		}
		if query.Version != "" {
			if ref.Version == query.Version {
				return ref, nil
			}
			continue
		}
		if !found || semver.MustParse(ref.Version).GreaterThan(semver.MustParse(latest.Version)) {
			latest = ref
			found = true
		}
	}
	if !found {
		return domain.PowerRef{}, ErrNotFound
	}
	return latest, nil
}

func safePowerIdentity(ref domain.PowerRef) bool {
	if ref.Name == "" || ref.Name == "." || ref.Name == ".." || strings.ContainsAny(ref.Name, `/\\`) || filepath.Base(ref.Name) != ref.Name {
		return false
	}
	switch ref.Type {
	case domain.PowerTypeSkill, domain.PowerTypeMCP, domain.PowerTypePlugin, domain.PowerTypeAgent:
		return true
	default:
		return false
	}
}

func (c *LocalClient) listResources(ctx context.Context, directory, markerName string, powerType domain.PowerType) ([]domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s resource directory %q: %w", powerType, entry.Name(), err)
		}
		for _, version := range versions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
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
