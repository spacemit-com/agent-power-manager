package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"apm/internal/domain"
	"apm/internal/paths"
	"apm/internal/utils/filesystem"
)

// LocalClient reads power resources from a local directory.
type LocalClient struct {
	resourcesRoot string
	cacheRoot     string
	cacheTempRoot string
}

// NewLocalClient creates a local client for an absolute resource root.
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

// List returns all valid local Skill resources in stable type, name, version order.
func (c *LocalClient) List() ([]domain.PowerRef, error) {
	if _, err := os.ReadDir(c.resourcesRoot); err != nil {
		return nil, fmt.Errorf("read resource root %q: %w", c.resourcesRoot, err)
	}
	skillsRoot := filepath.Join(c.resourcesRoot, "skills")
	skills, err := os.ReadDir(skillsRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return []domain.PowerRef{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills directory %q: %w", skillsRoot, err)
	}

	var refs []domain.PowerRef
	for _, skill := range skills {
		if !skill.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(skillsRoot, skill.Name()))
		if err != nil {
			return nil, fmt.Errorf("read skill directory %q: %w", skill.Name(), err)
		}
		for _, version := range versions {
			if !version.IsDir() {
				continue
			}
			marker, err := os.Stat(filepath.Join(skillsRoot, skill.Name(), version.Name(), "SKILL.md"))
			if err != nil || !marker.Mode().IsRegular() {
				continue
			}
			refs = append(refs, domain.PowerRef{Name: skill.Name(), Type: domain.PowerTypeSkill, Version: version.Name()})
		}
	}
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

// Get copies a Skill resource to the local cache atomically.
func (c *LocalClient) Get(ref domain.PowerRef) (resultErr error) {
	if ref.Version == "" {
		return errors.New("power version is required")
	}
	if ref.Type != domain.PowerTypeSkill {
		return ErrNotFound
	}
	if !validPathComponent(ref.Name) || !validPathComponent(ref.Version) {
		return errors.New("power name and version must be path components")
	}
	if err := os.RemoveAll(c.cacheTempRoot); err != nil {
		return fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err)
	}
	defer func() {
		if err := os.RemoveAll(c.cacheTempRoot); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err))
		}
	}()

	final := filepath.Join(c.cacheRoot, "skills", ref.Name, ref.Version)
	temporary := filepath.Join(c.cacheTempRoot, "skills", ref.Name, ref.Version)
	source := filepath.Join(c.resourcesRoot, "skills", ref.Name, ref.Version)

	if _, err := os.Stat(final); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat cache power %q: %w", final, err)
	}
	sourceInfo, err := os.Stat(source)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !sourceInfo.IsDir()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source skill %q: %w", source, err)
	}
	marker, err := os.Stat(filepath.Join(source, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !marker.Mode().IsRegular()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source skill marker %q: %w", source, err)
	}
	if err = filesystem.CopyDir(source, temporary); err != nil {
		return fmt.Errorf("copy source skill %q: %w", source, err)
	}
	if err = os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create cache parent %q: %w", filepath.Dir(final), err)
	}
	if err = os.Rename(temporary, final); err != nil {
		return fmt.Errorf("commit cached skill %q: %w", final, err)
	}
	return nil
}

func validPathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, `/\\`)
}
