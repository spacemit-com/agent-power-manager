package lifecycle

import (
	"testing"

	"apm/internal/adapter"
	"apm/internal/pluginresource"
)

func TestClassifyMarketplacePluginTarget(t *testing.T) {
	expected := pluginresource.Marketplace{Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"}
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
		cacheExists     bool
		want            targetState
	}{
		{
			name:            "both absent",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			want:            targetAbsent,
		},
		{
			name:            "both complete",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
			cacheExists:     true,
			want:            targetComplete,
		},
		{
			name:            "both complete but cache missing",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
			want:            targetIncomplete,
		},
		{
			name:            "marketplace only",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			want:            targetIncomplete,
		},
		{
			name:            "plugin only",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
			want:            targetIncomplete,
		},
		{
			name:            "wrong source",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/other"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
			cacheExists:     true,
			want:            targetConflict,
		},
		{
			name:            "wrong selector",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@foreign"},
			cacheExists:     true,
			want:            targetConflict,
		},
		{
			name:            "wrong absent marketplace name",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-other"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			want:            targetConflict,
		},
		{
			name:            "wrong absent plugin name",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "other"},
			want:            targetConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := classifyMarketplacePluginTarget("demo", expected, test.marketplaceFact, test.pluginFact, test.cacheExists)
			if facts.state != test.want {
				t.Fatalf("classifyMarketplacePluginTarget() state = %v, want %v", facts.state, test.want)
			}
			if facts.marketplace == nil {
				t.Fatal("classifyMarketplacePluginTarget() marketplace fact = nil")
			}
		})
	}
}

func TestClassifyFilesystemPluginTarget(t *testing.T) {
	tests := []struct {
		name     string
		fact     adapter.PluginFact
		physical filesystemPluginPhysicalState
		want     targetState
	}{
		{
			name:     "absent and missing",
			fact:     adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			physical: filesystemPhysicalAbsent,
			want:     targetAbsent,
		},
		{
			name:     "absent but directory exists",
			fact:     adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			physical: filesystemPhysicalDirectory,
			want:     targetConflict,
		},
		{
			name:     "absent but unsafe node exists",
			fact:     adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
			physical: filesystemPhysicalUnsafe,
			want:     targetConflict,
		},
		{
			name:     "present and directory",
			fact:     adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
			physical: filesystemPhysicalDirectory,
			want:     targetComplete,
		},
		{
			name:     "present but directory missing",
			fact:     adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
			physical: filesystemPhysicalAbsent,
			want:     targetIncomplete,
		},
		{
			name:     "present with foreign selector",
			fact:     adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@foreign"},
			physical: filesystemPhysicalDirectory,
			want:     targetConflict,
		},
		{
			name:     "absent with wrong name",
			fact:     adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "other"},
			physical: filesystemPhysicalAbsent,
			want:     targetConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := classifyFilesystemPluginTarget("demo", test.fact, test.physical)
			if facts.state != test.want {
				t.Fatalf("classifyFilesystemPluginTarget() state = %v, want %v", facts.state, test.want)
			}
			if facts.marketplace != nil {
				t.Fatal("classifyFilesystemPluginTarget() marketplace fact = non-nil")
			}
		})
	}
}
