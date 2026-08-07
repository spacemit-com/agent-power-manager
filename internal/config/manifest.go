// Package config 提供 APM 配置清单的占位类型。
package config

import "apm/internal/domain"

// Manifest 表示一个智能体的已安装能力清单。
type Manifest struct {
	Agent  domain.AgentID
	Powers []domain.InstalledPower
}

type manifestDocument struct {
	Agent   domain.AgentID `json:"agent"`
	Skills  []powerEntry   `json:"skills"`
	Plugins []powerEntry   `json:"plugins"`
	MCPs    []powerEntry   `json:"mcps"`
}

type powerEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func emptyPowerGroups() domain.PowerGroups {
	return domain.PowerGroups{
		domain.PowerTypeSkill:  make([]domain.PowerRef, 0),
		domain.PowerTypePlugin: make([]domain.PowerRef, 0),
		domain.PowerTypeMCP:    make([]domain.PowerRef, 0),
	}
}

func manifestToPowerGroups(document manifestDocument) domain.PowerGroups {
	groups := emptyPowerGroups()
	groups[domain.PowerTypeSkill] = entriesToPowerRefs(document.Skills, domain.PowerTypeSkill)
	groups[domain.PowerTypePlugin] = entriesToPowerRefs(document.Plugins, domain.PowerTypePlugin)
	groups[domain.PowerTypeMCP] = entriesToPowerRefs(document.MCPs, domain.PowerTypeMCP)
	return groups
}

func powerGroupsToManifest(agent domain.AgentID, groups domain.PowerGroups) manifestDocument {
	return manifestDocument{
		Agent:   agent,
		Skills:  powerRefsToEntries(groups[domain.PowerTypeSkill]),
		Plugins: powerRefsToEntries(groups[domain.PowerTypePlugin]),
		MCPs:    powerRefsToEntries(groups[domain.PowerTypeMCP]),
	}
}

func entriesToPowerRefs(entries []powerEntry, powerType domain.PowerType) []domain.PowerRef {
	powers := make([]domain.PowerRef, 0, len(entries))
	for _, entry := range entries {
		powers = append(powers, domain.PowerRef{
			Name:    entry.Name,
			Type:    powerType,
			Version: entry.Version,
		})
	}
	return powers
}

func powerRefsToEntries(powers []domain.PowerRef) []powerEntry {
	entries := make([]powerEntry, 0, len(powers))
	for _, power := range powers {
		entries = append(entries, powerEntry{Name: power.Name, Version: power.Version})
	}
	return entries
}

func clonePowerGroups(groups domain.PowerGroups) domain.PowerGroups {
	cloned := emptyPowerGroups()
	for _, powerType := range []domain.PowerType{
		domain.PowerTypeSkill,
		domain.PowerTypePlugin,
		domain.PowerTypeMCP,
	} {
		cloned[powerType] = append([]domain.PowerRef{}, groups[powerType]...)
	}
	return cloned
}
