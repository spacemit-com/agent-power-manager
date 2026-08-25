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

// catalogRefs 将不可变目录条目映射为保留既有输出结构的 Power 引用。
func catalogRefs(entries []domain.CatalogEntry) []domain.PowerRef {
	refs := make([]domain.PowerRef, len(entries))
	for index, entry := range entries {
		refs[index] = entry.Ref
	}
	return refs
}

func filterSearchPowers(flags cli.Flags, refs []domain.PowerRef) []domain.PowerRef {
	keyword := searchFold.String(strings.TrimSpace(flags.Keyword))
	latest := make(map[searchPowerKey]domain.PowerRef)

	for _, ref := range refs {
		if flags.Type != "" && ref.Type != domain.PowerType(flags.Type) {
			continue
		}
		if !strings.Contains(searchFold.String(ref.Name), keyword) {
			continue
		}
		key := searchPowerKey{powerType: ref.Type, name: ref.Name}
		current, found := latest[key]
		if !found || semver.MustParse(ref.Version).GreaterThan(semver.MustParse(current.Version)) {
			latest[key] = ref
		}
	}

	powers := make([]domain.PowerRef, 0, len(latest))
	for _, ref := range latest {
		powers = append(powers, ref)
	}
	sort.SliceStable(powers, func(i, j int) bool {
		if powers[i].Type != powers[j].Type {
			return powers[i].Type < powers[j].Type
		}
		return powers[i].Name < powers[j].Name
	})
	return powers
}
