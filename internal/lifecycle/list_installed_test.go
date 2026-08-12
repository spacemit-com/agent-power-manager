package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

func TestListInstalledRecoversWarnsRefreshesReadsThenReleases(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	fixture := listFixture{
		recovery: transaction.RecoveryResult{
			Recovered:     true,
			Operation:     transaction.OperationInstall,
			Agent:         domain.AgentCodex,
			Power:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
			OriginalPhase: transaction.PhasePublishing,
			Direction:     transaction.RecoveryCommit,
			Actions: []transaction.RecoveryAction{
				{Kind: transaction.RecoveryActionSetManifest},
				{Kind: transaction.RecoveryActionRemoveBackup, Path: "/tmp/list-backup"},
			},
		},
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		groups: map[domain.AgentID]domain.PowerGroups{
			domain.AgentClaudeCode: {
				domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v1.0.0"}},
			},
			domain.AgentCodex: {
				domain.PowerTypePlugin: {{Type: domain.PowerTypePlugin, Name: "beta", Version: "v2.0.0"}},
			},
		},
	}
	assertWarn := func(point string) {
		if !strings.Contains(stderr.String(), "level=WARN") {
			t.Fatalf("stderr before %s = %q, want recovery WARN", point, stderr.String())
		}
	}
	fixture.beforeRefresh = func() { assertWarn("refresh") }
	fixture.beforeRelease = func() { assertWarn("release") }
	service := newListService(t, &calls, fixture)

	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "config:codex", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if len(result.Powers) != 2 || result.Powers[0].Agent != domain.AgentClaudeCode || result.Powers[1].Agent != domain.AgentCodex {
		t.Fatalf("result = %#v, want ordered claude-code/codex powers", result)
	}
	for _, want := range []string{
		"level=WARN", "msg=\"transaction recovered\"", "operation=install", "agent=codex",
		"power=demo", "version=v2.0.0", "phase=publishing", "direction=commit", "actions=",
		"set-manifest", "remove-backup", "/tmp/list-backup",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
	if got := strings.Count(stderr.String(), "level=WARN"); got != 1 {
		t.Fatalf("recovery WARN count = %d, want 1; stderr = %q", got, stderr.String())
	}
}

func TestListInstalledFatalPathsReleaseOnceAndSuppressResults(t *testing.T) {
	acquireErr := errors.New("acquire failed")
	recoverErr := errors.New("recover failed")
	refreshErr := errors.New("refresh failed")
	configErr := errors.New("config failed")
	releaseErr := errors.New("release failed")
	installedAll := cli.Flags{Installed: true, Agent: "all"}
	installedCodex := cli.Flags{Installed: true, Agent: "codex"}
	codexSnapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}

	tests := []struct {
		name      string
		flags     cli.Flags
		fixture   listFixture
		wantCalls []string
		wantErrs  []error
		wantText  string
	}{
		{
			name:      "acquire",
			flags:     installedAll,
			fixture:   listFixture{acquireErr: acquireErr},
			wantCalls: []string{"acquire"},
			wantErrs:  []error{acquireErr},
		},
		{
			name:      "recover and release",
			flags:     installedAll,
			fixture:   listFixture{recoverErr: recoverErr, releaseErr: releaseErr},
			wantCalls: []string{"acquire", "recover", "release"},
			wantErrs:  []error{recoverErr, releaseErr},
		},
		{
			name:      "refresh and release",
			flags:     installedAll,
			fixture:   listFixture{refreshErr: refreshErr, releaseErr: releaseErr},
			wantCalls: []string{"acquire", "recover", "refresh", "release"},
			wantErrs:  []error{refreshErr, releaseErr},
		},
		{
			name:      "unavailable concrete agent and release",
			flags:     installedCodex,
			fixture:   listFixture{releaseErr: releaseErr},
			wantCalls: []string{"acquire", "recover", "refresh", "release"},
			wantErrs:  []error{releaseErr},
			wantText:  "codex",
		},
		{
			name:  "concrete config and release",
			flags: installedCodex,
			fixture: listFixture{
				releaseErr: releaseErr,
				snapshot:   codexSnapshot,
				configErrs: map[domain.AgentID]error{domain.AgentCodex: configErr},
			},
			wantCalls: []string{"acquire", "recover", "refresh", "config:codex", "release"},
			wantErrs:  []error{configErr, releaseErr},
		},
		{
			name:  "release gate",
			flags: installedAll,
			fixture: listFixture{
				releaseErr: releaseErr,
				snapshot:   codexSnapshot,
				groups: map[domain.AgentID]domain.PowerGroups{
					domain.AgentCodex: {
						domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "hidden", Version: "v1.0.0"}},
					},
				},
			},
			wantCalls: []string{"acquire", "recover", "refresh", "config:codex", "release"},
			wantErrs:  []error{releaseErr},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			service := newListService(t, &calls, tt.fixture)
			result, err := service.List(context.Background(), tt.flags)
			if err == nil {
				t.Fatal("List() error = nil, want failure")
			}
			for _, wantErr := range tt.wantErrs {
				if !errors.Is(err, wantErr) {
					t.Fatalf("List() error = %v, want cause %v", err, wantErr)
				}
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("List() error = %v, want text %q", err, tt.wantText)
			}
			if len(result.Powers) != 0 {
				t.Fatalf("List() result = %#v, want no powers", result)
			}
			if !reflect.DeepEqual(calls, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, tt.wantCalls)
			}
			wantReleases := 0
			if len(tt.wantCalls) > 1 {
				wantReleases = 1
			}
			gotReleases := 0
			for _, call := range calls {
				if call == "release" {
					gotReleases++
				}
			}
			if gotReleases != wantReleases {
				t.Fatalf("release calls = %d, want %d", gotReleases, wantReleases)
			}
		})
	}
}

func TestListInstalledEmptySnapshotReturnsNonNilEmptyResult(t *testing.T) {
	var calls []string
	service := newListService(t, &calls, listFixture{})

	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Powers == nil || len(result.Powers) != 0 {
		t.Fatalf("List() result = %#v, want non-nil empty powers", result)
	}
	if want := []string{"acquire", "recover", "refresh", "release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestListInstalledAllReturnsPartialAndContinuesAfterAgentFailure(t *testing.T) {
	var calls []string
	claudeErr := errors.New("claude config failed")
	service := newListService(t, &calls, listFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		groups: map[domain.AgentID]domain.PowerGroups{
			domain.AgentCodex: {
				domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
			},
		},
		configErrs: map[domain.AgentID]error{domain.AgentClaudeCode: claudeErr},
	})

	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	var partial *cli.PartialListError
	if !errors.As(err, &partial) || !errors.Is(err, claudeErr) {
		t.Fatalf("List() error = %v, want PartialListError wrapping %v", err, claudeErr)
	}
	if len(partial.Failures) != 1 || partial.Failures[0].Agent != domain.AgentClaudeCode {
		t.Fatalf("partial failures = %#v, want ordered claude-code failure", partial.Failures)
	}
	if len(result.Powers) != 1 || result.Powers[0].Agent != domain.AgentCodex {
		t.Fatalf("List() result = %#v, want codex success", result)
	}
	wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "config:codex", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestListInstalledAllKeepsFailureOrderAndReadsLaterAgents(t *testing.T) {
	var calls []string
	claudeErr := errors.New("claude config failed")
	codexErr := errors.New("codex config failed")
	service := newListService(t, &calls, listFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}},
		groups: map[domain.AgentID]domain.PowerGroups{
			domain.AgentHermes: {
				domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "later", Version: "v1.0.0"}},
			},
		},
		configErrs: map[domain.AgentID]error{
			domain.AgentClaudeCode: claudeErr,
			domain.AgentCodex:      codexErr,
		},
	})

	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	var partial *cli.PartialListError
	if !errors.As(err, &partial) || !errors.Is(err, claudeErr) || !errors.Is(err, codexErr) {
		t.Fatalf("List() error = %v, want both ordered partial causes", err)
	}
	wantFailures := []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}
	gotFailures := make([]domain.AgentID, 0, len(partial.Failures))
	for _, failure := range partial.Failures {
		gotFailures = append(gotFailures, failure.Agent)
	}
	if !reflect.DeepEqual(gotFailures, wantFailures) {
		t.Fatalf("failure agents = %v, want %v", gotFailures, wantFailures)
	}
	if len(result.Powers) != 1 || result.Powers[0].Agent != domain.AgentHermes {
		t.Fatalf("List() result = %#v, want later hermes success", result)
	}
	wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "config:codex", "config:hermes-agent", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestListInstalledPartialAndReleaseFailureSuppressesResultAndMarker(t *testing.T) {
	var calls []string
	claudeErr := errors.New("claude config failed")
	codexErr := errors.New("codex config failed")
	releaseErr := errors.New("release failed")
	service := newListService(t, &calls, listFixture{
		releaseErr: releaseErr,
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}},
		groups: map[domain.AgentID]domain.PowerGroups{
			domain.AgentHermes: {
				domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "hidden", Version: "v1.0.0"}},
			},
		},
		configErrs: map[domain.AgentID]error{
			domain.AgentClaudeCode: claudeErr,
			domain.AgentCodex:      codexErr,
		},
	})

	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	for _, wantErr := range []error{claudeErr, codexErr, releaseErr} {
		if !errors.Is(err, wantErr) {
			t.Fatalf("List() error = %v, want cause %v", err, wantErr)
		}
	}
	var partial *cli.PartialListError
	if errors.As(err, &partial) {
		t.Fatalf("List() error = %v, must not expose PartialListError after release failure", err)
	}
	if len(result.Powers) != 0 {
		t.Fatalf("List() result = %#v, want no powers", result)
	}
	wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "config:codex", "config:hermes-agent", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestListInstalledNoJournalHasNoNormalOutput(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	service := newListService(t, &calls, listFixture{})
	if _, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"}); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want no output", stdout.String(), stderr.String())
	}
}

func TestListInstalledNoJournalWritesDebugWhenVerbose(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, true))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	service := newListService(t, &calls, listFixture{})
	if _, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"level=DEBUG", "msg=\"no active transaction before installed list\""} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
