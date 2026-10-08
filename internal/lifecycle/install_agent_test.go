package lifecycle

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

func TestInstallAgentStateTable(t *testing.T) {
	target := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v2.0.0"}
	previous := domain.PowerRef{Name: "demo", Type: domain.PowerTypeExpert, Version: "v1.0.0"}

	tests := []struct {
		name       string
		installed  *domain.PowerRef
		presence   transaction.TargetPresence
		want       cli.InstallOutcome
		wantErr    error
		wantEvents []string
		forbidden  []string
		wantWarn   bool
	}{
		{
			name: "new absent installs",
			want: cli.InstallOutcomeInstalled,
			wantEvents: []string{
				"config:find:bibit", "transaction:inspect-target:bibit:expert",
				"cache:prepare-agent:bibit", "transaction:install-agent:bibit",
			},
		},
		{
			name:     "new present conflicts",
			presence: transaction.TargetPresent,
			wantErr:  adapter.ErrTargetConflict,
			wantEvents: []string{
				"config:find:bibit", "transaction:inspect-target:bibit:expert",
			},
			forbidden: []string{"cache:prepare-agent:bibit", "transaction:install-agent:bibit"},
		},
		{
			name:      "same version returns unchanged with zero probe",
			installed: &target,
			want:      cli.InstallOutcomeUnchanged,
			forbidden: []string{
				"transaction:inspect-target:bibit:expert", "cache:prepare-agent:bibit",
				"transaction:install-agent:bibit", "transaction:uninstall-agent:bibit",
			},
		},
		{
			name:      "different present upgrades",
			installed: &previous,
			presence:  transaction.TargetPresent,
			want:      cli.InstallOutcomeUpgraded,
			wantEvents: []string{
				"config:find:bibit", "transaction:inspect-target:bibit:expert",
				"cache:prepare-agent:bibit", "transaction:install-agent:bibit",
			},
			forbidden: []string{"transaction:uninstall-agent:bibit"},
		},
		{
			name:      "different missing repairs and installs",
			installed: &previous,
			want:      cli.InstallOutcomeInstalled,
			wantEvents: []string{
				"config:find:bibit", "transaction:inspect-target:bibit:expert",
				"transaction:uninstall-agent:bibit", "cache:prepare-agent:bibit", "transaction:install-agent:bibit",
			},
			wantWarn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var stdout, stderr bytes.Buffer
			originalLogger := slog.Default()
			slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
			t.Cleanup(func() { slog.SetDefault(originalLogger) })

			fixture := installFixture{
				snapshot:  domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentBibit}},
				cloudRefs: []domain.PowerRef{target},
				selected:  target.Version,
			}
			if tt.installed != nil {
				fixture.installed = map[domain.AgentID]domain.PowerRef{domain.AgentBibit: *tt.installed}
			}
			if tt.presence != "" {
				fixture.targetPresence = map[domain.AgentID]transaction.TargetPresence{domain.AgentBibit: tt.presence}
			}
			service := newInstallService(t, &calls, fixture)

			result, err := service.Install(context.Background(), cli.Flags{Agent: "bibit", Type: "expert", Name: "demo"})

			if tt.wantErr != nil {
				assertInstallBatchError(t, err, domain.AgentBibit, tt.wantErr)
				if len(result.Items) != 1 || result.Items[0].Outcome != cli.InstallOutcomeFailed {
					t.Fatalf("Install().Items = %#v, want single failed item", result.Items)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertInstallItems(t, result, []cli.InstallItem{{Agent: domain.AgentBibit, Power: target, Outcome: tt.want}})
			}
			if tt.wantEvents != nil {
				assertEventSubsequence(t, calls, tt.wantEvents)
			}
			for _, forbidden := range tt.forbidden {
				if containsEvent(calls, forbidden) {
					t.Fatalf("calls = %v, must not contain %q", calls, forbidden)
				}
			}
			if tt.wantWarn {
				if strings.Count(stderr.String(), "level=WARN") != 1 {
					t.Fatalf("stderr = %q, want exactly one warning", stderr.String())
				}
				for _, want := range []string{
					`msg="installed power target is missing"`, "operation=install", "agent=bibit",
					"type=expert", "power=demo", "version=v1.0.0",
				} {
					if !strings.Contains(stderr.String(), want) {
						t.Fatalf("stderr = %q, want %q", stderr.String(), want)
					}
				}
			}
		})
	}
}

func TestInstallPluginDispatchesThroughPluginAdapterOnly(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		snapshot:   domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		cloudRefs:  []domain.PowerRef{ref},
		selected:   ref.Version,
		pluginFact: adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
		pluginMode: domain.PluginDeploymentMarketplace,
	})

	result, err := service.Install(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo"})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	assertInstallItems(t, result, []cli.InstallItem{{Agent: domain.AgentCodex, Power: ref, Outcome: cli.InstallOutcomeInstalled}})
	if containsEvent(calls, "resolve:codex") || containsEvent(calls, "transaction:install:codex") {
		t.Fatalf("calls = %v, want no Skill adapter or filesystem transaction", calls)
	}
}
