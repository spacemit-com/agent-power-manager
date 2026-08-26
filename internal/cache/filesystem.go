package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"apm/internal/agentartifact"
	"apm/internal/domain"
	"apm/internal/paths"
	"apm/internal/utils/filesystem"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"
)

// FilesystemCache 将精确版本内容发布到缓存，并物化为公共 staging。
type FilesystemCache struct {
	source          Source
	cacheRoot       string
	stagingPath     string
	stagingTempPath string
	copyDir         func(string, string) error
	removeAll       func(string) error

	locksMu   sync.Mutex
	pathLocks map[string]*sync.Mutex
	stagingMu sync.Mutex
}

// NewFilesystem 创建使用默认缓存和 staging 路径的缓存；source 仅能写入 Cache 提供的私有目标。
func NewFilesystem(source Source) (*FilesystemCache, error) {
	if isNilSource(source) {
		return nil, errors.New("create filesystem cache with nil source")
	}
	cacheRoot, err := paths.CachePath()
	if err != nil {
		return nil, fmt.Errorf("resolve cache path: %w", err)
	}
	stagingPath, err := paths.StagingPath()
	if err != nil {
		return nil, fmt.Errorf("resolve staging path: %w", err)
	}
	stagingTempPath, err := paths.StagingTempPath()
	if err != nil {
		return nil, fmt.Errorf("resolve staging temporary path: %w", err)
	}
	return &FilesystemCache{
		source:          source,
		cacheRoot:       cacheRoot,
		stagingPath:     stagingPath,
		stagingTempPath: stagingTempPath,
		copyDir:         filesystem.CopyDir,
		removeAll:       os.RemoveAll,
		pathLocks:       make(map[string]*sync.Mutex),
	}, nil
}

func isNilSource(source Source) bool {
	if source == nil {
		return true
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// PrepareSkill 确保精确版本 Skill 已原子发布到缓存并返回可安装 staging 根目录。
func (c *FilesystemCache) PrepareSkill(ctx context.Context, entry domain.CatalogEntry) (string, error) {
	if err := entry.Validate(); err != nil {
		return "", fmt.Errorf("validate Skill catalog entry: %w", err)
	}
	ref := entry.Ref
	if !validExactCacheRef(ref, domain.PowerTypeSkill) {
		return "", errors.New("exact Skill type and version are required")
	}
	final := filepath.Join(c.cacheRoot, "skills", ref.Name, ref.Version)
	lock := c.lockFor(final)
	lock.Lock()
	defer lock.Unlock()
	if hit, err := cachedSkill(final, ref); err != nil {
		return "", err
	} else if !hit {
		if err := c.downloadSkill(ctx, entry, final); err != nil {
			return "", err
		}
	}
	c.stagingMu.Lock()
	defer c.stagingMu.Unlock()
	return c.stageSkill(ref)
}

// PrepareAgent 确保精确版本 Agent 已原子发布到缓存并返回可安装 staging 根目录。
func (c *FilesystemCache) PrepareAgent(ctx context.Context, entry domain.CatalogEntry) (string, error) {
	if err := entry.Validate(); err != nil {
		return "", fmt.Errorf("validate Agent catalog entry: %w", err)
	}
	ref := entry.Ref
	if !validExactCacheRef(ref, domain.PowerTypeAgent) {
		return "", errors.New("exact Agent type and version are required")
	}
	final := filepath.Join(c.cacheRoot, "agents", ref.Name, ref.Version)
	lock := c.lockFor(final)
	lock.Lock()
	defer lock.Unlock()
	if hit, err := cachedAgent(ctx, final, ref); err != nil {
		return "", err
	} else if !hit {
		if err := c.downloadAgent(ctx, entry, final); err != nil {
			return "", err
		}
	}
	c.stagingMu.Lock()
	defer c.stagingMu.Unlock()
	return c.stageAgent(ctx, ref)
}

func validExactCacheRef(ref domain.PowerRef, powerType domain.PowerType) bool {
	if ref.Type != powerType || ref.Name == "" || ref.Name == "." || ref.Name == ".." || strings.ContainsAny(ref.Name, `/\\`) || filepath.Base(ref.Name) != ref.Name {
		return false
	}
	if !strings.HasPrefix(ref.Version, "v") {
		return false
	}
	native := strings.TrimPrefix(ref.Version, "v")
	version, err := semver.StrictNewVersion(native)
	return err == nil && version.Prerelease() == "" && version.Metadata() == "" && version.String() == native
}

func (c *FilesystemCache) lockFor(path string) *sync.Mutex {
	c.locksMu.Lock()
	defer c.locksMu.Unlock()
	lock := c.pathLocks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		c.pathLocks[path] = lock
	}
	return lock
}

func (c *FilesystemCache) downloadSkill(ctx context.Context, entry domain.CatalogEntry, final string) (resultErr error) {
	ref := entry.Ref
	temporary, err := c.newTemporaryVersion("skill-" + ref.Name + "-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(temporary); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove Skill temporary version %q: %w", temporary, err))
		}
	}()
	if err := c.source.DownloadSkill(ctx, entry, temporary); err != nil {
		return fmt.Errorf("download Skill %+v: %w", ref, err)
	}
	if hit, err := cachedSkill(temporary, ref); err != nil || !hit {
		if err != nil {
			return err
		}
		return errors.New("downloaded Skill has no physical SKILL.md")
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create Skill cache parent %q: %w", filepath.Dir(final), err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("publish Skill cache %q: %w", final, err)
	}
	return nil
}

func (c *FilesystemCache) downloadAgent(ctx context.Context, entry domain.CatalogEntry, final string) (resultErr error) {
	ref := entry.Ref
	temporary, err := c.newTemporaryVersion("agent-" + ref.Name + "-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(temporary); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove Agent temporary version %q: %w", temporary, err))
		}
	}()
	// Source 在临时目录下写入完整的 agent.yaml + assets/ 目录树；缓存只接受完整目录。
	if err := c.source.DownloadAgent(ctx, entry, temporary); err != nil {
		return fmt.Errorf("download Agent %+v: %w", ref, err)
	}
	if hit, err := cachedAgent(ctx, temporary, ref); err != nil || !hit {
		if err != nil {
			return err
		}
		return errors.New("downloaded Agent directory is not a physical directory")
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create Agent cache parent %q: %w", filepath.Dir(final), err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("publish Agent cache %q: %w", final, err)
	}
	return nil
}

func (c *FilesystemCache) newTemporaryVersion(prefix string) (string, error) {
	if err := os.MkdirAll(filepath.Join(c.cacheRoot, ".tmp"), 0o755); err != nil {
		return "", fmt.Errorf("create cache temporary root: %w", err)
	}
	temporary, err := os.MkdirTemp(filepath.Join(c.cacheRoot, ".tmp"), prefix)
	if err != nil {
		return "", fmt.Errorf("create cache temporary version: %w", err)
	}
	return temporary, nil
}

func cachedSkill(root string, ref domain.PowerRef) (bool, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat Skill cache %q: %w", root, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("Skill cache %q is not a physical directory", root)
	}
	marker, err := os.Lstat(filepath.Join(root, "SKILL.md"))
	if errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("Skill cache %q has no SKILL.md", root)
	}
	if err != nil {
		return false, fmt.Errorf("stat Skill cache marker %q: %w", root, err)
	}
	if !marker.Mode().IsRegular() {
		return false, fmt.Errorf("Skill cache marker %q is not a physical regular file", root)
	}
	raw, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return false, fmt.Errorf("read Skill cache marker %q: %w", root, err)
	}
	name, version, err := cachedSkillIdentity(raw)
	if err != nil {
		return false, fmt.Errorf("validate Skill cache identity %q for %+v: %w", root, ref, err)
	}
	if name != ref.Name || version != ref.Version {
		return false, fmt.Errorf("Skill cache identity %s/%s does not match %+v", name, version, ref)
	}
	return true, nil
}

// cachedSkillIdentity 只读取缓存 Skill 的权威 frontmatter 身份；其余内容语义仍由发布边界负责。
func cachedSkillIdentity(raw []byte) (string, string, error) {
	firstEnd := bytes.IndexByte(raw, '\n')
	if firstEnd < 0 || string(bytes.TrimSpace(raw[:firstEnd])) != "---" {
		return "", "", errors.New("SKILL.md must start with YAML frontmatter")
	}
	rest := raw[firstEnd+1:]
	closing := -1
	lineStart := 0
	for lineStart <= len(rest) {
		lineLength := bytes.IndexByte(rest[lineStart:], '\n')
		lineEnd := len(rest)
		if lineLength >= 0 {
			lineEnd = lineStart + lineLength
		}
		marker := string(bytes.TrimSpace(bytes.TrimSuffix(rest[lineStart:lineEnd], []byte{'\r'})))
		if marker == "---" || marker == "..." {
			closing = lineStart
			break
		}
		if lineLength < 0 {
			break
		}
		lineStart = lineEnd + 1
	}
	if closing < 0 {
		return "", "", errors.New("SKILL.md frontmatter is not closed")
	}
	var document struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(rest[:closing], &document); err != nil {
		return "", "", fmt.Errorf("decode SKILL.md frontmatter: %w", err)
	}
	if document.Name == "" || document.Version == "" {
		return "", "", errors.New("SKILL.md frontmatter requires name and version")
	}
	return document.Name, document.Version, nil
}

// cachedAgent 报告 root 是否为身份与 ref 完全一致的已发布 Agent 版本目录。
// 缺失根目录视为 miss；已存在的目录必须通过 agentartifact.ValidateDirectory
// 的完整目录校验（物理树安全、根闭集、avatar PNG 与 PowerRef 身份）。
// 校验失败返回错误，由调用方 fail closed：绝不删除或联网替换现有缓存。
func cachedAgent(ctx context.Context, root string, ref domain.PowerRef) (bool, error) {
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat Agent cache %q: %w", root, err)
	}
	if _, err := agentartifact.ValidateDirectory(ctx, root, &ref); err != nil {
		return false, fmt.Errorf("validate Agent cache %q for %+v: %w", root, ref, err)
	}
	return true, nil
}

func (c *FilesystemCache) stageSkill(ref domain.PowerRef) (stagingRoot string, resultErr error) {
	candidate, err := c.newStagingCandidate()
	if err != nil {
		return "", err
	}
	defer func() {
		if err := c.removeAll(candidate); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean staging candidate %q: %w", candidate, err))
		}
	}()
	source := filepath.Join(c.cacheRoot, "skills", ref.Name, ref.Version)
	target := filepath.Join(candidate, "skills", ref.Name)
	if err := c.copyDir(source, target); err != nil {
		return "", fmt.Errorf("copy cache path %q to staging path %q: %w", source, target, err)
	}
	if err := c.publishStagingCandidate(candidate); err != nil {
		return "", err
	}
	return c.stagingPath, nil
}

// stageAgent 把已校验的完整 Agent 缓存目录物化为 staging/agents/<name>：
// 整目录复制到 staging candidate 后，发布前对副本执行权威目录校验，
// 校验或发布失败时保留旧 staging 现场。
func (c *FilesystemCache) stageAgent(ctx context.Context, ref domain.PowerRef) (stagingRoot string, resultErr error) {
	candidate, err := c.newStagingCandidate()
	if err != nil {
		return "", err
	}
	defer func() {
		if err := c.removeAll(candidate); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean staging candidate %q: %w", candidate, err))
		}
	}()
	source := filepath.Join(c.cacheRoot, "agents", ref.Name, ref.Version)
	target := filepath.Join(candidate, "agents", ref.Name)
	if err := c.copyDir(source, target); err != nil {
		return "", fmt.Errorf("copy cache path %q to staging path %q: %w", source, target, err)
	}
	if _, err := agentartifact.ValidateDirectory(ctx, target, &ref); err != nil {
		return "", fmt.Errorf("validate staged Agent %q for %+v: %w", target, ref, err)
	}
	if err := c.publishStagingCandidate(candidate); err != nil {
		return "", err
	}
	return c.stagingPath, nil
}

func (c *FilesystemCache) newStagingCandidate() (string, error) {
	parent := filepath.Dir(c.stagingTempPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create staging temporary parent %q: %w", parent, err)
	}
	candidate, err := os.MkdirTemp(parent, filepath.Base(c.stagingTempPath)+"-")
	if err != nil {
		return "", fmt.Errorf("create staging candidate: %w", err)
	}
	return candidate, nil
}

func (c *FilesystemCache) publishStagingCandidate(candidate string) error {
	if err := replaceStaging(candidate, c.stagingPath); err != nil {
		return fmt.Errorf("publish staging candidate %q to %q: %w", candidate, c.stagingPath, err)
	}
	return nil
}
