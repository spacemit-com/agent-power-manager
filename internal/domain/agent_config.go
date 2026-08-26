package domain

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"
)

// ErrInvalidAgent 表示 Agent 配置不满足安装约束。
var ErrInvalidAgent = errors.New("invalid agent")

// AgentConfig 表示 agent.yaml 中 APM 需要校验的最小 V0.5 字段；其余字段由安装流程原样透传。
type AgentConfig struct {
	SchemaVersion int       `yaml:"schema_version"`
	Agent         AgentInfo `yaml:"agent"`
}

// AgentInfo 表示 agent.yaml 的 agent 区块稳定字段。id 仅供 Bibit 内部区分，
// 外部身份必须使用 name+version；description 与 soul 只要求 trim 后非空。
type AgentInfo struct {
	ID          string        `yaml:"id"`
	Name        string        `yaml:"name"`
	Version     string        `yaml:"version"`
	Description string        `yaml:"description"`
	Soul        string        `yaml:"soul"`
	Metadata    AgentMetadata `yaml:"metadata"`
}

// AgentMetadata 表示 agent 区块的可选元数据。avatar 为 assets/ 下 PNG 的包内相对路径（可选）。
type AgentMetadata struct {
	Avatar *string `yaml:"avatar"`
}

// Validate 校验 Agent 配置的最小边界，不清洗或改写原始值。
func (config AgentConfig) Validate() error {
	if config.SchemaVersion != 1 {
		return fmt.Errorf("agent schema version %d must be 1: %w", config.SchemaVersion, ErrInvalidAgent)
	}
	if strings.TrimSpace(config.Agent.ID) == "" {
		return fmt.Errorf("agent id must be non-empty: %w", ErrInvalidAgent)
	}
	if !IsCanonicalPowerName(config.Agent.Name) {
		return fmt.Errorf("agent name %q must be a canonical power name: %w", config.Agent.Name, ErrInvalidAgent)
	}
	if _, err := canonicalAgentVersion(config.Agent.Version); err != nil {
		return err
	}
	if strings.TrimSpace(config.Agent.Description) == "" {
		return fmt.Errorf("agent description must be non-empty: %w", ErrInvalidAgent)
	}
	if strings.TrimSpace(config.Agent.Soul) == "" {
		return fmt.Errorf("agent soul must be non-empty: %w", ErrInvalidAgent)
	}
	return nil
}

// DecodeAgentConfigYAML 严格解码单个 agent.yaml 文档：拒绝多 document、重复映射键与非字符串键，
// 并校验最小字段。返回的结构只含稳定字段，调用方必须保留原始字节用于发布。
func DecodeAgentConfigYAML(raw []byte) (AgentConfig, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return AgentConfig{}, fmt.Errorf("decode agent YAML: %w", errors.Join(ErrInvalidAgent, err))
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple YAML documents")
		}
		return AgentConfig{}, fmt.Errorf("decode agent YAML tail: %w", errors.Join(ErrInvalidAgent, err))
	}
	if err := validateUniqueYAMLMappingKeys(&document); err != nil {
		return AgentConfig{}, err
	}
	// 使用聚焦 helper 取得根 schema_version 与 agent mapping 的稳定字段。
	config, err := decodeAgentConfigNode(&document)
	if err != nil {
		return AgentConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return AgentConfig{}, err
	}
	return config, nil
}

// ValidatePowerRef 校验 config 自身合法且与 ref 的 type/name/version 完全一致，
// 用于缓存、安装与恢复时确认内容身份未被替换；name 对应 agent.name，而非 agent.id。
func (config AgentConfig) ValidatePowerRef(ref PowerRef) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if ref.Type != PowerTypeAgent || ref.Name != config.Agent.Name || ref.Version != config.Agent.Version {
		return fmt.Errorf("agent identity does not match power %+v: %w", ref, ErrInvalidAgent)
	}
	return nil
}

// validateUniqueYAMLMappingKeys 递归检查所有 mapping 节点的 key 为字符串 scalar 且无重复（按 tag+value）。
func validateUniqueYAMLMappingKeys(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			if err := validateUniqueYAMLMappingKeys(child); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("agent YAML mapping key must be a string scalar: %w", ErrInvalidAgent)
			}
			identity := key.Tag + "\x00" + key.Value
			if _, duplicate := seen[identity]; duplicate {
				return fmt.Errorf("agent YAML has duplicate key %q: %w", key.Value, ErrInvalidAgent)
			}
			seen[identity] = struct{}{}
			if err := validateUniqueYAMLMappingKeys(node.Content[i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeAgentConfigNode 从已通过重复键检查的 document 节点读取稳定字段，
// 要求根与 agent 均为 mapping，schema_version 为整数，id/name/version/description/soul
// 为字符串 scalar，可选 metadata 为 mapping 且 avatar 为字符串 scalar。
func decodeAgentConfigNode(document *yaml.Node) (AgentConfig, error) {
	if document == nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return AgentConfig{}, fmt.Errorf("agent YAML must be a single mapping document: %w", ErrInvalidAgent)
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return AgentConfig{}, fmt.Errorf("agent YAML root must be a mapping: %w", ErrInvalidAgent)
	}

	var schemaNode, agentNode *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		switch root.Content[i].Value {
		case "schema_version":
			schemaNode = root.Content[i+1]
		case "agent":
			agentNode = root.Content[i+1]
		}
	}
	if schemaNode == nil || schemaNode.Kind != yaml.ScalarNode || schemaNode.Tag != "!!int" {
		return AgentConfig{}, fmt.Errorf("agent YAML requires integer schema_version: %w", ErrInvalidAgent)
	}
	schemaVersion, err := strconv.Atoi(schemaNode.Value)
	if err != nil {
		return AgentConfig{}, fmt.Errorf("agent YAML requires integer schema_version: %w", ErrInvalidAgent)
	}
	if agentNode == nil || agentNode.Kind != yaml.MappingNode {
		return AgentConfig{}, fmt.Errorf("agent YAML requires agent mapping: %w", ErrInvalidAgent)
	}

	var info AgentInfo
	var idNode, nameNode, versionNode, descriptionNode, soulNode, metadataNode *yaml.Node
	for i := 0; i+1 < len(agentNode.Content); i += 2 {
		switch agentNode.Content[i].Value {
		case "id":
			idNode = agentNode.Content[i+1]
		case "name":
			nameNode = agentNode.Content[i+1]
		case "version":
			versionNode = agentNode.Content[i+1]
		case "description":
			descriptionNode = agentNode.Content[i+1]
		case "soul":
			soulNode = agentNode.Content[i+1]
		case "metadata":
			metadataNode = agentNode.Content[i+1]
		}
	}
	for _, field := range []struct {
		name   string
		node   *yaml.Node
		target *string
	}{
		{"id", idNode, &info.ID},
		{"name", nameNode, &info.Name},
		{"version", versionNode, &info.Version},
		{"description", descriptionNode, &info.Description},
		{"soul", soulNode, &info.Soul},
	} {
		if field.node == nil || field.node.Kind != yaml.ScalarNode || field.node.Tag != "!!str" {
			return AgentConfig{}, fmt.Errorf("agent YAML agent.%s must be a string: %w", field.name, ErrInvalidAgent)
		}
		*field.target = field.node.Value
	}
	if metadataNode != nil {
		if metadataNode.Kind != yaml.MappingNode {
			return AgentConfig{}, fmt.Errorf("agent YAML agent.metadata must be a mapping: %w", ErrInvalidAgent)
		}
		var avatarNode *yaml.Node
		for i := 0; i+1 < len(metadataNode.Content); i += 2 {
			if metadataNode.Content[i].Value == "avatar" {
				avatarNode = metadataNode.Content[i+1]
			}
		}
		if avatarNode != nil {
			if avatarNode.Kind != yaml.ScalarNode || avatarNode.Tag != "!!str" {
				return AgentConfig{}, fmt.Errorf("agent YAML agent.metadata.avatar must be a string: %w", ErrInvalidAgent)
			}
			avatar := avatarNode.Value
			info.Metadata.Avatar = &avatar
		}
	}
	return AgentConfig{SchemaVersion: schemaVersion, Agent: info}, nil
}

func canonicalAgentVersion(version string) (string, error) {
	parsed, err := semver.StrictNewVersion(strings.TrimPrefix(version, "v"))
	if err != nil {
		return "", fmt.Errorf("agent version %q must use vMAJOR.MINOR.PATCH: %w", version, errors.Join(ErrInvalidAgent, err))
	}
	if parsed.Prerelease() != "" || parsed.Metadata() != "" || "v"+parsed.String() != version {
		return "", fmt.Errorf("agent version %q must use vMAJOR.MINOR.PATCH: %w", version, ErrInvalidAgent)
	}
	return parsed.String(), nil
}
