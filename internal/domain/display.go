package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 资源格式版本常量：v1 是历史格式，v2 在四类资源文件中新增仅用于展示的
// name_zh / name_en / description_zh。提交侧只接受 v2；安装、下载、缓存与目录
// 解码路径同时接受 v1 与 v2，保证已发布的 v1 资源仍可安装（ADR-038）。
const (
	// PowerSchemaVersionV1 是历史资源格式版本，不含展示字段。
	PowerSchemaVersionV1 = 1
	// PowerSchemaVersionV2 是当前资源格式版本，要求展示字段。
	PowerSchemaVersionV2 = 2
)

const (
	// minDisplayNameZHChars 与 maxDisplayNameZHChars 限定中文展示名的字符数（按 Unicode 字符计，不按字节）。
	minDisplayNameZHChars = 2
	maxDisplayNameZHChars = 16
	// maxDisplayNameENChars 限定英文展示名的字符数（英文为可打印 ASCII，字符数等于字节数）。
	maxDisplayNameENChars = 32
	// maxDisplayDescriptionBytes 是 description_zh 允许的最大 UTF-8 字节数。
	maxDisplayDescriptionBytes = 1024
	// asciiPrintableMin/asciiPrintableMax 定义英文展示名允许的可打印 ASCII 区间（0x20 空格到 0x7e 波浪号）。
	asciiPrintableMin = 0x20
	asciiPrintableMax = 0x7e
)

// ErrInvalidDisplayMetadata 表示展示字段不满足格式约束。
var ErrInvalidDisplayMetadata = errors.New("invalid display metadata")

// ValidateDisplayNameZH 校验中文展示名：trim 后非空、2–16 个 Unicode 字符、不含控制字符。
// name_zh 是自由展示文本，允许空格、中文与标点，不是 canonical Power name。
func ValidateDisplayNameZH(value string) error {
	if err := validateDisplayText("name_zh", value, false); err != nil {
		return err
	}
	if count := utf8.RuneCountInString(value); count < minDisplayNameZHChars || count > maxDisplayNameZHChars {
		return fmt.Errorf("display field name_zh is %d characters, want %d-%d: %w",
			count, minDisplayNameZHChars, maxDisplayNameZHChars, ErrInvalidDisplayMetadata)
	}
	return nil
}

// ValidateDisplayNameEN 校验英文展示名：trim 后非空、不超过 32 个字符、全部为可打印 ASCII
// 且至少包含一个 ASCII 字母。name_en 面向英文界面，不是 canonical Power name，
// 因此允许空格、大小写与常规标点，但拒绝非 ASCII 字符。
func ValidateDisplayNameEN(value string) error {
	if err := validateDisplayText("name_en", value, false); err != nil {
		return err
	}
	if count := utf8.RuneCountInString(value); count > maxDisplayNameENChars {
		return fmt.Errorf("display field name_en is %d characters, exceeds %d: %w",
			count, maxDisplayNameENChars, ErrInvalidDisplayMetadata)
	}
	hasLetter := false
	for _, character := range value {
		if character < asciiPrintableMin || character > asciiPrintableMax {
			return fmt.Errorf("display field name_en must be printable ASCII English text: %w", ErrInvalidDisplayMetadata)
		}
		if unicode.IsLetter(character) {
			hasLetter = true
		}
	}
	if !hasLetter {
		return fmt.Errorf("display field name_en must contain at least one ASCII letter: %w", ErrInvalidDisplayMetadata)
	}
	return nil
}

// ValidateDisplayDescriptionZH 校验中文描述：trim 后非空、不超过 1024 字节、不含 NUL 与控制字符
// （允许换行与制表符，与 description 的自由文本语义一致）。
func ValidateDisplayDescriptionZH(value string) error {
	if err := validateDisplayText("description_zh", value, true); err != nil {
		return err
	}
	if len(value) > maxDisplayDescriptionBytes {
		return fmt.Errorf("display field description_zh is %d bytes, exceeds %d bytes: %w",
			len(value), maxDisplayDescriptionBytes, ErrInvalidDisplayMetadata)
	}
	return nil
}

// ValidateOptionalDisplayMetadata 校验可选的展示字段：空值与空字符串视为未提供，
// 只在提供时校验形状。供安装、下载与目录解码路径使用，避免历史资源被新规则拒绝。
func ValidateOptionalDisplayMetadata(nameZH, nameEN, descriptionZH string) error {
	if nameZH != "" {
		if err := ValidateDisplayNameZH(nameZH); err != nil {
			return err
		}
	}
	if nameEN != "" {
		if err := ValidateDisplayNameEN(nameEN); err != nil {
			return err
		}
	}
	if descriptionZH != "" {
		if err := ValidateDisplayDescriptionZH(descriptionZH); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRequiredDisplayMetadata 校验提交侧必填的展示字段：三个字段都必须提供且形状合法。
func ValidateRequiredDisplayMetadata(nameZH, nameEN, descriptionZH string) error {
	for _, field := range []struct {
		name  string
		value string
		label string
	}{
		{name: "name_zh", value: nameZH, label: "Chinese display name"},
		{name: "name_en", value: nameEN, label: "English display name"},
		{name: "description_zh", value: descriptionZH, label: "Chinese description"},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s (%s) is required: %w", field.name, field.label, ErrInvalidDisplayMetadata)
		}
	}
	if err := ValidateDisplayNameZH(nameZH); err != nil {
		return err
	}
	if err := ValidateDisplayNameEN(nameEN); err != nil {
		return err
	}
	return ValidateDisplayDescriptionZH(descriptionZH)
}

// ValidateSubmitSchemaVersion 校验提交侧的资源格式版本必须为 v2。
func ValidateSubmitSchemaVersion(version int) error {
	if version != PowerSchemaVersionV2 {
		return fmt.Errorf("schema_version %d is not submittable; upgrade to %d: %w", version, PowerSchemaVersionV2, ErrInvalidDisplayMetadata)
	}
	return nil
}

// ValidateInstallSchemaVersion 校验安装侧的资源格式版本必须是已支持的 v1 或 v2。
func ValidateInstallSchemaVersion(version int) error {
	if version != PowerSchemaVersionV1 && version != PowerSchemaVersionV2 {
		return fmt.Errorf("schema_version %d is unsupported; want %d or %d: %w", version, PowerSchemaVersionV1, PowerSchemaVersionV2, ErrInvalidDisplayMetadata)
	}
	return nil
}

// validateDisplayText 校验展示文本的通用形状；allowNewlines 为真时放行换行与制表符，不限制字符数。
func validateDisplayText(field, value string, allowNewlines bool) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("display field %s must be non-empty: %w", field, ErrInvalidDisplayMetadata)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("display field %s must not have leading or trailing whitespace: %w", field, ErrInvalidDisplayMetadata)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("display field %s must be valid UTF-8: %w", field, ErrInvalidDisplayMetadata)
	}
	for _, character := range value {
		if allowNewlines && (character == '\n' || character == '\t') {
			continue
		}
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("display field %s must not contain control characters: %w", field, ErrInvalidDisplayMetadata)
		}
	}
	return nil
}
