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

// makeCatalogListResult 将目录条目复制为 CLI 列表结果，按可选 type/name 精确过滤、按类型与名称去重仅保留最高 SemVer 版本并完成排序。
func makeCatalogListResult(flags cli.Flags, entries []domain.CatalogEntry) cli.ListResult {
	latest := make(map[catalogPowerKey]cli.ListedPower)
	for _, entry := range entries {
		if flags.Type != "" && entry.Ref.Type != domain.PowerType(flags.Type) {
			continue
		}
		if flags.Name != "" && entry.Ref.Name != flags.Name {
			continue
		}
		power := cli.ListedPower{Power: entry.Ref, Description: entry.Description, Tags: entry.Tags}
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
			powers = append(powers, cli.ListedPower{Agent: agent, Power: power.Ref, Description: power.Description, Tags: power.Tags})
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
