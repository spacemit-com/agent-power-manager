package transaction

import (
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

func TestValidatePowerAcceptsAgent(t *testing.T) {
	if err := validatePower(domain.PowerRef{Name: "a", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, ErrInvalidRequest); err != nil {
		t.Fatalf("validatePower(agent) error = %v", err)
	}
}

func TestValidateAgentInstallRequest(t *testing.T) {
	valid := AgentInstallRequest{
		Agent:       domain.AgentBibit,
		Power:       domain.PowerRef{Name: "a", Type: domain.PowerTypeAgent, Version: "v1.0.0"},
		StagingRoot: agentStagingRoot(t),
		Adapter:     &agentValidationAdapter{},
	}
	if err := validateAgentInstallRequest(valid); err != nil {
		t.Fatalf("validateAgentInstallRequest() error = %v", err)
	}
	tests := map[string]func(*AgentInstallRequest){
		"empty agent":   func(r *AgentInstallRequest) { r.Agent = "" },
		"all agents":    func(r *AgentInstallRequest) { r.Agent = domain.AllAgents },
		"empty name":    func(r *AgentInstallRequest) { r.Power.Name = "" },
		"skill power":   func(r *AgentInstallRequest) { r.Power.Type = domain.PowerTypeSkill },
		"empty version": func(r *AgentInstallRequest) { r.Power.Version = "" },
		"empty staging": func(r *AgentInstallRequest) { r.StagingRoot = "" },
		"nil adapter":   func(r *AgentInstallRequest) { r.Adapter = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateAgentInstallRequest(request); err == nil {
				t.Fatalf("validateAgentInstallRequest() error = nil")
			}
		})
	}
}

type agentValidationAdapter struct{}

func (a *agentValidationAdapter) TargetPath(domain.PowerRef) (string, error) {
	return "/tmp/agent.yaml", nil
}
func (a *agentValidationAdapter) Install(domain.PowerRef, string) (adapter.AgentInstallResult, error) {
	return adapter.AgentInstallResult{Published: true}, nil
}

// agentStagingRoot 返回与 paths.StagingPath 一致的隔离 staging 绝对路径。
func agentStagingRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("APM_HOME", t.TempDir())
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	return staging
}
