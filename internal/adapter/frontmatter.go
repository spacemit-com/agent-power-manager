package adapter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// validateSkillFrontmatter 校验 SKILL.md 的 YAML frontmatter 与预期名称。
func validateSkillFrontmatter(path, expectedName string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read skill frontmatter %q: %w", path, err)
	}

	yamlBytes, err := extractYAMLFrontmatter(raw)
	if err != nil {
		return errors.Join(ErrInvalidSkill, err)
	}
	if err := validateFrontmatterNode(yamlBytes, expectedName); err != nil {
		return errors.Join(ErrInvalidSkill, err)
	}
	return nil
}

// extractYAMLFrontmatter 提取从文件首字节开始且换行风格一致的 YAML 区段。
func extractYAMLFrontmatter(raw []byte) ([]byte, error) {
	var newline []byte
	switch {
	case bytes.HasPrefix(raw, []byte("---\n")):
		newline = []byte("\n")
	case bytes.HasPrefix(raw, []byte("---\r\n")):
		newline = []byte("\r\n")
	default:
		return nil, errors.New("skill frontmatter must begin with an opening delimiter")
	}

	opening := append([]byte("---"), newline...)
	closing := opening
	for start := len(opening); start+len(closing) <= len(raw); {
		relative := bytes.Index(raw[start:], closing)
		if relative < 0 {
			break
		}
		position := start + relative
		if position == len(opening) || bytes.Equal(raw[position-len(newline):position], newline) {
			return raw[len(opening):position], nil
		}
		start = position + len(closing)
	}
	return nil, errors.New("skill frontmatter must end with a matching closing delimiter")
}

// validateFrontmatterNode 校验 frontmatter mapping 中的 name 和 description。
func validateFrontmatterNode(yamlBytes []byte, expectedName string) error {
	var document yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &document); err != nil {
		return fmt.Errorf("parse skill frontmatter YAML: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("skill frontmatter YAML must be a mapping")
	}

	fields := make(map[string]*yaml.Node, 2)
	for index := 0; index < len(document.Content[0].Content); index += 2 {
		key := document.Content[0].Content[index]
		if key.Kind != yaml.ScalarNode || (key.Value != "name" && key.Value != "description") {
			continue
		}
		if _, exists := fields[key.Value]; exists {
			return fmt.Errorf("skill frontmatter contains duplicate %q", key.Value)
		}
		fields[key.Value] = document.Content[0].Content[index+1]
	}

	name, err := frontmatterString(fields, "name")
	if err != nil {
		return err
	}
	if name != expectedName {
		return fmt.Errorf("skill frontmatter name %q does not match expected name %q", name, expectedName)
	}
	_, err = frontmatterString(fields, "description")
	return err
}

// frontmatterString 返回指定的非空字符串字段。
func frontmatterString(fields map[string]*yaml.Node, field string) (string, error) {
	value, exists := fields[field]
	if !exists {
		return "", fmt.Errorf("skill frontmatter is missing %q", field)
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return "", fmt.Errorf("skill frontmatter %q must be a string", field)
	}
	if strings.TrimSpace(value.Value) == "" {
		return "", fmt.Errorf("skill frontmatter %q must not be blank", field)
	}
	return value.Value, nil
}
