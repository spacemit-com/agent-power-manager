// Package pluginartifact 提供 Plugin Resource 与 Transaction 共同使用的只读 artifact 校验。
package pluginartifact

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
	"go.yaml.in/yaml/v3"
)

const (
	codexManifestPath     = ".codex-plugin/plugin.json"
	claudeManifestPath    = ".claude-plugin/plugin.json"
	hermesManifestPath    = ".hermes-plugin/plugin.yaml"
	hermesInitPath        = ".hermes-plugin/__init__.py"
	codexMarketplacePath  = ".agents/plugins/marketplace.json"
	claudeMarketplacePath = ".claude-plugin/marketplace.json"
	localPluginPrefix     = "./plugins/"
	codexSkillsPath       = "./skills/"
	hermesAuthor          = "APM"
	hermesInitContents    = "def register(ctx):\n    return None\n"
)

// ErrInvalid 表示持久 Plugin artifact 的物理树或 Agent 语义不完整。
var ErrInvalid = errors.New("invalid plugin artifact contents")

// CodexPluginManifest 定义 APM 派生并严格复验的 Codex 原生 manifest 完整 schema。
type CodexPluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Skills      string `json:"skills"`
}

// ClaudePluginManifest 定义 APM 派生并严格复验的 Claude 原生 manifest 完整 schema。
type ClaudePluginManifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
}

// HermesPluginManifest 定义 APM 派生并严格复验的 Hermes 原生 manifest 完整 schema。
type HermesPluginManifest struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	Author      string `yaml:"author"`
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

// ValidatePhysicalTree 在读取 payload 前拒绝 symlink、硬链接、设备、socket、FIFO 等非唯一普通节点。
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
		if mode.IsRegular() {
			if err := requireUniqueLink(entryInfo, path); err != nil {
				return err
			}
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

// ValidateRawPackage 复验 canonical 权威索引，并拒绝 Cloud 原始包中的任意 Agent 派生路径。
func ValidateRawPackage(ref domain.PowerRef, pluginPath string) error {
	if _, err := LoadAPCPluginManifest(ref, pluginPath); err != nil {
		return err
	}
	return filepath.WalkDir(pluginPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(pluginPath, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		portable := filepath.ToSlash(relative)
		if isNativeAgentPath(portable) || isMarketplacePath(portable) {
			return fmt.Errorf("raw Plugin package contains generated Agent path %q: %w", relative, ErrInvalid)
		}
		return nil
	})
}

// ValidateShared 复验 canonical 索引、四份 Agent 派生文件及双 marketplace 的完整语义。
func ValidateShared(ref domain.PowerRef, root string) error {
	if err := ValidatePhysicalTree(root); err != nil {
		return err
	}
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return errors.Join(ErrInvalid, err)
	}
	pluginPath := filepath.Join(root, "plugins", logicalName)
	manifest, err := LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		return err
	}
	if err := validateMarketplaceClosedWorld(root, codexMarketplacePath, claudeMarketplacePath); err != nil {
		return err
	}
	if err := validateCodex(ref, root, pluginPath, manifest); err != nil {
		return err
	}
	if err := validateClaude(ref, root, pluginPath, manifest); err != nil {
		return err
	}
	return validateHermes(pluginPath, manifest)
}

// ValidateMarketplace 先复验完整共享 artifact，再复验目标 Agent 的 marketplace 语义。
func ValidateMarketplace(agent domain.AgentID, ref domain.PowerRef, root string) error {
	if err := ValidateShared(ref, root); err != nil {
		return err
	}
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return errors.Join(ErrInvalid, err)
	}
	pluginPath := filepath.Join(root, "plugins", logicalName)
	manifest, err := LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		return err
	}
	switch agent {
	case domain.AgentCodex:
		return validateCodex(ref, root, pluginPath, manifest)
	case domain.AgentClaudeCode:
		return validateClaude(ref, root, pluginPath, manifest)
	default:
		return fmt.Errorf("validate Plugin artifact agent %q: %w", agent, ErrInvalid)
	}
}

func validateCodex(ref domain.PowerRef, root, pluginPath string, canonical APCPluginManifest) error {
	var gotManifest CodexPluginManifest
	if err := decodeRequiredJSON(filepath.Join(pluginPath, codexManifestPath), &gotManifest); err != nil {
		return fmt.Errorf("decode Codex Plugin manifest: %w", err)
	}
	wantManifest := expectedCodexManifest(canonical)
	if !reflect.DeepEqual(gotManifest, wantManifest) {
		return fmt.Errorf("Codex Plugin manifest does not match canonical index: %w", ErrInvalid)
	}
	wantMarketplace, err := expectedCodexMarketplace(ref, wantManifest)
	if err != nil {
		return err
	}
	var gotMarketplace codexMarketplaceDocument
	if err := decodeRequiredJSON(filepath.Join(root, codexMarketplacePath), &gotMarketplace); err != nil {
		return fmt.Errorf("decode Codex marketplace: %w", err)
	}
	if !reflect.DeepEqual(gotMarketplace, wantMarketplace) {
		return fmt.Errorf("Codex marketplace semantic value does not match canonical index: %w", ErrInvalid)
	}
	return nil
}

func validateClaude(ref domain.PowerRef, root, pluginPath string, canonical APCPluginManifest) error {
	var gotManifest ClaudePluginManifest
	if err := decodeRequiredJSON(filepath.Join(pluginPath, claudeManifestPath), &gotManifest); err != nil {
		return fmt.Errorf("decode Claude Plugin manifest: %w", err)
	}
	wantManifest := expectedClaudeManifest(canonical)
	if !reflect.DeepEqual(gotManifest, wantManifest) {
		return fmt.Errorf("Claude Plugin manifest does not match canonical index: %w", ErrInvalid)
	}
	wantMarketplace, err := expectedClaudeMarketplace(ref, wantManifest)
	if err != nil {
		return err
	}
	var gotMarketplace claudeMarketplaceDocument
	if err := decodeRequiredJSON(filepath.Join(root, claudeMarketplacePath), &gotMarketplace); err != nil {
		return fmt.Errorf("decode Claude marketplace: %w", err)
	}
	if !reflect.DeepEqual(gotMarketplace, wantMarketplace) {
		return fmt.Errorf("Claude marketplace semantic value does not match canonical index: %w", ErrInvalid)
	}
	return nil
}

func validateHermes(pluginPath string, canonical APCPluginManifest) error {
	var got HermesPluginManifest
	if err := decodeRequiredYAML(filepath.Join(pluginPath, hermesManifestPath), &got); err != nil {
		return fmt.Errorf("decode Hermes Plugin manifest: %w", err)
	}
	if want := expectedHermesManifest(canonical); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("Hermes Plugin manifest does not match canonical index: %w", ErrInvalid)
	}
	initContents, err := readRequiredFile(filepath.Join(pluginPath, hermesInitPath))
	if err != nil {
		return fmt.Errorf("read Hermes Plugin initializer: %w", err)
	}
	if string(initContents) != hermesInitContents {
		return fmt.Errorf("Hermes Plugin initializer is not the canonical no-op register function: %w", ErrInvalid)
	}
	return nil
}

func expectedCodexManifest(canonical APCPluginManifest) CodexPluginManifest {
	return CodexPluginManifest{
		Name: canonical.Name, Version: strings.TrimPrefix(canonical.Version, "v"), Description: canonical.Description, Skills: codexSkillsPath,
	}
}

func expectedClaudeManifest(canonical APCPluginManifest) ClaudePluginManifest {
	return ClaudePluginManifest{
		Name: canonical.Name, Version: strings.TrimPrefix(canonical.Version, "v"), Description: canonical.Description, Skills: []string{codexSkillsPath},
	}
}

func expectedHermesManifest(canonical APCPluginManifest) HermesPluginManifest {
	return HermesPluginManifest{
		Name: canonical.Name, Version: strings.TrimPrefix(canonical.Version, "v"), Description: canonical.Description, Author: hermesAuthor,
	}
}

func validateMarketplaceClosedWorld(root string, allowed ...string) error {
	allowedPaths := make(map[string]struct{}, len(allowed))
	for _, allowedPath := range allowed {
		allowedPaths[filepath.ToSlash(allowedPath)] = struct{}{}
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		portable := filepath.ToSlash(relative)
		_, permitted := allowedPaths[portable]
		if !isMarketplacePath(portable) || permitted {
			return nil
		}
		return fmt.Errorf("Plugin artifact contains disallowed marketplace %q: %w", relative, ErrInvalid)
	})
}

func isNativeAgentPath(relative string) bool {
	for _, component := range strings.Split(relative, "/") {
		switch component {
		case ".codex-plugin", ".claude-plugin", ".hermes-plugin":
			return true
		}
	}
	return false
}

func isMarketplacePath(relative string) bool {
	for _, candidate := range []string{filepath.ToSlash(codexMarketplacePath), filepath.ToSlash(claudeMarketplacePath)} {
		if relative == candidate || strings.HasSuffix(relative, "/"+candidate) {
			return true
		}
	}
	return false
}

func requirePhysicalRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat required file %q: %w", path, errors.Join(ErrInvalid, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("required file %q is not a physical regular file: %w", path, ErrInvalid)
	}
	return requireUniqueLink(info, path)
}

func requireUniqueLink(info fs.FileInfo, path string) error {
	count, err := physicalFileLinkCount(path, info)
	if err != nil {
		return fmt.Errorf("determine hard-link count for required file %q: %w", path, errors.Join(ErrInvalid, err))
	}
	if count != 1 {
		return fmt.Errorf("required file %q has %d hard links: %w", path, count, ErrInvalid)
	}
	return nil
}

func readRequiredFile(path string) ([]byte, error) {
	if err := requirePhysicalRegularFile(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read required file %q: %w", path, errors.Join(ErrInvalid, err))
	}
	return data, nil
}

func decodeRequiredJSON(path string, target any) error {
	data, err := readRequiredFile(path)
	if err != nil {
		return err
	}
	if err := jsonstrict.Decode(data, target, true); err != nil {
		return fmt.Errorf("decode required JSON %q: %w", path, errors.Join(ErrInvalid, err))
	}
	return nil
}

func decodeRequiredYAML(path string, target any) error {
	data, err := readRequiredFile(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode required YAML %q: %w", path, errors.Join(ErrInvalid, err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple YAML documents")
		}
		return fmt.Errorf("decode trailing YAML %q: %w", path, errors.Join(ErrInvalid, err))
	}
	return nil
}

func expectedCodexMarketplace(ref domain.PowerRef, manifest CodexPluginManifest) (codexMarketplaceDocument, error) {
	name, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return codexMarketplaceDocument{}, errors.Join(ErrInvalid, err)
	}
	document := codexMarketplaceDocument{Name: name}
	document.Interface.DisplayName = manifest.Name
	plugin := codexMarketplacePlugin{Name: manifest.Name, Category: "Coding"}
	plugin.Source.Source = "local"
	plugin.Source.Path = localPluginPrefix + manifest.Name
	plugin.Policy.Installation = "AVAILABLE"
	plugin.Policy.Authentication = "ON_INSTALL"
	document.Plugins = []codexMarketplacePlugin{plugin}
	return document, nil
}

func expectedClaudeMarketplace(ref domain.PowerRef, manifest ClaudePluginManifest) (claudeMarketplaceDocument, error) {
	name, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return claudeMarketplaceDocument{}, errors.Join(ErrInvalid, err)
	}
	document := claudeMarketplaceDocument{Name: name}
	document.Owner.Name = hermesAuthor
	document.Metadata.Description = manifest.Description
	document.Metadata.Version = manifest.Version
	document.Plugins = []claudeMarketplacePlugin{{
		Name: manifest.Name, Source: localPluginPrefix + manifest.Name, Description: manifest.Description,
	}}
	return document, nil
}
