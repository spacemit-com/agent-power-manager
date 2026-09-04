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
	if err := entry.Execute([]string{"show", "demo", "--type=skill", "--version=v1.2.3"}); err != nil {
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
	want := "type: skill\nname: demo\nversion: v1.0.0\ndescription: desc\ntags: []\n"
	if stdout.String() != want {
		t.Fatalf("output = %q, want %q", stdout.String(), want)
	}
}

func TestShowValidatesCanonicalNameAndVersionBeforeFactory(t *testing.T) {
	for _, args := range [][]string{{"show", "Bad Name"}, {"show", "demo", "--version=1.0.0"}} {
		var stdout, stderr bytes.Buffer
		factoryCalls := 0
		entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) {
			factoryCalls++
			return &recordingShowLifecycle{}, nil
		})
		if err := entry.Execute(args); err == nil {
			t.Fatalf("Execute(%q) unexpectedly succeeded", args)
		}
		if factoryCalls != 0 {
			t.Fatalf("factory calls = %d, want 0 for %q", factoryCalls, args)
		}
	}
}

func TestShowRequiresName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newShowEntry(t, &stdout, &stderr, func() (cli.ShowLifecycle, error) { t.Fatal("factory called"); return nil, nil })
	if err := entry.Execute([]string{"show"}); err == nil {
		t.Fatalf("error = %v", err)
	}
}
