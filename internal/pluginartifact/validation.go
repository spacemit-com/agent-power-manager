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
	pluginManifestPath    = "plugin.yaml"
	pluginInitPath        = "__init__.py"
	codexMarketplacePath  = ".agents/plugins/marketplace.json"
	claudeMarketplacePath = ".claude-plugin/marketplace.json"
	codexSkillsPath       = "./skills/"
	apmAuthor             = "APM"
	pluginInitContents    = "def register(ctx):\n    return None\n"

	// LandingRootDir 是共享 artifact 内各 Agent 落盘子树的公共父目录。
	// 每个目标 Agent 只发布自己的落盘插件叶子，因此安装目录不会出现其他 Agent 的配置。
	LandingRootDir = "landing"

	// PluginManifestSchemaVersion 是 Hermes 与 Bibit 共用 plugin.yaml 的固定契约版本；
	// 生成（pluginresource）与复验（本包）共用该常量，避免两侧漂移。
	PluginManifestSchemaVersion = 1
)

// landingAgents 固定落盘 Agent 的顺序，保证派生输出与复验遍历确定。
var landingAgents = []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes, domain.AgentBibit}

// PluginLandingAgents 返回共享 artifact 内必须存在落盘子树的 Agent；返回值不可被调用方修改顺序。
// 未列入的 Agent 没有落盘语义，marketplace 型与文件系统型 Plugin 均按此集合派生与复验。
func PluginLandingAgents() []domain.AgentID {
	return append([]domain.AgentID(nil), landingAgents...)
}

// LandingRelativeDir 返回 Agent 落盘子树相对 artifact 根的路径 `landing/<agent-id>`；
// 不支持的 Agent 返回 ErrInvalid。目录名直接使用 domain.AgentID，与 CLI `--agent` 取值同源。
func LandingRelativeDir(agent domain.AgentID) (string, error) {
	for _, candidate := range landingAgents {
		if candidate == agent {
			return path.Join(LandingRootDir, string(agent)), nil
		}
	}
	return "", fmt.Errorf("Plugin landing agent %q: %w", agent, ErrInvalid)
}

// LandingPluginRelativePath 返回落盘插件叶子相对 artifact 根的路径 `landing/<agent-id>/plugins/<name>`。
func LandingPluginRelativePath(agent domain.AgentID, logicalName string) (string, error) {
	landingDir, err := LandingRelativeDir(agent)
	if err != nil {
		return "", err
	}
	if err := validateLogicalPluginName(logicalName); err != nil {
		return "", err
	}
	return path.Join(landingDir, "plugins", logicalName), nil
}

// LandingPluginPath 返回落盘插件叶子的绝对路径，供派生与目标 Agent 发布共用。
func LandingPluginPath(root string, agent domain.AgentID, logicalName string) (string, error) {
	relative, err := LandingPluginRelativePath(agent, logicalName)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}

// LandingMarketplaceSource 返回 marketplace 文档内指向落盘插件叶子的相对 source；
// Codex/Claude CLI 只复制该 source 子树，因此指向落盘叶子即实现按目标过滤。
func LandingMarketplaceSource(agent domain.AgentID, logicalName string) (string, error) {
	relative, err := LandingPluginRelativePath(agent, logicalName)
	if err != nil {
		return "", err
	}
	return "./" + relative, nil
}

func landingManifestRelativePath(agent domain.AgentID) (string, error) {
	switch agent {
	case domain.AgentCodex:
		return codexManifestPath, nil
	case domain.AgentClaudeCode:
		return claudeManifestPath, nil
	case domain.AgentHermes, domain.AgentBibit:
		return pluginManifestPath, nil
	default:
		return "", fmt.Errorf("Plugin landing agent %q: %w", agent, ErrInvalid)
	}
}

func validateLogicalPluginName(name string) error {
	if name == "" || name == "." || name == ".." || path.IsAbs(name) || strings.ContainsAny(name, `/\`) || path.Base(name) != name {
		return fmt.Errorf("Plugin logical name %q is not a single canonical path element: %w", name, ErrInvalid)
	}
	return nil
}

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

// FilesystemPluginManifest 定义 APM 派生并严格复验的 filesystem Agent manifest 完整 schema。
// 该 schema 对应落盘插件叶子根下的 plugin.yaml，由 Hermes 与 Bibit 共用；schema_version 固定为 1，
// 与 apc-plugin.json 等 canonical 资源清单保持同一版本约定。字段顺序即 YAML 输出顺序；
// 两侧消费方都不消费作者信息，因此不接受 author（严格解码会拒绝该未知键）。
type FilesystemPluginManifest struct {
	SchemaVersion int    `yaml:"schema_version"`
	Name          string `yaml:"name"`
	Version       string `yaml:"version"`
	Description   string `yaml:"description"`
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

// ValidatePhysicalTreeFd 与 ValidatePhysicalTree 语义一致，但在调用方持有的固定目录
// 句柄（如 securefs pinned 边界内的私有输出）上以 fd 相对方式遍历：路径可被并发
// 重定向，而句柄边界不可，因此任何可被竞态攻击的校验都必须使用本入口。
func ValidatePhysicalTreeFd(directory *os.File) error {
	info, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("stat Plugin artifact directory handle: %w", errors.Join(ErrInvalid, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Plugin artifact directory handle is not a physical directory: %w", ErrInvalid)
	}
	return validatePhysicalTreeFdWalk(directory, "")
}

func validatePhysicalTreeFdWalk(directory *os.File, relative string) error {
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("read Plugin artifact directory %q: %w", relative, errors.Join(ErrInvalid, err))
	}
	for _, entry := range entries {
		childRelative := entry.Name()
		if relative != "" {
			childRelative = relative + "/" + entry.Name()
		}
		entryType := entry.Type()
		switch {
		case entryType&fs.ModeSymlink != 0 || (!entryType.IsDir() && !entryType.IsRegular()):
			return fmt.Errorf("Plugin artifact path %q is not a physical directory or regular file: %w", childRelative, ErrInvalid)
		case entryType.IsRegular():
			child, err := openChildFd(directory, entry.Name())
			if err != nil {
				return fmt.Errorf("open Plugin artifact path %q: %w", childRelative, errors.Join(ErrInvalid, err))
			}
			info, statErr := child.Stat()
			closeErr := child.Close()
			if statErr != nil || closeErr != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("inspect Plugin artifact path %q: %w", childRelative, errors.Join(ErrInvalid, statErr, closeErr))
			}
			if err := requireUniqueLink(info, childRelative); err != nil {
				return err
			}
		case entryType.IsDir():
			child, err := openChildFd(directory, entry.Name())
			if err != nil {
				return fmt.Errorf("open Plugin artifact directory %q: %w", childRelative, errors.Join(ErrInvalid, err))
			}
			info, statErr := child.Stat()
			if statErr != nil || !info.IsDir() {
				_ = child.Close()
				return fmt.Errorf("inspect Plugin artifact directory %q: %w", childRelative, errors.Join(ErrInvalid, statErr))
			}
			walkErr := validatePhysicalTreeFdWalk(child, childRelative)
			closeErr := child.Close()
			if walkErr != nil || closeErr != nil {
				return errors.Join(walkErr, closeErr)
			}
		}
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
// 调用方必须先以 ValidateAPCPluginManifest 完成 manifest 身份、schema 与声明 Skill 校验。
func ValidateRawPackageEntries(manifest APCPluginManifest, entries []RawPackageEntry) error {
	seen := make(map[string]RawPackageEntry, len(entries))
	for _, entry := range entries {
		if entry.Path == "" || entry.Path == "." || path.IsAbs(entry.Path) || strings.Contains(entry.Path, `\`) || path.Clean(entry.Path) != entry.Path {
			return fmt.Errorf("Plugin artifact snapshot path %q is not canonical: %w", entry.Path, ErrInvalid)
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return fmt.Errorf("Plugin artifact snapshot repeats path %q: %w", entry.Path, ErrInvalid)
		}
		seen[entry.Path] = entry
		if !rawPackageEntryAllowed(manifest, entry.Path, entry.Directory) {
			return fmt.Errorf("Plugin artifact contains path outside canonical schema %q: %w", entry.Path, ErrInvalid)
		}
	}
	require := func(required string, directory bool) error {
		entry, ok := seen[required]
		if !ok || entry.Directory != directory {
			return fmt.Errorf("Plugin artifact snapshot requires %q (directory=%t): %w", required, directory, ErrInvalid)
		}
		return nil
	}
	for _, required := range []RawPackageEntry{
		{Path: apcPluginManifestPath},
		{Path: "skills", Directory: true},
	} {
		if err := require(required.Path, required.Directory); err != nil {
			return err
		}
	}
	for _, skillPath := range manifest.Skills {
		for _, required := range []RawPackageEntry{
			{Path: skillPath, Directory: true},
			{Path: path.Join(skillPath, "SKILL.md")},
			{Path: path.Join(skillPath, "skill-card.md")},
		} {
			if err := require(required.Path, required.Directory); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateShared 复验 canonical 子树、四个 Agent 的落盘插件叶子及双 marketplace 的精确路径与完整语义。
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
	// 旧布局制品没有按 Agent 落盘，无法在复验时补全；保持 fail closed 并给出可操作提示，不自动清理 cache。
	if _, err := requireExactPhysicalDirectory(root, LandingRootDir); err != nil {
		return fmt.Errorf("Plugin artifact %q has no %q directory, its layout predates per-Agent landing; remove this artifact root to re-derive: %w", root, LandingRootDir, err)
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
	if err := validateCodexLanding(ref, root, manifest); err != nil {
		return err
	}
	if err := validateClaudeLanding(ref, root, manifest); err != nil {
		return err
	}
	for _, agent := range []domain.AgentID{domain.AgentHermes, domain.AgentBibit} {
		if err := validateFilesystemLanding(agent, root, manifest); err != nil {
			return err
		}
	}
	return validateLandingSkillPayloads(root, manifest)
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
		return validateCodexLanding(ref, root, manifest)
	case domain.AgentClaudeCode:
		return validateClaudeLanding(ref, root, manifest)
	default:
		return fmt.Errorf("validate Plugin artifact agent %q: %w", agent, ErrInvalid)
	}
}

func validateCodexLanding(ref domain.PowerRef, root string, canonical APCPluginManifest) error {
	pluginPath, err := LandingPluginPath(root, domain.AgentCodex, canonical.Name)
	if err != nil {
		return err
	}
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

func validateClaudeLanding(ref domain.PowerRef, root string, canonical APCPluginManifest) error {
	pluginPath, err := LandingPluginPath(root, domain.AgentClaudeCode, canonical.Name)
	if err != nil {
		return err
	}
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

// validateFilesystemLanding 复验单个 filesystem Agent 落盘插件叶子内的 plugin.yaml；
// Hermes 额外要求规范注册器，Bibit 的闭集由 validateSharedClosedWorld 保证没有该文件。
func validateFilesystemLanding(agent domain.AgentID, root string, canonical APCPluginManifest) error {
	pluginPath, err := LandingPluginPath(root, agent, canonical.Name)
	if err != nil {
		return err
	}
	var got FilesystemPluginManifest
	if err := decodeExactRequiredYAML(pluginPath, pluginManifestPath, &got); err != nil {
		return fmt.Errorf("decode %s filesystem Plugin manifest: %w", agent, err)
	}
	if want := expectedFilesystemPluginManifest(canonical); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%s filesystem Plugin manifest does not match canonical index: %w", agent, ErrInvalid)
	}
	if agent != domain.AgentHermes {
		return nil
	}
	initContents, err := readExactRequiredFile(pluginPath, pluginInitPath)
	if err != nil {
		return fmt.Errorf("read Hermes Plugin initializer: %w", err)
	}
	if string(initContents) != pluginInitContents {
		return fmt.Errorf("Hermes Plugin initializer is not the canonical no-op register function: %w", ErrInvalid)
	}
	return nil
}

// validateLandingSkillPayloads 逐文件比较四个落盘插件叶子的声明 Skill 子树与 canonical 子树。
// 闭集规则只保证路径完整，本函数额外保证落盘内容确实是 canonical 内容的完整副本。
func validateLandingSkillPayloads(root string, canonical APCPluginManifest) error {
	canonicalPlugin := filepath.Join(root, "plugins", canonical.Name)
	for _, agent := range PluginLandingAgents() {
		landingPlugin, err := LandingPluginPath(root, agent, canonical.Name)
		if err != nil {
			return err
		}
		for _, skillPath := range canonical.Skills {
			want := filepath.Join(canonicalPlugin, filepath.FromSlash(skillPath))
			got := filepath.Join(landingPlugin, filepath.FromSlash(skillPath))
			if err := compareSkillSubtree(want, got); err != nil {
				return fmt.Errorf("%s landing skill %q differs from canonical package: %w", agent, skillPath, errors.Join(ErrInvalid, err))
			}
		}
	}
	return nil
}

func compareSkillSubtree(wantRoot, gotRoot string) error {
	want, err := readSubtreeFiles(wantRoot)
	if err != nil {
		return fmt.Errorf("read canonical skill subtree %q: %w", wantRoot, err)
	}
	got, err := readSubtreeFiles(gotRoot)
	if err != nil {
		return fmt.Errorf("read landing skill subtree %q: %w", gotRoot, err)
	}
	if len(want) != len(got) {
		return fmt.Errorf("landing skill subtree %q has %d files, canonical has %d", gotRoot, len(got), len(want))
	}
	for relative, wantContents := range want {
		gotContents, ok := got[relative]
		if !ok {
			return fmt.Errorf("landing skill subtree %q is missing %q", gotRoot, relative)
		}
		if !bytes.Equal(wantContents, gotContents) {
			return fmt.Errorf("landing skill file %q differs from canonical package", filepath.Join(gotRoot, filepath.FromSlash(relative)))
		}
	}
	return nil
}

func readSubtreeFiles(root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = contents
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
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

// expectedFilesystemPluginManifest 是 Hermes 与 Bibit 共用 plugin.yaml 的唯一期望值。
func expectedFilesystemPluginManifest(canonical APCPluginManifest) FilesystemPluginManifest {
	return FilesystemPluginManifest{
		SchemaVersion: PluginManifestSchemaVersion,
		Name:          canonical.Name,
		Version:       strings.TrimPrefix(canonical.Version, "v"),
		Description:   canonical.Description,
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

// validateSharedClosedWorld 定义共享 artifact 的唯一合法物理树：
// canonical 包只保留 `apc-plugin.json` 与声明 Skill 子树，派生内容全部位于各 Agent 的落盘插件叶子。
func validateSharedClosedWorld(root, logicalName string, manifest APCPluginManifest) error {
	pluginPrefix := path.Join("plugins", logicalName)
	allowedDirectories := map[string]struct{}{
		".agents": {}, ".agents/plugins": {}, ".claude-plugin": {}, "plugins": {}, pluginPrefix: {},
		pluginPrefix + "/skills": {},
	}
	allowedFiles := map[string]struct{}{
		filepath.ToSlash(codexMarketplacePath):     {},
		filepath.ToSlash(claudeMarketplacePath):    {},
		pluginPrefix + "/" + apcPluginManifestPath: {},
	}
	declaredSkillPrefixes := make([]string, 0, len(manifest.Skills)*(len(landingAgents)+1))
	for _, skillPath := range manifest.Skills {
		prefix := pluginPrefix + "/" + skillPath
		declaredSkillPrefixes = append(declaredSkillPrefixes, prefix)
		allowedDirectories[prefix] = struct{}{}
	}
	for _, agent := range landingAgents {
		landingDir, err := LandingRelativeDir(agent)
		if err != nil {
			return err
		}
		landingPlugin, err := LandingPluginRelativePath(agent, logicalName)
		if err != nil {
			return err
		}
		manifestRelative, err := landingManifestRelativePath(agent)
		if err != nil {
			return err
		}
		allowedDirectories[LandingRootDir] = struct{}{}
		allowedDirectories[landingDir] = struct{}{}
		allowedDirectories[path.Join(landingDir, "plugins")] = struct{}{}
		allowedDirectories[landingPlugin] = struct{}{}
		allowedDirectories[landingPlugin+"/skills"] = struct{}{}
		if directory := path.Dir(manifestRelative); directory != "." {
			allowedDirectories[path.Join(landingPlugin, directory)] = struct{}{}
		}
		allowedFiles[path.Join(landingPlugin, manifestRelative)] = struct{}{}
		if agent == domain.AgentHermes {
			allowedFiles[path.Join(landingPlugin, pluginInitPath)] = struct{}{}
		}
		for _, skillPath := range manifest.Skills {
			prefix := landingPlugin + "/" + skillPath
			declaredSkillPrefixes = append(declaredSkillPrefixes, prefix)
			allowedDirectories[prefix] = struct{}{}
		}
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
	source, err := LandingMarketplaceSource(domain.AgentCodex, manifest.Name)
	if err != nil {
		return codexMarketplaceDocument{}, err
	}
	document := codexMarketplaceDocument{Name: name}
	document.Interface.DisplayName = manifest.Name
	plugin := codexMarketplacePlugin{Name: manifest.Name, Category: "Coding"}
	plugin.Source.Source = "local"
	plugin.Source.Path = source
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
	source, err := LandingMarketplaceSource(domain.AgentClaudeCode, manifest.Name)
	if err != nil {
		return claudeMarketplaceDocument{}, err
	}
	document := claudeMarketplaceDocument{Name: name}
	document.Owner.Name = apmAuthor
	document.Metadata.Description = manifest.Description
	document.Metadata.Version = manifest.Version
	document.Plugins = []claudeMarketplacePlugin{{
		Name: manifest.Name, Source: source, Description: manifest.Description,
	}}
	return document, nil
}
