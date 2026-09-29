package cli_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
)

type recordingShowLifecycle struct {
	flags  cli.Flags
	result cli.ShowResult
	calls  int
}

func (l *recordingShowLifecycle) Show(_ context.Context, flags cli.Flags) (cli.ShowResult, error) {
	l.calls++
	l.flags = flags
	return l.result, nil
}

func newShowEntry(t *testing.T, stdout, stderr io.Writer, factory cli.ShowLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, unexpectedShowSearch, unexpectedShowList, unexpectedShowInstall, unexpectedShowUninstall, unexpectedShowFix, unexpectedShowValidate, unexpectedShowSubmit, unexpectedShowSubmission, factory)
}

func unexpectedShowSearch() (cli.SearchLifecycle, error)              { return nil, nil }
func unexpectedShowList() (cli.ListLifecycle, error)                  { return nil, nil }
func unexpectedShowInstall() (cli.InstallLifecycle, error)            { return nil, nil }
func unexpectedShowUninstall() (cli.UninstallLifecycle, error)        { return nil, nil }
func unexpectedShowFix() (cli.FixLifecycle, error)                    { return nil, nil }
func unexpectedShowValidate() (cli.ValidateLifecycle, error)          { return nil, nil }
func unexpectedShowSubmit() (cli.SubmitLifecycle, error)              { return nil, nil }
func unexpectedShowSubmission() (cli.SubmissionQueryLifecycle, error) { return nil, nil }

func TestShowPassesNameTypeAndVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingShowLifecycle{result: cli.ShowResult{Power: cli.ListedPower{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2.3"}, Description: "demo desc", Tags: []string{"tag"}}}}
	entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) { return lifecycle, nil })
	if err := entry.Execute([]string{"show", "demo=v1.2.3", "--type=skill"}); err != nil {
		t.Fatalf("Execute(show) error = %v", err)
	}
	if lifecycle.flags != (cli.Flags{Name: "demo", Type: "skill", Version: "v1.2.3"}) {
		t.Fatalf("flags = %#v", lifecycle.flags)
	}
	if !strings.Contains(stdout.String(), "type: skill") || !strings.Contains(stdout.String(), "version: v1.2.3") || !strings.Contains(stdout.String(), "description: demo desc") || !strings.Contains(stdout.String(), "tags: tag") {
		t.Fatalf("output = %q", stdout.String())
	}
}

func TestShowDefaultsToLatestVersionAndSupportsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingShowLifecycle{result: cli.ShowResult{Power: cli.ListedPower{Power: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo", Version: "v2.0.0"}, Description: "desc", Tags: []string{"a", "b"}}}}
	entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) { return lifecycle, nil })
	if err := entry.Execute([]string{"show", "demo", "--json"}); err != nil {
		t.Fatalf("Execute(show --json) error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"version": "v2.0.0"`) || !strings.Contains(stdout.String(), `"description": "desc"`) {
		t.Fatalf("json output = %q", stdout.String())
	}
}

func TestShowRendersLabeledFieldsAndEmptyTags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingShowLifecycle{result: cli.ShowResult{Power: cli.ListedPower{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Description: "desc", Tags: []string{}}}}
	entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) { return lifecycle, nil })
	if err := entry.Execute([]string{"show", "demo"}); err != nil {
		t.Fatalf("Execute(show) error = %v", err)
	}
	want := "type: skill\nname: demo\nversion: v1.0.0\ndescription: desc\ntags:\n"
	if stdout.String() != want {
		t.Fatalf("output = %q, want %q", stdout.String(), want)
	}
}

func TestShowValidatesNameAndVersionBeforeFactory(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "invalid name", args: []string{"show", "Bad Name"}, wantErr: "invalid power name"},
		{name: "non-canonical version", args: []string{"show", "demo=1.0.0"}, wantErr: "invalid power version"},
		{name: "empty version", args: []string{"show", "demo="}, wantErr: "power version must not be empty"},
		{name: "empty name", args: []string{"show", "=v1.0.0"}, wantErr: "power name must not be empty"},
		{name: "extra equals", args: []string{"show", "demo=v1.2.3=extra"}, wantErr: "invalid power version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			factoryCalls := 0
			entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) {
				factoryCalls++
				return &recordingShowLifecycle{}, nil
			})
			err := entry.Execute(test.args)
			if err == nil {
				t.Fatalf("Execute(%q) unexpectedly succeeded", test.args)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Execute(%q) error = %v, want containing %q", test.args, err, test.wantErr)
			}
			if factoryCalls != 0 {
				t.Fatalf("factory calls = %d, want 0 for %q", factoryCalls, test.args)
			}
		})
	}
}

func TestShowRejectsVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) {
		t.Fatal("factory called for removed --version flag")
		return nil, nil
	})
	if err := entry.Execute([]string{"show", "demo", "--version=v1.2.3"}); err == nil {
		t.Fatalf("Execute(show --version) unexpectedly succeeded")
	}
}

func TestShowRequiresName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) { t.Fatal("factory called"); return nil, nil })
	if err := entry.Execute([]string{"show"}); err == nil {
		t.Fatalf("error = %v", err)
	}
}

// TestShowJSONCarriesDisplayFields 锁定 show --json 是精确版本的展示元数据通道：
// 与 list --json 字段集一致，含 v2 展示字段（空为 ""）。
func TestShowJSONCarriesDisplayFields(t *testing.T) {
	result := cli.ShowResult{Power: cli.ListedPower{
		Power:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "brief-status", Version: "v0.0.1"},
		Description:   "Use when writing a concise progress status update.",
		Tags:          []string{"official"},
		NameZH:        "简报状态",
		NameEN:        "Brief Status",
		DescriptionZH: "用于撰写简洁的进度状态更新。",
	}}
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingShowLifecycle{result: result}
	entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"show", "brief-status", "--type=skill", "--json"}); err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	const want = "{\n  \"type\": \"skill\",\n  \"name\": \"brief-status\",\n  \"version\": \"v0.0.1\",\n  \"description\": \"Use when writing a concise progress status update.\",\n  \"tags\": [\n    \"official\"\n  ],\n  \"name_zh\": \"简报状态\",\n  \"name_en\": \"Brief Status\",\n  \"description_zh\": \"用于撰写简洁的进度状态更新。\"\n}\n"
	if got := stdout.String(); got != want {
		t.Fatalf("show --json stdout = %q, want %q", got, want)
	}
}
