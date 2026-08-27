package lifecycle

import (
	"fmt"
	"slices"

	"apm/internal/cli"
	"apm/internal/domain"
)

// resolveUninstallPower 从单个 Agent 的可信清单中解析待卸载能力；同类别重复取清单首项，仅跨类别匹配报歧义，并保持输入清单不变。
func resolveUninstallPower(flags cli.Flags, groups domain.InstalledPowerGroups) (domain.PowerRef, bool, error) {
	types := []domain.PowerType{
		domain.PowerTypeSkill,
		domain.PowerTypePlugin,
		domain.PowerTypeMCP,
	}
	if flags.Type != "" {
		powerType := domain.PowerType(flags.Type)
		for _, installed := range groups[powerType] {
			if installed.Ref.Name == flags.Name {
				return installed.Ref, true, nil
			}
		}
		return domain.PowerRef{}, false, nil
	}

	matches := make([]domain.PowerRef, 0, 1)
	matchedTypes := make([]string, 0, 1)
	for _, powerType := range types {
		for _, installed := range groups[powerType] {
			if installed.Ref.Name == flags.Name {
				matches = append(matches, installed.Ref)
				matchedTypes = append(matchedTypes, string(powerType))
				break
			}
		}
	}
	if len(matches) == 0 {
		return domain.PowerRef{}, false, nil
	}
	if len(matches) > 1 {
		slices.Sort(matchedTypes)
		return domain.PowerRef{}, false, fmt.Errorf(
			"uninstall power %q has multiple power types %v; specify --type",
			flags.Name, matchedTypes,
		)
	}
	return matches[0], true, nil
}
