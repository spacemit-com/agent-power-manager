package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newValidateCommand(newLifecycle ValidateLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "validate <path>",
		Short: "Validate a skill, plugin, or MCP source",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := requireSubmissionType(flags); err != nil {
				return err
			}
			if err := rejectAgentVersionOverride(flags); err != nil {
				return err
			}
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("validate lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create validate lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create validate lifecycle: nil service")
			}
			result, err := service.Validate(command.Context(), flags, args)
			if err != nil {
				return err
			}
			return renderValidate(command.OutOrStdout(), result, flags.JSON)
		},
	}
	bindTypeFlag(command, &flags)
	command.Flags().StringVar(&flags.Version, "version", "", "skill version vMAJOR.MINOR.PATCH")
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

// requireSubmissionType 限定提交类命令的 --type 只能是 skill/plugin/mcp/agent。
func requireSubmissionType(flags Flags) error {
	switch flags.Type {
	case "skill", "plugin", "mcp", "agent":
		return nil
	default:
		return fmt.Errorf("--type must be skill, plugin, mcp, or agent, got %q", flags.Type)
	}
}

// rejectAgentVersionOverride 拒绝 --type agent 携带非空 --version；Agent 版本只来自 agent.yaml。
func rejectAgentVersionOverride(flags Flags) error {
	if flags.Type == "agent" && strings.TrimSpace(flags.Version) != "" {
		return errors.New("--version is not allowed for agent; version comes from agent.yaml")
	}
	return nil
}

func renderValidate(writer io.Writer, result ValidateResult, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("encode validate result: %w", err)
		}
		return nil
	}
	lines := []string{
		"type: " + result.Type,
		"name: " + result.Name,
	}
	if result.Version != "" {
		lines = append(lines, "version: "+result.Version)
	}
	lines = append(lines,
		"description: "+result.Description,
		fmt.Sprintf("file_count: %d", result.FileCount),
		fmt.Sprintf("content_size: %d", result.ContentSize),
	)
	for _, warning := range result.Warnings {
		lines = append(lines, "warning: "+warning)
	}
	for _, line := range lines {
		if _, err := io.WriteString(writer, line+"\n"); err != nil {
			return fmt.Errorf("write validate result: %w", err)
		}
	}
	return nil
}
