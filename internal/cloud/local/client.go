// Package local 提供只读本地资源目录的 Cloud Client 实现。
package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"apm/internal/cloud"
	"apm/internal/domain"
	"github.com/Masterminds/semver/v3"
)

// Client 从绝对本地目录读取能力资源，不拥有缓存、staging 或用户目录。
type Client struct {
	resourcesRoot                string
	beforeAgentPublish           func()
	beforeTreeSnapshot           func()
	beforeSnapshotFileOpen       func(string)
	afterSnapshotDataOpen        func(string)
	beforeTreePublish            func()
	afterMCPDecode               func()
	afterMCPClone                func()
	catalogDestinationAfterOpen  func(string, *os.File)
	catalogDestinationStat       func(*os.File) (fs.FileInfo, error)
	catalogArchiveAfterBodyChunk func(string)
}

var _ cloud.Client = (*Client)(nil)

// New 使用绝对资源根目录创建本地 Client；相对路径不被接受。
func New(resourcesRoot string) (*Client, error) {
	if !filepath.IsAbs(resourcesRoot) {
		return nil, fmt.Errorf("resource root must be absolute: %q", resourcesRoot)
	}
	return &Client{
		resourcesRoot: resourcesRoot,
	}, nil
}

// catalogRefs 仅为 Local Catalog 构建器枚举受信 fixture；不对 lifecycle 暴露独立发现接口。
func (c *Client) catalogRefs(ctx context.Context) ([]domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.ReadDir(c.resourcesRoot); err != nil {
		return nil, fmt.Errorf("read resource root %q: %w", c.resourcesRoot, err)
	}
	refs, err := c.listResources(ctx, "skills", domain.PowerTypeSkill, "SKILL.md", "skill-card.md")
	if err != nil {
		return nil, err
	}
	mcpRefs, err := c.listResources(ctx, "mcps", domain.PowerTypeMCP, "MCP.json")
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
		left, leftOK := canonicalPowerVersion(refs[i].Version)
		right, rightOK := canonicalPowerVersion(refs[j].Version)
		if !leftOK || !rightOK {
			return refs[i].Version < refs[j].Version
		}
		return left.LessThan(right)
	})
	return refs, nil
}

// canonicalPowerVersion 解析严格的 vMAJOR.MINOR.PATCH 版本；预发布和构建元数据不属于本地资源标识。
func canonicalPowerVersion(version string) (*semver.Version, bool) {
	if !strings.HasPrefix(version, "v") {
		return nil, false
	}
	native := strings.TrimPrefix(version, "v")
	parsed, err := semver.StrictNewVersion(native)
	if err != nil || parsed.Prerelease() != "" || parsed.Metadata() != "" || parsed.String() != native {
		return nil, false
	}
	return parsed, true
}

func safePowerIdentity(ref domain.PowerRef) bool {
	if !domain.IsCanonicalPowerName(ref.Name) {
		return false
	}
	switch ref.Type {
	case domain.PowerTypeSkill, domain.PowerTypeMCP, domain.PowerTypePlugin, domain.PowerTypeExpert:
		return true
	default:
		return false
	}
}

// listResources 仅枚举名称、版本与全部物理 marker 均符合 Cloud 契约的资源。
func (c *Client) listResources(ctx context.Context, directory string, powerType domain.PowerType, markerNames ...string) ([]domain.PowerRef, error) {
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
		if !domain.IsCanonicalPowerName(entry.Name()) {
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
			if _, valid := canonicalPowerVersion(version.Name()); !valid {
				continue
			}
			complete := true
			for _, markerName := range markerNames {
				markerPath := filepath.Join(root, entry.Name(), version.Name(), markerName)
				marker, err := os.Lstat(markerPath)
				if errors.Is(err, fs.ErrNotExist) {
					complete = false
					break
				}
				if err != nil {
					return nil, fmt.Errorf("stat %s marker %q: %w", powerType, markerPath, err)
				}
				if !marker.Mode().IsRegular() {
					complete = false
					break
				}
			}
			if !complete {
				continue
			}
			refs = append(refs, domain.PowerRef{Name: entry.Name(), Type: powerType, Version: version.Name()})
		}
	}
	return refs, nil
}
