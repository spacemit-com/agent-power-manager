package lifecycle

import (
	"sort"

	"apm/internal/cli"
	"apm/internal/domain"
	"github.com/Masterminds/semver/v3"
)

// catalogPowerKey 标识目录条目的逻辑身份；同身份仅保留最高 SemVer 版本。
type catalogPowerKey struct {
	powerType domain.PowerType
	name      string
}

// listedPowerFromCatalogEntry 把目录条目复制为 CLI 记录；发布元数据（description、tags 与
// v2 展示字段）逐字段复制，身份与排序仍只由 Ref 决定。
func listedPowerFromCatalogEntry(entry domain.CatalogEntry) cli.ListedPower {
	return cli.ListedPower{
		Power:         entry.Ref,
		Description:   entry.Description,
		Tags:          entry.Tags,
		NameZH:        entry.NameZH,
		NameEN:        entry.NameEN,
		DescriptionZH: entry.DescriptionZH,
	}
}

// listedPowerFromInstalled 把清单条目复制为 CLI 记录；Agent 维度由调用方给出。
func listedPowerFromInstalled(agent domain.AgentID, power domain.InstalledPower) cli.ListedPower {
	return cli.ListedPower{
		Agent:         agent,
		Power:         power.Ref,
		Description:   power.Description,
		Tags:          power.Tags,
		NameZH:        power.NameZH,
		NameEN:        power.NameEN,
		DescriptionZH: power.DescriptionZH,
	}
}

// makeCatalogListResult 将目录条目复制为 CLI 列表结果；命名 --all 展开全部版本，其余情况按 type/name 保留最高 SemVer 并排序。
func makeCatalogListResult(flags cli.Flags, entries []domain.CatalogEntry) cli.ListResult {
	// --all 仅在指定名称时展开该能力的全部版本；否则沿用每个身份保留最新版本的默认行为。
	if flags.All && flags.Name != "" {
		powers := make([]cli.ListedPower, 0, len(entries))
		for _, entry := range entries {
			if flags.Type != "" && entry.Ref.Type != domain.PowerType(flags.Type) {
				continue
			}
			if entry.Ref.Name != flags.Name {
				continue
			}
			powers = append(powers, listedPowerFromCatalogEntry(entry))
		}
		sortListedPowers(powers)
		return cli.ListResult{Powers: powers}
	}

	latest := make(map[catalogPowerKey]cli.ListedPower)
	for _, entry := range entries {
		if flags.Type != "" && entry.Ref.Type != domain.PowerType(flags.Type) {
			continue
		}
		if flags.Name != "" && entry.Ref.Name != flags.Name {
			continue
		}
		power := listedPowerFromCatalogEntry(entry)
		key := catalogPowerKey{powerType: entry.Ref.Type, name: entry.Ref.Name}
		current, found := latest[key]
		if !found || semver.MustParse(power.Power.Version).GreaterThan(semver.MustParse(current.Power.Version)) {
			latest[key] = power
		}
	}

	powers := make([]cli.ListedPower, 0, len(latest))
	for _, power := range latest {
		powers = append(powers, power)
	}
	sortListedPowers(powers)
	return cli.ListResult{Powers: powers}
}

// makeInstalledListResult 展平各 Agent 能力，按可选 type/name 精确过滤并完成排序。
func makeInstalledListResult(flags cli.Flags, inputs []agentPowerGroups) cli.ListResult {
	powers := make([]cli.ListedPower, 0)
	for _, input := range inputs {
		powers = append(powers, filterAgentPowers(input.agent, flags, input.groups)...)
	}

	sortListedPowers(powers)
	return cli.ListResult{Powers: powers}
}

// filterAgentPowers 将单个 Agent 的已安装能力按可选 type/name 精确过滤为 CLI 列表记录。
func filterAgentPowers(agent domain.AgentID, flags cli.Flags, groups domain.InstalledPowerGroups) []cli.ListedPower {
	powers := make([]cli.ListedPower, 0)
	for _, installed := range groups {
		for _, power := range installed {
			if flags.Type != "" && power.Ref.Type != domain.PowerType(flags.Type) {
				continue
			}
			if flags.Name != "" && power.Ref.Name != flags.Name {
				continue
			}
			powers = append(powers, listedPowerFromInstalled(agent, power))
		}
	}
	return powers
}

func sortListedPowers(powers []cli.ListedPower) {
	sort.SliceStable(powers, func(i, j int) bool {
		left, right := powers[i], powers[j]
		if left.Agent != right.Agent {
			return left.Agent < right.Agent
		}
		if left.Power.Type != right.Power.Type {
			return left.Power.Type < right.Power.Type
		}
		if left.Power.Name != right.Power.Name {
			return left.Power.Name < right.Power.Name
		}
		if left.Power.Version == right.Power.Version {
			return false
		}
		return semver.MustParse(left.Power.Version).GreaterThan(semver.MustParse(right.Power.Version))
	})
}
