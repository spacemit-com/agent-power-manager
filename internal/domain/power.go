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

// PowerRef 表示能力的名称和类别。
type PowerRef struct {
	Name string
	Type PowerType
}

// Power 表示可分发的能力及其数据。
type Power struct {
	Ref     PowerRef
	Version string
	Data    []byte
}

// InstalledPower 表示已安装能力的元数据。
type InstalledPower struct {
	Ref         PowerRef
	Version     string
	InstalledAt time.Time
}
