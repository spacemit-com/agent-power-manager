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
	// PowerTypeExpert 表示 Expert 能力（Bibit 子代理，由 agent.yaml 配置）。它是唯一
	// 名字与线上值不一致的类型：APC 仍把它叫 agent（见 APC-API.md 与 ADR-044），翻译只
	// 发生在 internal/cloud/remote 的 HTTP 边界与目录组名上，本地一律叫 expert。
	PowerTypeExpert PowerType = "expert"
)

// PowerRef 表示能力的类别、名称和可选版本。
type PowerRef struct {
	Name    string
	Type    PowerType
	Version string
}

// InstalledPower 表示清单中一条已安装 Power：安装时捕获的精确身份与发布元数据
// （description、tags，以及 v2 起的展示字段 name_zh/name_en/description_zh）。
// Ref 仍是唯一身份；其余字段不参与身份比较、缓存键或排序（ADR-024、ADR-040）。
type InstalledPower struct {
	Ref           PowerRef
	Description   string
	Tags          []string
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// Clone 返回不共享 Tags 底层数组的清单条目副本，供清单快照向调用方暴露数据。
func (power InstalledPower) Clone() InstalledPower {
	if power.Tags != nil {
		power.Tags = append([]string{}, power.Tags...)
	}
	return power
}

// InstalledPowerGroups 表示按能力类别组织的已安装 Power 清单。
type InstalledPowerGroups map[PowerType][]InstalledPower
