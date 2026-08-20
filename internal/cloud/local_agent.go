package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

// listAgents 枚举 agents 目录下的版本化 agent.yaml 资源。
func (c *LocalClient) listAgents() ([]domain.PowerRef, error) {
	return c.listResources("agents", "agent.yaml", domain.PowerTypeAgent)
}

// DownloadAgent 将指定版本的 Agent yaml 原子复制到本地缓存；不做内容解析。
func (c *LocalClient) DownloadAgent(ref domain.PowerRef) (resultErr error) {
	if ref.Version == "" {
		return errors.New("power version is required")
	}
	if ref.Type != domain.PowerTypeAgent {
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

	final := filepath.Join(c.cacheRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	temporary := filepath.Join(c.cacheTempRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	source := filepath.Join(c.resourcesRoot, "agents", ref.Name, ref.Version, "agent.yaml")

	if _, err := os.Stat(final); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat cache power %q: %w", final, err)
	}
	sourceInfo, err := os.Stat(source)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !sourceInfo.Mode().IsRegular()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source agent %q: %w", source, err)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read source agent %q: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(temporary), 0o755); err != nil {
		return fmt.Errorf("create cache temporary parent %q: %w", filepath.Dir(temporary), err)
	}
	if err := os.WriteFile(temporary, contents, 0o644); err != nil {
		return fmt.Errorf("write cache temporary agent %q: %w", temporary, err)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create cache parent %q: %w", filepath.Dir(final), err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("commit cached agent %q: %w", final, err)
	}
	return nil
}
