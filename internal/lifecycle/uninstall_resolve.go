package lifecycle

import (
	"fmt"
	"slices"

	"apm/internal/cli"
	"apm/internal/domain"
)

// resolveUninstallPower 从单个 Agent 的可信清单中解析待卸载能力，保持输入清单不变。
func resolveUninstallPower(flags cli.Flags, groups domain.PowerGroups) (domain.PowerRef, bool, error) {
	types := []domain.PowerType{
		domain.PowerTypeSkill,
		domain.PowerTypePlugin,
		domain.PowerTypeMCP,
	}
	if flags.Type != "" {
		powerType := domain.PowerType(flags.Type)
		for _, ref := range groups[powerType] {
			if ref.Name == flags.Name {
				return ref, true, nil
			}
		}
		return domain.PowerRef{}, false, nil
	}

	matches := make([]domain.PowerRef, 0, 1)
	for _, powerType := range types {
		for _, ref := range groups[powerType] {
			if ref.Name == flags.Name {
				matches = append(matches, ref)
			}
		}
	}
	if len(matches) == 0 {
		return domain.PowerRef{}, false, nil
	}
	if len(matches) > 1 {
		values := make([]string, 0, len(matches))
		for _, ref := range matches {
			values = append(values, string(ref.Type))
		}
		slices.Sort(values)
		return domain.PowerRef{}, false, fmt.Errorf(
			"uninstall power %q has multiple power types %v; specify --type",
			flags.Name, values,
		)
	}
	return matches[0], true, nil
}
