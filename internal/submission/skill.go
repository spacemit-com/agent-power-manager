package submission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"go.yaml.in/yaml/v3"
)

type skillSourceLoader struct{}

func (skillSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	snapshot, err := captureSourceTree(ctx, path)
	if err != nil {
		return source{}, err
	}
	name, version, description, err := validateSkillContent(snapshot, "")
	if err != nil {
		return source{}, err
	}
	if err := validateCommonMetadata(name, version, description); err != nil {
		return source{}, err
	}
	return source{
		validation: Validation{
			Type: domain.PowerTypeSkill, Name: name, Version: version, Description: description,
			FileCount: snapshot.stats.fileCount, ContentSize: snapshot.stats.totalSize,
		},
		contentType: "application/gzip",
		build:       snapshot.build,
	}, nil
}

// validateSkillContent 校验资源根下 SKILL.md 与 skill-card.md 的完整格式，
// 返回 frontmatter 中的 name、version 与 description。独立 Skill 与内嵌 Plugin Skill 共用。
func validateSkillContent(snapshot sourceTreeSnapshot, root string) (name, version, description string, err error) {
	skillMD, err := snapshot.regularFile(filepath.ToSlash(filepath.Join(root, "SKILL.md")))
	if err != nil {
		return "", "", "", err
	}
	cardMD, err := snapshot.regularFile(filepath.ToSlash(filepath.Join(root, "skill-card.md")))
	if err != nil {
		return "", "", "", err
	}
	name, version, description, err = parseSkillFrontmatter(skillMD)
	if err != nil {
		return "", "", "", err
	}
	if err := validateSkillCard(cardMD); err != nil {
		return "", "", "", err
	}
	return name, version, description, nil
}

// parseSkillFrontmatter 解析 SKILL.md 首字节起的 YAML frontmatter，
// 要求 mapping、无重复键，且 name/version/description 均为非空字符串。
func parseSkillFrontmatter(contents []byte) (name, version, description string, err error) {
	block, _, err := extractFrontmatter(contents)
	if err != nil {
		return "", "", "", fmt.Errorf("SKILL.md %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(block, &document); err != nil {
		return "", "", "", fmt.Errorf("decode SKILL.md frontmatter: %w", errors.Join(ErrInvalid, err))
	}
	node := &document
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return "", "", "", fmt.Errorf("SKILL.md frontmatter must be a mapping: %w", ErrInvalid)
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return "", "", "", fmt.Errorf("SKILL.md frontmatter must be a mapping: %w", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(node.Content)/2)
	var nameNode, versionNode, descriptionNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, duplicate := seen[key]; duplicate {
			return "", "", "", fmt.Errorf("SKILL.md frontmatter has duplicate key %q: %w", key, ErrInvalid)
		}
		seen[key] = struct{}{}
		switch key {
		case "name":
			nameNode = node.Content[i+1]
		case "version":
			versionNode = node.Content[i+1]
		case "description":
			descriptionNode = node.Content[i+1]
		}
	}
	if !isNonEmptyStringScalar(nameNode) {
		return "", "", "", fmt.Errorf("SKILL.md frontmatter requires non-empty string name: %w", ErrInvalid)
	}
	if !isNonEmptyStringScalar(versionNode) {
		return "", "", "", fmt.Errorf("SKILL.md frontmatter requires non-empty string version: %w", ErrInvalid)
	}
	if _, err := domain.CanonicalPowerVersion(versionNode.Value); err != nil {
		return "", "", "", fmt.Errorf("SKILL.md frontmatter version: %w", errors.Join(ErrInvalid, err))
	}
	if !isNonEmptyStringScalar(descriptionNode) {
		return "", "", "", fmt.Errorf("SKILL.md frontmatter requires non-empty string description: %w", ErrInvalid)
	}
	return nameNode.Value, versionNode.Value, descriptionNode.Value, nil
}

func isNonEmptyStringScalar(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" && strings.TrimSpace(node.Value) != ""
}

// extractFrontmatter 提取首字节起的 frontmatter；起始标记只允许精确 "---"、
// 可选水平空白以及 LF/CRLF，避免把同行 YAML 内容误当作合法标记。
// 返回 frontmatter 正文（不含起止标记）与关闭标记之后的剩余正文。
func extractFrontmatter(contents []byte) (block []byte, remainder []byte, err error) {
	lineEnd := bytes.IndexByte(contents, '\n')
	if lineEnd < 0 {
		return nil, nil, fmt.Errorf("must start with YAML frontmatter at first byte: %w", ErrInvalid)
	}
	opener := contents[:lineEnd]
	if len(opener) > 0 && opener[len(opener)-1] == '\r' {
		opener = opener[:len(opener)-1]
	}
	if !bytes.HasPrefix(opener, []byte("---")) || len(bytes.Trim(opener[len("---"):], " \t")) != 0 {
		return nil, nil, fmt.Errorf("must start with YAML frontmatter at first byte: %w", ErrInvalid)
	}
	rest := contents[lineEnd+1:]
	lineStart := 0
	for lineStart <= len(rest) {
		lineEnd := bytes.IndexByte(rest[lineStart:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = rest[lineStart:]
		} else {
			line = rest[lineStart : lineStart+lineEnd]
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "---" || trimmed == "..." {
			bodyStart := lineStart + len(line)
			if bodyStart < len(rest) && rest[bodyStart] == '\n' {
				bodyStart++
			}
			return rest[:lineStart], rest[bodyStart:], nil
		}
		if lineEnd < 0 {
			break
		}
		lineStart += lineEnd + 1
	}
	return nil, nil, fmt.Errorf("frontmatter is not closed with --- or ...: %w", ErrInvalid)
}

// validateSkillCardFrontmatter 校验 skill-card.md 首字节起的 frontmatter 必须含整数 schema_version == 1，
// 并返回关闭标记之后的正文供标题与章节校验；未知键与 SKILL.md frontmatter 一致允许忽略。
func validateSkillCardFrontmatter(contents []byte) ([]byte, error) {
	block, body, err := extractFrontmatter(contents)
	if err != nil {
		return nil, fmt.Errorf("skill-card.md %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(block, &document); err != nil {
		return nil, fmt.Errorf("decode skill-card.md frontmatter: %w", errors.Join(ErrInvalid, err))
	}
	node := &document
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil, fmt.Errorf("skill-card.md frontmatter must be a mapping: %w", ErrInvalid)
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("skill-card.md frontmatter must be a mapping: %w", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(node.Content)/2)
	var schemaNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("skill-card.md frontmatter has duplicate key %q: %w", key, ErrInvalid)
		}
		seen[key] = struct{}{}
		if key == "schema_version" {
			schemaNode = node.Content[i+1]
		}
	}
	if schemaNode == nil || schemaNode.Kind != yaml.ScalarNode || schemaNode.Tag != "!!int" || schemaNode.Value != "1" {
		return nil, fmt.Errorf("skill-card.md frontmatter requires integer schema_version 1: %w", ErrInvalid)
	}
	return body, nil
}

var requiredSkillCardSections = []string{
	"Capability",
	"Use Cases",
	"Inputs and Outputs",
	"Dependencies",
	"Limitations",
	"Maintainer",
}

// validateSkillCard 校验 skill-card.md 的 schema_version frontmatter、标题、六个已填章节与 Maintainer 字段。
func validateSkillCard(contents []byte) error {
	body, err := validateSkillCardFrontmatter(contents)
	if err != nil {
		return err
	}
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 {
		return fmt.Errorf("skill-card.md is empty: %w", ErrInvalid)
	}
	title := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(title, "# Skill Card: ") || strings.TrimSpace(strings.TrimPrefix(title, "# Skill Card: ")) == "" {
		return fmt.Errorf("skill-card.md must start with \"# Skill Card: <display name>\": %w", ErrInvalid)
	}
	sections := make(map[string]string)
	current := ""
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			current = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if current != "" {
			sections[current] += trimmed + "\n"
		}
	}
	for _, required := range requiredSkillCardSections {
		if strings.TrimSpace(sections[required]) == "" {
			return fmt.Errorf("skill-card.md section %q is missing or empty: %w", required, ErrInvalid)
		}
	}
	return validateMaintainer(sections["Maintainer"])
}

// validateMaintainer 校验 Maintainer 含非空 Owner 与联系/支持路径，并拒绝模板占位。
func validateMaintainer(section string) error {
	lower := strings.ToLower(section)
	if strings.Contains(lower, "replace_owner") || strings.Contains(lower, "replace_contact") {
		return fmt.Errorf("skill-card.md Maintainer contains placeholder: %w", ErrInvalid)
	}
	hasOwner := false
	hasContact := false
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "owner":
			hasOwner = value != ""
		case "contact", "support":
			hasContact = value != ""
		}
	}
	if !hasOwner {
		return fmt.Errorf("skill-card.md Maintainer has no non-empty Owner: %w", ErrInvalid)
	}
	if !hasContact {
		return fmt.Errorf("skill-card.md Maintainer has no non-empty Contact or support path: %w", ErrInvalid)
	}
	return nil
}
