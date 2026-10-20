// Package skillartifact 提供 Skill 资源格式的唯一权威校验器。
//
// 独立 Skill 提交、Plugin 内嵌 Skill 以及 Agent 携带的独有 Skill 必须复用本包，
// 不得各自维护 frontmatter 或 skill-card 规则。本包只读取与校验，不打包、
// 不改写原始字节，也不访问文件系统：调用方负责先固定资源树再传入正文。
//
// 依赖方向：本包只依赖 domain，因此被 submission 与 agentartifact 共同依赖也不会成环。
package skillartifact

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"apm/internal/domain"

	"go.yaml.in/yaml/v3"
)

// ErrInvalid 表示 Skill 内容不满足格式约束；具体原因包装在错误链中。
var ErrInvalid = errors.New("invalid skill content")

// Metadata 保存 SKILL.md frontmatter 的 v2 稳定字段。
// name/version/description 是身份与调用信息；NameZH/NameEN/DescriptionZH 仅用于展示。
type Metadata struct {
	SchemaVersion int
	Name          string
	Version       string
	Description   string
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// requiredCardSections 是 skill-card.md 必须全部非空填写的章节。
var requiredCardSections = []string{
	"Capability",
	"Use Cases",
	"Inputs and Outputs",
	"Dependencies",
	"Limitations",
	"Maintainer",
}

// ValidateContent 校验 Skill 资源根下 SKILL.md 与 skill-card.md 的完整 v2 格式，
// 返回 SKILL.md frontmatter 的稳定字段。readFile 以资源根相对路径读取文件，
// 使独立 Skill、Plugin 内嵌 Skill 与 Agent 子资源可以共用同一套规则。
func ValidateContent(root string, readFile func(relative string) ([]byte, error)) (Metadata, error) {
	skillMD, err := readFile(joinRelative(root, "SKILL.md"))
	if err != nil {
		return Metadata{}, err
	}
	cardMD, err := readFile(joinRelative(root, "skill-card.md"))
	if err != nil {
		return Metadata{}, err
	}
	metadata, err := ParseMetadata(skillMD)
	if err != nil {
		return Metadata{}, err
	}
	if err := ValidateCard(cardMD); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

// joinRelative 以 "/" 拼接资源根与文件名，根为空时直接返回文件名。
func joinRelative(root, name string) string {
	if root == "" {
		return name
	}
	return strings.TrimSuffix(root, "/") + "/" + name
}

// ParseMetadata 解析 SKILL.md 首字节起的 YAML frontmatter，要求 mapping、无重复键、
// 整数 schema_version 2，name/version/description 为非空字符串，且展示字段
// name_zh/name_en/description_zh 均为非空字符串。提交侧只接受 v2；安装侧不经过
// 本函数，因此已发布的 v1 资源仍可安装（ADR-038）。
func ParseMetadata(contents []byte) (Metadata, error) {
	block, _, err := ExtractFrontmatter(contents)
	if err != nil {
		return Metadata{}, fmt.Errorf("SKILL.md %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(block, &document); err != nil {
		return Metadata{}, fmt.Errorf("decode SKILL.md frontmatter: %w", errors.Join(ErrInvalid, err))
	}
	node := &document
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return Metadata{}, fmt.Errorf("SKILL.md frontmatter must be a mapping: %w", ErrInvalid)
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter must be a mapping: %w", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(node.Content)/2)
	var schemaNode, nameNode, versionNode, descriptionNode *yaml.Node
	var nameZHNode, nameENNode, descriptionZHNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, duplicate := seen[key]; duplicate {
			return Metadata{}, fmt.Errorf("SKILL.md frontmatter has duplicate key %q: %w", key, ErrInvalid)
		}
		seen[key] = struct{}{}
		switch key {
		case "schema_version":
			schemaNode = node.Content[i+1]
		case "name":
			nameNode = node.Content[i+1]
		case "version":
			versionNode = node.Content[i+1]
		case "description":
			descriptionNode = node.Content[i+1]
		case "name_zh":
			nameZHNode = node.Content[i+1]
		case "name_en":
			nameENNode = node.Content[i+1]
		case "description_zh":
			descriptionZHNode = node.Content[i+1]
		}
	}
	schemaVersion, err := IntegerSchemaVersion(schemaNode, "SKILL.md frontmatter")
	if err != nil {
		return Metadata{}, err
	}
	if !isNonEmptyStringScalar(nameNode) {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter requires non-empty string name: %w", ErrInvalid)
	}
	if !isNonEmptyStringScalar(versionNode) {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter requires non-empty string version: %w", ErrInvalid)
	}
	if _, err := domain.CanonicalPowerVersion(versionNode.Value); err != nil {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter version: %w", errors.Join(ErrInvalid, err))
	}
	if !isNonEmptyStringScalar(descriptionNode) {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter requires non-empty string description: %w", ErrInvalid)
	}
	if !isNonEmptyStringScalar(nameZHNode) {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter requires non-empty string name_zh (Chinese display name): %w", ErrInvalid)
	}
	if !isNonEmptyStringScalar(nameENNode) {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter requires non-empty string name_en (English display name): %w", ErrInvalid)
	}
	if !isNonEmptyStringScalar(descriptionZHNode) {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter requires non-empty string description_zh (Chinese description): %w", ErrInvalid)
	}
	if err := domain.ValidateRequiredDisplayMetadata(nameZHNode.Value, nameENNode.Value, descriptionZHNode.Value); err != nil {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter %w", errors.Join(ErrInvalid, err))
	}
	if err := domain.ValidateDescription(descriptionNode.Value); err != nil {
		return Metadata{}, fmt.Errorf("SKILL.md frontmatter %w", errors.Join(ErrInvalid, err))
	}
	return Metadata{
		SchemaVersion: schemaVersion,
		Name:          nameNode.Value,
		Version:       versionNode.Value,
		Description:   descriptionNode.Value,
		NameZH:        nameZHNode.Value,
		NameEN:        nameENNode.Value,
		DescriptionZH: descriptionZHNode.Value,
	}, nil
}

// IntegerSchemaVersion 读取整数 schema_version 节点；缺失或非整数时返回可操作错误。
// 提交侧只接受 v2，因此错误信息固定提示 upgrade to 2（v1 资源必须补齐版本与展示字段）。
func IntegerSchemaVersion(node *yaml.Node, where string) (int, error) {
	if node == nil {
		return 0, fmt.Errorf("%s requires integer schema_version %d; upgrade to %d: %w", where, domain.PowerSchemaVersionV2, domain.PowerSchemaVersionV2, ErrInvalid)
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, fmt.Errorf("%s schema_version must be an integer %d; upgrade to %d: %w", where, domain.PowerSchemaVersionV2, domain.PowerSchemaVersionV2, ErrInvalid)
	}
	version, err := strconv.Atoi(node.Value)
	if err != nil {
		return 0, fmt.Errorf("%s schema_version must be an integer %d; upgrade to %d: %w", where, domain.PowerSchemaVersionV2, domain.PowerSchemaVersionV2, ErrInvalid)
	}
	if version != domain.PowerSchemaVersionV2 {
		return 0, fmt.Errorf("%s schema_version %d is not submittable; upgrade to %d: %w", where, version, domain.PowerSchemaVersionV2, ErrInvalid)
	}
	return version, nil
}

func isNonEmptyStringScalar(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" && strings.TrimSpace(node.Value) != ""
}

// ExtractFrontmatter 提取首字节起的 frontmatter；起始标记只允许精确 "---"、
// 可选水平空白以及 LF/CRLF，避免把同行 YAML 内容误当作合法标记。
// 返回 frontmatter 正文（不含起止标记）与关闭标记之后的剩余正文。
func ExtractFrontmatter(contents []byte) (block []byte, remainder []byte, err error) {
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

// ValidateCardFrontmatter 校验 skill-card.md 首字节起的 frontmatter 必须含整数
// schema_version == 2，并返回关闭标记之后的正文供标题与章节校验；未知键与
// SKILL.md frontmatter 一致允许忽略。skill-card.md 结构本身未变，升到 2 只为让
// 同一份资源的 schema_version 取值一致（ADR-038）。
func ValidateCardFrontmatter(contents []byte) ([]byte, error) {
	block, body, err := ExtractFrontmatter(contents)
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
	if schemaNode == nil || schemaNode.Kind != yaml.ScalarNode || schemaNode.Tag != "!!int" || schemaNode.Value != "2" {
		return nil, fmt.Errorf("skill-card.md frontmatter requires integer schema_version 2: %w", ErrInvalid)
	}
	return body, nil
}

// ValidateCard 校验 skill-card.md 的 schema_version frontmatter、标题、六个已填章节与 Maintainer 字段。
func ValidateCard(contents []byte) error {
	body, err := ValidateCardFrontmatter(contents)
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
	for _, required := range requiredCardSections {
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
