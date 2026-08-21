package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestInstallMCPFirstInstallOrdersCLIAndCloudWithoutCache(t *testing.T) {
	var calls []string
	server := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"}
	service := newInstallService(t, &calls, installMCPFixture("v1.0.0", map[domain.AgentID]adapter.MCPPresence{
		domain.AgentCodex: adapter.MCPAbsent,
	}, map[string]domain.MCPServer{"v1.0.0": server}))

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"acquire", "recover", "refresh", "cloud:resolve", "resolve-mcp:codex",
		"config:find:codex", "mcp:inspect:codex", "cloud:get-mcp:v1.0.0",
		"transaction:install-mcp:codex", "release",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent: domain.AgentCodex, Power: mcpRef("v1.0.0"), Outcome: cli.InstallOutcomeInstalled,
	}})
}

func TestInstallMCPStateTableAvoidsUnneededCloudDefinitions(t *testing.T) {
	tests := []struct {
		name      string
		installed *domain.PowerRef
		presence  adapter.MCPPresence
		wantErr   error
		outcome   cli.InstallOutcome
		wantTxn   bool
	}{
		{name: "same present unchanged", installed: mcpRefPointer("v2.0.0"), presence: adapter.MCPPresent, outcome: cli.InstallOutcomeUnchanged},
		{name: "unmanaged present conflict", presence: adapter.MCPPresent, wantErr: adapter.ErrMCPConflict},
		{name: "same absent stale", installed: mcpRefPointer("v2.0.0"), presence: adapter.MCPAbsent, wantErr: transaction.ErrStaleManifest, wantTxn: true},
		{name: "different absent stale", installed: mcpRefPointer("v1.0.0"), presence: adapter.MCPAbsent, wantErr: transaction.ErrStaleManifest, wantTxn: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			fixture := installMCPFixture("v2.0.0", map[domain.AgentID]adapter.MCPPresence{domain.AgentCodex: test.presence}, nil)
			if test.installed != nil {
				fixture.installed = map[domain.AgentID]domain.PowerRef{domain.AgentCodex: *test.installed}
			}
			if test.wantTxn {
				fixture.transactionMCPInstallErr = map[domain.AgentID]error{domain.AgentCodex: transaction.ErrStaleManifest}
			}
			service := newInstallService(t, &calls, fixture)
			result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
			if test.wantErr == nil {
				if err != nil {
					t.Fatal(err)
				}
				assertInstallItems(t, result, []cli.InstallItem{{Agent: domain.AgentCodex, Power: mcpRef("v2.0.0"), Outcome: test.outcome}})
			} else if !errors.Is(err, test.wantErr) {
				t.Fatalf("Install() error = %v, want %v", err, test.wantErr)
			}
			for _, call := range calls {
				if call == "cloud:get-mcp:v2.0.0" || call == "cloud:get-mcp:v1.0.0" || call == "cache:prepare-skill:codex" {
					t.Fatalf("calls = %v, want no MCP definition/cache call", calls)
				}
			}
			if containsEvent(calls, "transaction:install-mcp:codex") != test.wantTxn {
				t.Fatalf("calls = %v, transaction wanted %v", calls, test.wantTxn)
			}
		})
	}
}

func TestInstallMCPUpdateFetchesOldThenNewAndClassifies(t *testing.T) {
	tests := []struct {
		name     string
		previous string
		target   string
		outcome  cli.InstallOutcome
	}{
		{name: "upgrade", previous: "v1.0.0", target: "v2.0.0", outcome: cli.InstallOutcomeUpgraded},
		{name: "downgrade", previous: "v3.0.0", target: "v2.0.0", outcome: cli.InstallOutcomeDowngraded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			oldServer := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "old"}
			newServer := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"}
			fixture := installMCPFixture(test.target, map[domain.AgentID]adapter.MCPPresence{domain.AgentCodex: adapter.MCPPresent}, map[string]domain.MCPServer{
				test.previous: oldServer,
				test.target:   newServer,
			})
			fixture.installed = map[domain.AgentID]domain.PowerRef{domain.AgentCodex: mcpRef(test.previous)}
			service := newInstallService(t, &calls, fixture)
			result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			wantOrder := []string{"cloud:get-mcp:" + test.previous, "cloud:get-mcp:" + test.target, "transaction:install-mcp:codex"}
			assertEventSubsequence(t, calls, wantOrder)
			assertInstallItems(t, result, []cli.InstallItem{{Agent: domain.AgentCodex, Power: mcpRef(test.target), Outcome: test.outcome}})
		})
	}
}

func TestInstallMCPDefinitionAndInspectFailuresStopBeforeTransaction(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*installFixture, error)
	}{
		{name: "inspect", configure: func(f *installFixture, cause error) {
			f.mcpInspectErrors = map[domain.AgentID]error{domain.AgentCodex: cause}
		}},
		{name: "old definition", configure: func(f *installFixture, cause error) { f.mcpGetErrors = map[string]error{"v1.0.0": cause} }},
		{name: "new definition", configure: func(f *installFixture, cause error) { f.mcpGetErrors = map[string]error{"v2.0.0": cause} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			cause := errors.New(test.name)
			fixture := installMCPFixture("v2.0.0", map[domain.AgentID]adapter.MCPPresence{domain.AgentCodex: adapter.MCPPresent}, map[string]domain.MCPServer{
				"v1.0.0": {Transport: domain.MCPTransportStdio, Command: "old"},
				"v2.0.0": {Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"},
			})
			fixture.installed = map[domain.AgentID]domain.PowerRef{domain.AgentCodex: mcpRef("v1.0.0")}
			test.configure(&fixture, cause)
			service := newInstallService(t, &calls, fixture)
			_, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})
			if !errors.Is(err, cause) || containsEvent(calls, "transaction:install-mcp:codex") {
				t.Fatalf("error/calls = %v/%v", err, calls)
			}
		})
	}
}

func installMCPFixture(version string, presence map[domain.AgentID]adapter.MCPPresence, definitions map[string]domain.MCPServer) installFixture {
	return installFixture{
		snapshot:       domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs:      []domain.PowerRef{mcpRef(version)},
		selected:       version,
		mcpPresence:    presence,
		mcpDefinitions: definitions,
	}
}

func mcpRef(version string) domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: version}
}

func mcpRefPointer(version string) *domain.PowerRef {
	ref := mcpRef(version)
	return &ref
}
