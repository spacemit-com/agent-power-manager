package transaction

import (
	"errors"
	"testing"

	"apm/internal/domain"
)

func TestValidateMCPInstallRequest(t *testing.T) {
	valid := newMCPInstallFixture(t).request
	if err := validateMCPInstallRequest(valid); err != nil {
		t.Fatalf("validateMCPInstallRequest(valid) error = %v", err)
	}
	tests := map[string]func(*MCPInstallRequest){
		"empty agent":   func(r *MCPInstallRequest) { r.Agent = "" },
		"all agents":    func(r *MCPInstallRequest) { r.Agent = domain.AllAgents },
		"non MCP":       func(r *MCPInstallRequest) { r.Power.Type = domain.PowerTypeSkill },
		"empty name":    func(r *MCPInstallRequest) { r.Power.Name = "" },
		"empty version": func(r *MCPInstallRequest) { r.Power.Version = "" },
		"nil adapter":   func(r *MCPInstallRequest) { r.Adapter = nil },
		"previous type": func(r *MCPInstallRequest) {
			r.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"})
		},
		"previous name": func(r *MCPInstallRequest) {
			r.Previous = powerRefPointer(domain.PowerRef{Name: "other", Type: domain.PowerTypeMCP, Version: "1.0.0"})
		},
		"same version": func(r *MCPInstallRequest) {
			r.Previous = powerRefPointer(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "2.0.0"})
		},
		"previous server without ref": func(r *MCPInstallRequest) {
			old := domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://old.example/mcp"}
			r.PreviousServer = &old
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateMCPInstallRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateMCPInstallRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}
