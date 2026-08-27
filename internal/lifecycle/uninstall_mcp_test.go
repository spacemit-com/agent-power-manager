package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestUninstallMCPPresentUsesOnlyLocalStateAndTransaction(t *testing.T) {
	var calls []string
	fixture := uninstallMCPFixture(adapter.MCPPresent)
	service := newUninstallService(t, &calls, fixture)

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"acquire", "recover", "refresh", "config:codex", "resolve-mcp:codex", "mcp:inspect:codex",
		"transaction:uninstall-mcp:codex", "release",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Power: powerPointer(uninstallMCPRef()), Outcome: cli.UninstallOutcomeCompleted}})
}

func TestUninstallMCPAbsentUsesOnlyLocalStateAndWarns(t *testing.T) {
	var stdout, stderr bytes.Buffer
	uninstallTestLogger(t, &stdout, &stderr, false)
	var calls []string
	fixture := uninstallMCPFixture(adapter.MCPAbsent)
	fixture.transactionMCPResults = map[domain.AgentID]transaction.UninstallResult{domain.AgentCodex: {TargetMissing: true}}
	fixture.beforeRelease = func() {
		if !strings.Contains(stderr.String(), `msg="installed power target is missing"`) {
			t.Fatalf("stderr before release = %q", stderr.String())
		}
	}
	service := newUninstallService(t, &calls, fixture)
	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Power: powerPointer(uninstallMCPRef()), Outcome: cli.UninstallOutcomeCompleted}})
	want := []string{"acquire", "recover", "refresh", "config:codex", "resolve-mcp:codex", "mcp:inspect:codex", "transaction:uninstall-mcp:codex", "release"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestUninstallMCPMissingManifestStopsBeforeAdapterCloudAndTransaction(t *testing.T) {
	var calls []string
	fixture := uninstallMCPFixture(adapter.MCPPresent)
	fixture.groups = nil
	service := newUninstallService(t, &calls, fixture)
	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted}})
	want := []string{"acquire", "recover", "refresh", "config:codex", "release"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestUninstallMCPFailuresKeepFullRefAndStopBeforeTransaction(t *testing.T) {
	cause := errors.New("inspect")
	fixture := uninstallMCPFixture(adapter.MCPPresent)
	fixture.mcpInspectErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
	var calls []string
	service := newUninstallService(t, &calls, fixture)
	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
	assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Power: powerPointer(uninstallMCPRef()), Outcome: cli.UninstallOutcomeFailed}})
	assertUninstallBatchError(t, err, domain.AgentCodex, cause)
	if containsEvent(calls, "transaction:uninstall-mcp:codex") {
		t.Fatalf("calls = %v, want no transaction", calls)
	}
}

func TestUninstallMCPMissingFactWithErrorWarnsBeforeRelease(t *testing.T) {
	var stdout, stderr bytes.Buffer
	uninstallTestLogger(t, &stdout, &stderr, false)
	cause := errors.New("manifest failed")
	fixture := uninstallMCPFixture(adapter.MCPAbsent)
	fixture.transactionMCPResults = map[domain.AgentID]transaction.UninstallResult{domain.AgentCodex: {TargetMissing: true}}
	fixture.transactionMCPErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
	fixture.beforeRelease = func() {
		if !strings.Contains(stderr.String(), `msg="installed power target is missing"`) {
			t.Fatalf("stderr before release = %q", stderr.String())
		}
	}
	var calls []string
	service := newUninstallService(t, &calls, fixture)
	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
	assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Power: powerPointer(uninstallMCPRef()), Outcome: cli.UninstallOutcomeFailed}})
	assertUninstallBatchError(t, err, domain.AgentCodex, cause)
}

func uninstallMCPFixture(presence adapter.MCPPresence) uninstallFixture {
	ref := uninstallMCPRef()
	return uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups: map[domain.AgentID]domain.InstalledPowerGroups{
			domain.AgentCodex: {domain.PowerTypeMCP: {domain.InstalledPower{Ref: ref}}},
		},
		mcpPresence: map[domain.AgentID]adapter.MCPPresence{domain.AgentCodex: presence},
	}
}

func uninstallMCPRef() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.2.3"}
}
