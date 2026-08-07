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

type adaptFunc func(domain.PowerRef, string) error

type agentConfig struct {
	id          domain.AgentID
	homeEnv     string
	defaultRoot string
	adapt       adaptFunc
}

var builtInAgentConfigs = []agentConfig{
	{id: domain.AgentCodex, homeEnv: "CODEX_HOME", defaultRoot: ".codex", adapt: noOpAdapt},
	{id: domain.AgentClaudeCode, homeEnv: "CLAUDE_CONFIG_DIR", defaultRoot: ".claude", adapt: noOpAdapt},
	{id: domain.AgentHermes, homeEnv: "HERMES_HOME", defaultRoot: ".hermes", adapt: noOpAdapt},
}

func noOpAdapt(domain.PowerRef, string) error { return nil }

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

func isSafeSkillName(name string) bool {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return false
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	return filepath.Clean(name) == name
}
