package domain_test

import (
	"testing"

	"apm/internal/domain"
)

func TestPluginNameMappingRoundTrips(t *testing.T) {
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"}
	actual, err := domain.ActualPluginName(ref)
	if err != nil || actual != "demo-plugin-v0.0.1" {
		t.Fatalf("ActualPluginName() = %q, %v", actual, err)
	}
	got, err := domain.PluginRefFromMarketplace(actual, "0.0.1")
	if err != nil || got != ref {
		t.Fatalf("PluginRefFromMarketplace() = %#v, %v", got, err)
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
			if _, err := domain.ActualPluginName(test.ref); err == nil {
				t.Fatalf("ActualPluginName(%#v) error = nil", test.ref)
			}
		})
	}
}

func TestPluginRefFromMarketplaceRejectsMismatchedSuffix(t *testing.T) {
	for _, actual := range []string{"demo", "demo-v0.0.2", "-v0.0.1", "../demo-v0.0.1"} {
		if _, err := domain.PluginRefFromMarketplace(actual, "0.0.1"); err == nil {
			t.Fatalf("PluginRefFromMarketplace(%q) error = nil", actual)
		}
	}
}

func TestPluginDefinitionValidate(t *testing.T) {
	valid := domain.PluginDefinition{
		ActualName: "demo-plugin-v0.0.1",
		Marketplace: domain.MarketplaceDefinition{
			Name:   "apm",
			Source: "/srv/apm-marketplace",
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	for _, definition := range []domain.PluginDefinition{
		{},
		{ActualName: "../demo-v0.0.1", Marketplace: valid.Marketplace},
		{ActualName: valid.ActualName, Marketplace: domain.MarketplaceDefinition{Source: valid.Marketplace.Source}},
		{ActualName: valid.ActualName, Marketplace: domain.MarketplaceDefinition{Name: "apm"}},
	} {
		if err := definition.Validate(); err == nil {
			t.Fatalf("Validate(%#v) error = nil", definition)
		}
	}
}
