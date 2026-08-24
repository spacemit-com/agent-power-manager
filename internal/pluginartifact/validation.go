// Package pluginartifact 提供 Plugin Resource 与 Transaction 共同使用的只读 artifact 校验。
package pluginartifact

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
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

// ValidateRawPackage 复验 canonical 权威索引，并仅允许根 marker 与已声明 Skill 子树。
func ValidateRawPackage(ref domain.PowerRef, pluginPath string) error {
	manifest, err := LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		return err
	}
	return validateRawClosedWorld(pluginPath, manifest)
}

// RawPackageEntry 描述已固定 Plugin 快照中的 canonical 相对路径及其目录属性。
type RawPackageEntry struct {
	Path      string
	Directory bool
}

// ValidateRawPackageEntries 复用 raw package 闭集规则校验不可变快照路径。
// 调用方必须先以 ValidateRawPackage 完成 manifest 身份、schema 与声明 Skill 校验。
func ValidateRawPackageEntries(manifest APCPluginManifest, entries []RawPackageEntry) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Path == "" || entry.Path == "." || path.IsAbs(entry.Path) || strings.Contains(entry.Path, `\`) || path.Clean(entry.Path) != entry.Path {
			return fmt.Errorf("Plugin artifact snapshot path %q is not canonical: %w", entry.Path, ErrInvalid)
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return fmt.Errorf("Plugin artifact snapshot repeats path %q: %w", entry.Path, ErrInvalid)
		}
		seen[entry.Path] = struct{}{}
		if !rawPackageEntryAllowed(manifest, entry.Path, entry.Directory) {
			return fmt.Errorf("Plugin artifact contains path outside canonical schema %q: %w", entry.Path, ErrInvalid)
		}
	}
	return nil
}

// ValidateShared 复验 canonical 子树、四份 Agent 派生文件及双 marketplace 的精确路径与完整语义。
func ValidateShared(ref domain.PowerRef, root string) error {
	if err := ValidatePhysicalTree(root); err != nil {
		return err
	}
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return errors.Join(ErrInvalid, err)
	}
	if _, err := requireExactPhysicalDirectory(root, "plugins"); err != nil {
		return err
	}
	pluginRelative := filepath.ToSlash(filepath.Join("plugins", logicalName))
	pluginPath, err := requireExactPhysicalDirectory(root, pluginRelative)
	if err != nil {
		return err
	}
	manifest, err := LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		return err
	}
	if err := validateSharedClosedWorld(root, logicalName, manifest); err != nil {
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
	if err := decodeExactRequiredJSON(pluginPath, codexManifestPath, &gotManifest); err != nil {
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
	if err := decodeExactRequiredJSON(root, codexMarketplacePath, &gotMarketplace); err != nil {
		return fmt.Errorf("decode Codex marketplace: %w", err)
	}
	if !reflect.DeepEqual(gotMarketplace, wantMarketplace) {
		return fmt.Errorf("Codex marketplace semantic value does not match canonical index: %w", ErrInvalid)
	}
	return nil
}

func validateClaude(ref domain.PowerRef, root, pluginPath string, canonical APCPluginManifest) error {
	var gotManifest ClaudePluginManifest
	if err := decodeExactRequiredJSON(pluginPath, claudeManifestPath, &gotManifest); err != nil {
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
	if err := decodeExactRequiredJSON(root, claudeMarketplacePath, &gotMarketplace); err != nil {
		return fmt.Errorf("decode Claude marketplace: %w", err)
	}
	if !reflect.DeepEqual(gotMarketplace, wantMarketplace) {
		return fmt.Errorf("Claude marketplace semantic value does not match canonical index: %w", ErrInvalid)
	}
	return nil
}

func validateHermes(pluginPath string, canonical APCPluginManifest) error {
	var got HermesPluginManifest
	if err := decodeExactRequiredYAML(pluginPath, hermesManifestPath, &got); err != nil {
		return fmt.Errorf("decode Hermes Plugin manifest: %w", err)
	}
	if want := expectedHermesManifest(canonical); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("Hermes Plugin manifest does not match canonical index: %w", ErrInvalid)
	}
	initContents, err := readExactRequiredFile(pluginPath, hermesInitPath)
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

func validateRawClosedWorld(root string, manifest APCPluginManifest) error {
	entries := make([]RawPackageEntry, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative != "." {
			entries = append(entries, RawPackageEntry{Path: filepath.ToSlash(relative), Directory: entry.IsDir()})
		}
		return nil
	})
	if err != nil {
		return err
	}
	return ValidateRawPackageEntries(manifest, entries)
}

func rawPackageEntryAllowed(manifest APCPluginManifest, relative string, directory bool) bool {
	switch relative {
	case apcPluginManifestPath:
		return !directory
	case "skills":
		return directory
	}
	for _, skillPath := range manifest.Skills {
		if relative == skillPath || strings.HasPrefix(relative, skillPath+"/") {
			return true
		}
	}
	return false
}

func validateSharedClosedWorld(root, logicalName string, manifest APCPluginManifest) error {
	pluginPrefix := "plugins/" + logicalName
	allowedDirectories := map[string]struct{}{
		".agents": {}, ".agents/plugins": {}, ".claude-plugin": {}, "plugins": {}, pluginPrefix: {},
		pluginPrefix + "/skills": {}, pluginPrefix + "/.codex-plugin": {},
		pluginPrefix + "/.claude-plugin": {}, pluginPrefix + "/.hermes-plugin": {},
	}
	allowedFiles := map[string]struct{}{
		filepath.ToSlash(codexMarketplacePath):                    {},
		filepath.ToSlash(claudeMarketplacePath):                   {},
		pluginPrefix + "/" + apcPluginManifestPath:                {},
		pluginPrefix + "/" + filepath.ToSlash(codexManifestPath):  {},
		pluginPrefix + "/" + filepath.ToSlash(claudeManifestPath): {},
		pluginPrefix + "/" + filepath.ToSlash(hermesManifestPath): {},
		pluginPrefix + "/" + filepath.ToSlash(hermesInitPath):     {},
	}
	declaredSkillPrefixes := make([]string, len(manifest.Skills))
	for index, skillPath := range manifest.Skills {
		declaredSkillPrefixes[index] = pluginPrefix + "/" + skillPath
		allowedDirectories[declaredSkillPrefixes[index]] = struct{}{}
	}
	return validateRelativeClosedWorld(root, func(relative string, entry fs.DirEntry) bool {
		if entry.IsDir() {
			if _, allowed := allowedDirectories[relative]; allowed {
				return true
			}
		} else if _, allowed := allowedFiles[relative]; allowed {
			return true
		}
		for _, skillPrefix := range declaredSkillPrefixes {
			if strings.HasPrefix(relative, skillPrefix+"/") {
				return true
			}
		}
		return false
	})
}

func validateRelativeClosedWorld(root string, allowed func(string, fs.DirEntry) bool) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if allowed(filepath.ToSlash(relative), entry) {
			return nil
		}
		return fmt.Errorf("Plugin artifact contains path outside canonical schema %q: %w", relative, ErrInvalid)
	})
}

func requireExactPhysicalDirectory(root, relative string) (string, error) {
	return requireExactPhysicalEntry(root, relative, true)
}

func requireExactPhysicalRegularFile(root, relative string) (string, error) {
	return requireExactPhysicalEntry(root, relative, false)
}

func requireExactPhysicalEntry(root, relative string, directory bool) (string, error) {
	portable := filepath.ToSlash(relative)
	if portable == "" || portable == "." || strings.Contains(portable, `\`) || strings.HasPrefix(portable, "/") ||
		filepath.ToSlash(filepath.Clean(filepath.FromSlash(portable))) != portable {
		return "", fmt.Errorf("required canonical path %q is invalid: %w", relative, ErrInvalid)
	}
	current := root
	components := strings.Split(portable, "/")
	for index, component := range components {
		entries, err := os.ReadDir(current)
		if err != nil {
			return "", fmt.Errorf("read canonical parent %q: %w", current, errors.Join(ErrInvalid, err))
		}
		exact := false
		for _, entry := range entries {
			if entry.Name() == component {
				exact = true
				break
			}
		}
		if !exact {
			return "", fmt.Errorf("canonical parent %q has no exact entry %q: %w", current, component, ErrInvalid)
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("lstat canonical entry %q: %w", current, errors.Join(ErrInvalid, err))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("canonical entry %q is a symlink: %w", current, ErrInvalid)
		}
		last := index == len(components)-1
		if !last && !info.IsDir() {
			return "", fmt.Errorf("canonical parent entry %q is not a directory: %w", current, ErrInvalid)
		}
		if last && directory && !info.IsDir() {
			return "", fmt.Errorf("canonical directory %q is not a physical directory: %w", current, ErrInvalid)
		}
		if last && !directory && !info.Mode().IsRegular() {
			return "", fmt.Errorf("canonical file %q is not a physical regular file: %w", current, ErrInvalid)
		}
	}
	if !directory {
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("lstat canonical file %q: %w", current, errors.Join(ErrInvalid, err))
		}
		if err := requireUniqueLink(info, current); err != nil {
			return "", err
		}
	}
	return current, nil
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

func readExactRequiredFile(root, relative string) ([]byte, error) {
	path, err := requireExactPhysicalRegularFile(root, relative)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read required file %q: %w", path, errors.Join(ErrInvalid, err))
	}
	return data, nil
}

func decodeExactRequiredJSON(root, relative string, target any) error {
	data, err := readExactRequiredFile(root, relative)
	if err != nil {
		return err
	}
	if err := jsonstrict.Decode(data, target, true); err != nil {
		return fmt.Errorf("decode required JSON %q: %w", filepath.Join(root, filepath.FromSlash(relative)), errors.Join(ErrInvalid, err))
	}
	return nil
}

func decodeExactRequiredYAML(root, relative string, target any) error {
	data, err := readExactRequiredFile(root, relative)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode required YAML %q: %w", filepath.Join(root, filepath.FromSlash(relative)), errors.Join(ErrInvalid, err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple YAML documents")
		}
		return fmt.Errorf("decode trailing YAML %q: %w", filepath.Join(root, filepath.FromSlash(relative)), errors.Join(ErrInvalid, err))
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
