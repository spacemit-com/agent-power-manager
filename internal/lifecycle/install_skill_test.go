package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestInstallFirstInstallOrdersDependenciesAndReturnsInstalled(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}},
		selected:  "v1.0.0",
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"acquire", "recover", "refresh", "cloud:resolve",
		"resolve:codex", "config:find:codex", "cache:prepare-skill:codex",
		"transaction:install:codex", "release",
	}
	assertEventSubsequence(t, calls, wantEvents)
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		Outcome: cli.InstallOutcomeInstalled,
	}})
}

func TestInstallPreparesCacheBeforeTransaction(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}},
		selected:  "v1.0.0",
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"acquire", "recover", "refresh", "cloud:resolve", "resolve:codex", "config:find:codex",
		"cache:prepare-skill:codex", "transaction:install:codex", "release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		Outcome: cli.InstallOutcomeInstalled,
	}})
}

func TestInstallSameVersionReturnsUnchangedWithoutFetchOrTransaction(t *testing.T) {
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}},
		selected:  "v1.0.0",
		installed: map[domain.AgentID]domain.PowerRef{
			domain.AgentCodex: {Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		},
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{
		"acquire", "recover", "refresh", "cloud:resolve", "resolve:codex", "config:find:codex", "release",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		Outcome: cli.InstallOutcomeUnchanged,
	}})
}

func TestInstallClassifiesUpgradeAndDowngradeBySemVer(t *testing.T) {
	tests := []struct {
		name     string
		previous string
		target   string
		outcome  cli.InstallOutcome
	}{
		{name: "upgrade", previous: "v1.0.0", target: "v2.0.0", outcome: cli.InstallOutcomeUpgraded},
		{name: "downgrade", previous: "v3.0.0", target: "v2.0.0", outcome: cli.InstallOutcomeDowngraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			service := newInstallService(t, &calls, installFixture{
				snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
				cloudRefs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: tt.target}},
				selected:  tt.target,
				installed: map[domain.AgentID]domain.PowerRef{
					domain.AgentCodex: {Name: "demo", Type: domain.PowerTypeSkill, Version: tt.previous},
				},
			})

			result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			if !containsEvent(calls, "transaction:install:codex") {
				t.Fatalf("calls = %v, want install transaction", calls)
			}
			assertInstallItems(t, result, []cli.InstallItem{{
				Agent:   domain.AgentCodex,
				Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: tt.target},
				Outcome: tt.outcome,
			}})
		})
	}
}

func TestInstallConcreteFailureReturnsFailedItemAndMarkerAfterRelease(t *testing.T) {
	cause := errors.New("find installed Codex power")
	target := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:         domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs:        targetRefs(target),
		selected:         target.Version,
		configFindErrors: map[domain.AgentID]error{domain.AgentCodex: cause},
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "skill", Name: "demo"})
	if err == nil {
		t.Fatal("Install() error = nil, want failed Agent marker")
	}
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent: domain.AgentCodex, Power: target, Outcome: cli.InstallOutcomeFailed,
	}})
	assertInstallBatchError(t, err, domain.AgentCodex, cause)
	if !containsEvent(calls, "release") {
		t.Fatalf("calls = %v, want release", calls)
	}
}

func TestInstallAgentFailuresReturnFailedResultAndMarkerAfterRelease(t *testing.T) {
	tests := []struct {
		name      string
		cause     error
		configure func(*installFixture, error)
	}{
		{name: "Adapter Resolve", configure: func(f *installFixture, cause error) {
			f.resolveErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
		{name: "Config Find", configure: func(f *installFixture, cause error) {
			f.configFindErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
		{name: "Cache Prepare", configure: func(f *installFixture, cause error) {
			f.cacheResponses = map[domain.AgentID][]installCacheResponse{domain.AgentCodex: {{err: cause}}}
		}},
		{name: "Transaction Install", configure: func(f *installFixture, cause error) {
			f.transactionInstallErr = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
		{name: "Transaction Install stale manifest", cause: transaction.ErrStaleManifest, configure: func(f *installFixture, cause error) {
			f.transactionInstallErr = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := tt.cause
			if cause == nil {
				cause = errors.New(tt.name)
			}
			fixture := installTargetFixture()
			tt.configure(&fixture, cause)
			var calls []string
			service := newInstallService(t, &calls, fixture)

			result, err := service.Install(context.Background(), installTargetFlags("codex"))
			if err == nil {
				t.Fatal("Install() error = nil, want failed Agent marker")
			}
			assertInstallItems(t, result, []cli.InstallItem{{
				Agent: domain.AgentCodex, Power: installTarget(), Outcome: cli.InstallOutcomeFailed,
			}})
			assertInstallBatchError(t, err, domain.AgentCodex, cause)
			if !containsEvent(calls, "release") {
				t.Fatalf("calls = %v, want release", calls)
			}
		})
	}
}
