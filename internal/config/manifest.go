// Package config 提供 APM 配置清单的 JSON 转换。
package config

import "apm/internal/domain"

type manifestDocument struct {
	Agent   domain.AgentID `json:"agent"`
	Skills  []powerEntry   `json:"skills"`
	Agents  []powerEntry   `json:"agents"`
	Plugins []powerEntry   `json:"plugins"`
	MCPs    []powerEntry   `json:"mcps"`
}

// powerEntry 是清单中单条 Power 的 JSON 形状；Description 与 Tags 是安装时捕获的发布元数据，
// 不写入 Power 资源文件本身，仅作为本地清单记录；空值时省略写出以保持向后兼容。
type powerEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

func emptyInstalledPowerGroups() domain.InstalledPowerGroups {
	return domain.InstalledPowerGroups{
		domain.PowerTypeSkill:  make([]domain.InstalledPower, 0),
		domain.PowerTypeAgent:  make([]domain.InstalledPower, 0),
		domain.PowerTypePlugin: make([]domain.InstalledPower, 0),
		domain.PowerTypeMCP:    make([]domain.InstalledPower, 0),
	}
}

func manifestToInstalledPowerGroups(document manifestDocument) domain.InstalledPowerGroups {
	groups := emptyInstalledPowerGroups()
	groups[domain.PowerTypeSkill] = entriesToInstalledPowers(document.Skills, domain.PowerTypeSkill)
	groups[domain.PowerTypeAgent] = entriesToInstalledPowers(document.Agents, domain.PowerTypeAgent)
	groups[domain.PowerTypePlugin] = entriesToInstalledPowers(document.Plugins, domain.PowerTypePlugin)
	groups[domain.PowerTypeMCP] = entriesToInstalledPowers(document.MCPs, domain.PowerTypeMCP)
	return groups
}

func installedPowerGroupsToManifest(agent domain.AgentID, groups domain.InstalledPowerGroups) manifestDocument {
	return manifestDocument{
		Agent:   agent,
		Skills:  installedPowersToEntries(groups[domain.PowerTypeSkill]),
		Agents:  installedPowersToEntries(groups[domain.PowerTypeAgent]),
		Plugins: installedPowersToEntries(groups[domain.PowerTypePlugin]),
		MCPs:    installedPowersToEntries(groups[domain.PowerTypeMCP]),
	}
}

func entriesToInstalledPowers(entries []powerEntry, powerType domain.PowerType) []domain.InstalledPower {
	powers := make([]domain.InstalledPower, 0, len(entries))
	for _, entry := range entries {
		powers = append(powers, domain.InstalledPower{
			Ref:         domain.PowerRef{Name: entry.Name, Type: powerType, Version: entry.Version},
			Description: entry.Description,
			Tags:        normalizeTags(entry.Tags),
		})
	}
	return powers
}

func installedPowersToEntries(powers []domain.InstalledPower) []powerEntry {
	entries := make([]powerEntry, 0, len(powers))
	for _, power := range powers {
		entries = append(entries, powerEntry{
			Name:        power.Ref.Name,
			Version:     power.Ref.Version,
			Description: power.Description,
			Tags:        normalizeTags(power.Tags),
		})
	}
	return entries
}

// normalizeTags 将 nil 规范化为空数组并复制，保证清单写出时 tags 恒为 [] 而非 null，
// 且不共享调用方底层数组。
func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return append([]string{}, tags...)
}

func cloneInstalledPowerGroups(groups domain.InstalledPowerGroups) domain.InstalledPowerGroups {
	cloned := emptyInstalledPowerGroups()
	for _, powerType := range []domain.PowerType{
		domain.PowerTypeSkill,
		domain.PowerTypeAgent,
		domain.PowerTypePlugin,
		domain.PowerTypeMCP,
	} {
		cloned[powerType] = make([]domain.InstalledPower, len(groups[powerType]))
		for index, power := range groups[powerType] {
			cloned[powerType][index] = power.Clone()
		}
	}
	return cloned
}
