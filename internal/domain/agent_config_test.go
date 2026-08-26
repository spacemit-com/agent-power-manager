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

// validAgentYAML 生成满足 V0.5 最小必填字段的 agent.yaml 文本。
func validAgentYAML(id, name, version string) string {
	return "schema_version: 1\nagent:\n" +
		"  id: " + id + "\n" +
		"  name: " + name + "\n" +
		"  version: " + version + "\n" +
		"  description: demo\n" +
		"  soul: help safely\n"
}

// mustDecodeAgent 解码 agent.yaml 并在失败时终止测试。
func mustDecodeAgent(t *testing.T, raw string) AgentConfig {
	t.Helper()
	config, err := DecodeAgentConfigYAML([]byte(raw))
	if err != nil {
		t.Fatalf("DecodeAgentConfigYAML() error = %v", err)
	}
	return config
}

func TestAgentConfigValidate(t *testing.T) {
	valid := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{
		ID: "bibit-internal-42", Name: "demo-agent", Version: "v1.0.0",
		Description: "demo", Soul: "help safely",
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := map[string]func(*AgentConfig){
		"wrong schema":        func(c *AgentConfig) { c.SchemaVersion = 2 },
		"empty id":            func(c *AgentConfig) { c.Agent.ID = "" },
		"blank id":            func(c *AgentConfig) { c.Agent.ID = "  \t\n" },
		"empty name":          func(c *AgentConfig) { c.Agent.Name = "" },
		"uppercase name":      func(c *AgentConfig) { c.Agent.Name = "Demo-Agent" },
		"underscore name":     func(c *AgentConfig) { c.Agent.Name = "demo_agent" },
		"leading dash name":   func(c *AgentConfig) { c.Agent.Name = "-demo" },
		"trailing dash name":  func(c *AgentConfig) { c.Agent.Name = "demo-" },
		"empty version":       func(c *AgentConfig) { c.Agent.Version = "" },
		"bare version":        func(c *AgentConfig) { c.Agent.Version = "1.0.0" },
		"prerelease":          func(c *AgentConfig) { c.Agent.Version = "v1.0.0-rc.1" },
		"version metadata":    func(c *AgentConfig) { c.Agent.Version = "v1.0.0+build" },
		"missing description": func(c *AgentConfig) { c.Agent.Description = "" },
		"blank description":   func(c *AgentConfig) { c.Agent.Description = "  \t\n" },
		"missing soul":        func(c *AgentConfig) { c.Agent.Soul = "" },
		"blank soul":          func(c *AgentConfig) { c.Agent.Soul = "  \t\n" },
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

	// agent.id 只要求非空，不做 canonical/safe-path 校验，允许与外部 name 无关的自由形式。
	t.Run("free-form id", func(t *testing.T) {
		config := valid
		config.Agent.ID = "Demo/Internal.ID_42"
		if err := config.Validate(); err != nil {
			t.Fatalf("Validate() error = %v, want nil for free-form id", err)
		}
	})
}

func TestDecodeAgentConfigYAMLAcceptsUnknownFields(t *testing.T) {
	raw := []byte("schema_version: 1\nagent:\n  id: bibit-internal-42\n  name: demo-agent\n  version: v1.2.3\n  description: demo\n  soul: help safely\n  future: {enabled: true}\n")
	got, err := DecodeAgentConfigYAML(raw)
	if err != nil {
		t.Fatalf("DecodeAgentConfigYAML() error = %v", err)
	}
	if got.Agent.ID != "bibit-internal-42" || got.Agent.Name != "demo-agent" ||
		got.Agent.Version != "v1.2.3" || got.Agent.Description != "demo" || got.Agent.Soul != "help safely" {
		t.Fatalf("DecodeAgentConfigYAML() = %+v", got)
	}
}

func TestDecodeAgentConfigYAMLRejectsDuplicateAndMultipleDocuments(t *testing.T) {
	cases := [][]byte{
		[]byte("schema_version: 1\nschema_version: 1\nagent: {id: bibit, name: demo, version: v1.0.0, description: demo, soul: help safely}\n"),
		[]byte("schema_version: 1\nagent: {id: bibit, id: other, name: demo, version: v1.0.0, description: demo, soul: help safely}\n"),
		[]byte("schema_version: 1\nagent: {id: bibit, name: demo, version: v1.0.0, description: demo, soul: help safely}\n---\n{}\n"),
	}
	for _, raw := range cases {
		if _, err := DecodeAgentConfigYAML(raw); !errors.Is(err, ErrInvalidAgent) {
			t.Fatalf("DecodeAgentConfigYAML(%q) error = %v, want ErrInvalidAgent", raw, err)
		}
	}
}

func TestDecodeAgentConfigYAMLValid(t *testing.T) {
	tests := map[string]struct {
		raw    string
		id     string
		name   string
		descr  string
		soul   string
		avatar *string
	}{
		"minimal": {
			raw:   validAgentYAML("bibit-internal-42", "demo-agent", "v1.0.0"),
			id:    "bibit-internal-42",
			name:  "demo-agent",
			descr: "demo",
			soul:  "help safely",
		},
		"extension sequence": {
			raw:   "schema_version: 1\nagent:\n  id: bibit\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n  tools: ['*']\n",
			id:    "bibit",
			name:  "demo-agent",
			descr: "demo",
			soul:  "help safely",
		},
		"empty metadata": {
			raw:   "schema_version: 1\nagent:\n  id: bibit\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n  metadata: {}\n",
			id:    "bibit",
			name:  "demo-agent",
			descr: "demo",
			soul:  "help safely",
		},
		"avatar": {
			raw:    "schema_version: 1\nagent:\n  id: bibit\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n  metadata:\n    avatar: assets/avatar.png\n    future: 1\n",
			id:     "bibit",
			name:   "demo-agent",
			descr:  "demo",
			soul:   "help safely",
			avatar: strPtr("assets/avatar.png"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			config, err := DecodeAgentConfigYAML([]byte(test.raw))
			if err != nil {
				t.Fatalf("DecodeAgentConfigYAML() error = %v", err)
			}
			if config.Agent.ID != test.id || config.Agent.Name != test.name ||
				config.Agent.Version != "v1.0.0" || config.Agent.Description != test.descr || config.Agent.Soul != test.soul {
				t.Fatalf("DecodeAgentConfigYAML() = %+v", config)
			}
			if test.avatar == nil {
				if config.Agent.Metadata.Avatar != nil {
					t.Fatalf("avatar = %q, want nil", *config.Agent.Metadata.Avatar)
				}
			} else if config.Agent.Metadata.Avatar == nil || *config.Agent.Metadata.Avatar != *test.avatar {
				t.Fatalf("avatar = %v, want %q", config.Agent.Metadata.Avatar, *test.avatar)
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
		"missing schema":        "agent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"schema string":         "schema_version: \"1\"\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"schema version two":    "schema_version: 2\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"missing id":            "schema_version: 1\nagent: {name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"empty id":              "schema_version: 1\nagent: {id: \"\", name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"blank id":              "schema_version: 1\nagent: {id: \"  \", name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"nonstring id":          "schema_version: 1\nagent: {id: 123, name: demo-agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"missing name":          "schema_version: 1\nagent: {id: bibit, version: v1.0.0, description: demo, soul: help safely}\n",
		"empty name":            "schema_version: 1\nagent: {id: bibit, name: \"\", version: v1.0.0, description: demo, soul: help safely}\n",
		"nonstring name":        "schema_version: 1\nagent: {id: bibit, name: 123, version: v1.0.0, description: demo, soul: help safely}\n",
		"noncanonical name":     "schema_version: 1\nagent: {id: bibit, name: Demo-Agent, version: v1.0.0, description: demo, soul: help safely}\n",
		"missing version":       "schema_version: 1\nagent: {id: bibit, name: demo-agent, description: demo, soul: help safely}\n",
		"nonstring version":     "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: 1.2, description: demo, soul: help safely}\n",
		"invalid version":       "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: 1.0.0, description: demo, soul: help safely}\n",
		"prerelease version":    "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0-rc.1, description: demo, soul: help safely}\n",
		"missing description":   "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, soul: help safely}\n",
		"empty description":     "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: \"\", soul: help safely}\n",
		"blank description":     "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: \"  \", soul: help safely}\n",
		"nonstring description": "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: 123, soul: help safely}\n",
		"missing soul":          "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo}\n",
		"empty soul":            "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: \"\"}\n",
		"blank soul":            "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: \"  \"}\n",
		"nonstring soul":        "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: 123}\n",
		"nonmapping metadata":   "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: help safely, metadata: demo}\n",
		"nonstring avatar":      "schema_version: 1\nagent: {id: bibit, name: demo-agent, version: v1.0.0, description: demo, soul: help safely, metadata: {avatar: 123}}\n",
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
	config := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{
		ID: "bibit-internal-42", Name: "demo-agent", Version: "v1.2.3",
		Description: "demo", Soul: "help safely",
	}}
	if err := config.ValidatePowerRef(PowerRef{Type: PowerTypeAgent, Name: "demo-agent", Version: "v1.2.3"}); err != nil {
		t.Fatalf("ValidatePowerRef() error = %v", err)
	}
	for _, ref := range []PowerRef{
		{Type: PowerTypeSkill, Name: "demo-agent", Version: "v1.2.3"},
		{Type: PowerTypeAgent, Name: "other", Version: "v1.2.3"},
		{Type: PowerTypeAgent, Name: "demo-agent", Version: "v1.2.4"},
		{Type: PowerTypeAgent, Name: "bibit-internal-42", Version: "v1.2.3"},
	} {
		if err := config.ValidatePowerRef(ref); !errors.Is(err, ErrInvalidAgent) {
			t.Fatalf("ValidatePowerRef(%+v) error = %v, want ErrInvalidAgent", ref, err)
		}
	}
}

// TestDecodeAgentConfigIdentityUsesName 验证解码后的外部身份取自 agent.name/agent.version；
// agent.id 只作为 Bibit 内部标识，不参与 PowerRef 比较。
func TestDecodeAgentConfigIdentityUsesName(t *testing.T) {
	config := mustDecodeAgent(t, validAgentYAML("bibit-internal-42", "demo-agent", "v1.2.3"))
	if err := config.ValidatePowerRef(PowerRef{Type: PowerTypeAgent, Name: "demo-agent", Version: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	if err := config.ValidatePowerRef(PowerRef{Type: PowerTypeAgent, Name: "bibit-internal-42", Version: "v1.2.3"}); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("error = %v", err)
	}
}

func strPtr(value string) *string { return &value }
