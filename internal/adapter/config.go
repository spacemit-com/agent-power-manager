package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/paths"
)

// adaptFunc 定义 Agent 对 staging 中 Skill 的原地适配操作。
type adaptFunc func(domain.PowerRef, string) error

// agentConfig 描述内置 Agent 的根目录与 Skill 适配规则。
type agentConfig struct {
	id          domain.AgentID
	homeEnv     string
	defaultRoot string
	adapt       adaptFunc
}

// builtInAgentConfigs 按固定顺序保存所有内置 Agent 配置。
var builtInAgentConfigs = []agentConfig{
	{id: domain.AgentCodex, homeEnv: "CODEX_HOME", defaultRoot: ".codex", adapt: noOpAdapt},
	{id: domain.AgentClaudeCode, homeEnv: "CLAUDE_CONFIG_DIR", defaultRoot: ".claude", adapt: noOpAdapt},
	{id: domain.AgentHermes, homeEnv: "HERMES_HOME", defaultRoot: ".hermes", adapt: noOpAdapt},
}

func noOpAdapt(domain.PowerRef, string) error { return nil }

// rootPath 返回环境变量覆盖或有效 HOME 下的 Agent 根目录。
func (c agentConfig) rootPath() (string, error) {
	if root := strings.TrimSpace(os.Getenv(c.homeEnv)); root != "" {
		return root, nil
	}

	home, err := paths.HomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve agent %q root: %w", c.id, err)
	}
	return filepath.Join(home, c.defaultRoot), nil
}

// detectAgent 检查 Agent 根目录是否存在且为目录。
func (c agentConfig) detectAgent() (bool, error) {
	root, err := c.rootPath()
	if err != nil {
		return false, err
	}

	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("detect agent %q root %q: %w", c.id, root, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("detect agent %q root %q: %w", c.id, root, ErrAgentNotInstalled)
	}
	return true, nil
}

// targetPath 返回指定 Skill 在 Agent 全局目录中的安装路径。
func (c agentConfig) targetPath(ref domain.PowerRef) (string, error) {
	if ref.Type != domain.PowerTypeSkill || !isSafeSkillName(ref.Name) {
		return "", fmt.Errorf("target path for agent %q skill %q: %w", c.id, ref.Name, ErrInvalidSkill)
	}

	root, err := c.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "skills", ref.Name), nil
}

// isSafeSkillName 报告名称是否为安全且未经清洗的单个路径段。
func isSafeSkillName(name string) bool {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return false
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	return filepath.Clean(name) == name
}
