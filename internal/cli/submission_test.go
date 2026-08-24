package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestSubmissionGetInvokesLifecycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmissionLifecycle{result: cli.SubmissionQueryResult{Detail: &domain.Submission{SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Status: domain.SubmissionReviewPending}}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"submission", "get", "sub_1"}); err != nil {
		t.Fatalf("Execute(submission get) error = %v", err)
	}
	if lifecycle.getCalls != 1 || len(lifecycle.getIDs) != 1 || lifecycle.getIDs[0] != "sub_1" {
		t.Fatalf("Get calls = %v, want [sub_1]", lifecycle.getIDs)
	}
	if out := stdout.String(); !strings.Contains(out, "submission_id: sub_1") || !strings.Contains(out, "status: review_pending") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestSubmissionListInvokesLifecycleWithFilters(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmissionLifecycle{result: cli.SubmissionQueryResult{Summaries: []domain.SubmissionSummary{{SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Status: domain.SubmissionReviewPending, CreatedAt: "2026-08-24T10:00:00Z"}}}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"submission", "list", "--status=review_pending", "--type=skill", "--name=demo"}); err != nil {
		t.Fatalf("Execute(submission list) error = %v", err)
	}
	if lifecycle.listCalls != 1 {
		t.Fatalf("List calls = %d, want 1", lifecycle.listCalls)
	}
	flags := lifecycle.listFlags[0]
	if flags.Status != "review_pending" || flags.Type != "skill" || flags.Name != "demo" || flags.Output != "text" {
		t.Fatalf("flags = %#v", flags)
	}
	if out := stdout.String(); out != "sub_1 skill/demo@v1.0.0 review_pending 2026-08-24T10:00:00Z\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestSubmissionGetRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) { return &recordingSubmissionLifecycle{}, nil })
	if err := entry.Execute([]string{"submission", "get"}); err == nil {
		t.Fatal("Execute(submission get) error = nil, want missing id error")
	}
}

func TestSubmissionCommandsValidateQueryArgumentsBeforeLifecycle(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "terminal status", args: []string{"submission", "list", "--status=published"}},
		{name: "unknown status", args: []string{"submission", "list", "--status=unknown"}},
		{name: "invalid type", args: []string{"submission", "list", "--type=widget"}},
		{name: "invalid name", args: []string{"submission", "list", "--name=Demo"}},
		{name: "invalid list output", args: []string{"submission", "list", "--output=yaml"}},
		{name: "legacy limit", args: []string{"submission", "list", "--limit=10"}},
		{name: "invalid get output", args: []string{"submission", "get", "sub_1", "--output=yaml"}},
		{name: "blank get id", args: []string{"submission", "get", "   "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			factoryCalls := 0
			entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) {
				factoryCalls++
				return &recordingSubmissionLifecycle{}, nil
			})

			if err := entry.Execute(tt.args); err == nil {
				t.Fatalf("Execute(%q) error = nil", tt.args)
			}
			if factoryCalls != 0 {
				t.Fatalf("lifecycle factory calls = %d, want 0", factoryCalls)
			}
		})
	}
}

func TestSubmissionListTextOutputForEmptyResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) {
		return &recordingSubmissionLifecycle{}, nil
	})

	if err := entry.Execute([]string{"submission", "list"}); err != nil {
		t.Fatalf("Execute(submission list) error = %v", err)
	}
	if got, want := stdout.String(), "No active submissions.\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestSubmissionGetTextOutputIncludesFullDetailAndOrderedTimeline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	detail := submissionDetailFixture()
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) {
		return &recordingSubmissionLifecycle{result: cli.SubmissionQueryResult{Detail: &detail}}, nil
	})

	if err := entry.Execute([]string{"submission", "get", "sub_1"}); err != nil {
		t.Fatalf("Execute(submission get) error = %v", err)
	}
	want := "submission_id: sub_1\n" +
		"type: skill\n" +
		"name: demo\n" +
		"version: v1.0.0\n" +
		"description: Demo resource\n" +
		"message: First release\n" +
		"status: review_pending\n" +
		"created_at: 2026-08-24T10:00:00Z\n" +
		"updated_at: 2026-08-24T11:00:00Z\n" +
		"content_available: true\n" +
		"validation_error: manifest warning\n" +
		"review_comment: awaiting review\n" +
		"published_version: v0.9.0\n" +
		"timeline:\n" +
		"2026-08-24T10:00:00Z submitted  -> received: received\n" +
		"2026-08-24T11:00:00Z review_started received -> review_pending: queued for review\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestSubmissionJSONOutputUsesExplicitSnakeCaseContracts(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		detail := submissionDetailFixture()
		entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) {
			return &recordingSubmissionLifecycle{result: cli.SubmissionQueryResult{Detail: &detail}}, nil
		})

		if err := entry.Execute([]string{"submission", "get", "sub_1", "--output=json"}); err != nil {
			t.Fatalf("Execute(submission get --output=json) error = %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
			t.Fatalf("unmarshal stdout %q: %v", stdout.String(), err)
		}
		wantKeys := []string{"submission_id", "type", "name", "version", "description", "message", "status", "created_at", "updated_at", "content_available", "validation_errors", "review_comment", "published_version", "timeline"}
		if len(decoded) != len(wantKeys) {
			t.Fatalf("decoded keys = %v, want %v", decoded, wantKeys)
		}
		for _, key := range wantKeys {
			if _, ok := decoded[key]; !ok {
				t.Fatalf("decoded = %v, missing %q", decoded, key)
			}
		}
		if decoded["submission_id"] != "sub_1" || decoded["description"] != "Demo resource" || decoded["created_at"] != "2026-08-24T10:00:00Z" {
			t.Fatalf("decoded = %v", decoded)
		}
		timeline, ok := decoded["timeline"].([]any)
		if !ok || len(timeline) != 2 {
			t.Fatalf("timeline = %#v", decoded["timeline"])
		}
		first, ok := timeline[0].(map[string]any)
		if !ok || first["event_id"] != "evt_1" || first["status_from"] != "" || first["status_to"] != "received" {
			t.Fatalf("first timeline event = %#v", timeline[0])
		}
	})

	t.Run("list", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) {
			return &recordingSubmissionLifecycle{result: cli.SubmissionQueryResult{Summaries: []domain.SubmissionSummary{{SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Status: domain.SubmissionReviewPending, CreatedAt: "2026-08-24T10:00:00Z"}}}}, nil
		})

		if err := entry.Execute([]string{"submission", "list", "--output=json"}); err != nil {
			t.Fatalf("Execute(submission list --output=json) error = %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
			t.Fatalf("unmarshal stdout %q: %v", stdout.String(), err)
		}
		if len(decoded) != 1 || decoded["items"] == nil {
			t.Fatalf("decoded = %#v, want only items", decoded)
		}
	})
}

func submissionDetailFixture() domain.Submission {
	return domain.Submission{
		SubmissionID:     "sub_1",
		Type:             domain.PowerTypeSkill,
		Name:             "demo",
		Version:          "v1.0.0",
		Description:      "Demo resource",
		Message:          "First release",
		Status:           domain.SubmissionReviewPending,
		CreatedAt:        "2026-08-24T10:00:00Z",
		UpdatedAt:        "2026-08-24T11:00:00Z",
		ContentAvailable: true,
		ValidationErrors: []string{"manifest warning"},
		ReviewComment:    "awaiting review",
		PublishedVersion: "v0.9.0",
		Timeline: []domain.SubmissionEvent{
			{EventID: "evt_1", EventType: "submitted", StatusTo: domain.SubmissionReceived, Timestamp: "2026-08-24T10:00:00Z", Message: "received"},
			{EventID: "evt_2", EventType: "review_started", StatusFrom: domain.SubmissionReceived, StatusTo: domain.SubmissionReviewPending, Timestamp: "2026-08-24T11:00:00Z", Message: "queued for review"},
		},
	}
}
