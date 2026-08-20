// Package pluginartifact 提供 Plugin Resource 与 Transaction 共同使用的只读 artifact 校验。
package pluginartifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"apm/internal/domain"
)

const (
	codexManifestPath     = ".codex-plugin/plugin.json"
	claudeManifestPath    = ".claude-plugin/plugin.json"
	codexMarketplacePath  = ".agents/plugins/marketplace.json"
	claudeMarketplacePath = ".claude-plugin/marketplace.json"
	localPluginPrefix     = "./plugins/"
)

// ErrInvalid 表示持久 Plugin artifact 的物理树或 Agent 语义不完整。
var ErrInvalid = errors.New("invalid plugin artifact contents")

type pluginAuthor struct {
	Name string `json:"name"`
}

// CodexPluginManifest 定义 builder 与只读校验器共同解码的 Codex 原生 manifest 已知字段。
type CodexPluginManifest struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	Description string       `json:"description"`
	Author      pluginAuthor `json:"author"`
	Interface   struct {
		DisplayName string `json:"displayName"`
		Category    string `json:"category"`
	} `json:"interface"`
}

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

// ValidatePhysicalTree 在读取 payload 前拒绝 symlink、设备、socket、FIFO 等非普通节点。
// 调用方仍须通过 lifecycle 全局锁维持已批准的 cooperative stdlib 竞态边界。
func ValidatePhysicalTree(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("lstat Plugin artifact root %q: %w", root, errors.Join(ErrInvalid, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Plugin artifact root %q is not a physical directory: %w", root, ErrInvalid)
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("lstat Plugin artifact path %q: %w", path, err)
		}
		mode := entryInfo.Mode()
		if mode&os.ModeSymlink != 0 || !entryInfo.IsDir() && !mode.IsRegular() {
			return fmt.Errorf("Plugin artifact path %q is not a physical directory or regular file: %w", path, ErrInvalid)
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrInvalid) {
			return err
		}
		return fmt.Errorf("walk Plugin artifact root %q: %w", root, errors.Join(ErrInvalid, err))
	}
	return nil
}

// ValidateRawPackage 拒绝 Cloud 原始包中任意层级的 Agent marketplace 文件。
func ValidateRawPackage(pluginPath string) error {
	if err := ValidatePhysicalTree(pluginPath); err != nil {
		return err
	}
	return validateMarketplaceClosedWorld(pluginPath, "")
}

// Validate 完整复验不可变 artifact 的物理树、原生 manifest 与 Agent marketplace 语义。
func Validate(agent domain.AgentID, ref domain.PowerRef, root string) error {
	if err := ValidatePhysicalTree(root); err != nil {
		return err
	}
	actualName, err := domain.ActualPluginName(ref)
	if err != nil {
		return errors.Join(ErrInvalid, err)
	}
	pluginPath := filepath.Join(root, "plugins", actualName)
	pluginInfo, err := os.Lstat(pluginPath)
	if err != nil {
		return fmt.Errorf("lstat Plugin content root %q: %w", pluginPath, errors.Join(ErrInvalid, err))
	}
	if pluginInfo.Mode()&os.ModeSymlink != 0 || !pluginInfo.IsDir() {
		return fmt.Errorf("Plugin content root %q is not a physical directory: %w", pluginPath, ErrInvalid)
	}

	allowedMarketplace := ""
	switch agent {
	case domain.AgentCodex:
		allowedMarketplace = codexMarketplacePath
	case domain.AgentClaudeCode:
		allowedMarketplace = claudeMarketplacePath
	case domain.AgentHermes:
	default:
		return fmt.Errorf("validate Plugin artifact agent %q: %w", agent, ErrInvalid)
	}
	if err := validateMarketplaceClosedWorld(root, allowedMarketplace); err != nil {
		return err
	}

	switch agent {
	case domain.AgentCodex:
		return validateCodex(ref, root, pluginPath)
	case domain.AgentClaudeCode:
		return validateClaude(ref, root, pluginPath)
	case domain.AgentHermes:
		var manifest claudePluginManifest
		if err := decodeRequiredJSON(filepath.Join(pluginPath, claudeManifestPath), &manifest); err != nil {
			return fmt.Errorf("decode Hermes Plugin manifest: %w", err)
		}
		return validateManifestIdentity(ref, manifest.Name, manifest.Version)
	default:
		panic("validated Agent switch became unreachable")
	}
}

func validateCodex(ref domain.PowerRef, root, pluginPath string) error {
	var manifest CodexPluginManifest
	if err := decodeRequiredJSON(filepath.Join(pluginPath, codexManifestPath), &manifest); err != nil {
		return fmt.Errorf("decode Codex Plugin manifest: %w", err)
	}
	if err := validateManifestIdentity(ref, manifest.Name, manifest.Version); err != nil {
		return err
	}
	want, err := expectedCodexMarketplace(ref, manifest)
	if err != nil {
		return err
	}
	var got codexMarketplaceDocument
	if err := decodeRequiredJSON(filepath.Join(root, codexMarketplacePath), &got); err != nil {
		return fmt.Errorf("decode Codex marketplace: %w", err)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("Codex marketplace semantic value does not match manifest: %w", ErrInvalid)
	}
	return nil
}

func validateClaude(ref domain.PowerRef, root, pluginPath string) error {
	var manifest claudePluginManifest
	if err := decodeRequiredJSON(filepath.Join(pluginPath, claudeManifestPath), &manifest); err != nil {
		return fmt.Errorf("decode Claude Plugin manifest: %w", err)
	}
	if err := validateManifestIdentity(ref, manifest.Name, manifest.Version); err != nil {
		return err
	}
	want, err := expectedClaudeMarketplace(ref, manifest)
	if err != nil {
		return err
	}
	var got claudeMarketplaceDocument
	if err := decodeRequiredJSON(filepath.Join(root, claudeMarketplacePath), &got); err != nil {
		return fmt.Errorf("decode Claude marketplace: %w", err)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("Claude marketplace semantic value does not match manifest: %w", ErrInvalid)
	}
	return nil
}

func validateMarketplaceClosedWorld(root, allowed string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !isMarketplacePath(relative) || relative == allowed {
			return nil
		}
		return fmt.Errorf("Plugin artifact contains disallowed marketplace %q: %w", relative, ErrInvalid)
	})
}

func isMarketplacePath(relative string) bool {
	for _, candidate := range []string{codexMarketplacePath, claudeMarketplacePath} {
		if relative == candidate || strings.HasSuffix(relative, string(filepath.Separator)+candidate) {
			return true
		}
	}
	return false
}

func decodeRequiredJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat required JSON %q: %w", path, errors.Join(ErrInvalid, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("required JSON %q is not a physical regular file: %w", path, ErrInvalid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read required JSON %q: %w", path, errors.Join(ErrInvalid, err))
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode required JSON %q: %w", path, errors.Join(ErrInvalid, err))
	}
	return nil
}

func validateManifestIdentity(ref domain.PowerRef, actualName, nativeVersion string) error {
	wantActualName, err := domain.ActualPluginName(ref)
	if err != nil {
		return errors.Join(ErrInvalid, err)
	}
	if actualName != wantActualName || nativeVersion != strings.TrimPrefix(ref.Version, "v") {
		return fmt.Errorf("Plugin manifest identity %q@%q: %w", actualName, nativeVersion, ErrInvalid)
	}
	return nil
}

func expectedCodexMarketplace(ref domain.PowerRef, manifest CodexPluginManifest) (codexMarketplaceDocument, error) {
	name, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return codexMarketplaceDocument{}, errors.Join(ErrInvalid, err)
	}
	displayName := manifest.Interface.DisplayName
	if displayName == "" {
		displayName = ref.Name
	}
	category := manifest.Interface.Category
	if category == "" {
		category = "Coding"
	}
	document := codexMarketplaceDocument{Name: name}
	document.Interface.DisplayName = displayName
	plugin := codexMarketplacePlugin{Name: manifest.Name, Category: category}
	plugin.Source.Source = "local"
	plugin.Source.Path = localPluginPrefix + manifest.Name
	plugin.Policy.Installation = "AVAILABLE"
	plugin.Policy.Authentication = "ON_INSTALL"
	document.Plugins = []codexMarketplacePlugin{plugin}
	return document, nil
}

func expectedClaudeMarketplace(ref domain.PowerRef, manifest claudePluginManifest) (claudeMarketplaceDocument, error) {
	name, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return claudeMarketplaceDocument{}, errors.Join(ErrInvalid, err)
	}
	description := manifest.Description
	if description == "" {
		description = "APM local marketplace for " + ref.Name
	}
	author := manifest.Author.Name
	if author == "" {
		author = "APM"
	}
	document := claudeMarketplaceDocument{Name: name}
	document.Owner.Name = author
	document.Metadata.Description = description
	document.Metadata.Version = manifest.Version
	document.Plugins = []claudeMarketplacePlugin{{
		Name: manifest.Name, Source: localPluginPrefix + manifest.Name, Description: description,
	}}
	return document, nil
}
