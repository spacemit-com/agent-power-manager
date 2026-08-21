package domain_test

import (
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestPluginRuntimeNameUsesLogicalNameAndValidatesVersion(t *testing.T) {
	got, err := domain.PluginRuntimeName(domain.PowerRef{
		Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v1.2.3",
	})
	if err != nil || got != "demo-plugin" {
		t.Fatalf("PluginRuntimeName() = %q, %v", got, err)
	}
}

func TestCanonicalPowerNameUsesAPCLowercaseKebabGrammar(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{name: "a", valid: true},
		{name: "demo-plugin-2", valid: true},
		{name: strings.Repeat("a", 64), valid: true},
		{name: ""},
		{name: strings.Repeat("a", 65)},
		{name: "Demo"},
		{name: "a--b"},
		{name: "-demo"},
		{name: "demo-"},
		{name: "a:b"},
		{name: "a_b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := domain.IsCanonicalPowerName(test.name); got != test.valid {
				t.Fatalf("IsCanonicalPowerName(%q) = %t, want %t", test.name, got, test.valid)
			}
		})
	}
}

func TestPluginRuntimeNameRejectsInvalidIdentity(t *testing.T) {
	tests := []struct {
		name string
		ref  domain.PowerRef
	}{
		{name: "empty", ref: domain.PowerRef{Name: "", Type: domain.PowerTypePlugin, Version: "v1.2.3"}},
		{name: "parent", ref: domain.PowerRef{Name: "../demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}},
		{name: "separator", ref: domain.PowerRef{Name: "group/demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}},
		{name: "uppercase", ref: domain.PowerRef{Name: "Demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}},
		{name: "double hyphen", ref: domain.PowerRef{Name: "a--b", Type: domain.PowerTypePlugin, Version: "v1.2.3"}},
		{name: "colon", ref: domain.PowerRef{Name: "a:b", Type: domain.PowerTypePlugin, Version: "v1.2.3"}},
		{name: "too long", ref: domain.PowerRef{Name: strings.Repeat("a", 65), Type: domain.PowerTypePlugin, Version: "v1.2.3"}},
		{name: "wrong type", ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.2.3"}},
		{name: "missing v", ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "1.2.3"}},
		{name: "prerelease", ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3-rc.1"}},
		{name: "metadata", ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3+build"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := domain.PluginRuntimeName(test.ref); err == nil {
				t.Fatalf("PluginRuntimeName(%#v) error = nil", test.ref)
			}
		})
	}
}

func TestPluginMarketplaceNameUsesLogicalNameOnly(t *testing.T) {
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	got, err := domain.PluginMarketplaceName(ref)
	if err != nil || got != "apm-demo-plugin" {
		t.Fatalf("PluginMarketplaceName() = %q, %v", got, err)
	}
}

func TestPluginMarketplaceNameRejectsUnsafeIdentity(t *testing.T) {
	for _, ref := range []domain.PowerRef{
		{Name: "../demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
		{Name: "demo", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
		{Name: "demo", Type: domain.PowerTypePlugin, Version: "0.1.0"},
	} {
		if _, err := domain.PluginMarketplaceName(ref); err == nil {
			t.Fatalf("PluginMarketplaceName(%#v) error = nil", ref)
		}
	}
}

func TestPluginNameMappingRejectsUnsafeOrNonCanonicalValues(t *testing.T) {
	tests := []struct {
		name string
		ref  domain.PowerRef
	}{
		{name: "parent", ref: domain.PowerRef{Name: "../demo", Type: domain.PowerTypePlugin, Version: "v0.0.1"}},
		{name: "separator", ref: domain.PowerRef{Name: "group/demo", Type: domain.PowerTypePlugin, Version: "v0.0.1"}},
		{name: "wrong type", ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v0.0.1"}},
		{name: "missing v", ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "0.0.1"}},
		{name: "non canonical", ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3+build"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := domain.PluginRuntimeName(test.ref); err == nil {
				t.Fatalf("PluginRuntimeName(%#v) error = nil", test.ref)
			}
		})
	}
}
