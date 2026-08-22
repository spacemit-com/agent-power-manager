package submission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
)

type mcpSubmission struct {
	Name          string
	Version       string
	Description   string
	ChangeSummary string
	Definition    json.RawMessage
}

type mcpSubmissionWire struct {
	Name          string
	Version       string
	Description   string
	ChangeSummary string `json:"change_summary"`
	Definition    json.RawMessage
}

func validateMCP(ctx context.Context, request Request) (Validation, error) {
	if err := ctx.Err(); err != nil {
		return Validation{}, err
	}
	submission, err := readMCPSubmission(request.Path)
	if err != nil {
		return Validation{}, err
	}
	if err := validateCommonMetadata(submission.Name, submission.Version, submission.Description); err != nil {
		return Validation{}, err
	}
	if strings.TrimSpace(submission.ChangeSummary) == "" {
		return Validation{}, fmt.Errorf("MCP change_summary is empty: %w", ErrInvalid)
	}
	if _, err := domain.DecodeMCPServerJSON(submission.Definition); err != nil {
		return Validation{}, fmt.Errorf("decode MCP definition: %w", errors.Join(ErrInvalid, err))
	}
	canonical, err := CanonicalizeJSON(submission.Definition)
	if err != nil {
		return Validation{}, err
	}
	return Validation{
		Type:          domain.PowerTypeMCP,
		Name:          submission.Name,
		Version:       submission.Version,
		Description:   submission.Description,
		ChangeSummary: submission.ChangeSummary,
		FileCount:     1,
		ContentSize:   int64(len(canonical)),
	}, nil
}

// readMCPSubmission 读取并严格解析单个 MCP JSON 文件；拒绝未知字段、重复键与尾随值。
func readMCPSubmission(path string) (mcpSubmission, error) {
	info, err := os.Stat(path)
	if err != nil {
		return mcpSubmission{}, fmt.Errorf("read MCP file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return mcpSubmission{}, fmt.Errorf("MCP path %q is not a regular file: %w", path, ErrInvalid)
	}
	if info.Size() > maxSingleFileSize {
		return mcpSubmission{}, fmt.Errorf("MCP file exceeds %d bytes: %w", maxSingleFileSize, ErrInvalid)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return mcpSubmission{}, fmt.Errorf("read MCP file: %w", err)
	}
	var wire mcpSubmissionWire
	if err := jsonstrict.Decode(contents, &wire, true); err != nil {
		return mcpSubmission{}, fmt.Errorf("decode MCP submission: %w", errors.Join(ErrInvalid, err))
	}
	if len(wire.Definition) == 0 {
		return mcpSubmission{}, fmt.Errorf("MCP definition is missing: %w", ErrInvalid)
	}
	return mcpSubmission{
		Name:          wire.Name,
		Version:       wire.Version,
		Description:   wire.Description,
		ChangeSummary: wire.ChangeSummary,
		Definition:    wire.Definition,
	}, nil
}
