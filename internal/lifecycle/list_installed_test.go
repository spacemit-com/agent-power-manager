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

func TestListInstalledActiveJournalWarnsThenRefreshesAndReads(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	fixture := listFixture{
		inspection: transaction.InspectionResult{Active: true, Operation: transaction.OperationInstall, Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, OriginalPhase: transaction.PhasePublishing},
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		groups: map[domain.AgentID]domain.InstalledPowerGroups{
			domain.AgentClaudeCode: {domain.PowerTypeSkill: {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v1.0.0"}}}},
			domain.AgentCodex:      {domain.PowerTypePlugin: {{Ref: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "beta", Version: "v2.0.0"}}}},
		},
	}
	fixture.beforeRefresh = func() {
		if !strings.Contains(stderr.String(), "transaction recovery required") {
			t.Fatalf("stderr before refresh = %q, want active journal warning", stderr.String())
		}
	}
	service := newListService(t, &calls, fixture)

	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inspect", "refresh", "config:claude-code", "config:codex"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if len(result.Powers) != 2 || result.Powers[0].Agent != domain.AgentClaudeCode || result.Powers[1].Agent != domain.AgentCodex {
		t.Fatalf("result = %#v, want ordered claude-code/codex powers", result)
	}
	for _, want := range []string{"level=WARN", "operation=install", "agent=codex", "power=demo", "version=v2.0.0", "phase=publishing", "hint=\"run apm fix\""} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestListInstalledFatalPathsSuppressResults(t *testing.T) {
	refreshErr := errors.New("refresh failed")
	configErr := errors.New("config failed")
	installedAll := cli.Flags{Installed: true, Agent: "all"}
	installedCodex := cli.Flags{Installed: true, Agent: "codex"}
	codexSnapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}
	tests := []struct {
		name      string
		flags     cli.Flags
		fixture   listFixture
		wantCalls []string
		wantErr   error
		wantText  string
	}{
		{"refresh", installedAll, listFixture{refreshErr: refreshErr}, []string{"inspect", "refresh"}, refreshErr, ""},
		{"unavailable concrete agent", installedCodex, listFixture{}, []string{"inspect", "refresh"}, nil, "codex"},
		{"concrete config", installedCodex, listFixture{snapshot: codexSnapshot, configErrs: map[domain.AgentID]error{domain.AgentCodex: configErr}}, []string{"inspect", "refresh", "config:codex"}, configErr, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			service := newListService(t, &calls, tt.fixture)
			result, err := service.List(context.Background(), tt.flags)
			if err == nil {
				t.Fatal("List() error = nil, want failure")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("List() error = %v, want cause %v", err, tt.wantErr)
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
		})
	}
}

func TestListInstalledEmptySnapshotReturnsNonNilEmptyResult(t *testing.T) {
	var calls []string
	service := newListService(t, &calls, listFixture{})
	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	if err != nil || result.Powers == nil || len(result.Powers) != 0 {
		t.Fatalf("List() = (%#v, %v), want non-nil empty powers without error", result, err)
	}
	if want := []string{"inspect", "refresh"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestListInstalledAllReturnsPartialAndContinuesAfterAgentFailure(t *testing.T) {
	var calls []string
	claudeErr := errors.New("claude config failed")
	service := newListService(t, &calls, listFixture{
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		groups:     map[domain.AgentID]domain.InstalledPowerGroups{domain.AgentCodex: {domain.PowerTypeSkill: {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}}}}},
		configErrs: map[domain.AgentID]error{domain.AgentClaudeCode: claudeErr},
	})
	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	var partial *cli.PartialListError
	if !errors.As(err, &partial) || !errors.Is(err, claudeErr) || len(partial.Failures) != 1 || partial.Failures[0].Agent != domain.AgentClaudeCode {
		t.Fatalf("List() error = %v, want ordered claude-code partial failure", err)
	}
	if len(result.Powers) != 1 || result.Powers[0].Agent != domain.AgentCodex {
		t.Fatalf("List() result = %#v, want codex success", result)
	}
	if want := []string{"inspect", "refresh", "config:claude-code", "config:codex"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestListInstalledAllKeepsFailureOrderAndReadsLaterAgents(t *testing.T) {
	var calls []string
	bibitErr := errors.New("bibit config failed")
	claudeErr := errors.New("claude config failed")
	codexErr := errors.New("codex config failed")
	service := newListService(t, &calls, listFixture{
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentBibit, domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}},
		groups:     map[domain.AgentID]domain.InstalledPowerGroups{domain.AgentHermes: {domain.PowerTypeSkill: {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "later", Version: "v1.0.0"}}}}},
		configErrs: map[domain.AgentID]error{domain.AgentBibit: bibitErr, domain.AgentClaudeCode: claudeErr, domain.AgentCodex: codexErr},
	})
	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	var partial *cli.PartialListError
	if !errors.As(err, &partial) || !errors.Is(err, bibitErr) || !errors.Is(err, claudeErr) || !errors.Is(err, codexErr) || len(partial.Failures) != 3 {
		t.Fatalf("List() error = %v, want all three partial causes", err)
	}
	if got, want := []domain.AgentID{partial.Failures[0].Agent, partial.Failures[1].Agent, partial.Failures[2].Agent}, []domain.AgentID{domain.AgentBibit, domain.AgentClaudeCode, domain.AgentCodex}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failure agents = %v, want %v", got, want)
	}
	if len(result.Powers) != 1 || result.Powers[0].Agent != domain.AgentHermes {
		t.Fatalf("List() result = %#v, want later hermes success", result)
	}
	if want := []string{"inspect", "refresh", "config:bibit", "config:claude-code", "config:codex", "config:hermes-agent"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestListInstalledDamagedJournalWarnsAndContinues(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	service := newListService(t, &calls, listFixture{
		inspectErr: errors.New("damaged journal"),
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups:     map[domain.AgentID]domain.InstalledPowerGroups{domain.AgentCodex: {domain.PowerTypeSkill: {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "visible", Version: "v1.0.0"}}}}},
	})
	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	if err != nil || len(result.Powers) != 1 || result.Powers[0].Agent != domain.AgentCodex {
		t.Fatalf("List() = (%#v, %v), want codex result without error", result, err)
	}
	if want := []string{"inspect", "refresh", "config:codex"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !strings.Contains(stderr.String(), "transaction journal is damaged") || stdout.Len() != 0 {
		t.Fatalf("output = stdout %q stderr %q, want damaged journal warning", stdout.String(), stderr.String())
	}
}

func TestListInstalledNoJournalOutputDependsOnVerbosity(t *testing.T) {
	for _, tt := range []struct {
		name    string
		verbose bool
		wantLog string
	}{{"normal", false, ""}, {"verbose", true, "no active transaction before read-only command"}} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var stdout, stderr bytes.Buffer
			originalLogger := slog.Default()
			slog.SetDefault(logging.NewLogger(&stdout, &stderr, tt.verbose))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })
			service := newListService(t, &calls, listFixture{})
			if _, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"}); err != nil {
				t.Fatal(err)
			}
			if tt.wantLog == "" && stderr.Len() != 0 || tt.wantLog != "" && !strings.Contains(stderr.String(), tt.wantLog) || stdout.Len() != 0 {
				t.Fatalf("output = stdout %q stderr %q, want log %q", stdout.String(), stderr.String(), tt.wantLog)
			}
		})
	}
}
