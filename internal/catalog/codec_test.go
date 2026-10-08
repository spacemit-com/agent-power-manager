package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestDecodeStrictCatalogV1(t *testing.T) {
	valid := validCatalogJSON()
	tests := []struct {
		name string
		data []byte
	}{
		{name: "top schema mismatch", data: replaceJSON(t, valid, `"schema_version":1`, `"schema_version":2`)},
		{name: "group schema mismatch", data: replaceJSON(t, valid, `"skills":{"schema_version":1`, `"skills":{"schema_version":2`)},
		{name: "missing item field", data: replaceJSON(t, valid, `,"message":"skill message"`, ``)},
		{name: "unknown item field", data: replaceJSON(t, valid, `"name":"alpha"`, `"unknown":true,"name":"alpha"`)},
		{name: "duplicate item field", data: replaceJSON(t, valid, `"name":"alpha"`, `"name":"alpha","name":"other"`)},
		{name: "missing group", data: replaceJSON(t, valid, `,"agents":{"schema_version":1,"items":[{"name":"agent"`, `,"agents_removed":{"schema_version":1,"items":[{"name":"agent"`)},
		{name: "null items", data: []byte(`{"schema_version":1,"skills":{"schema_version":1,"items":[]},"plugins":{"schema_version":1,"items":null},"mcps":{"schema_version":1,"items":[]},"agents":{"schema_version":1,"items":[]}}`)},
		{name: "duplicate ref", data: replaceJSON(t, valid, `]}}`, `,{"name":"agent","version":"v1.0.0","description":"agent description","message":"agent message","content_type":"application/gzip","content_size":4,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":["agent"]}]}}`)},
		{name: "bad item order", data: replaceJSON(t, valid, `"name":"alpha","version":"v2.0.0"`, `"name":"zulu","version":"v2.0.0"`)},
		{name: "bad tag order", data: replaceJSON(t, valid, `"tags":["alpha","catalog"]`, `"tags":["catalog","alpha"]`)},
		{name: "invalid metadata", data: replaceJSON(t, valid, `"content_size":2`, `"content_size":0`)},
	}
	if _, err := Decode(valid); err != nil {
		t.Fatalf("Decode(valid) error = %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(tt.data); err == nil {
				t.Fatal("Decode() error = nil, want strict validation failure")
			}
		})
	}
}

func TestDecodeRejectsCaseVariantCatalogFields(t *testing.T) {
	valid := validCatalogJSON()
	tests := []struct {
		name string
		data []byte
	}{
		{name: "top schema version", data: replaceJSON(t, valid, `"schema_version":1`, `"SCHEMA_VERSION":1`)},
		{name: "group schema version", data: replaceJSON(t, valid, `"skills":{"schema_version":1`, `"skills":{"SCHEMA_VERSION":1`)},
		{name: "group items", data: replaceJSON(t, valid, `"skills":{"schema_version":1,"items":`, `"skills":{"schema_version":1,"ITEMS":`)},
		{name: "item name", data: replaceJSON(t, valid, `"name":"alpha"`, `"NAME":"alpha"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(tt.data); err == nil {
				t.Fatal("Decode() error = nil, want exact field-name validation failure")
			}
		})
	}
}

func TestEncodeDecodeClosesOverEmptyTags(t *testing.T) {
	entry := catalogEntriesFixture()[0].Clone()
	entry.Tags = []string{}
	encoded, err := Encode([]domain.CatalogEntry{entry})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"tags":[]`)) {
		t.Fatalf("Encode() = %s, want non-null empty tags", encoded)
	}
	snapshot, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode(Encode()) error = %v", err)
	}
	got := snapshot.List(domain.PowerTypeSkill)
	if len(got) != 1 || got[0].Tags == nil || len(got[0].Tags) != 0 {
		t.Fatalf("Decode(Encode()).List() = %#v, want non-nil empty tags", got)
	}
}

func TestDecodeCatalogSizeBoundary(t *testing.T) {
	if _, err := Decode(validCatalogJSONWithSize(t, MaxCatalogSize)); err != nil {
		t.Fatalf("Decode(exactly MaxCatalogSize) error = %v", err)
	}
	if _, err := Decode(validCatalogJSONWithSize(t, MaxCatalogSize+1)); err == nil {
		t.Fatal("Decode(MaxCatalogSize+1) error = nil, want size-limit failure")
	}
}

func TestEncodeIsDeterministicAndEmitsAllGroups(t *testing.T) {
	entries := catalogEntriesFixture()
	shuffled := []domain.CatalogEntry{entries[3], entries[0], entries[2], entries[1]}
	reversed := []domain.CatalogEntry{entries[1], entries[2], entries[0], entries[3]}
	first, err := Encode(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("deterministic bytes differ\nfirst: %s\nsecond: %s", first, second)
	}
	for _, group := range []string{"plugins", "mcps", "agents"} {
		want := `"` + group + `":{"schema_version":1,"items":[]}`
		if !bytes.Contains(first, []byte(want)) {
			t.Fatalf("Encode() = %s, missing %s", first, want)
		}
	}
	alphaV2 := bytes.Index(first, []byte(`"name":"alpha","version":"v2.0.0"`))
	alphaV1 := bytes.Index(first, []byte(`"name":"alpha","version":"v1.0.0"`))
	bravoV1 := bytes.Index(first, []byte(`"name":"bravo","version":"v1.0.0"`))
	if alphaV2 < 0 || alphaV1 < 0 || bravoV1 < 0 || !(alphaV2 < alphaV1 && alphaV1 < bravoV1) {
		t.Fatalf("Encode() did not order name ascending/version descending: %s", first)
	}
}

func TestDecodeServerActualCatalogSchema(t *testing.T) {
	// 复现测试环境 https://developer.bianbu.xyz 实际返回的目录结构：
	// 无顶层/分组 schema_version，条目含 schema_version 与 download_url，
	// MCP 误声明 application/gzip 且缺 message，published_at 无时区。
	raw := []byte(`{
		"skills":{"items":[{
			"schema_version":1,
			"content_size":160,
			"content_type":"application/gzip",
			"name":"code-review-helper",
			"download_url":"https://cdn-resource.spacemit.com/file/apc/resource/skill/code-review-helper-1-v1.0.0.tar.gz",
			"description":"代码审查助手",
			"message":"首次发布",
			"content_md5":"0274020ce87b4b9c067786d8fdf342ef",
			"published_at":"2026-09-01T17:48:14",
			"version":"v1.0.0",
			"tags":["hot","new","popular","recommended"]
		}]},
		"plugins":{"items":[]},
		"mcps":{"items":[{
			"schema_version":1,
			"content_size":269,
			"content_type":"application/gzip",
			"name":"filesystem",
			"download_url":"https://cdn-resource.spacemit.com/file/apc/resource/mcp/filesystem-1-v1.0.0.json",
			"description":"提供受限的文件系统访问能力",
			"content_md5":"ac771c6a1392087ccdb299d29820a2b5",
			"published_at":"2026-09-03T10:12:04",
			"version":"v1.0.0",
			"tags":["hot","official"]
		}]},
		"agents":{"items":[]}
	}`)
	snapshot, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode(server catalog) error = %v", err)
	}
	mcps := snapshot.List(domain.PowerTypeMCP)
	if len(mcps) != 1 || mcps[0].Ref.Name != "filesystem" || mcps[0].ContentType != "application/json" {
		t.Fatalf("MCP entries = %#v, want normalized filesystem/application/json", mcps)
	}
	skills := snapshot.List(domain.PowerTypeSkill)
	if len(skills) != 1 || skills[0].Ref.Name != "code-review-helper" || skills[0].ContentType != "application/gzip" {
		t.Fatalf("Skill entries = %#v, want code-review-helper/application/gzip", skills)
	}
}

func replaceJSON(t *testing.T, data []byte, old, replacement string) []byte {
	t.Helper()
	got := strings.Replace(string(data), old, replacement, 1)
	if got == string(data) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return []byte(got)
}

func validCatalogJSON() []byte {
	return []byte(`{"schema_version":1,"skills":{"schema_version":1,"items":[{"name":"alpha","version":"v2.0.0","description":"skill description","message":"skill message","content_type":"application/gzip","content_size":2,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":["alpha","catalog"]},{"name":"alpha","version":"v1.0.0","description":"skill description","message":"skill message","content_type":"application/gzip","content_size":1,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":["alpha"]},{"name":"bravo","version":"v1.0.0","description":"skill description","message":"skill message","content_type":"application/gzip","content_size":1,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":["bravo"]}]},"plugins":{"schema_version":1,"items":[{"name":"plugin","version":"v1.0.0","description":"plugin description","message":"plugin message","content_type":"application/gzip","content_size":1,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":["plugin"]}]},"mcps":{"schema_version":1,"items":[{"name":"mcp","version":"v1.0.0","description":"mcp description","message":"mcp message","content_type":"application/json","content_size":1,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":["mcp"]}]},"agents":{"schema_version":1,"items":[{"name":"agent","version":"v1.0.0","description":"agent description","message":"agent message","content_type":"application/gzip","content_size":4,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":["agent"]}]}}`)
}

// Expert 类别在本地叫 expert，但在 APC 的目录格式里那一组仍叫 agents（APC-API.md、
// ADR-044）。这条断言把「组名是线上契约、不是本地词汇」钉住：跟着改成 experts 会让
// 这一组在 APC 侧整组消失，而且是静默的——目录仍然合法，只是 Expert 一个都没有。
func TestExpertCatalogGroupKeepsAPCName(t *testing.T) {
	entry := domain.CatalogEntry{
		Ref:         domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo", Version: "v1.0.0"},
		Description: "description",
		Message:     "message",
		ContentType: "application/gzip",
		ContentSize: 1,
		ContentMD5:  "0123456789abcdef0123456789abcdef",
		PublishedAt: "2026-08-25T12:34:56Z",
		Tags:        []string{},
	}
	encoded, err := Encode([]domain.CatalogEntry{entry})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &groups); err != nil {
		t.Fatalf("decode encoded catalog: %v", err)
	}
	if _, present := groups["agents"]; !present {
		t.Fatalf("Encode() = %s, want an agents group for the Expert type", encoded)
	}
	if _, present := groups["experts"]; present {
		t.Fatalf("Encode() = %s, want no experts group: the wire name belongs to APC", encoded)
	}
	snapshot, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got := snapshot.List(domain.PowerTypeExpert); len(got) != 1 || got[0].Ref.Name != "demo" {
		t.Fatalf("List(expert) = %+v, want the demo entry read back from the agents group", got)
	}
}

func catalogEntriesFixture() []domain.CatalogEntry {
	return []domain.CatalogEntry{
		{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v2.0.0"}, Description: "description", Message: "message", ContentType: "application/gzip", ContentSize: 2, ContentMD5: "0123456789abcdef0123456789abcdef", PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{"alpha"}},
		{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v1.0.0"}, Description: "description", Message: "message", ContentType: "application/gzip", ContentSize: 1, ContentMD5: "0123456789abcdef0123456789abcdef", PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{"alpha"}},
		{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "bravo", Version: "v1.0.0"}, Description: "description", Message: "message", ContentType: "application/gzip", ContentSize: 1, ContentMD5: "0123456789abcdef0123456789abcdef", PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{"bravo"}},
		{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "charlie", Version: "v1.0.0"}, Description: "description", Message: "message", ContentType: "application/gzip", ContentSize: 1, ContentMD5: "0123456789abcdef0123456789abcdef", PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{"charlie"}},
	}
}

func validCatalogJSONWithSize(t *testing.T, size int64) []byte {
	t.Helper()
	const prefix = `{"schema_version":1,"skills":{"schema_version":1,"items":[{"name":"size-boundary","version":"v1.0.0","description":"`
	const suffix = `","message":"message","content_type":"application/gzip","content_size":1,"content_md5":"0123456789abcdef0123456789abcdef","published_at":"2026-08-25T12:34:56Z","tags":[]}]},"plugins":{"schema_version":1,"items":[]},"mcps":{"schema_version":1,"items":[]},"agents":{"schema_version":1,"items":[]}}`
	if size < int64(len(prefix)+len(suffix)) {
		t.Fatalf("size %d is too small for valid fixture", size)
	}
	data := make([]byte, size)
	copy(data, prefix)
	descriptionLength := len(data) - len(prefix) - len(suffix)
	for index := 0; index+6 <= descriptionLength; index += 6 {
		copy(data[len(prefix)+index:], `\u0061`)
	}
	for index := descriptionLength - descriptionLength%6; index < descriptionLength; index++ {
		data[len(prefix)+index] = 'a'
	}
	copy(data[len(data)-len(suffix):], suffix)
	return data
}

// TestDecodeCatalogWithDisplayFields 锁定目录条目携带 v2 展示字段：APC 在 item 上追加
// name_zh/name_en/description_zh 时，APM 必须解码而不是把整份目录判为非法（ADR-040）。
func TestDecodeCatalogWithDisplayFields(t *testing.T) {
	raw := replaceJSON(t, validCatalogJSON(), `"tags":["alpha","catalog"]`,
		`"tags":["alpha","catalog"],"name_zh":"演示技能","name_en":"Demo Skill","description_zh":"用于演示的 Skill。"`)
	snapshot, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode(catalog with display fields) error = %v", err)
	}
	entry, err := snapshot.Resolve(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if entry.NameZH != "演示技能" || entry.NameEN != "Demo Skill" || entry.DescriptionZH != "用于演示的 Skill。" {
		t.Fatalf("entry display fields = %q/%q/%q, want the decoded localized values", entry.NameZH, entry.NameEN, entry.DescriptionZH)
	}
	// 未提供展示字段的条目解码为空串，代表历史 v1 条目。
	other, err := snapshot.Resolve(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "bravo", Version: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if other.NameZH != "" || other.NameEN != "" || other.DescriptionZH != "" {
		t.Fatalf("v1 entry display fields = %q/%q/%q, want all empty", other.NameZH, other.NameEN, other.DescriptionZH)
	}
}

// TestDecodeCatalogItemSchemaVersionIsNotValidated 锁定条目级 schema_version 始终不参与校验：
// 它是资源格式版本，不是目录契约版本；若未来误加门禁，旧客户端会在 APC 升版本时冻结目录。
func TestDecodeCatalogItemSchemaVersionIsNotValidated(t *testing.T) {
	// committed fixture 只写了一个条目级 schema_version，这里额外覆盖其它取值同样被容忍。
	for name, raw := range map[string][]byte{
		"as-committed": validCatalogJSON(),
		"v2": replaceJSON(t, validCatalogJSON(), `{"name":"alpha","version":"v2.0.0"`,
			`{"schema_version":2,"name":"alpha","version":"v2.0.0"`),
		"v99": replaceJSON(t, validCatalogJSON(), `{"name":"alpha","version":"v2.0.0"`,
			`{"schema_version":99,"name":"alpha","version":"v2.0.0"`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(raw); err != nil {
				t.Fatalf("Decode() error = %v, want item schema_version to be tolerated", err)
			}
		})
	}
}

// TestEncodeOmitsEmptyDisplayFields 锁定编码侧对称性：空展示字段不写出字节（旧客户端读到
// 的目录与改动前逐字节一致的场景），有值时按字段名输出。
func TestEncodeOmitsEmptyDisplayFields(t *testing.T) {
	entries := []domain.CatalogEntry{{
		Ref:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		Description: "demo", Message: "message", ContentType: "application/gzip", ContentSize: 1,
		ContentMD5: "0123456789abcdef0123456789abcdef", PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{},
	}}
	raw, err := Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name_zh", "name_en", "description_zh"} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("Encode(empty display fields) = %s, want %q omitted", raw, field)
		}
	}

	entries[0].NameZH = "演示"
	entries[0].NameEN = "Demo"
	entries[0].DescriptionZH = "演示描述"
	raw, err = Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := roundTripped.Resolve(entries[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if entry.NameZH != "演示" || entry.NameEN != "Demo" || entry.DescriptionZH != "演示描述" {
		t.Fatalf("Encode/Decode round trip display fields = %q/%q/%q", entry.NameZH, entry.NameEN, entry.DescriptionZH)
	}
}

// TestDecodeRejectsInvalidDisplayFields 锁定目录解码路径仍会拒绝形状非法的展示字段，
// 因为它们是唯一的展示文本真相源（ADR-040 的已知代价）。
func TestDecodeRejectsInvalidDisplayFields(t *testing.T) {
	cases := map[string]string{
		"blank name_zh":          `"name_zh":"  "`,
		"too short name_zh":      `"name_zh":"演"`,
		"too long name_zh":       `"name_zh":"一二三四五六七八九十一二三四五六七"`,
		"non-ascii name_en":      `"name_en":"演示"`,
		"name_en without letter": `"name_en":"1234"`,
		"too long name_en":       `"name_en":"` + strings.Repeat("a", 33) + `"`,
		"blank description_zh":   `"description_zh":"   "`,
		// 342 个汉字 = 1026 字节，旧契约按字节判断；新契约统一为 500 个 UTF-16 码元（ADR-042），
		// 因此改用 501 个汉字触发。
		"description_zh too long": `"description_zh":"` + strings.Repeat("描", 501) + `"`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			raw := replaceJSON(t, validCatalogJSON(), `"tags":["alpha","catalog"]`, `"tags":["alpha","catalog"],`+field)
			if _, err := Decode(raw); err == nil {
				t.Fatalf("Decode(%s) error = nil, want display metadata rejection", field)
			}
		})
	}
}
