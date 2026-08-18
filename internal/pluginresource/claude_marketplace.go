package pluginresource

import (
	"errors"
	"fmt"
	"path/filepath"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

const (
	claudeManifestPath    = ".claude-plugin/plugin.json"
	claudeMarketplacePath = ".claude-plugin/marketplace.json"
)

type claudeMarketplaceBuilder struct{}

type claudePluginManifest struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	Description string       `json:"description"`
	Author      pluginAuthor `json:"author"`
}

type claudeMarketplaceDocument struct {
	Name  string `json:"name"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Metadata struct {
		Description string `json:"description"`
		Version     string `json:"version"`
	} `json:"metadata"`
	Plugins []claudeMarketplacePlugin `json:"plugins"`
}

type claudeMarketplacePlugin struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// Build 根据 Claude 原生 manifest 生成只含当前版本的本地 marketplace。
func (claudeMarketplaceBuilder) Build(agent domain.AgentID, ref domain.PowerRef, root, pluginPath string) error {
	if agent != domain.AgentClaudeCode {
		return fmt.Errorf("build Claude marketplace for agent %q: %w", agent, ErrUnsupportedAgent)
	}
	var manifest claudePluginManifest
	if err := decodePluginManifest(pluginPath, claudeManifestPath, &manifest); err != nil {
		return fmt.Errorf("decode Claude plugin manifest: %w", err)
	}
	if err := validateManifestIdentity(ref, manifest.Name, manifest.Version); err != nil {
		return err
	}
	document, err := newClaudeMarketplaceDocument(ref, manifest)
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(root, claudeMarketplacePath), document); err != nil {
		return fmt.Errorf("write Claude marketplace: %w", err)
	}
	return nil
}

// Validate 复验 Claude manifest，并按类型语义比较本地 marketplace。
func (claudeMarketplaceBuilder) Validate(agent domain.AgentID, ref domain.PowerRef, root string) error {
	if agent != domain.AgentClaudeCode {
		return fmt.Errorf("validate Claude marketplace for agent %q: %w", agent, ErrUnsupportedAgent)
	}
	if err := pluginartifact.Validate(agent, ref, root); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	return nil
}

func newClaudeMarketplaceDocument(ref domain.PowerRef, manifest claudePluginManifest) (claudeMarketplaceDocument, error) {
	marketplaceName, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return claudeMarketplaceDocument{}, err
	}
	description := manifest.Description
	if description == "" {
		description = "APM local marketplace for " + ref.Name
	}
	author := manifest.Author.Name
	if author == "" {
		author = "APM"
	}
	document := claudeMarketplaceDocument{Name: marketplaceName}
	document.Owner.Name = author
	document.Metadata.Description = description
	document.Metadata.Version = manifest.Version
	document.Plugins = []claudeMarketplacePlugin{{
		Name:        manifest.Name,
		Source:      localPluginSourcePrefix + manifest.Name,
		Description: description,
	}}
	return document, nil
}
