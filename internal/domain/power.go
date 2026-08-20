package domain

// PowerType 表示能力的类别。
type PowerType string

const (
	// PowerTypeSkill 表示 Skill 能力。
	PowerTypeSkill PowerType = "skill"
	// PowerTypeMCP 表示 MCP 能力。
	PowerTypeMCP PowerType = "mcp"
	// PowerTypePlugin 表示插件能力。
	PowerTypePlugin PowerType = "plugin"
	// PowerTypeAgent 表示 Agent 能力（Bibit 子代理，由 agent.yaml 配置）。
	PowerTypeAgent PowerType = "agent"
)

// PowerRef 表示能力的类别、名称和可选版本。
type PowerRef struct {
	Name    string
	Type    PowerType
	Version string
}

// PowerGroups 表示按能力类别组织的能力引用集合。
type PowerGroups map[PowerType][]PowerRef
