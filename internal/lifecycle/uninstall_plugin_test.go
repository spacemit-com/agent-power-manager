package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestUninstallPluginWithoutManifestSkipsAllPluginDependencies(t *testing.T) {
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups:   map[domain.AgentID]domain.PowerGroups{domain.AgentCodex: {}},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo"})
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeUnexecuted {
		t.Fatalf("Uninstall() = %+v, want manifest-missing unexecuted", result)
	}
	want := []string{"acquire", "recover", "refresh", "config:codex", "release"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want manifest-only %v", calls, want)
	}
}

func TestUninstallPluginMarketplaceDelegatesCleanupForNonConflictStates(t *testing.T) {
	ref := pluginRef("v1.0.0")
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
		setExists       bool
		exists          bool
		wantMissing     bool
	}{
		{
			name:            "both absent",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			wantMissing:     true,
		},
		{
			name:            "marketplace only",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			wantMissing:     true,
		},
		{
			name:            "plugin only",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
			wantMissing:     true,
		},
		{
			name:            "both complete",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
			wantMissing:     false,
		},
		{
			name:            "missing cache does not block",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
			setExists:       true,
			exists:          false,
			wantMissing:     false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			resources := &fakePluginResources{}
			if test.setExists {
				resources.exists = map[string]bool{pluginArtifactKey(ref): test.exists}
			}
			service := newUninstallService(t, &calls, uninstallFixture{
				pluginMode:         domain.PluginDeploymentMarketplace,
				marketplaceFacts:   map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:        map[string]adapter.PluginFact{"demo": test.pluginFact},
				pluginResources:    resources,
				transactionResults: map[domain.AgentID]transaction.UninstallResult{domain.AgentCodex: {TargetMissing: test.wantMissing}},
			})
			result, err := service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
			if err != nil || result.TargetMissing != test.wantMissing {
				t.Fatalf("uninstallPlugin() = %+v, %v; want TargetMissing=%t", result, err, test.wantMissing)
			}
			if !containsEvent(calls, "transaction:uninstall-plugin:codex") {
				t.Fatalf("calls = %v, want delegated transaction", calls)
			}
			if containsEvent(calls, "config:remove:codex:plugin:demo") {
				t.Fatalf("calls = %v, manifest removal must be delegated to transaction", calls)
			}
			if resources.lookupCalls != 0 || resources.ensureCalls != 0 {
				t.Fatalf("resource calls lookup/ensure = %d/%d, want 0/0", resources.lookupCalls, resources.ensureCalls)
			}
		})
	}
}

func TestUninstallPluginMarketplaceExactManagedStateUsesLocator(t *testing.T) {
	var calls []string
	var requests []transaction.PluginUninstallRequest
	ref := pluginRef("v1.0.0")
	service := newUninstallService(t, &calls, uninstallFixture{
		pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
		},
		pluginFacts: map[string]adapter.PluginFact{
			"demo": {Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
		},
		pluginRequestSink: &requests,
	})

	result, err := service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
	if err != nil || result.TargetMissing {
		t.Fatalf("uninstallPlugin() = %+v, %v, want transaction", result, err)
	}
	want := []string{
		"resolve-plugin:codex", "resource:locate:v1.0.0", "marketplace:inspect:codex:apm-demo", "plugin:inspect:codex:demo",
		"resource:exists:v1.0.0", "transaction:uninstall-plugin:codex",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if len(requests) != 1 || requests[0].Target != (adapter.PluginTarget{ActualName: "demo", Selector: "demo"}) || requests[0].Marketplace == nil || requests[0].Marketplace.Source != "/cache/plugins/demo/v1.0.0" {
		t.Fatalf("requests = %+v, want exact located marketplace", requests)
	}
}

func TestUninstallPluginMarketplaceRejectsConflict(t *testing.T) {
	ref := pluginRef("v1.0.0")
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
	}{
		{
			name:            "wrong source",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/other"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
		},
		{
			name:            "wrong selector",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@foreign"},
		},
		{
			name:            "wrong absent marketplace name",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-other"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			service := newUninstallService(t, &calls, uninstallFixture{
				pluginMode:       domain.PluginDeploymentMarketplace,
				marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:      map[string]adapter.PluginFact{"demo": test.pluginFact},
			})
			_, err := service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
			if !errors.Is(err, transaction.ErrIndeterminateState) {
				t.Fatalf("uninstallPlugin() error = %v, want ErrIndeterminateState", err)
			}
			if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:uninstall-plugin:codex") {
				t.Fatalf("calls = %v, conflict must preserve manifest", calls)
			}
		})
	}
}

func TestUninstallPluginUnknownCollectionFactRetainsManifestWithoutMutation(t *testing.T) {
	// 捕获错误变更：malformed Plugin list 的 unknown 状态被当成 absent 并删除已安装 manifest。
	ref := pluginRef("v1.0.0")
	actualName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
		},
		pluginInspectErrors: map[string]error{actualName: adapter.ErrPluginStateUnknown},
	})

	_, err = service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
	if !errors.Is(err, adapter.ErrPluginStateUnknown) {
		t.Fatalf("uninstallPlugin() error = %v, want ErrPluginStateUnknown", err)
	}
	if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:uninstall-plugin:codex") {
		t.Fatalf("calls = %v, unknown fact must retain manifest and Agent state", calls)
	}
}

func TestUninstallFilesystemPluginUsesFactsOfflineForBibitAndHermes(t *testing.T) {
	ref := pluginRef("v1.0.0")
	tests := []struct {
		name        string
		fact        adapter.PluginFact
		createDir   bool
		wantMissing bool
	}{
		{name: "absent delegates cleanup", fact: adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"}, wantMissing: true},
		{name: "present uninstalls", fact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"}, createDir: true, wantMissing: false},
	}
	for _, agent := range []domain.AgentID{domain.AgentBibit, domain.AgentHermes} {
		for _, test := range tests {
			t.Run(string(agent)+"/"+test.name, func(t *testing.T) {
				var calls []string
				resources := &fakePluginResources{}
				target := filepath.Join(t.TempDir(), "demo")
				if test.createDir {
					if err := os.MkdirAll(target, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				service := newUninstallService(t, &calls, uninstallFixture{
					pluginMode:         domain.PluginDeploymentFilesystem,
					pluginFacts:        map[string]adapter.PluginFact{"demo": test.fact},
					pluginTargets:      map[string]string{"demo": target},
					pluginResources:    resources,
					transactionResults: map[domain.AgentID]transaction.UninstallResult{agent: {TargetMissing: test.wantMissing}},
				})
				result, err := service.uninstallPlugin(context.Background(), agent, ref)
				if err != nil || result.TargetMissing != test.wantMissing {
					t.Fatalf("uninstallPlugin() = %+v, %v; want TargetMissing=%t", result, err, test.wantMissing)
				}
				if !containsEvent(calls, "transaction:uninstall-plugin:"+string(agent)) || resources.lookupCalls != 0 || resources.ensureCalls != 0 {
					t.Fatalf("calls = %v, lookup/ensure=%d/%d", calls, resources.lookupCalls, resources.ensureCalls)
				}
			})
		}
	}
}

func TestUninstallFilesystemPluginAbsentFactValidatesPhysicalManagedTargetBeforeManifestCleanup(t *testing.T) {
	ref := pluginRef("v1.0.0")
	tests := []struct {
		name    string
		create  func(*testing.T, string)
		wantErr error
	}{
		{name: "physical directory", create: func(t *testing.T, target string) {
			t.Helper()
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
		}, wantErr: transaction.ErrIndeterminateState},
		{name: "symlink", create: func(t *testing.T, target string) {
			t.Helper()
			if err := os.Symlink(t.TempDir(), target); err != nil {
				t.Fatal(err)
			}
		}, wantErr: transaction.ErrUnsafePath},
		{name: "regular file", create: func(t *testing.T, target string) {
			t.Helper()
			if err := os.WriteFile(target, []byte("foreign"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, wantErr: transaction.ErrUnsafePath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "demo")
			test.create(t, target)
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			var calls []string
			service := newUninstallService(t, &calls, uninstallFixture{
				pluginMode:    domain.PluginDeploymentFilesystem,
				pluginFacts:   map[string]adapter.PluginFact{"demo": {Presence: adapter.PluginAbsent, ActualName: "demo"}},
				pluginTargets: map[string]string{"demo": target},
			})

			result, err := service.uninstallPlugin(context.Background(), domain.AgentHermes, ref)
			if !errors.Is(err, test.wantErr) || result.TargetMissing {
				t.Fatalf("uninstallPlugin() = %+v, %v; want %v without cleanup", result, err, test.wantErr)
			}
			after, statErr := os.Lstat(target)
			if statErr != nil || after.Mode() != before.Mode() {
				t.Fatalf("managed target changed: before=%v after=%v err=%v", before.Mode(), after, statErr)
			}
			for _, forbidden := range []string{"config:remove:hermes-agent:plugin:demo", "transaction:uninstall-plugin:hermes-agent"} {
				if containsEvent(calls, forbidden) {
					t.Fatalf("calls = %v, want no mutation %q", calls, forbidden)
				}
			}
		})
	}
}
