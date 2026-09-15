package pluginresource

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

const (
	claudeManifestPath    = ".claude-plugin/plugin.json"
	claudeMarketplacePath = ".claude-plugin/marketplace.json"
)

type claudeMarketplaceBuilder struct{}

type claudePluginManifest = pluginartifact.ClaudePluginManifest

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

// Build 根据 Claude 落盘叶子内的原生 manifest 生成只含当前逻辑 Plugin 的本地 marketplace，
// 其 plugin source 指向该落盘叶子，使 Claude 只复制属于自身的文件。
func (claudeMarketplaceBuilder) Build(ref domain.PowerRef, root string) error {
	manifest, err := readClaudeLandingManifest(ref, root)
	if err != nil {
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

// Validate 复验 Claude 落盘 manifest，并按类型语义比较共享根中的 Claude marketplace。
func (claudeMarketplaceBuilder) Validate(ref domain.PowerRef, root string) error {
	manifest, err := readClaudeLandingManifest(ref, root)
	if err != nil {
		return err
	}
	want, err := newClaudeMarketplaceDocument(ref, manifest)
	if err != nil {
		return err
	}
	var got claudeMarketplaceDocument
	if err := decodePluginManifest(root, claudeMarketplacePath, &got); err != nil {
		return fmt.Errorf("decode Claude marketplace: %w", err)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("Claude marketplace semantic value does not match manifest: %w", ErrInvalidArtifact)
	}
	return nil
}

func readClaudeLandingManifest(ref domain.PowerRef, root string) (claudePluginManifest, error) {
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return claudePluginManifest{}, err
	}
	landingPlugin, err := pluginartifact.LandingPluginPath(root, domain.AgentClaudeCode, logicalName)
	if err != nil {
		return claudePluginManifest{}, errors.Join(ErrInvalidArtifact, err)
	}
	var manifest claudePluginManifest
	if err := decodePluginManifest(landingPlugin, claudeManifestPath, &manifest); err != nil {
		return claudePluginManifest{}, fmt.Errorf("decode Claude plugin manifest: %w", err)
	}
	if err := validateClaudePluginManifest(ref, manifest); err != nil {
		return claudePluginManifest{}, err
	}
	return manifest, nil
}

func newClaudeMarketplaceDocument(ref domain.PowerRef, manifest claudePluginManifest) (claudeMarketplaceDocument, error) {
	marketplaceName, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return claudeMarketplaceDocument{}, err
	}
	source, err := pluginartifact.LandingMarketplaceSource(domain.AgentClaudeCode, manifest.Name)
	if err != nil {
		return claudeMarketplaceDocument{}, errors.Join(ErrInvalidArtifact, err)
	}
	document := claudeMarketplaceDocument{Name: marketplaceName}
	document.Owner.Name = "APM"
	document.Metadata.Description = manifest.Description
	document.Metadata.Version = manifest.Version
	document.Plugins = []claudeMarketplacePlugin{{
		Name:        manifest.Name,
		Source:      source,
		Description: manifest.Description,
	}}
	return document, nil
}

func validateClaudePluginManifest(ref domain.PowerRef, manifest claudePluginManifest) error {
	if err := validateManifestIdentity(ref, manifest.Name, manifest.Version); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Description) == "" || !reflect.DeepEqual(manifest.Skills, []string{"./skills/"}) {
		return fmt.Errorf("Claude Plugin manifest is incomplete: %w", ErrInvalidArtifact)
	}
	return nil
}
