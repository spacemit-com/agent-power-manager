package cli

import (
	"errors"
	"fmt"
	"strings"
)

// Flags 保存命令解析后的公共选项和位置参数。
type Flags struct {
	Agent     string
	Type      string
	Name      string
	Version   string
	Keyword   string
	Installed bool
}

func validateCommon(flags Flags) error {
	if strings.TrimSpace(flags.Agent) == "" {
		return errors.New("--agent is required")
	}
	switch flags.Type {
	case "", "skill", "plugin", "mcp":
		return nil
	default:
		return fmt.Errorf("invalid --type %q: must be skill, plugin, or mcp", flags.Type)
	}
}

func parsePower(value string) (string, string, error) {
	name, version, hasVersion := strings.Cut(value, "=")
	if strings.TrimSpace(name) == "" {
		return "", "", errors.New("power name must not be empty")
	}
	if hasVersion && strings.TrimSpace(version) == "" {
		return "", "", errors.New("power version must not be empty")
	}
	return name, version, nil
}
