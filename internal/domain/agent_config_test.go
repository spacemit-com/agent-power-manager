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
	valid := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{ID: "bianbu-agent-default", Version: "v1.0.0", Description: "demo"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := map[string]func(*AgentConfig){
		"wrong schema":        func(c *AgentConfig) { c.SchemaVersion = 2 },
		"empty id":            func(c *AgentConfig) { c.Agent.ID = "" },
		"dot id":              func(c *AgentConfig) { c.Agent.ID = "." },
		"slash id":            func(c *AgentConfig) { c.Agent.ID = "a/b" },
		"uppercase id":        func(c *AgentConfig) { c.Agent.ID = "Demo-Agent" },
		"underscore id":       func(c *AgentConfig) { c.Agent.ID = "demo_agent" },
		"empty version":       func(c *AgentConfig) { c.Agent.Version = "" },
		"bare version":        func(c *AgentConfig) { c.Agent.Version = "1.0.0" },
		"prerelease":          func(c *AgentConfig) { c.Agent.Version = "v1.0.0-rc.1" },
		"metadata":            func(c *AgentConfig) { c.Agent.Version = "v1.0.0+build" },
		"missing description": func(c *AgentConfig) { c.Agent.Description = "" },
		"blank description":   func(c *AgentConfig) { c.Agent.Description = "  \t\n" },
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

func TestDecodeAgentConfigYAMLAcceptsUnknownFields(t *testing.T) {
	raw := []byte("schema_version: 1\nagent:\n  id: demo-agent\n  version: v1.2.3\n  description: demo\n  future: {enabled: true}\n")
	got, err := DecodeAgentConfigYAML(raw)
	if err != nil {
		t.Fatalf("DecodeAgentConfigYAML() error = %v", err)
	}
	if got.Agent.ID != "demo-agent" || got.Agent.Version != "v1.2.3" || got.Agent.Description != "demo" {
		t.Fatalf("DecodeAgentConfigYAML() = %+v", got)
	}
}

func TestDecodeAgentConfigYAMLRejectsDuplicateAndMultipleDocuments(t *testing.T) {
	cases := [][]byte{
		[]byte("schema_version: 1\nschema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n"),
		[]byte("schema_version: 1\nagent: {id: demo, id: other, version: v1.0.0, description: demo}\n"),
		[]byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n---\n{}\n"),
	}
	for _, raw := range cases {
		if _, err := DecodeAgentConfigYAML(raw); !errors.Is(err, ErrInvalidAgent) {
			t.Fatalf("DecodeAgentConfigYAML(%q) error = %v, want ErrInvalidAgent", raw, err)
		}
	}
}

func TestDecodeAgentConfigYAMLValid(t *testing.T) {
	tests := map[string]struct {
		raw   string
		id    string
		descr string
	}{
		"minimal": {
			raw:   "schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n",
			id:    "demo",
			descr: "demo",
		},
		"extension sequence": {
			raw:   "schema_version: 1\nagent:\n  id: demo\n  version: v1.0.0\n  description: demo\n  tools: ['*']\n",
			id:    "demo",
			descr: "demo",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			config, err := DecodeAgentConfigYAML([]byte(test.raw))
			if err != nil {
				t.Fatalf("DecodeAgentConfigYAML() error = %v", err)
			}
			if config.Agent.ID != test.id || config.Agent.Version != "v1.0.0" || config.Agent.Description != test.descr {
				t.Fatalf("DecodeAgentConfigYAML() = %+v", config)
			}
		})
	}
}

func TestDecodeAgentConfigYAMLRejectsInvalidYAML(t *testing.T) {
	tests := map[string]string{
		"root sequence":         "- a\n",
		"root scalar":           "demo\n",
		"empty":                 "",
		"agent scalar":          "schema_version: 1\nagent: demo\n",
		"agent sequence":        "schema_version: 1\nagent: []\n",
		"missing agent":         "schema_version: 1\n",
		"missing schema":        "agent: {id: demo, version: v1.0.0, description: demo}\n",
		"schema string":         "schema_version: \"1\"\nagent: {id: demo, version: v1.0.0, description: demo}\n",
		"schema version two":    "schema_version: 2\nagent: {id: demo, version: v1.0.0, description: demo}\n",
		"missing id":            "schema_version: 1\nagent: {version: v1.0.0, description: demo}\n",
		"missing version":       "schema_version: 1\nagent: {id: demo, description: demo}\n",
		"missing description":   "schema_version: 1\nagent: {id: demo, version: v1.0.0}\n",
		"empty description":     "schema_version: 1\nagent: {id: demo, version: v1.0.0, description: \"\"}\n",
		"blank description":     "schema_version: 1\nagent: {id: demo, version: v1.0.0, description: \"  \"}\n",
		"nonstring description": "schema_version: 1\nagent: {id: demo, version: v1.0.0, description: 123}\n",
		"nonstring id":          "schema_version: 1\nagent: {id: 123, version: v1.0.0, description: demo}\n",
		"nonstring version":     "schema_version: 1\nagent: {id: demo, version: 1.2, description: demo}\n",
		"noncanonical id":       "schema_version: 1\nagent: {id: Demo-Agent, version: v1.0.0, description: demo}\n",
		"unsafe id":             "schema_version: 1\nagent: {id: demo/agent, version: v1.0.0, description: demo}\n",
		"invalid version":       "schema_version: 1\nagent: {id: demo, version: 1.0.0, description: demo}\n",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAgentConfigYAML([]byte(raw)); !errors.Is(err, ErrInvalidAgent) {
				t.Fatalf("DecodeAgentConfigYAML(%q) error = %v, want ErrInvalidAgent", raw, err)
			}
		})
	}
}

func TestAgentConfigValidatePowerRefRequiresExactIdentity(t *testing.T) {
	config := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{ID: "demo", Version: "v1.2.3", Description: "demo"}}
	if err := config.ValidatePowerRef(PowerRef{Type: PowerTypeAgent, Name: "demo", Version: "v1.2.3"}); err != nil {
		t.Fatalf("ValidatePowerRef() error = %v", err)
	}
	for _, ref := range []PowerRef{
		{Type: PowerTypeSkill, Name: "demo", Version: "v1.2.3"},
		{Type: PowerTypeAgent, Name: "other", Version: "v1.2.3"},
		{Type: PowerTypeAgent, Name: "demo", Version: "v1.2.4"},
	} {
		if err := config.ValidatePowerRef(ref); !errors.Is(err, ErrInvalidAgent) {
			t.Fatalf("ValidatePowerRef(%+v) error = %v, want ErrInvalidAgent", ref, err)
		}
	}
}
