package pluginresource

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"apm/internal/domain"
)

// Downloader 下载指定版本的完整共享 Plugin artifact。
type Downloader interface {
	DownloadPlugin(context.Context, domain.PowerRef, string) error
}

// Manager 管理本地共享 Plugin artifact；同一 Plugin 版本对所有 Agent 只有一份不可变制品。
type Manager interface {
	Ensure(context.Context, domain.PowerRef) (Artifact, error)
	Lookup(domain.PowerRef) (Artifact, error)
	StageFilesystem(domain.PowerRef) (string, error)
}

// Marketplace 是 Plugin Resource 返回的 Agent marketplace 注册信息。
// Lifecycle 负责在事务边界将其映射为 Adapter 的目标类型。
type Marketplace struct {
	Name   string
	Source string
}

// Artifact 描述不携带 Agent 身份的共享、不可变 Plugin artifact。
// Codex 与 Claude marketplace 必须位于同一个 Root，PluginPath 使用逻辑名称。
type Artifact struct {
	Power                   domain.PowerRef
	Root                    string
	PluginPath              string
	CodexMarketplaceSource  string
	ClaudeMarketplaceSource string
}

// MarketplaceFor 返回指定 marketplace Agent 对应的资源层注册信息。
// Hermes 与 Bibit 使用文件系统 staging，不提供 marketplace。
func (artifact Artifact) MarketplaceFor(agent domain.AgentID) (Marketplace, error) {
	name, err := domain.PluginMarketplaceName(artifact.Power)
	if err != nil {
		return Marketplace{}, err
	}
	switch agent {
	case domain.AgentCodex:
		return Marketplace{Name: name, Source: artifact.CodexMarketplaceSource}, nil
	case domain.AgentClaudeCode:
		return Marketplace{Name: name, Source: artifact.ClaudeMarketplaceSource}, nil
	default:
		return Marketplace{}, fmt.Errorf("plugin artifact marketplace agent %q: %w", agent, ErrUnsupportedAgent)
	}
}

// Validate 校验共享 Artifact 的路径恒等式；不读取或审计 Cloud Plugin 内容。
func (artifact Artifact) Validate() error {
	logicalName, err := domain.PluginRuntimeName(artifact.Power)
	if err != nil {
		return fmt.Errorf("plugin artifact identity: %w", errors.Join(ErrInvalidArtifact, err))
	}
	root := filepath.Clean(artifact.Root)
	if !filepath.IsAbs(root) || artifact.Root != root ||
		artifact.PluginPath != filepath.Join(root, "plugins", logicalName) ||
		artifact.CodexMarketplaceSource != root || artifact.ClaudeMarketplaceSource != root {
		return fmt.Errorf("plugin artifact %+v: %w", artifact, ErrInvalidArtifact)
	}
	return nil
}
