package domain

import "time"

// PowerType 表示能力的类别。
type PowerType string

const (
	// PowerTypeSkill 表示 Skill 能力。
	PowerTypeSkill PowerType = "skill"
	// PowerTypePlugin 表示插件能力。
	PowerTypePlugin PowerType = "plugin"
	// PowerTypeMCP 表示 MCP 能力。
	PowerTypeMCP PowerType = "mcp"
)

// PowerRef 表示能力的类别、名称和可选版本。
type PowerRef struct {
	Name    string
	Type    PowerType
	Version string
}

// Power 是后续 Adapter 安装参数的占位类型。
type Power struct{}

// InstalledPower 表示已安装能力的元数据。
type InstalledPower struct {
	Ref         PowerRef
	InstalledAt time.Time
}
