package local

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
func (c *Client) listAgents(ctx context.Context) ([]domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.listResources(ctx, "agents", domain.PowerTypeAgent, "agent.yaml")
}

// DownloadAgent 将精确版本 Agent yaml 原子复制至调用方提供的私有文件，不访问全局缓存。
func (c *Client) DownloadAgent(ctx context.Context, entry domain.CatalogEntry, destination string) (resultErr error) {
	content, err := c.loadMatchingCatalogContent(ctx, entry, domain.PowerTypeAgent)
	if err != nil {
		return err
	}
	destination, err = absoluteDestination(destination)
	if err != nil {
		return err
	}
	if err := validateFileDestination(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create Agent destination parent %q: %w", filepath.Dir(destination), err)
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
	if c.beforeAgentWrite != nil {
		c.beforeAgentWrite(temporary)
	}
	if _, err := temporary.Write(content.bytes); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Agent temporary destination %q: %w", temporaryName, err)
	}
	var chmodErr error
	if c.agentTemporaryChmod != nil {
		chmodErr = c.agentTemporaryChmod(temporary, publishedFileMode)
	} else {
		chmodErr = temporary.Chmod(publishedFileMode)
	}
	if chmodErr != nil {
		_ = temporary.Close()
		return fmt.Errorf("set Agent temporary mode %q: %w", temporaryName, chmodErr)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Agent temporary destination %q: %w", temporaryName, err)
	}
	if c.beforeAgentPublish != nil {
		c.beforeAgentPublish()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameAgentNoReplace(temporaryName, destination); err != nil {
		return fmt.Errorf("publish Agent destination %q: %w", destination, err)
	}
	return nil
}
