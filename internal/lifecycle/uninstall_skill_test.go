package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestUninstallPostResolutionFailuresReturnFullPowerFailedItems(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*uninstallFixture, error)
	}{
		{name: "Adapter Resolve", configure: func(f *uninstallFixture, cause error) {
			f.resolveErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
		{name: "Transaction Uninstall", configure: func(f *uninstallFixture, cause error) {
			f.transactionErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New(tt.name)
			fixture := uninstallTargetFixture()
			tt.configure(&fixture, cause)
			var calls []string
			service := newUninstallService(t, &calls, fixture)

			result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))
			assertUninstallItems(t, result, []cli.UninstallItem{{
				Agent: domain.AgentCodex, Power: powerPointer(uninstallTarget()), Outcome: cli.UninstallOutcomeFailed,
			}})
			assertUninstallBatchError(t, err, domain.AgentCodex, cause)
			if !containsEvent(calls, "release") {
				t.Fatalf("calls = %v, want release", calls)
			}
		})
	}
}

func TestUninstallMissingTargetWarnsSynchronouslyAndCompletes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	uninstallTestLogger(t, &stdout, &stderr, false)
	fixture := uninstallTargetFixture()
	fixture.transactionResults = map[domain.AgentID]transaction.UninstallResult{
		domain.AgentCodex: {TargetMissing: true},
	}
	fixture.beforeRelease = func() {
		assertContainsAll(t, stderr.String(), []string{
			`msg="installed power target is missing"`, "operation=uninstall",
			"agent=codex", "power=demo", "type=skill", "version=v1.2.3",
		})
	}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))
	if err != nil {
		t.Fatal(err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{{
		Agent: domain.AgentCodex, Power: powerPointer(uninstallTarget()), Outcome: cli.UninstallOutcomeCompleted,
	}})
	if strings.Count(stderr.String(), "level=WARN") != 1 {
		t.Fatalf("stderr = %q, want exactly one Warn", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestUninstallAdapterResolutionFailureKeepsResolvedPowerInFailedItem(t *testing.T) {
	cause := errors.New("adapter unavailable")
	fixture := uninstallTargetFixture()
	fixture.resolveErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))

	assertUninstallItems(t, result, []cli.UninstallItem{{
		Agent: domain.AgentCodex, Power: powerPointer(uninstallTarget()), Outcome: cli.UninstallOutcomeFailed,
	}})
	assertUninstallBatchError(t, err, domain.AgentCodex, cause)
	if containsEvent(calls, "transaction:uninstall:codex") {
		t.Fatalf("calls = %v, want no transaction after adapter resolution failure", calls)
	}
}

func TestUninstallTransactionFailureSuppressesLaterAgentAndKeepsResolvedPower(t *testing.T) {
	cause := errors.New("manifest removal failed")
	ref := uninstallTarget()
	later := domain.AgentClaudeCode
	fixture := uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex, later}},
		groups: map[domain.AgentID]domain.InstalledPowerGroups{
			domain.AgentCodex: {domain.PowerTypeSkill: {domain.InstalledPower{Ref: ref}}},
			later:             {domain.PowerTypeSkill: {domain.InstalledPower{Ref: ref}}},
		},
		transactionErrors: map[domain.AgentID]error{domain.AgentCodex: cause},
	}
	fixture.onEvent = rejectLaterUninstallEvents(t, []domain.AgentID{later})
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Type: "skill", Name: "demo"})

	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentCodex, Power: powerPointer(ref), Outcome: cli.UninstallOutcomeFailed},
		{Agent: later, Outcome: cli.UninstallOutcomeUnexecuted},
	})
	assertUninstallBatchError(t, err, domain.AgentCodex, cause)
}

func TestUninstallMissingTargetFactWarnsBeforeReleaseWhenTransactionAlsoFails(t *testing.T) {
	transactionCause := errors.New("manifest removal failed")
	var stdout, stderr bytes.Buffer
	uninstallTestLogger(t, &stdout, &stderr, false)
	fixture := uninstallTargetFixture()
	fixture.transactionResults = map[domain.AgentID]transaction.UninstallResult{
		domain.AgentCodex: {TargetMissing: true},
	}
	fixture.transactionErrors = map[domain.AgentID]error{domain.AgentCodex: transactionCause}
	fixture.beforeRelease = func() {
		assertContainsAll(t, stderr.String(), []string{
			`msg="installed power target is missing"`, "operation=uninstall",
			"agent=codex", "power=demo", "type=skill", "version=v1.2.3",
		})
		if strings.Count(stderr.String(), "level=WARN") != 1 {
			t.Fatalf("stderr = %q, want exactly one target-missing Warn before Release", stderr.String())
		}
	}
	var calls []string
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), uninstallTargetFlags("codex"))

	assertUninstallItems(t, result, []cli.UninstallItem{{
		Agent: domain.AgentCodex, Power: powerPointer(uninstallTarget()), Outcome: cli.UninstallOutcomeFailed,
	}})
	assertUninstallBatchError(t, err, domain.AgentCodex, transactionCause)
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
