package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var canonicalInstallVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

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
	if flags.Type == "agent" {
		return validateAgentTypeFlags(flags)
	}
	if strings.TrimSpace(flags.Agent) == "" {
		return errors.New("--agent is required")
	}
	return validatePowerType(flags)
}

// validateAgentTypeFlags 约束 agent power 仅适用于 Bibit；空 --agent 由 lifecycle 默认 bibit。
func validateAgentTypeFlags(flags Flags) error {
	if err := validatePowerType(flags); err != nil {
		return err
	}
	switch flags.Agent {
	case "", "bibit":
		return nil
	default:
		return fmt.Errorf("--type agent only supports --agent bibit, got %q", flags.Agent)
	}
}

func validatePowerType(flags Flags) error {
	switch flags.Type {
	case "", "skill", "plugin", "mcp", "agent":
		return nil
	default:
		return fmt.Errorf("invalid --type %q: must be skill, plugin, mcp, or agent", flags.Type)
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

func validateInstallVersion(version string) error {
	if version == "" {
		return nil
	}
	if !canonicalInstallVersion.MatchString(version) {
		return fmt.Errorf("invalid power version %q: must be vMAJOR.MINOR.PATCH", version)
	}
	return nil
}
