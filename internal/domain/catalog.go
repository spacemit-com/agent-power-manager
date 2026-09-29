package domain

import (
	"fmt"
	"strings"
)

// CatalogVersion 表示目录原始 JSON 字节的内容地址及其长度。
type CatalogVersion struct {
	SHA256 string
	Size   int64
}

// Validate 校验目录版本使用小写 SHA-256，且长度不为负数。
func (version CatalogVersion) Validate() error {
	if !isLowerHex(version.SHA256, 64) {
		return fmt.Errorf("catalog SHA-256 %q must be 64 lowercase hexadecimal characters", version.SHA256)
	}
	if version.Size < 0 {
		return fmt.Errorf("catalog size %d must not be negative", version.Size)
	}
	return nil
}

// CatalogEntry 表示目录中一个已发布的精确 Power 版本及其内容元数据。
// NameZH/NameEN/DescriptionZH 是 v2 起仅供展示的可选本地化字段：缺失即空串，
// 不参与身份、排序、缓存键或内容 MD5；Ref 仍是唯一身份（ADR-024、ADR-040）。
type CatalogEntry struct {
	Ref           PowerRef
	Description   string
	Message       string
	ContentType   string
	ContentSize   int64
	ContentMD5    string
	PublishedAt   string
	Tags          []string
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// Clone 返回不共享 Tags 底层数组的条目副本，供不可变快照向调用方暴露数据。
func (entry CatalogEntry) Clone() CatalogEntry {
	if entry.Tags != nil {
		entry.Tags = append([]string{}, entry.Tags...)
	}
	return entry
}

// Validate 校验目录条目的精确身份、内容元数据及有序去重标签；不清洗输入值。
func (entry CatalogEntry) Validate() error {
	if !isCatalogPowerType(entry.Ref.Type) {
		return fmt.Errorf("catalog entry has unknown power type %q", entry.Ref.Type)
	}
	if !IsCanonicalPowerName(entry.Ref.Name) {
		return fmt.Errorf("catalog entry name %q is not canonical", entry.Ref.Name)
	}
	if _, err := CanonicalPowerVersion(entry.Ref.Version); err != nil {
		return fmt.Errorf("catalog entry version %q: %w", entry.Ref.Version, err)
	}
	for _, field := range []struct {
		name     string
		value    string
		optional bool
	}{
		{name: "description", value: entry.Description},
		{name: "message", value: entry.Message, optional: entry.Ref.Type == PowerTypeMCP},
		{name: "content type", value: entry.ContentType},
	} {
		if field.optional && field.value == "" {
			continue
		}
		if field.value == "" || strings.TrimSpace(field.value) != field.value {
			return fmt.Errorf("catalog entry %s must be non-empty and trimmed", field.name)
		}
	}
	if entry.ContentSize <= 0 {
		return fmt.Errorf("catalog entry content size %d must be positive", entry.ContentSize)
	}
	if entry.ContentType != catalogContentType(entry.Ref.Type) {
		return fmt.Errorf("catalog entry content type %q does not match power type %q", entry.ContentType, entry.Ref.Type)
	}
	if !isLowerHex(entry.ContentMD5, 32) {
		return fmt.Errorf("catalog entry MD5 %q must be 32 lowercase hexadecimal characters", entry.ContentMD5)
	}
	if !isCatalogPublishedAt(entry.PublishedAt) {
		return fmt.Errorf("catalog entry published at %q must be RFC3339 or ISO 8601 without timezone", entry.PublishedAt)
	}
	if entry.Tags == nil {
		return fmt.Errorf("catalog entry tags must be a non-null array")
	}
	for index, tag := range entry.Tags {
		if tag == "" || strings.TrimSpace(tag) != tag {
			return fmt.Errorf("catalog entry tag %d must be non-empty and trimmed", index)
		}
		if len(tag) > 32 || !IsCanonicalPowerName(tag) {
			return fmt.Errorf("catalog entry tag %q must be a 1-32 byte lowercase kebab name", tag)
		}
		if index > 0 && entry.Tags[index-1] >= tag {
			return fmt.Errorf("catalog entry tags must be sorted and unique")
		}
	}
	// 展示字段是 v2 起的可选追加字段：缺失（空串）合法，提供时校验形状。
	// 因为历史 v1 目录条目没有它们，安装侧不能因此拒绝已发布资源（ADR-038/ADR-040）。
	if err := ValidateOptionalDisplayMetadata(entry.NameZH, entry.NameEN, entry.DescriptionZH); err != nil {
		return fmt.Errorf("catalog entry display metadata: %w", err)
	}
	return nil
}

func isCatalogPowerType(powerType PowerType) bool {
	switch powerType {
	case PowerTypeSkill, PowerTypePlugin, PowerTypeMCP, PowerTypeAgent:
		return true
	default:
		return false
	}
}

func catalogContentType(powerType PowerType) string {
	switch powerType {
	case PowerTypeSkill, PowerTypePlugin, PowerTypeAgent:
		return "application/gzip"
	case PowerTypeMCP:
		return "application/json"
	default:
		return ""
	}
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range []byte(value) {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}

// isCatalogPublishedAt 接受 RFC3339（含纳秒）或测试环境实际返回的无时区 ISO 8601 时间戳；
// 格式定义复用 ParseAPCTimestamp，避免与 submission 时间戳校验漂移。
func isCatalogPublishedAt(value string) bool {
	_, err := ParseAPCTimestamp(value)
	return err == nil
}
