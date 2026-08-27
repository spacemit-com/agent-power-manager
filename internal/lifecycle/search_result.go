package lifecycle

import (
	"sort"
	"strings"

	"apm/internal/cli"
	"apm/internal/domain"
	"github.com/Masterminds/semver/v3"
	"golang.org/x/text/cases"
)

var searchFold = cases.Fold()

type searchPowerKey struct {
	powerType domain.PowerType
	name      string
}

// catalogPowers 将不可变目录条目复制为保留 description 与 tags 的 CLI 列表记录。
func catalogPowers(entries []domain.CatalogEntry) []cli.ListedPower {
	powers := make([]cli.ListedPower, len(entries))
	for index, entry := range entries {
		powers[index] = cli.ListedPower{Power: entry.Ref, Description: entry.Description, Tags: entry.Tags}
	}
	return powers
}

func filterSearchPowers(flags cli.Flags, powers []cli.ListedPower) []cli.ListedPower {
	keyword := searchFold.String(strings.TrimSpace(flags.Keyword))
	latest := make(map[searchPowerKey]cli.ListedPower)

	for _, power := range powers {
		if flags.Type != "" && power.Power.Type != domain.PowerType(flags.Type) {
			continue
		}
		if !strings.Contains(searchFold.String(power.Power.Name), keyword) {
			continue
		}
		key := searchPowerKey{powerType: power.Power.Type, name: power.Power.Name}
		current, found := latest[key]
		if !found || semver.MustParse(power.Power.Version).GreaterThan(semver.MustParse(current.Power.Version)) {
			latest[key] = power
		}
	}

	result := make([]cli.ListedPower, 0, len(latest))
	for _, power := range latest {
		result = append(result, power)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Power.Type != result[j].Power.Type {
			return result[i].Power.Type < result[j].Power.Type
		}
		return result[i].Power.Name < result[j].Power.Name
	})
	return result
}
