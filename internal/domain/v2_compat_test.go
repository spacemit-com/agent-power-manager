package domain

import (
	"strings"
	"testing"
)

// TestInstallPathAcceptsV1Resources 锁定安装侧的双版本兼容：
// v1 的 MCP 与 agent.yaml 仍然可解码并通过校验，只是不能提交（ADR-038）。
func TestInstallPathAcceptsV1Resources(t *testing.T) {
	t.Run("mcp v1 without display fields", func(t *testing.T) {
		raw := `{"schema_version":1,"name":"filesystem","version":"v0.0.1","description":"Filesystem MCP server","definition":{"transport":"stdio","command":"npx"}}`
		resource, err := DecodeMCPResourceJSON([]byte(raw))
		if err != nil {
			t.Fatalf("DecodeMCPResourceJSON(v1) error = %v, want nil", err)
		}
		if resource.SchemaVersion != PowerSchemaVersionV1 || resource.NameZH != "" || resource.NameEN != "" || resource.DescriptionZH != "" {
			t.Fatalf("DecodeMCPResourceJSON(v1) = %+v, want v1 with empty display fields", resource)
		}
		if err := resource.ValidateSubmit(); err == nil {
			t.Fatal("ValidateSubmit(v1) error = nil, want a submit-side rejection")
		}
	})

	t.Run("mcp v2 with display fields", func(t *testing.T) {
		raw := `{"schema_version":2,"name":"filesystem","name_zh":"文件系统","name_en":"Filesystem MCP","version":"v0.0.1","description":"Filesystem MCP server","description_zh":"文件系统服务","definition":{"transport":"stdio","command":"npx"}}`
		resource, err := DecodeMCPResourceJSON([]byte(raw))
		if err != nil {
			t.Fatalf("DecodeMCPResourceJSON(v2) error = %v, want nil", err)
		}
		if err := resource.ValidateSubmit(); err != nil {
			t.Fatalf("ValidateSubmit(v2) error = %v, want nil", err)
		}
	})

	t.Run("agent v1 without display fields", func(t *testing.T) {
		raw := "schema_version: 1\nagent:\n  id: demo-agent\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n"
		config, err := DecodeAgentConfigYAML([]byte(raw))
		if err != nil {
			t.Fatalf("DecodeAgentConfigYAML(v1) error = %v, want nil", err)
		}
		if config.Agent.NameZH != "" || config.Agent.NameEN != "" || config.Agent.DescriptionZH != "" {
			t.Fatalf("DecodeAgentConfigYAML(v1) = %+v, want empty display fields", config.Agent)
		}
		if err := config.ValidateSubmit(); err == nil {
			t.Fatal("ValidateSubmit(v1) error = nil, want a submit-side rejection")
		}
	})

	t.Run("agent v2 requires display fields on submit", func(t *testing.T) {
		raw := "schema_version: 2\nagent:\n  id: demo-agent\n  name: demo-agent\n  version: v1.0.0\n  name_zh: 演示名称\n  name_en: Demo Agent\n  description: demo\n  description_zh: 演示\n  soul: help safely\n"
		config, err := DecodeAgentConfigYAML([]byte(raw))
		if err != nil {
			t.Fatalf("DecodeAgentConfigYAML(v2) error = %v, want nil", err)
		}
		if err := config.ValidateSubmit(); err != nil {
			t.Fatalf("ValidateSubmit(v2) error = %v, want nil", err)
		}
	})
}

// TestSubmitSchemaVersionGate 锁定提交侧版本门禁与安装侧兼容范围的差异。
func TestSubmitSchemaVersionGate(t *testing.T) {
	for _, version := range []int{PowerSchemaVersionV1, PowerSchemaVersionV2} {
		if err := ValidateInstallSchemaVersion(version); err != nil {
			t.Fatalf("ValidateInstallSchemaVersion(%d) error = %v, want nil", version, err)
		}
	}
	for _, version := range []int{0, 3} {
		if err := ValidateInstallSchemaVersion(version); err == nil {
			t.Fatalf("ValidateInstallSchemaVersion(%d) error = nil, want rejection", version)
		}
	}
	if err := ValidateSubmitSchemaVersion(PowerSchemaVersionV2); err != nil {
		t.Fatalf("ValidateSubmitSchemaVersion(2) error = %v, want nil", err)
	}
	if err := ValidateSubmitSchemaVersion(PowerSchemaVersionV1); err == nil {
		t.Fatal("ValidateSubmitSchemaVersion(1) error = nil, want rejection")
	}
}

// TestDisplayMetadataRequiredFields 锁定提交侧三个展示字段的必填性。
func TestDisplayMetadataRequiredFields(t *testing.T) {
	validZH, validEN := "演示名称", "Demo Name"
	if err := ValidateRequiredDisplayMetadata(validZH, validEN, "演示描述"); err != nil {
		t.Fatalf("ValidateRequiredDisplayMetadata(valid) error = %v", err)
	}
	for _, field := range []struct {
		name                          string
		nameZH, nameEN, descriptionZH string
	}{
		{name: "empty name_zh", nameZH: "", nameEN: validEN, descriptionZH: "演示描述"},
		{name: "empty name_en", nameZH: validZH, nameEN: "", descriptionZH: "演示描述"},
		{name: "empty description_zh", nameZH: validZH, nameEN: validEN, descriptionZH: ""},
		{name: "blank name_en", nameZH: validZH, nameEN: "  ", descriptionZH: "演示描述"},
	} {
		t.Run(field.name, func(t *testing.T) {
			if err := ValidateRequiredDisplayMetadata(field.nameZH, field.nameEN, field.descriptionZH); err == nil {
				t.Fatal("ValidateRequiredDisplayMetadata() error = nil, want rejection")
			}
		})
	}
}

// TestDisplayNameZHLengthBounds 锁定 name_zh 的 2–16 字符窗口（按 Unicode 字符计，不按字节）。
func TestDisplayNameZHLengthBounds(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "one character", value: "演", valid: false},
		{name: "two characters", value: "演示", valid: true},
		{name: "sixteen characters", value: "一二三四五六七八九十一二三四五六", valid: true},
		{name: "seventeen characters", value: "一二三四五六七八九十一二三四五六七", valid: false},
		{name: "sixteen mixed", value: "演示 Demo 名称一二三四五", valid: true},
		{name: "newline", value: "演示\n名称", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDisplayNameZH(test.value)
			if test.valid && err != nil {
				t.Fatalf("ValidateDisplayNameZH(%q) error = %v, want nil", test.value, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("ValidateDisplayNameZH(%q) error = nil, want rejection", test.value)
			}
		})
	}
}

// TestDisplayNameENLengthAndCharset 锁定 name_en 的 ≤32 字符上限与可打印 ASCII 字母要求。
func TestDisplayNameENLengthAndCharset(t *testing.T) {
	long := ""
	for index := 0; index < maxDisplayNameENChars+1; index++ {
		long += "a"
	}
	exact := long[:maxDisplayNameENChars]
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "short", value: "Demo", valid: true},
		{name: "with spaces and punctuation", value: "Foo Before Summary (v2)", valid: true},
		{name: "exactly max", value: exact, valid: true},
		{name: "one over max", value: long, valid: false},
		{name: "chinese characters", value: "演示", valid: false},
		{name: "digits only", value: "12345", valid: false},
		{name: "punctuation only", value: "---", valid: false},
		{name: "newline", value: "Demo\nName", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDisplayNameEN(test.value)
			if test.valid && err != nil {
				t.Fatalf("ValidateDisplayNameEN(%q) error = %v, want nil", test.value, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("ValidateDisplayNameEN(%q) error = nil, want rejection", test.value)
			}
		})
	}
}

// TestOptionalDisplayMetadataSkipsEmptyFields 锁定安装侧宽松语义：
// 三个字段全部为空时视为 v1 兼容资源，不做任何校验。
func TestOptionalDisplayMetadataSkipsEmptyFields(t *testing.T) {
	if err := ValidateOptionalDisplayMetadata("", "", ""); err != nil {
		t.Fatalf("ValidateOptionalDisplayMetadata(empty) error = %v, want nil", err)
	}
	// 提供即为非法时不因"可选"而放行。
	if err := ValidateOptionalDisplayMetadata("演", "", ""); err == nil {
		t.Fatal("ValidateOptionalDisplayMetadata(short name_zh) error = nil, want rejection")
	}
	if err := ValidateOptionalDisplayMetadata("演示", "演示", ""); err == nil {
		t.Fatal("ValidateOptionalDisplayMetadata(non-ASCII name_en) error = nil, want rejection")
	}
}

// TestDescriptionLengthLimit 锁定 description 与 description_zh 的 500 码元上限（ADR-042）。
// 上限来自 APC 服务端实测：两个字段各 500 码元通过、501 码元返回 400 VALIDATION_FAILED；
// 计数单位是 UTF-16 码元，因此 500 个汉字（1500 字节）通过，而 251 个 emoji（502 码元、
// 1004 字节）被拒——按字节判断会得出相反的结论。
func TestDescriptionLengthLimit(t *testing.T) {
	atLimit := strings.Repeat("描", 500)
	overLimit := strings.Repeat("描", 501)
	// 500 个汉字 = 1500 字节；按字节判断会被误拒，必须放行。
	if err := ValidateDisplayDescriptionZH(atLimit); err != nil {
		t.Fatalf("ValidateDisplayDescriptionZH(500 CJK) error = %v, want nil", err)
	}
	if err := ValidateDescription(atLimit); err != nil {
		t.Fatalf("ValidateDescription(500 CJK) error = %v, want nil", err)
	}
	// 501 个汉字 = 501 码元，超限。
	if err := ValidateDisplayDescriptionZH(overLimit); err == nil {
		t.Fatal("ValidateDisplayDescriptionZH(501 CJK) error = nil, want rejection")
	}
	if err := ValidateDescription(overLimit); err == nil {
		t.Fatal("ValidateDescription(501 CJK) error = nil, want rejection")
	}
	// 代理对按 2 个码元计：250 个 emoji 正好 500 码元通过，251 个即 502 码元被拒。
	if err := ValidateDescription(strings.Repeat("😀", 250)); err != nil {
		t.Fatalf("ValidateDescription(250 emoji = 500 units) error = %v, want nil", err)
	}
	if err := ValidateDescription(strings.Repeat("😀", 251)); err == nil {
		t.Fatal("ValidateDescription(251 emoji = 502 units) error = nil, want rejection")
	}
	// BMP 之外的补码字符只占 1 个 Unicode 码点但占 2 个码元，防止实现退回 RuneCountInString。
	if err := ValidateDescription(strings.Repeat("é", 500)); err != nil {
		t.Fatalf("ValidateDescription(500 é) error = %v, want nil", err)
	}
	if err := ValidateDescription(strings.Repeat("é", 501)); err == nil {
		t.Fatal("ValidateDescription(501 é) error = nil, want rejection")
	}
}
