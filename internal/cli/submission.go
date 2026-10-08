package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"apm/internal/domain"

	"github.com/spf13/cobra"
)

func newSubmissionCommand(newLifecycle SubmissionQueryLifecycleFactory) *cobra.Command {
	command := &cobra.Command{
		Use:   "submission",
		Short: "Query submissions",
	}
	command.AddCommand(newSubmissionGetCommand(newLifecycle), newSubmissionListCommand(newLifecycle))
	return command
}

func newSubmissionGetCommand(newLifecycle SubmissionQueryLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "get <submission-id>",
		Short: "Show one submission",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateSubmissionOutput(flags.Output); err != nil {
				return err
			}
			submissionID := strings.TrimSpace(args[0])
			if submissionID == "" {
				return errors.New("submission ID must not be empty")
			}
			if newLifecycle == nil {
				return errors.New("submission lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create submission query lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create submission query lifecycle: nil service")
			}
			result, err := service.Get(command.Context(), submissionID)
			if err != nil {
				return err
			}
			return renderSubmissionGet(command.OutOrStdout(), result, flags.Output)
		},
	}
	command.Flags().StringVar(&flags.Output, "output", "text", "output format: text or json")
	return command
}

func newSubmissionListCommand(newLifecycle SubmissionQueryLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "list",
		Short: "List submissions",
		Args:  cobra.ExactArgs(0),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateSubmissionListFlags(&flags); err != nil {
				return err
			}
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("submission lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create submission query lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create submission query lifecycle: nil service")
			}
			result, err := service.List(command.Context(), flags)
			if err != nil {
				return err
			}
			return renderSubmissionList(command.OutOrStdout(), result, flags.Output)
		},
	}
	command.Flags().StringVar(&flags.Status, "status", "", "filter by submission status")
	command.Flags().StringVar(&flags.Type, "type", "", "filter by power type")
	command.Flags().StringVar(&flags.Name, "name", "", "filter by power name")
	command.Flags().StringVar(&flags.Output, "output", "text", "output format: text or json")
	return command
}

func validateSubmissionListFlags(flags *Flags) error {
	if flags.Status != "" && !domain.IsActiveSubmissionStatus(domain.SubmissionStatus(flags.Status)) {
		return fmt.Errorf("--status must be an active submission status, got %q", flags.Status)
	}
	if err := resolvePowerType(flags); err != nil {
		return fmt.Errorf("--type must be skill, plugin, mcp, or expert, got %q", flags.Type)
	}
	if flags.Type != "" && !isSubmissionPowerType(domain.PowerType(flags.Type)) {
		return fmt.Errorf("--type must be skill, plugin, mcp, or expert, got %q", flags.Type)
	}
	if flags.Name != "" && !domain.IsCanonicalPowerName(flags.Name) {
		return fmt.Errorf("--name must be a canonical power name, got %q", flags.Name)
	}
	return validateSubmissionOutput(flags.Output)
}

func validateSubmissionOutput(output string) error {
	if output != "text" && output != "json" {
		return fmt.Errorf("--output must be text or json, got %q", output)
	}
	return nil
}

func isSubmissionPowerType(powerType domain.PowerType) bool {
	switch powerType {
	case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP, domain.PowerTypeExpert:
		return true
	default:
		return false
	}
}

func renderSubmissionGet(writer io.Writer, result SubmissionQueryResult, output string) error {
	if result.Detail == nil {
		return errors.New("submission get returned no detail")
	}
	detail := result.Detail
	if output == "json" {
		return encodeSubmissionJSON(writer, newSubmissionDetailOutput(*detail))
	}
	lines := []string{
		"submission_id: " + detail.SubmissionID,
		"type: " + string(detail.Type),
		"name: " + detail.Name,
		"version: " + detail.Version,
		"description: " + detail.Description,
		"message: " + detail.Message,
		"status: " + string(detail.Status),
		"created_at: " + detail.CreatedAt,
		"updated_at: " + detail.UpdatedAt,
		"content_available: " + strconv.FormatBool(detail.ContentAvailable),
	}
	for _, validationError := range detail.ValidationErrors {
		lines = append(lines, "validation_error: "+validationError)
	}
	lines = append(lines,
		"review_comment: "+detail.ReviewComment,
		"published_version: "+detail.PublishedVersion,
		"timeline:",
	)
	for _, event := range detail.Timeline {
		lines = append(lines, event.Timestamp+" "+event.EventType+" "+string(event.StatusFrom)+" -> "+string(event.StatusTo)+": "+event.Message)
	}
	for _, line := range lines {
		if _, err := io.WriteString(writer, line+"\n"); err != nil {
			return fmt.Errorf("write submission detail: %w", err)
		}
	}
	return nil
}

func renderSubmissionList(writer io.Writer, result SubmissionQueryResult, output string) error {
	if output == "json" {
		return encodeSubmissionJSON(writer, newSubmissionListOutput(result.Summaries))
	}
	if len(result.Summaries) == 0 {
		if _, err := io.WriteString(writer, "No active submissions.\n"); err != nil {
			return fmt.Errorf("write submission list: %w", err)
		}
		return nil
	}
	for _, item := range result.Summaries {
		line := item.SubmissionID + " " + string(item.Type) + "/" + item.Name + "@" + item.Version + " " + string(item.Status)
		if item.CreatedAt != "" {
			line += " " + item.CreatedAt
		}
		line += "\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return fmt.Errorf("write submission list: %w", err)
		}
	}
	return nil
}

func encodeSubmissionJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode submission result: %w", err)
	}
	return nil
}
