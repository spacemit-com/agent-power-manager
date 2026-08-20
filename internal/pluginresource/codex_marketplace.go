package pluginresource

import (
	"errors"
	"fmt"
	"path/filepath"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

const (
	codexManifestPath    = ".codex-plugin/plugin.json"
	codexMarketplacePath = ".agents/plugins/marketplace.json"
)

type codexMarketplaceBuilder struct{}

type codexPluginManifest = pluginartifact.CodexPluginManifest

type codexMarketplaceDocument struct {
	Name      string `json:"name"`
	Interface struct {
		DisplayName string `json:"displayName"`
	} `json:"interface"`
	Plugins []codexMarketplacePlugin `json:"plugins"`
}

type codexMarketplacePlugin struct {
	Name   string `json:"name"`
	Source struct {
		Source string `json:"source"`
		Path   string `json:"path"`
	} `json:"source"`
	Policy struct {
		Installation   string `json:"installation"`
		Authentication string `json:"authentication"`
	} `json:"policy"`
	Category string `json:"category"`
}

// Build 根据 Codex 原生 manifest 生成只含当前版本的本地 marketplace。
func (codexMarketplaceBuilder) Build(agent domain.AgentID, ref domain.PowerRef, root, pluginPath string) error {
	if agent != domain.AgentCodex {
		return fmt.Errorf("build Codex marketplace for agent %q: %w", agent, ErrUnsupportedAgent)
	}
	var manifest codexPluginManifest
	if err := decodePluginManifest(pluginPath, codexManifestPath, &manifest); err != nil {
		return fmt.Errorf("decode Codex plugin manifest: %w", err)
	}
	if err := validateManifestIdentity(ref, manifest.Name, manifest.Version); err != nil {
		return err
	}
	document, err := newCodexMarketplaceDocument(ref, manifest)
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(root, codexMarketplacePath), document); err != nil {
		return fmt.Errorf("write Codex marketplace: %w", err)
	}
	return nil
}

// Validate 复验 Codex manifest，并按类型语义比较本地 marketplace。
func (codexMarketplaceBuilder) Validate(agent domain.AgentID, ref domain.PowerRef, root string) error {
	if agent != domain.AgentCodex {
		return fmt.Errorf("validate Codex marketplace for agent %q: %w", agent, ErrUnsupportedAgent)
	}
	if err := pluginartifact.Validate(agent, ref, root); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	return nil
}

func newCodexMarketplaceDocument(ref domain.PowerRef, manifest codexPluginManifest) (codexMarketplaceDocument, error) {
	marketplaceName, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return codexMarketplaceDocument{}, err
	}
	displayName := manifest.Interface.DisplayName
	if displayName == "" {
		displayName = ref.Name
	}
	category := manifest.Interface.Category
	if category == "" {
		category = "Coding"
	}
	document := codexMarketplaceDocument{Name: marketplaceName}
	document.Interface.DisplayName = displayName
	plugin := codexMarketplacePlugin{Name: manifest.Name, Category: category}
	plugin.Source.Source = "local"
	plugin.Source.Path = localPluginSourcePrefix + manifest.Name
	plugin.Policy.Installation = "AVAILABLE"
	plugin.Policy.Authentication = "ON_INSTALL"
	document.Plugins = []codexMarketplacePlugin{plugin}
	return document, nil
}
