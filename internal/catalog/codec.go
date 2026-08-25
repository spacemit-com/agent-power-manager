package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
	"github.com/Masterminds/semver/v3"
)

// MaxCatalogSize 是接受或生成的完整 catalog JSON 的最大字节数。
const MaxCatalogSize int64 = 100 << 20

const catalogSchemaVersion = 1

var catalogPowerTypes = []domain.PowerType{
	domain.PowerTypeSkill,
	domain.PowerTypePlugin,
	domain.PowerTypeMCP,
	domain.PowerTypeAgent,
}

type catalogDTO struct {
	SchemaVersion int       `json:"schema_version"`
	Skills        *groupDTO `json:"skills"`
	Plugins       *groupDTO `json:"plugins"`
	MCPs          *groupDTO `json:"mcps"`
	Agents        *groupDTO `json:"agents"`
}

type groupDTO struct {
	SchemaVersion int       `json:"schema_version"`
	Items         []itemDTO `json:"items"`
}

type itemDTO struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Message     string   `json:"message"`
	ContentType string   `json:"content_type"`
	ContentSize int64    `json:"content_size"`
	ContentMD5  string   `json:"content_md5"`
	PublishedAt string   `json:"published_at"`
	Tags        []string `json:"tags"`
}

// Decode 严格解码一个 catalog JSON v1，并将其构造成不执行 I/O 的不可变快照。
func Decode(data []byte) (Snapshot, error) {
	if int64(len(data)) > MaxCatalogSize {
		return Snapshot{}, fmt.Errorf("catalog size %d exceeds %d bytes: %w", len(data), MaxCatalogSize, ErrInvalidCatalog)
	}
	if err := validateExactCatalogJSONKeys(data); err != nil {
		return Snapshot{}, fmt.Errorf("validate catalog JSON field names: %w", fmt.Errorf("%w: %w", ErrInvalidCatalog, err))
	}
	var dto catalogDTO
	if err := jsonstrict.Decode(data, &dto, true); err != nil {
		return Snapshot{}, fmt.Errorf("decode catalog JSON: %w", fmt.Errorf("%w: %w", ErrInvalidCatalog, err))
	}
	if dto.SchemaVersion != catalogSchemaVersion {
		return Snapshot{}, fmt.Errorf("catalog schema version %d must be %d: %w", dto.SchemaVersion, catalogSchemaVersion, ErrInvalidCatalog)
	}

	groups := []struct {
		powerType domain.PowerType
		name      string
		group     *groupDTO
	}{
		{powerType: domain.PowerTypeSkill, name: "skills", group: dto.Skills},
		{powerType: domain.PowerTypePlugin, name: "plugins", group: dto.Plugins},
		{powerType: domain.PowerTypeMCP, name: "mcps", group: dto.MCPs},
		{powerType: domain.PowerTypeAgent, name: "agents", group: dto.Agents},
	}
	entriesByType := make(map[domain.PowerType][]domain.CatalogEntry, len(groups))
	for _, group := range groups {
		entries, err := decodeGroup(group.powerType, group.name, group.group)
		if err != nil {
			return Snapshot{}, err
		}
		entriesByType[group.powerType] = entries
	}
	return newSnapshot(entriesByType)
}

// Encode 校验条目、按目录规范排序，并输出一个确定性的紧凑 catalog JSON v1。
func Encode(entries []domain.CatalogEntry) ([]byte, error) {
	groups := make(map[domain.PowerType][]domain.CatalogEntry, len(catalogPowerTypes))
	for _, powerType := range catalogPowerTypes {
		groups[powerType] = make([]domain.CatalogEntry, 0)
	}
	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("validate catalog entry %s/%s/%s: %w", entry.Ref.Type, entry.Ref.Name, entry.Ref.Version, fmt.Errorf("%w: %w", ErrInvalidCatalog, err))
		}
		groups[entry.Ref.Type] = append(groups[entry.Ref.Type], entry.Clone())
	}
	for powerType, group := range groups {
		sortCatalogEntries(group)
		if err := validateDistinctEntries(group); err != nil {
			return nil, fmt.Errorf("encode %s catalog group: %w", powerType, err)
		}
		groups[powerType] = group
	}
	dto := catalogDTO{
		SchemaVersion: catalogSchemaVersion,
		Skills:        encodeGroup(groups[domain.PowerTypeSkill]),
		Plugins:       encodeGroup(groups[domain.PowerTypePlugin]),
		MCPs:          encodeGroup(groups[domain.PowerTypeMCP]),
		Agents:        encodeGroup(groups[domain.PowerTypeAgent]),
	}
	data, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("marshal catalog JSON: %w", err)
	}
	if int64(len(data)) > MaxCatalogSize {
		return nil, fmt.Errorf("encoded catalog size %d exceeds %d bytes: %w", len(data), MaxCatalogSize, ErrInvalidCatalog)
	}
	return data, nil
}

func decodeGroup(powerType domain.PowerType, name string, group *groupDTO) ([]domain.CatalogEntry, error) {
	if group == nil {
		return nil, fmt.Errorf("catalog group %q is required: %w", name, ErrInvalidCatalog)
	}
	if group.SchemaVersion != catalogSchemaVersion {
		return nil, fmt.Errorf("catalog group %q schema version %d must be %d: %w", name, group.SchemaVersion, catalogSchemaVersion, ErrInvalidCatalog)
	}
	if group.Items == nil {
		return nil, fmt.Errorf("catalog group %q items must be an array: %w", name, ErrInvalidCatalog)
	}
	entries := make([]domain.CatalogEntry, len(group.Items))
	for index, item := range group.Items {
		if item.Tags == nil {
			return nil, fmt.Errorf("catalog group %q item %d tags must be an array: %w", name, index, ErrInvalidCatalog)
		}
		entry := domain.CatalogEntry{
			Ref:         domain.PowerRef{Type: powerType, Name: item.Name, Version: item.Version},
			Description: item.Description,
			Message:     item.Message,
			ContentType: item.ContentType,
			ContentSize: item.ContentSize,
			ContentMD5:  item.ContentMD5,
			PublishedAt: item.PublishedAt,
			Tags:        cloneTags(item.Tags),
		}
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("validate catalog group %q item %d: %w", name, index, fmt.Errorf("%w: %w", ErrInvalidCatalog, err))
		}
		entries[index] = entry
	}
	if err := validateCatalogOrder(entries); err != nil {
		return nil, fmt.Errorf("catalog group %q: %w", name, err)
	}
	return entries, nil
}

func encodeGroup(entries []domain.CatalogEntry) *groupDTO {
	items := make([]itemDTO, len(entries))
	for index, entry := range entries {
		items[index] = itemDTO{
			Name:        entry.Ref.Name,
			Version:     entry.Ref.Version,
			Description: entry.Description,
			Message:     entry.Message,
			ContentType: entry.ContentType,
			ContentSize: entry.ContentSize,
			ContentMD5:  entry.ContentMD5,
			PublishedAt: entry.PublishedAt,
			Tags:        cloneTags(entry.Tags),
		}
	}
	return &groupDTO{SchemaVersion: catalogSchemaVersion, Items: items}
}

func validateCatalogOrder(entries []domain.CatalogEntry) error {
	if err := validateDistinctEntries(entries); err != nil {
		return err
	}
	for index := 1; index < len(entries); index++ {
		if catalogEntryLess(entries[index], entries[index-1]) {
			return fmt.Errorf("items must be ordered by name ascending and version descending: %w", ErrInvalidCatalog)
		}
	}
	return nil
}

func validateDistinctEntries(entries []domain.CatalogEntry) error {
	seen := make(map[domain.PowerRef]struct{}, len(entries))
	for _, entry := range entries {
		if _, duplicate := seen[entry.Ref]; duplicate {
			return fmt.Errorf("duplicate catalog entry %s/%s/%s: %w", entry.Ref.Type, entry.Ref.Name, entry.Ref.Version, ErrInvalidCatalog)
		}
		seen[entry.Ref] = struct{}{}
	}
	return nil
}

func sortCatalogEntries(entries []domain.CatalogEntry) {
	sort.Slice(entries, func(left, right int) bool {
		return catalogEntryLess(entries[left], entries[right])
	})
}

func catalogEntryLess(left, right domain.CatalogEntry) bool {
	if left.Ref.Name != right.Ref.Name {
		return left.Ref.Name < right.Ref.Name
	}
	leftVersion := semver.MustParse(left.Ref.Version[1:])
	rightVersion := semver.MustParse(right.Ref.Version[1:])
	return leftVersion.GreaterThan(rightVersion)
}

type catalogObjectKind uint8

const (
	catalogTopObject catalogObjectKind = iota
	catalogGroupObject
	catalogItemObject
)

// validateExactCatalogJSONKeys 在 encoding/json 解码前校验每层对象的精确字段拼写，
// 避免其大小写折叠规则将不属于 v1 的字段静默接受。
func validateExactCatalogJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	return validateCatalogObject(decoder, catalogTopObject)
}

func validateCatalogObject(decoder *json.Decoder, kind catalogObjectKind) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("catalog object must begin with '{'")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("catalog object key has type %T", keyToken)
		}
		if !catalogObjectAllowsKey(kind, key) {
			return fmt.Errorf("unknown catalog field %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate catalog field %q", key)
		}
		seen[key] = struct{}{}

		switch {
		case kind == catalogTopObject && catalogGroupKey(key):
			if err := validateCatalogObject(decoder, catalogGroupObject); err != nil {
				return err
			}
		case kind == catalogGroupObject && key == "items":
			if err := validateCatalogItems(decoder); err != nil {
				return err
			}
		default:
			if err := skipCatalogJSONValue(decoder); err != nil {
				return err
			}
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return fmt.Errorf("catalog object must end with '}'")
	}
	return nil
}

func validateCatalogItems(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return fmt.Errorf("catalog items must be an array")
	}
	for decoder.More() {
		if err := validateCatalogObject(decoder, catalogItemObject); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return fmt.Errorf("catalog items must end with ']'")
	}
	return nil
}

func skipCatalogJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := skipCatalogJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := skipCatalogJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func catalogObjectAllowsKey(kind catalogObjectKind, key string) bool {
	switch kind {
	case catalogTopObject:
		return key == "schema_version" || catalogGroupKey(key)
	case catalogGroupObject:
		return key == "schema_version" || key == "items"
	case catalogItemObject:
		switch key {
		case "name", "version", "description", "message", "content_type", "content_size", "content_md5", "published_at", "tags":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func catalogGroupKey(key string) bool {
	switch key {
	case "skills", "plugins", "mcps", "agents":
		return true
	default:
		return false
	}
}

func cloneTags(tags []string) []string {
	if tags == nil {
		return nil
	}
	return append([]string{}, tags...)
}
