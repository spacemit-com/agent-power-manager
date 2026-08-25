package catalog

import (
	"fmt"

	"apm/internal/domain"
)

// Snapshot 是已严格校验的目录只读索引；其查询不访问网络或文件系统。
type Snapshot struct {
	entriesByType map[domain.PowerType][]domain.CatalogEntry
	byRef         map[domain.PowerRef]domain.CatalogEntry
}

func newSnapshot(entriesByType map[domain.PowerType][]domain.CatalogEntry) (Snapshot, error) {
	snapshot := Snapshot{
		entriesByType: make(map[domain.PowerType][]domain.CatalogEntry, len(catalogPowerTypes)),
		byRef:         make(map[domain.PowerRef]domain.CatalogEntry),
	}
	for _, powerType := range catalogPowerTypes {
		entries := entriesByType[powerType]
		cloned := make([]domain.CatalogEntry, len(entries))
		for index, entry := range entries {
			if _, duplicate := snapshot.byRef[entry.Ref]; duplicate {
				return Snapshot{}, fmt.Errorf("duplicate snapshot entry %s/%s/%s: %w", entry.Ref.Type, entry.Ref.Name, entry.Ref.Version, ErrInvalidCatalog)
			}
			cloned[index] = entry.Clone()
			snapshot.byRef[entry.Ref] = cloned[index].Clone()
		}
		snapshot.entriesByType[powerType] = cloned
	}
	return snapshot, nil
}

// List 返回指定类型的有序条目副本；空类型返回全部类型的条目副本。
func (snapshot Snapshot) List(powerType domain.PowerType) []domain.CatalogEntry {
	if powerType == "" {
		entries := make([]domain.CatalogEntry, 0)
		for _, candidateType := range catalogPowerTypes {
			entries = append(entries, cloneEntries(snapshot.entriesByType[candidateType])...)
		}
		return entries
	}
	return cloneEntries(snapshot.entriesByType[powerType])
}

// Resolve 返回精确版本，或在省略版本时返回同类型（或唯一类型）的最高 SemVer 条目副本。
func (snapshot Snapshot) Resolve(ref domain.PowerRef) (domain.CatalogEntry, error) {
	powerType, err := snapshot.resolvePowerType(ref)
	if err != nil {
		return domain.CatalogEntry{}, err
	}
	if ref.Version != "" {
		entry, ok := snapshot.byRef[domain.PowerRef{Type: powerType, Name: ref.Name, Version: ref.Version}]
		if !ok {
			return domain.CatalogEntry{}, fmt.Errorf("catalog entry %s/%s/%s: %w", powerType, ref.Name, ref.Version, ErrNotFound)
		}
		return entry.Clone(), nil
	}
	for _, entry := range snapshot.entriesByType[powerType] {
		if entry.Ref.Name == ref.Name {
			return entry.Clone(), nil
		}
	}
	return domain.CatalogEntry{}, fmt.Errorf("catalog entry %s/%s: %w", powerType, ref.Name, ErrNotFound)
}

// Metadata 返回精确资源版本的元数据副本；不执行 latest 选择。
func (snapshot Snapshot) Metadata(ref domain.PowerRef) (domain.CatalogEntry, error) {
	if !knownPowerType(ref.Type) || ref.Name == "" || ref.Version == "" {
		return domain.CatalogEntry{}, fmt.Errorf("catalog metadata reference %+v is not exact: %w", ref, ErrNotFound)
	}
	entry, ok := snapshot.byRef[ref]
	if !ok {
		return domain.CatalogEntry{}, fmt.Errorf("catalog entry %s/%s/%s: %w", ref.Type, ref.Name, ref.Version, ErrNotFound)
	}
	return entry.Clone(), nil
}

func (snapshot Snapshot) resolvePowerType(ref domain.PowerRef) (domain.PowerType, error) {
	if ref.Name == "" {
		return "", fmt.Errorf("catalog entry with empty name: %w", ErrNotFound)
	}
	if ref.Type != "" {
		if !knownPowerType(ref.Type) {
			return "", fmt.Errorf("catalog entry has unknown power type %q: %w", ref.Type, ErrNotFound)
		}
		return ref.Type, nil
	}
	var found domain.PowerType
	for _, powerType := range catalogPowerTypes {
		for _, entry := range snapshot.entriesByType[powerType] {
			if entry.Ref.Name != ref.Name {
				continue
			}
			if found != "" && found != powerType {
				return "", fmt.Errorf("catalog entry %q has multiple power types: %w", ref.Name, ErrAmbiguous)
			}
			found = powerType
			break
		}
	}
	if found == "" {
		return "", fmt.Errorf("catalog entry %q: %w", ref.Name, ErrNotFound)
	}
	return found, nil
}

func knownPowerType(powerType domain.PowerType) bool {
	for _, candidate := range catalogPowerTypes {
		if powerType == candidate {
			return true
		}
	}
	return false
}

func cloneEntries(entries []domain.CatalogEntry) []domain.CatalogEntry {
	cloned := make([]domain.CatalogEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry.Clone()
	}
	return cloned
}
