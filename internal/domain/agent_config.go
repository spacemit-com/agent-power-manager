package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ErrInvalidAgent 表示 Agent 配置不满足安装约束。
var ErrInvalidAgent = errors.New("invalid agent")

// AgentConfig 表示 agent.yaml 中 APM 需要的最小字段；其余字段由安装流程原样透传。
type AgentConfig struct {
	SchemaVersion int       `yaml:"schema_version"`
	Agent         AgentInfo `yaml:"agent"`
}

// AgentInfo 表示 agent.yaml 的 agent 区块标识字段。
type AgentInfo struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
}

// Validate 校验 Agent 配置的最小边界，不清洗或改写原始值。
func (config AgentConfig) Validate() error {
	if config.SchemaVersion != 1 {
		return fmt.Errorf("agent schema version %d must be 1: %w", config.SchemaVersion, ErrInvalidAgent)
	}
	if !isSafeAgentID(config.Agent.ID) {
		return fmt.Errorf("agent id %q: %w", config.Agent.ID, ErrInvalidAgent)
	}
	if _, err := canonicalAgentVersion(config.Agent.Version); err != nil {
		return err
	}
	return nil
}

// isSafeAgentID 报告 agent id 是否为安全且未经清洗的单个路径段。
func isSafeAgentID(id string) bool {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." {
		return false
	}
	if filepath.IsAbs(id) || strings.ContainsAny(id, `/\\`) {
		return false
	}
	return filepath.Clean(id) == id
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
