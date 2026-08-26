package catalog

import (
	"bytes"
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
