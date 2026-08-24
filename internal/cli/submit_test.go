package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/cloud"
)

func TestSubmitRequiresType(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing type", args: []string{"submit", "dir"}, want: "--type must be skill, plugin, mcp, or agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			lifecycle := &recordingSubmitLifecycle{}
			entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

			err := entry.Execute(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute(%q) error = %v, want containing %q", tt.args, err, tt.want)
			}
			if lifecycle.calls != 0 {
				t.Fatalf("factory calls = %d, want 0 (validation precedes factory)", lifecycle.calls)
			}
		})
	}
}

func TestSubmitRequiresMessageForEveryPowerType(t *testing.T) {
	for _, powerType := range []string{"skill", "plugin", "mcp", "agent"} {
		t.Run(powerType, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			lifecycle := &recordingSubmitLifecycle{}
			entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

			err := entry.Execute([]string{"submit", "fixture", "--type=" + powerType})
			if err == nil || !strings.Contains(err.Error(), "--message is required") {
				t.Fatalf("type %s error = %v", powerType, err)
			}
			if lifecycle.calls != 0 {
				t.Fatalf("lifecycle calls = %d, want 0", lifecycle.calls)
			}
		})
	}
}

func TestSubmissionCommandsRejectLegacyMetadataFlags(t *testing.T) {
	for _, args := range [][]string{
		{"validate", "fixture", "--type=skill", "--version=v1.0.0"},
		{"submit", "fixture", "--type=skill", "--change-summary=first"},
		{"submit", "fixture", "--type=skill", "--version=v1.0.0", "--message=first"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), unexpectedSubmission(t))

			if err := entry.Execute(args); err == nil {
				t.Fatalf("accepted %v", args)
			}
		})
	}
}

func TestSubmitInvokesLifecycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmitLifecycle{result: cli.SubmitResult{SubmissionID: "sub_1", Status: "received"}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

	if err := entry.Execute([]string{"submit", "dir", "--type=skill", "--message=  first release  "}); err != nil {
		t.Fatalf("Execute(submit) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("factory calls = %d, want 1", lifecycle.calls)
	}
	if lifecycle.flags.Type != "skill" || lifecycle.flags.Version != "" || lifecycle.flags.Message != "first release" {
		t.Fatalf("flags = %#v", lifecycle.flags)
	}
	if out := stdout.String(); !strings.Contains(out, "submission_id: sub_1") || !strings.Contains(out, "status: received") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestSubmitMCPPassesMessage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmitLifecycle{result: cli.SubmitResult{SubmissionID: "sub_1", Status: "received"}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

	if err := entry.Execute([]string{"submit", "dir", "--type=mcp", "--message=update"}); err != nil {
		t.Fatalf("Execute(submit mcp) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("factory calls = %d, want 1", lifecycle.calls)
	}
	if lifecycle.flags.Message != "update" {
		t.Fatalf("message = %q, want update", lifecycle.flags.Message)
	}
}

func TestSubmitQuotaErrorGuidesListAndPreservesSentinel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmitLifecycle{err: cloud.ErrTooManyActiveSubmissions}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

	err := entry.Execute([]string{"submit", "dir", "--type=skill", "--message=first"})
	if !errors.Is(err, cloud.ErrTooManyActiveSubmissions) {
		t.Fatalf("Execute(submit) error = %v, want ErrTooManyActiveSubmissions", err)
	}
	if !strings.Contains(err.Error(), "apm submission list") {
		t.Fatalf("Execute(submit) error = %v, want list guidance", err)
	}
}
