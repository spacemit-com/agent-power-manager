package transaction

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

type marketplacePluginValidationAdapter struct {
	nilPluginValidationAdapter
	identity byte
}

func (*marketplacePluginValidationAdapter) InspectMarketplace(context.Context, string) (adapter.MarketplaceFact, error) {
	return adapter.MarketplaceFact{}, nil
}
func (*marketplacePluginValidationAdapter) RegisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (*marketplacePluginValidationAdapter) UnregisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}

type filesystemPluginValidationAdapter struct{}

func (*filesystemPluginValidationAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentFilesystem
}
func (*filesystemPluginValidationAdapter) Inspect(context.Context, string) (adapter.PluginFact, error) {
	return adapter.PluginFact{}, nil
}
func (*filesystemPluginValidationAdapter) Add(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (*filesystemPluginValidationAdapter) Remove(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (*filesystemPluginValidationAdapter) TargetPath(string) (string, error) { return "", nil }

type nonComparableMarketplacePluginValidationAdapter struct {
	state []string
}

type comparableMarketplacePluginValidationAdapter int

func (comparableMarketplacePluginValidationAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}
func (comparableMarketplacePluginValidationAdapter) Inspect(context.Context, string) (adapter.PluginFact, error) {
	return adapter.PluginFact{}, nil
}
func (comparableMarketplacePluginValidationAdapter) Add(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (comparableMarketplacePluginValidationAdapter) Remove(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (comparableMarketplacePluginValidationAdapter) TargetPath(string) (string, error) {
	return "", nil
}
func (comparableMarketplacePluginValidationAdapter) InspectMarketplace(context.Context, string) (adapter.MarketplaceFact, error) {
	return adapter.MarketplaceFact{}, nil
}
func (comparableMarketplacePluginValidationAdapter) RegisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (comparableMarketplacePluginValidationAdapter) UnregisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}

func (nonComparableMarketplacePluginValidationAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}
func (nonComparableMarketplacePluginValidationAdapter) Inspect(context.Context, string) (adapter.PluginFact, error) {
	return adapter.PluginFact{}, nil
}
func (nonComparableMarketplacePluginValidationAdapter) Add(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (nonComparableMarketplacePluginValidationAdapter) Remove(context.Context, adapter.PluginTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (nonComparableMarketplacePluginValidationAdapter) TargetPath(string) (string, error) {
	return "", nil
}
func (nonComparableMarketplacePluginValidationAdapter) InspectMarketplace(context.Context, string) (adapter.MarketplaceFact, error) {
	return adapter.MarketplaceFact{}, nil
}
func (nonComparableMarketplacePluginValidationAdapter) RegisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}
func (nonComparableMarketplacePluginValidationAdapter) UnregisterMarketplace(context.Context, adapter.MarketplaceTarget) (adapter.PluginMutationResult, error) {
	return adapter.PluginMutationResult{}, nil
}

func TestValidatePluginRequests(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	var typedNil *nilPluginValidationAdapter
	pluginAdapter := &marketplacePluginValidationAdapter{}
	target := adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}
	marketplace := adapter.MarketplaceTarget{Name: "apm-demo", Source: filepath.Join(cacheHome, "apm", "plugins", "codex", "demo", "v1.0.0")}
	install := PluginInstallRequest{
		Agent: domain.AgentCodex, Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		Adapter: pluginAdapter, Target: target,
		Marketplace: &PluginMarketplaceInstallRequest{Target: marketplace, Adapter: pluginAdapter},
	}
	if err := validatePluginInstallRequest(install); err != nil {
		t.Fatalf("validatePluginInstallRequest(valid) = %v", err)
	}
	install.Adapter = typedNil
	if err := validatePluginInstallRequest(install); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("typed nil install error = %v", err)
	}
	uninstall := PluginUninstallRequest{
		Agent: domain.AgentCodex, Power: install.Power,
		Adapter: pluginAdapter, Target: target, Marketplace: &marketplace, MarketplaceAdapter: pluginAdapter,
	}
	if err := validatePluginUninstallRequest(uninstall); err != nil {
		t.Fatalf("validatePluginUninstallRequest(valid) = %v", err)
	}
	uninstall.Target.ActualName = "../demo"
	if err := validatePluginUninstallRequest(uninstall); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsafe uninstall error = %v", err)
	}
}

func TestValidatePluginMarketplaceRequests(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	pluginAdapter := &marketplacePluginValidationAdapter{}
	targetSource := filepath.Join(cacheHome, "apm", "plugins", "codex", "demo", "v2.0.0")
	previousSource := filepath.Join(cacheHome, "apm", "plugins", "codex", "demo", "v1.0.0")
	targetMarketplace := adapter.MarketplaceTarget{Name: "apm-demo", Source: targetSource}
	previousMarketplace := adapter.MarketplaceTarget{Name: "apm-demo", Source: previousSource}
	targetPlugin := adapter.PluginTarget{ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0@apm-demo"}
	previousPlugin := adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}
	var typedNilMarketplaceAdapter *marketplacePluginValidationAdapter
	firstInstall := PluginInstallRequest{
		Agent:   domain.AgentCodex,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"},
		Adapter: pluginAdapter,
		Target:  targetPlugin,
		Marketplace: &PluginMarketplaceInstallRequest{
			Target: targetMarketplace, Adapter: pluginAdapter,
		},
	}
	if err := validatePluginInstallRequest(firstInstall); err != nil {
		t.Fatalf("validatePluginInstallRequest(first install) = %v", err)
	}

	for name, mutate := range map[string]func(*PluginInstallRequest){
		"missing target Plugin": func(r *PluginInstallRequest) { r.Target = adapter.PluginTarget{} },
		"missing target marketplace": func(r *PluginInstallRequest) {
			r.Marketplace.Target = adapter.MarketplaceTarget{}
		},
		"wrong target marketplace source": func(r *PluginInstallRequest) {
			r.Marketplace.Target.Source = previousSource
		},
		"missing marketplace Adapter":   func(r *PluginInstallRequest) { r.Marketplace.Adapter = nil },
		"typed nil marketplace Adapter": func(r *PluginInstallRequest) { r.Marketplace.Adapter = typedNilMarketplaceAdapter },
		"cross-wired marketplace Adapter": func(r *PluginInstallRequest) {
			r.Marketplace.Adapter = &marketplacePluginValidationAdapter{}
		},
		"non-comparable Adapter value": func(r *PluginInstallRequest) {
			value := nonComparableMarketplacePluginValidationAdapter{state: []string{"same-value"}}
			r.Adapter = value
			r.Marketplace.Adapter = value
		},
		"comparable Adapter value has no instance identity": func(r *PluginInstallRequest) {
			value := comparableMarketplacePluginValidationAdapter(1)
			r.Adapter = value
			r.Marketplace.Adapter = value
		},
	} {
		t.Run("first install/"+name, func(t *testing.T) {
			request := firstInstall
			marketplace := *firstInstall.Marketplace
			request.Marketplace = &marketplace
			mutate(&request)
			if err := validatePluginInstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validatePluginInstallRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}

	previousRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	upgrade := firstInstall
	upgrade.Previous = &previousRef
	upgrade.PreviousTarget = &previousPlugin
	upgradeMarketplace := *firstInstall.Marketplace
	upgradeMarketplace.Previous = &previousMarketplace
	upgrade.Marketplace = &upgradeMarketplace
	if err := validatePluginInstallRequest(upgrade); err != nil {
		t.Fatalf("validatePluginInstallRequest(upgrade) = %v", err)
	}
	for name, mutate := range map[string]func(*PluginInstallRequest){
		"missing previous Plugin":      func(r *PluginInstallRequest) { r.PreviousTarget = nil },
		"missing previous marketplace": func(r *PluginInstallRequest) { r.Marketplace.Previous = nil },
		"different marketplace name": func(r *PluginInstallRequest) {
			previous := *r.Marketplace.Previous
			previous.Name = "apm-other"
			r.Marketplace.Previous = &previous
		},
		"same marketplace source": func(r *PluginInstallRequest) {
			previous := *r.Marketplace.Previous
			previous.Source = r.Marketplace.Target.Source
			r.Marketplace.Previous = &previous
		},
		"wrong previous marketplace source": func(r *PluginInstallRequest) {
			previous := *r.Marketplace.Previous
			previous.Source = filepath.Join(cacheHome, "apm", "plugins", "codex", "demo", "v0.9.0")
			r.Marketplace.Previous = &previous
		},
	} {
		t.Run("upgrade/"+name, func(t *testing.T) {
			request := upgrade
			marketplace := *upgrade.Marketplace
			request.Marketplace = &marketplace
			mutate(&request)
			if err := validatePluginInstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validatePluginInstallRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}

	uninstall := PluginUninstallRequest{
		Agent: domain.AgentCodex, Power: firstInstall.Power,
		Adapter: pluginAdapter, Target: targetPlugin, Marketplace: &targetMarketplace, MarketplaceAdapter: pluginAdapter,
	}
	if err := validatePluginUninstallRequest(uninstall); err != nil {
		t.Fatalf("validatePluginUninstallRequest(marketplace) = %v", err)
	}
	for name, mutate := range map[string]func(*PluginUninstallRequest){
		"missing target Plugin":       func(r *PluginUninstallRequest) { r.Target = adapter.PluginTarget{} },
		"missing target marketplace":  func(r *PluginUninstallRequest) { r.Marketplace = nil },
		"missing marketplace Adapter": func(r *PluginUninstallRequest) { r.MarketplaceAdapter = nil },
		"typed nil marketplace Adapter": func(r *PluginUninstallRequest) {
			r.MarketplaceAdapter = typedNilMarketplaceAdapter
		},
		"cross-wired marketplace Adapter": func(r *PluginUninstallRequest) {
			r.MarketplaceAdapter = &marketplacePluginValidationAdapter{}
		},
		"non-comparable Adapter value": func(r *PluginUninstallRequest) {
			value := nonComparableMarketplacePluginValidationAdapter{state: []string{"same-value"}}
			r.Adapter = value
			r.MarketplaceAdapter = value
		},
		"comparable Adapter value has no instance identity": func(r *PluginUninstallRequest) {
			value := comparableMarketplacePluginValidationAdapter(1)
			r.Adapter = value
			r.MarketplaceAdapter = value
		},
		"wrong marketplace source": func(r *PluginUninstallRequest) {
			marketplace := *r.Marketplace
			marketplace.Source = previousSource
			r.Marketplace = &marketplace
		},
	} {
		t.Run("uninstall/"+name, func(t *testing.T) {
			request := uninstall
			mutate(&request)
			if err := validatePluginUninstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validatePluginUninstallRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}

	t.Run("Hermes forbids marketplace data", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("APM_HOME", home)
		staging, err := paths.StagingPath()
		if err != nil {
			t.Fatal(err)
		}
		request := firstInstall
		request.Agent = domain.AgentHermes
		request.StagingRoot = staging
		request.Adapter = &filesystemPluginValidationAdapter{}
		if err := validatePluginInstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("validatePluginInstallRequest(Hermes marketplace) error = %v, want ErrInvalidRequest", err)
		}
	})
}
