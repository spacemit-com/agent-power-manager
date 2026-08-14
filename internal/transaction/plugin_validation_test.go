package transaction

import (
	"errors"
	"testing"

	"apm/internal/domain"
)

func TestValidatePluginRequests(t *testing.T) {
	var typedNil *nilPluginValidationAdapter
	install := PluginInstallRequest{
		Agent:      domain.AgentCodex,
		Power:      domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		Definition: domain.PluginDefinition{ActualName: "demo-v1.0.0", Marketplace: domain.MarketplaceDefinition{Name: "apm", Source: "/marketplace"}},
		Adapter:    &nilPluginValidationAdapter{},
	}
	if err := validatePluginInstallRequest(install); err != nil {
		t.Fatalf("validatePluginInstallRequest(valid) = %v", err)
	}
	install.Adapter = typedNil
	if err := validatePluginInstallRequest(install); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("typed nil install error = %v", err)
	}
	uninstall := PluginUninstallRequest{
		Agent:      domain.AgentHermes,
		Power:      domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		ActualName: "demo-v1.0.0",
		Adapter:    &nilPluginValidationAdapter{},
	}
	if err := validatePluginUninstallRequest(uninstall); err != nil {
		t.Fatalf("validatePluginUninstallRequest(valid) = %v", err)
	}
	uninstall.ActualName = "../demo"
	if err := validatePluginUninstallRequest(uninstall); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe uninstall error = %v", err)
	}
}
