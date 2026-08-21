package cloud

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

// listAgents 枚举 agents 目录下的版本化 agent.yaml 资源。
func (c *LocalClient) listAgents(ctx context.Context) ([]domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.listResources(ctx, "agents", "agent.yaml", domain.PowerTypeAgent)
}

// DownloadAgent 将精确版本 Agent yaml 原子复制至调用方提供的私有文件，不访问全局缓存。
func (c *LocalClient) DownloadAgent(ctx context.Context, ref domain.PowerRef, destination string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Type != domain.PowerTypeAgent || !safePowerIdentity(ref) || !isCanonicalVersion(ref.Version) {
		return ErrNotFound
	}
	source := filepath.Join(c.resourcesRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source Agent %q: %w", source, err)
	}
	destination, err = absoluteDestination(destination)
	if err != nil {
		return err
	}
	if err := validateAgentDestination(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create Agent destination parent %q: %w", filepath.Dir(destination), err)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read source Agent %q: %w", source, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-")
	if err != nil {
		return fmt.Errorf("create Agent temporary destination beside %q: %w", destination, err)
	}
	temporaryName := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove Agent temporary destination %q: %w", temporaryName, err))
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set Agent temporary mode %q: %w", temporaryName, err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Agent temporary destination %q: %w", temporaryName, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Agent temporary destination %q: %w", temporaryName, err)
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("publish Agent destination %q: %w", destination, err)
	}
	return nil
}

func validateAgentDestination(destination string) error {
	if err := validatePluginDestinationAncestors(filepath.Dir(destination)); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat Agent destination %q: %w", destination, err)
	}
	return fmt.Errorf("Agent destination %q must not exist", destination)
}
