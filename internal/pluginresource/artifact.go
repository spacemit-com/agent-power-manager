package pluginresource

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"apm/internal/domain"
)

// Downloader 下载指定版本的完整共享 Plugin artifact。
type Downloader interface {
	DownloadPlugin(context.Context, domain.PowerRef, string) error
}

// Manager 管理本地 Plugin artifact 的缓存、查询和 Hermes 暂存。
type Manager interface {
	Ensure(context.Context, domain.AgentID, domain.PowerRef) (Artifact, error)
	Lookup(domain.AgentID, domain.PowerRef) (Artifact, error)
	StageHermes(domain.PowerRef) (string, error)
}

// Artifact 描述某个 Agent 可消费的不可变 Plugin artifact。
type Artifact struct {
	Agent             domain.AgentID
	Power             domain.PowerRef
	ActualName        string
	Root              string
	PluginPath        string
	MarketplaceName   string
	MarketplaceSource string
}

// Validate 校验 artifact 的 Agent marketplace 约束。
func (artifact Artifact) Validate() error {
	switch artifact.Agent {
	case domain.AgentCodex, domain.AgentClaudeCode:
		if strings.TrimSpace(artifact.MarketplaceName) == "" ||
			strings.TrimSpace(artifact.MarketplaceSource) == "" ||
			!filepath.IsAbs(artifact.MarketplaceSource) {
			return fmt.Errorf("plugin artifact %+v: %w", artifact, ErrInvalidArtifact)
		}
	case domain.AgentHermes:
		if artifact.MarketplaceName != "" || artifact.MarketplaceSource != "" {
			return fmt.Errorf("plugin artifact %+v: %w", artifact, ErrInvalidArtifact)
		}
	default:
		return fmt.Errorf("plugin artifact agent %q: %w", artifact.Agent, ErrUnsupportedAgent)
	}
	return nil
}
