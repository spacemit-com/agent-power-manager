package domain

import (
	"errors"
	"testing"
)

func TestPowerTypeAgentIsStable(t *testing.T) {
	if PowerTypeAgent != "agent" {
		t.Fatalf("PowerTypeAgent = %q, want %q", PowerTypeAgent, "agent")
	}
}

func TestAgentConfigValidate(t *testing.T) {
	valid := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{ID: "bianbu-agent-default", Version: "v1.0.0"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := map[string]func(*AgentConfig){
		"wrong schema":  func(c *AgentConfig) { c.SchemaVersion = 2 },
		"empty id":      func(c *AgentConfig) { c.Agent.ID = "" },
		"dot id":        func(c *AgentConfig) { c.Agent.ID = "." },
		"slash id":      func(c *AgentConfig) { c.Agent.ID = "a/b" },
		"empty version": func(c *AgentConfig) { c.Agent.Version = "" },
		"bare version":  func(c *AgentConfig) { c.Agent.Version = "1.0.0" },
		"prerelease":    func(c *AgentConfig) { c.Agent.Version = "v1.0.0-rc.1" },
		"metadata":      func(c *AgentConfig) { c.Agent.Version = "v1.0.0+build" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if err := config.Validate(); !errors.Is(err, ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}
