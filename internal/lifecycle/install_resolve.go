package lifecycle

import (
	"fmt"
	"slices"

	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/domain"
)

// resolveCatalogEntry 仅在同一不可变目录快照中选择类型、版本与权威内容元数据。
func resolveCatalogEntry(flags cli.Flags, snapshot catalog.Snapshot, selector VersionSelector) (domain.CatalogEntry, error) {
	entries := snapshot.List("")
	refs := make([]domain.PowerRef, len(entries))
	for index, entry := range entries {
		refs[index] = entry.Ref
	}
	ref, err := resolveInstallPower(flags, refs, selector)
	if err != nil {
		return domain.CatalogEntry{}, err
	}
	entry, err := snapshot.Resolve(ref)
	if err != nil {
		return domain.CatalogEntry{}, fmt.Errorf("resolve catalog entry for install power %+v: %w", ref, err)
	}
	return entry, nil
}

// resolveInstallPower 在可信引用中解析 Install 的能力类型和目标版本。
func resolveInstallPower(flags cli.Flags, refs []domain.PowerRef, selector VersionSelector) (domain.PowerRef, error) {
	matching := make([]domain.PowerRef, 0)
	for _, ref := range refs {
		if ref.Name == flags.Name {
			matching = append(matching, ref)
		}
	}
	if len(matching) == 0 {
		return domain.PowerRef{}, fmt.Errorf("power %q not found", flags.Name)
	}

	selectedType := domain.PowerType(flags.Type)
	if flags.Type == "" {
		types := make(map[domain.PowerType]struct{}, len(matching))
		for _, ref := range matching {
			types[ref.Type] = struct{}{}
		}
		if flags.Type == "" && len(types) > 1 {
			values := make([]string, 0, len(types))
			for powerType := range types {
				values = append(values, string(powerType))
			}
			slices.Sort(values)
			return domain.PowerRef{}, fmt.Errorf("install power %q has multiple power types %v; specify --type", flags.Name, values)
		}
		for powerType := range types {
			selectedType = powerType
		}
	}

	candidates := make([]string, 0, len(matching))
	for _, ref := range matching {
		if ref.Type == selectedType {
			candidates = append(candidates, ref.Version)
		}
	}
	if len(candidates) == 0 {
		return domain.PowerRef{}, fmt.Errorf("install power %q type %q not found", flags.Name, selectedType)
	}

	selectedVersion, err := selector.Select(candidates, flags.Version)
	if err != nil {
		return domain.PowerRef{}, fmt.Errorf("select version for install power %q type %q: %w", flags.Name, selectedType, err)
	}
	return domain.PowerRef{Name: flags.Name, Type: selectedType, Version: selectedVersion}, nil
}
