package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestUninstallConfigFailureReturnsAgentOnlyFailedAndLaterUnexecutedItems(t *testing.T) {
	cause := errors.New("read manifest")
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot:     domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		configErrors: map[domain.AgentID]error{domain.AgentClaudeCode: cause},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})
	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Outcome: cli.UninstallOutcomeFailed},
		{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted},
	})
	assertUninstallBatchError(t, err, domain.AgentClaudeCode, cause)
	wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

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

func TestUninstallMissingRecordWarnsSynchronouslyWithOptionalType(t *testing.T) {
	tests := []struct {
		name     string
		flags    cli.Flags
		wantType string
	}{
		{name: "without type", flags: cli.Flags{Agent: "codex", Name: "Demo"}},
		{name: "with type", flags: cli.Flags{Agent: "codex", Type: "plugin", Name: "Demo"}, wantType: "type=plugin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			installTestLogger(t, &stdout, &stderr, false)
			fixture := uninstallFixture{snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}}
			fixture.beforeRelease = func() {
				assertContainsAll(t, stderr.String(), []string{
					`msg="power is not installed"`, "operation=uninstall", "agent=codex", "power=Demo",
				})
			}
			var calls []string
			service := newUninstallService(t, &calls, fixture)

			result, err := service.Uninstall(context.Background(), tt.flags)
			if err != nil {
				t.Fatal(err)
			}
			assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted}})
			if strings.Count(stderr.String(), "level=WARN") != 1 {
				t.Fatalf("stderr = %q, want exactly one Warn", stderr.String())
			}
			if tt.wantType == "" && strings.Contains(stderr.String(), "type=") {
				t.Fatalf("stderr = %q, want no type attribute", stderr.String())
			}
			if tt.wantType != "" && !strings.Contains(stderr.String(), tt.wantType) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantType)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestUninstallMissingTargetWarnsSynchronouslyAndCompletes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	installTestLogger(t, &stdout, &stderr, false)
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
		groups: map[domain.AgentID]domain.PowerGroups{
			domain.AgentCodex: {domain.PowerTypeSkill: {ref}},
			later:             {domain.PowerTypeSkill: {ref}},
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
	installTestLogger(t, &stdout, &stderr, false)
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
