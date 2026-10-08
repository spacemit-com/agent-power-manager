package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"apm/internal/cli"
)

func TestValidateRequiresSubmissionType(t *testing.T) {
	for _, args := range [][]string{
		{"validate", "dir"},
		{"validate", "dir", "--type=widget"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			lifecycle := &recordingValidateLifecycle{}
			entry := newSubmissionEntry(t, &stdout, &stderr, func() (cli.ValidateLifecycle, error) { return lifecycle, nil }, unexpectedSubmit(t), unexpectedSubmission(t))

			err := entry.Execute(args)
			if err == nil || !strings.Contains(err.Error(), "--type must be skill, plugin, mcp, or expert") {
				t.Fatalf("Execute(%q) error = %v", args, err)
			}
			if lifecycle.calls != 0 {
				t.Fatalf("factory calls = %d, want 0 (validation precedes factory)", lifecycle.calls)
			}
		})
	}
}

// 改名前的拼写仍要能跑完，而且必须已经变成规范取值再进 lifecycle：留在 agent 会让
// Expert 类别在过滤、清单解析与提交路由上凭空消失。同时它会给出弃用提示。
func TestValidateNormalizesLegacyAgentTypeSpelling(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingValidateLifecycle{result: cli.ValidateResult{Type: "expert", Name: "demo", Version: "v1.0.0", Description: "demo", FileCount: 1, ContentSize: 10}}
	entry := newSubmissionEntry(t, &stdout, &stderr, func() (cli.ValidateLifecycle, error) { return lifecycle, nil }, unexpectedSubmit(t), unexpectedSubmission(t))
	if err := entry.Execute([]string{"validate", "agent.yaml", "--type=agent"}); err != nil {
		t.Fatalf("Execute(validate agent) error = %v", err)
	}
	if lifecycle.calls != 1 || lifecycle.flags.Type != "expert" {
		t.Fatalf("lifecycle = %+v", lifecycle)
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Fatalf("stderr = %q, want a deprecation warning", stderr.String())
	}
}

// --type=expert 是规范拼写：不进 lifecycle 之前不该再有任何改写。
func TestValidateAllowsExpertWithoutVersionOverride(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingValidateLifecycle{result: cli.ValidateResult{Type: "expert", Name: "demo", Version: "v1.0.0", Description: "demo", FileCount: 1, ContentSize: 10}}
	entry := newSubmissionEntry(t, &stdout, &stderr, func() (cli.ValidateLifecycle, error) { return lifecycle, nil }, unexpectedSubmit(t), unexpectedSubmission(t))
	if err := entry.Execute([]string{"validate", "agent.yaml", "--type=expert"}); err != nil {
		t.Fatalf("Execute(validate expert) error = %v", err)
	}
	if lifecycle.calls != 1 || lifecycle.flags.Type != "expert" {
		t.Fatalf("lifecycle = %+v", lifecycle)
	}
	if strings.Contains(stderr.String(), "deprecated") {
		t.Fatalf("stderr = %q, want no deprecation warning for the canonical spelling", stderr.String())
	}
}

func TestValidateInvokesLifecycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingValidateLifecycle{result: cli.ValidateResult{Type: "skill", Name: "demo", Description: "does things", FileCount: 2, ContentSize: 100}}
	entry := newSubmissionEntry(t, &stdout, &stderr, func() (cli.ValidateLifecycle, error) { return lifecycle, nil }, unexpectedSubmit(t), unexpectedSubmission(t))

	if err := entry.Execute([]string{"validate", "dir", "--type=skill"}); err != nil {
		t.Fatalf("Execute(validate) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("factory calls = %d, want 1", lifecycle.calls)
	}
	if lifecycle.flags.Type != "skill" || lifecycle.flags.Version != "" {
		t.Fatalf("flags = %#v", lifecycle.flags)
	}
	if len(lifecycle.args) != 1 || lifecycle.args[0] != "dir" {
		t.Fatalf("args = %v, want [dir]", lifecycle.args)
	}
	if out := stdout.String(); !strings.Contains(out, "type: skill") || !strings.Contains(out, "name: demo") || !strings.Contains(out, "file_count: 2") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestValidateJSONOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingValidateLifecycle{result: cli.ValidateResult{Type: "skill", Name: "demo", Description: "does things", FileCount: 2, ContentSize: 100, Warnings: []string{"warn"}}}
	entry := newSubmissionEntry(t, &stdout, &stderr, func() (cli.ValidateLifecycle, error) { return lifecycle, nil }, unexpectedSubmit(t), unexpectedSubmission(t))

	if err := entry.Execute([]string{"validate", "dir", "--type=skill", "--json"}); err != nil {
		t.Fatalf("Execute(validate --json) error = %v", err)
	}
	var decoded cli.ValidateResult
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", stdout.String(), err)
	}
	if decoded.Name != "demo" || decoded.FileCount != 2 || len(decoded.Warnings) != 1 || decoded.Warnings[0] != "warn" {
		t.Fatalf("decoded = %#v", decoded)
	}
}
