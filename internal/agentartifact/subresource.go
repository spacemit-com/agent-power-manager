package agentartifact

import (
	"fmt"
	"sort"
	"strings"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/skillartifact"
	"apm/internal/utils/jsonstrict"
)

// Agent 根下可选的子资源容器目录。Agent 可以把「只属于自己」的 Skill、Plugin 与
// MCP 随资源根一起打包上传，沿用既有「资源文件夹 → 确定性 tar.gz → 校验 → 解包 →
// 落盘」链路，不引入第二套打包或传输机制（ADR-046）。
const (
	subSkillsDir  = "skills"
	subPluginsDir = "plugins"
	subMCPDir     = "mcp"

	// subMCPExtension 是 MCP 子资源的形态：mcp/ 下一个完整的单 JSON 文件。
	subMCPExtension = ".json"
	// subPluginManifestName 是 Plugin 子资源的权威索引，与独立 Plugin 同名。
	subPluginManifestName = "apc-plugin.json"
)

// validateSubResources 校验 Agent 根下 skills/、plugins/、mcp/ 三个可选子资源。
//
// 子资源是裸目录/裸文件：身份取自子资源自身内容（SKILL.md frontmatter、
// apc-plugin.json、MCP JSON 的 name），目录名只是容器，不参与身份校验。
// 每个子资源必须自洽——缺标记文件即失败，不允许半成品被装到机器上。
// 同一类型内身份必须唯一；跨类型同名不冲突。
func validateSubResources(index map[string]Entry) error {
	if err := validateSubResourcePaths(index); err != nil {
		return err
	}

	var failures []error

	seenSkills := make(map[string]string)
	for _, root := range subResourceRoots(index, subSkillsDir) {
		metadata, err := skillartifact.ValidateContent(root, subResourceReader(index))
		if err != nil {
			failures = append(failures, agentError("skill sub-resource %q: %v", root, err))
			continue
		}
		if previous, duplicate := seenSkills[metadata.Name]; duplicate {
			failures = append(failures, agentError(
				"skill sub-resources %q and %q share frontmatter name %q", previous, root, metadata.Name))
			continue
		}
		seenSkills[metadata.Name] = root
	}

	seenPlugins := make(map[string]string)
	for _, root := range subResourceRoots(index, subPluginsDir) {
		name, err := validatePluginSubResource(index, root)
		if err != nil {
			failures = append(failures, agentError("plugin sub-resource %q: %v", root, err))
			continue
		}
		if previous, duplicate := seenPlugins[name]; duplicate {
			failures = append(failures, agentError(
				"plugin sub-resources %q and %q share name %q", previous, root, name))
			continue
		}
		seenPlugins[name] = root
	}

	seenMCPs := make(map[string]string)
	for _, file := range subResourceFiles(index, subMCPDir) {
		resource, err := validateMCPSubResource(index, file)
		if err != nil {
			failures = append(failures, agentError("mcp sub-resource %q: %v", file, err))
			continue
		}
		if previous, duplicate := seenMCPs[resource.Name]; duplicate {
			failures = append(failures, agentError(
				"mcp sub-resources %q and %q share name %q", previous, file, resource.Name))
			continue
		}
		seenMCPs[resource.Name] = file
	}

	if len(failures) > 0 {
		return joinAgentErrors(failures)
	}
	return nil
}

// subResourceReader 以 Agent 快照为源读取子资源内的文件；路径为快照绝对相对路径。
func subResourceReader(index map[string]Entry) func(string) ([]byte, error) {
	return func(relative string) ([]byte, error) {
		entry, ok := index[relative]
		if !ok || entry.Directory {
			return nil, fmt.Errorf("requires %q as a regular file", relative)
		}
		return entry.Data, nil
	}
}

// subResourceRoots 返回 <dir>/<name> 形式的一层子资源目录路径，按字典序稳定输出。
//
// 按路径首段推导，而不是只认目录条目：外部归档可能只带文件条目、不带显式目录条目
// （解包后文件系统里目录自然存在）。若只认目录条目，这类资源的子资源会被静默跳过
// 校验——宁可多校验一个推导出的根，也不能漏校验。
func subResourceRoots(index map[string]Entry, dir string) []string {
	prefix := dir + "/"
	names := make(map[string]struct{})
	for relative := range index {
		if !strings.HasPrefix(relative, prefix) {
			continue
		}
		first, _, _ := strings.Cut(strings.TrimPrefix(relative, prefix), "/")
		if first != "" {
			names[first] = struct{}{}
		}
	}
	roots := make([]string, 0, len(names))
	for name := range names {
		roots = append(roots, prefix+name)
	}
	sort.Strings(roots)
	return roots
}

// subResourceFiles 返回 <dir>/<name> 形式的一层普通文件路径，按字典序稳定输出。
func subResourceFiles(index map[string]Entry, dir string) []string {
	prefix := dir + "/"
	var files []string
	for relative, entry := range index {
		if !strings.HasPrefix(relative, prefix) || entry.Directory {
			continue
		}
		if !strings.Contains(strings.TrimPrefix(relative, prefix), "/") {
			files = append(files, relative)
		}
	}
	sort.Strings(files)
	return files
}

// validatePluginSubResource 校验一个完整的 APC Plugin 子资源：apc-plugin.json 必须
// 可解码且身份合法，声明的 skills/ 子树必须齐全，且不得出现未声明目录。
// 校验复用 Plugin 资源本身的权威规则，因此子资源与独立 Plugin 完全同构。
func validatePluginSubResource(index map[string]Entry, root string) (string, error) {
	manifestRelative := root + "/" + subPluginManifestName
	entry, ok := index[manifestRelative]
	if !ok || entry.Directory {
		return "", fmt.Errorf("requires %s as a regular file", subPluginManifestName)
	}
	var manifest pluginartifact.APCPluginManifest
	if err := jsonstrict.Decode(entry.Data, &manifest, true); err != nil {
		return "", fmt.Errorf("decode %s: %v", subPluginManifestName, err)
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" {
		return "", fmt.Errorf("%s requires non-empty name and version", subPluginManifestName)
	}
	ref := domain.PowerRef{Type: domain.PowerTypePlugin, Name: manifest.Name, Version: manifest.Version}
	if err := pluginartifact.ValidateSubmitAPCPluginManifest(ref, manifest); err != nil {
		return "", fmt.Errorf("%s %v", subPluginManifestName, err)
	}

	// 转换为 Plugin 根相对的条目视图，交给既有闭集校验判断「声明 == 实物」。
	prefix := root + "/"
	entries := make([]pluginartifact.RawPackageEntry, 0, len(index))
	for relative, candidate := range index {
		if !strings.HasPrefix(relative, prefix) {
			continue
		}
		entries = append(entries, pluginartifact.RawPackageEntry{
			Path:      strings.TrimPrefix(relative, prefix),
			Directory: candidate.Directory,
		})
	}
	if err := pluginartifact.ValidateRawPackageEntries(manifest, entries); err != nil {
		return "", err
	}

	// 内嵌 Skill 的格式与独立 Skill 完全一致，逐个子树复用同一套校验。
	for _, skillPath := range manifest.Skills {
		if _, err := skillartifact.ValidateContent(root+"/"+skillPath, subResourceReader(index)); err != nil {
			return "", fmt.Errorf("skill %q: %v", skillPath, err)
		}
	}
	return manifest.Name, nil
}

// validateMCPSubResource 校验一个 MCP 子资源：mcp/<name>.json 必须是严格可解码的
// 完整 MCP 资源，并通过提交侧的 v2 与展示字段要求。
func validateMCPSubResource(index map[string]Entry, file string) (domain.MCPResource, error) {
	entry, ok := index[file]
	if !ok || entry.Directory {
		return domain.MCPResource{}, fmt.Errorf("must be a regular file")
	}
	resource, err := domain.DecodeMCPResourceJSON(entry.Data)
	if err != nil {
		return domain.MCPResource{}, err
	}
	if err := resource.ValidateSubmit(); err != nil {
		return domain.MCPResource{}, err
	}
	return resource, nil
}

// validateSubResourcePaths 校验子资源的形态与命名：skills/ 与 plugins/ 下只能有
// 一层目录，mcp/ 下只能有一层 .json 普通文件；名字不得为空或以点号开头。
// 目录名不承载身份，因此只要求安全字符集，比 canonical Power name 宽松。
func validateSubResourcePaths(index map[string]Entry) error {
	var failures []error

	for _, dir := range []string{subSkillsDir, subPluginsDir} {
		for _, root := range subResourceRoots(index, dir) {
			name := strings.TrimPrefix(root, dir+"/")
			if !isSafeSubResourceName(name) {
				failures = append(failures, agentError(
					"sub-resource path %q has an invalid name %q", root, name))
			}
			// 显式存在但不是目录：不要把同名文件当成子资源容器。
			if entry, ok := index[root]; ok && !entry.Directory {
				failures = append(failures, agentError(
					"sub-resource path %q must be a directory", root))
			}
		}
	}

	for _, relative := range childEntries(index, subMCPDir) {
		if index[relative].Directory {
			// MCP 子资源是单文件，不允许再套一层目录。
			failures = append(failures, agentError(
				"sub-resource path %q must be a regular file", relative))
			continue
		}
		name, _ := splitChild(relative, subMCPDir)
		if !strings.HasSuffix(name, subMCPExtension) {
			failures = append(failures, agentError(
				"sub-resource path %q must end with %q", relative, subMCPExtension))
			continue
		}
		if !isSafeSubResourceName(strings.TrimSuffix(name, subMCPExtension)) {
			failures = append(failures, agentError(
				"sub-resource path %q has an invalid name", relative))
		}
	}

	if len(failures) > 0 {
		return joinAgentErrors(failures)
	}
	return nil
}

// childEntries 返回 dir/ 下的一层条目路径，按字典序稳定输出。
func childEntries(index map[string]Entry, dir string) []string {
	prefix := dir + "/"
	var children []string
	for relative := range index {
		if !strings.HasPrefix(relative, prefix) {
			continue
		}
		if !strings.Contains(strings.TrimPrefix(relative, prefix), "/") {
			children = append(children, relative)
		}
	}
	sort.Strings(children)
	return children
}

// splitChild 返回 dir/ 下一层条目的名字与「是否还有更深层」。
func splitChild(relative, dir string) (name string, nested bool) {
	name, _, nested = strings.Cut(strings.TrimPrefix(relative, dir+"/"), "/")
	return name, nested
}

// isSafeSubResourceName 判定子资源容器名是否安全：非空、不以点号开头，
// 且只含字母、数字、点、下划线与连字符。
func isSafeSubResourceName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '-', character == '_', character == '.':
		default:
			return false
		}
	}
	return true
}

// isSubResourceRoot 判断根条目名是否为子资源容器目录。
func isSubResourceRoot(name string) bool {
	switch name {
	case subSkillsDir, subPluginsDir, subMCPDir:
		return true
	default:
		return false
	}
}
