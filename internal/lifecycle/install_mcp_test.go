package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
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
		"acquire", "recover", "refresh", "catalog:snapshot-locked", "config:find:codex",
		"resolve-mcp:codex", "mcp:inspect:codex", "cloud:get-mcp:v1.0.0",
		"transaction:install-mcp:codex", "release",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	assertInstallItems(t, result, []cli.InstallItem{{
		Agent: domain.AgentCodex, Power: mcpRef("v1.0.0"), Outcome: cli.InstallOutcomeInstalled,
	}})
}

func TestInstallMCPStateTable(t *testing.T) {
	target := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v2.0.0"}
	previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}
	oldServer := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "old"}
	newServer := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"}

	tests := []struct {
		name       string
		installed  *domain.PowerRef
		presence   adapter.MCPPresence
		want       cli.InstallOutcome
		wantErr    error
		wantEvents []string
		forbidden  []string
		wantWarn   bool
		check      func(*testing.T, []transaction.MCPInstallRequest, []transaction.MCPUninstallRequest)
	}{
		{
			name: "new absent installs",
			want: cli.InstallOutcomeInstalled,
			check: func(t *testing.T, installs []transaction.MCPInstallRequest, _ []transaction.MCPUninstallRequest) {
				if len(installs) != 1 || installs[0].Previous != nil {
					t.Fatalf("MCP install requests = %+v, want single first-install with nil Previous", installs)
				}
			},
			wantEvents: []string{
				"config:find:codex", "resolve-mcp:codex", "mcp:inspect:codex",
				"cloud:get-mcp:v2.0.0", "transaction:install-mcp:codex",
			},
		},
		{
			name:     "new present conflicts",
			presence: adapter.MCPPresent,
			wantErr:  adapter.ErrTargetConflict,
			wantEvents: []string{
				"config:find:codex", "resolve-mcp:codex", "mcp:inspect:codex",
			},
			forbidden: []string{"cloud:get-mcp:v2.0.0", "transaction:install-mcp:codex", "transaction:uninstall-mcp:codex"},
		},
		{
			name:      "same absent unchanged",
			installed: &target,
			presence:  adapter.MCPAbsent,
			want:      cli.InstallOutcomeUnchanged,
			wantEvents: []string{
				"config:find:codex",
			},
			forbidden: []string{
				"resolve-mcp:codex", "mcp:inspect:codex", "cloud:get-mcp:v1.0.0", "cloud:get-mcp:v2.0.0",
				"transaction:install-mcp:codex", "transaction:uninstall-mcp:codex",
			},
		},
		{
			name:      "same present unchanged",
			installed: &target,
			presence:  adapter.MCPPresent,
			want:      cli.InstallOutcomeUnchanged,
			wantEvents: []string{
				"config:find:codex",
			},
			forbidden: []string{
				"resolve-mcp:codex", "mcp:inspect:codex", "cloud:get-mcp:v1.0.0", "cloud:get-mcp:v2.0.0",
				"transaction:install-mcp:codex", "transaction:uninstall-mcp:codex",
			},
		},
		{
			name:      "different present replaces",
			installed: &previous,
			presence:  adapter.MCPPresent,
			want:      cli.InstallOutcomeUpgraded,
			wantEvents: []string{
				"config:find:codex", "resolve-mcp:codex", "mcp:inspect:codex",
				"cloud:get-mcp:v1.0.0", "cloud:get-mcp:v2.0.0", "transaction:install-mcp:codex",
			},
			forbidden: []string{"transaction:uninstall-mcp:codex"},
		},
		{
			name:      "different absent repairs and installs",
			installed: &previous,
			presence:  adapter.MCPAbsent,
			want:      cli.InstallOutcomeInstalled,
			wantEvents: []string{
				"config:find:codex", "resolve-mcp:codex", "mcp:inspect:codex",
				"transaction:uninstall-mcp:codex", "cloud:get-mcp:v2.0.0", "transaction:install-mcp:codex",
			},
			forbidden: []string{"cloud:get-mcp:v1.0.0"},
			wantWarn:  true,
			check: func(t *testing.T, installs []transaction.MCPInstallRequest, uninstalls []transaction.MCPUninstallRequest) {
				if len(installs) != 1 || installs[0].Previous != nil {
					t.Fatalf("MCP install requests = %+v, want single first-install with nil Previous", installs)
				}
				if len(uninstalls) != 1 || uninstalls[0].Power != previous {
					t.Fatalf("MCP uninstall requests = %+v, want single repair of %+v", uninstalls, previous)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var installRequests []transaction.MCPInstallRequest
			var uninstallRequests []transaction.MCPUninstallRequest
			var stdout, stderr bytes.Buffer
			originalLogger := slog.Default()
			slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })

			fixture := installFixture{
				snapshot:                domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
				cloudRefs:               []domain.PowerRef{target},
				selected:                target.Version,
				mcpPresence:             map[domain.AgentID]adapter.MCPPresence{domain.AgentCodex: tt.presence},
				mcpDefinitions:          map[string]domain.MCPServer{"v1.0.0": oldServer, "v2.0.0": newServer},
				mcpRequestSink:          &installRequests,
				mcpUninstallRequestSink: &uninstallRequests,
			}
			if tt.installed != nil {
				fixture.installed = map[domain.AgentID]domain.PowerRef{domain.AgentCodex: *tt.installed}
			}
			service := newInstallService(t, &calls, fixture)

			result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "mcp", Name: "demo"})

			if tt.wantErr != nil {
				assertInstallBatchError(t, err, domain.AgentCodex, tt.wantErr)
				if errors.Is(err, adapter.ErrMCPConflict) {
					t.Fatalf("Install() error = %v, must not wrap ErrMCPConflict", err)
				}
				if len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
					t.Fatalf("Install().Items = %#v, want single failed item", result.Items)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertInstallItems(t, result, []cli.InstallItem{{Agent: domain.AgentCodex, Power: target, Outcome: tt.want}})
			}
			if tt.wantEvents != nil {
				assertEventSubsequence(t, calls, tt.wantEvents)
			}
			for _, forbidden := range tt.forbidden {
				if containsEvent(calls, forbidden) {
					t.Fatalf("calls = %v, must not contain %q", calls, forbidden)
				}
			}
			if tt.check != nil {
				tt.check(t, installRequests, uninstallRequests)
			}
			if tt.wantWarn {
				if strings.Count(stderr.String(), "level=WARN") != 1 {
					t.Fatalf("stderr = %q, want exactly one warning", stderr.String())
				}
				for _, want := range []string{
					`msg="installed power target is missing"`, "operation=install", "agent=codex",
					"type=mcp", "power=demo", "version=v1.0.0",
				} {
					if !strings.Contains(stderr.String(), want) {
						t.Fatalf("stderr = %q, want %q", stderr.String(), want)
					}
				}
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
