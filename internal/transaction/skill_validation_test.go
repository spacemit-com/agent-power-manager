package transaction

import (
	"errors"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestValidateSkillInstallRequest(t *testing.T) {
	staging, target := validationPaths(t)
	valid := SkillInstallRequest{
		Agent:       domain.AgentCodex,
		Power:       domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "2.0.0"},
		Previous:    powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}),
		StagingRoot: staging,
		Adapter:     validationAdapter{target: target},
	}
	if err := validateSkillInstallRequest(valid); err != nil {
		t.Fatalf("validateSkillInstallRequest(valid) error = %v", err)
	}

	tests := map[string]func(*SkillInstallRequest){
		"empty agent":       func(r *SkillInstallRequest) { r.Agent = "" },
		"all agents":        func(r *SkillInstallRequest) { r.Agent = domain.AllAgents },
		"empty name":        func(r *SkillInstallRequest) { r.Power.Name = "" },
		"empty type":        func(r *SkillInstallRequest) { r.Power.Type = "" },
		"empty version":     func(r *SkillInstallRequest) { r.Power.Version = "" },
		"empty staging":     func(r *SkillInstallRequest) { r.StagingRoot = "" },
		"nil adapter":       func(r *SkillInstallRequest) { r.Adapter = nil },
		"typed nil adapter": func(r *SkillInstallRequest) { r.Adapter = (*nilValidationAdapter)(nil) },
		"previous name":     func(r *SkillInstallRequest) { r.Previous.Name = "other" },
		"previous type":     func(r *SkillInstallRequest) { r.Previous.Type = domain.PowerTypePlugin },
		"previous version":  func(r *SkillInstallRequest) { r.Previous.Version = "" },
		"plugin power": func(r *SkillInstallRequest) {
			r.Power.Type, r.Previous.Type = domain.PowerTypePlugin, domain.PowerTypePlugin
		},
		"MCP power": func(r *SkillInstallRequest) {
			r.Power.Type, r.Previous.Type = domain.PowerTypeMCP, domain.PowerTypeMCP
		},
		"noncanonical stage": func(r *SkillInstallRequest) { r.StagingRoot += string(filepath.Separator) + ".." },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid
			previous := *valid.Previous
			request.Previous = &previous
			mutate(&request)
			if err := validateSkillInstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateSkillInstallRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestValidateSkillUninstallRequest(t *testing.T) {
	_, target := validationPaths(t)
	valid := SkillUninstallRequest{
		Agent:   domain.AgentHermes,
		Power:   domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"},
		Adapter: validationAdapter{target: target},
	}
	if err := validateSkillUninstallRequest(valid); err != nil {
		t.Fatalf("validateSkillUninstallRequest(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*SkillUninstallRequest){
		"nil adapter":  func(r *SkillUninstallRequest) { r.Adapter = nil },
		"plugin power": func(r *SkillUninstallRequest) { r.Power.Type = domain.PowerTypePlugin },
		"MCP power":    func(r *SkillUninstallRequest) { r.Power.Type = domain.PowerTypeMCP },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateSkillUninstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateSkillUninstallRequest(nil adapter) error = %v", err)
			}
		})
	}
}
