package domain

import "testing"

func TestCatalogVersionValidate(t *testing.T) {
	valid := CatalogVersion{SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Size: 0}
	tests := []struct {
		name    string
		version CatalogVersion
		wantErr bool
	}{
		{name: "valid", version: valid},
		{name: "short digest", version: CatalogVersion{SHA256: valid.SHA256[:63], Size: 1}, wantErr: true},
		{name: "uppercase digest", version: CatalogVersion{SHA256: "A" + valid.SHA256[1:], Size: 1}, wantErr: true},
		{name: "non hex digest", version: CatalogVersion{SHA256: "g" + valid.SHA256[1:], Size: 1}, wantErr: true},
		{name: "negative size", version: CatalogVersion{SHA256: valid.SHA256, Size: -1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.version.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestCatalogEntryValidate(t *testing.T) {
	valid := catalogEntryFixture()
	tests := []struct {
		name    string
		mutate  func(*CatalogEntry)
		wantErr bool
	}{
		{name: "valid"},
		{name: "unknown type", mutate: func(entry *CatalogEntry) { entry.Ref.Type = "workflow" }, wantErr: true},
		{name: "non canonical name", mutate: func(entry *CatalogEntry) { entry.Ref.Name = "Not Canonical" }, wantErr: true},
		{name: "non exact version", mutate: func(entry *CatalogEntry) { entry.Ref.Version = "1.2.3" }, wantErr: true},
		{name: "empty description", mutate: func(entry *CatalogEntry) { entry.Description = " " }, wantErr: true},
		{name: "untrimmed description", mutate: func(entry *CatalogEntry) { entry.Description = " description" }, wantErr: true},
		{name: "empty message", mutate: func(entry *CatalogEntry) { entry.Message = "\t" }, wantErr: true},
		{name: "untrimmed message", mutate: func(entry *CatalogEntry) { entry.Message = "message " }, wantErr: true},
		{name: "empty content type", mutate: func(entry *CatalogEntry) { entry.ContentType = "" }, wantErr: true},
		{name: "untrimmed content type", mutate: func(entry *CatalogEntry) { entry.ContentType = " application/gzip" }, wantErr: true},
		{name: "content type does not match power type", mutate: func(entry *CatalogEntry) { entry.ContentType = "application/json" }, wantErr: true},
		{name: "zero content size", mutate: func(entry *CatalogEntry) { entry.ContentSize = 0 }, wantErr: true},
		{name: "uppercase MD5", mutate: func(entry *CatalogEntry) { entry.ContentMD5 = "A" + entry.ContentMD5[1:] }, wantErr: true},
		{name: "short MD5", mutate: func(entry *CatalogEntry) { entry.ContentMD5 = entry.ContentMD5[:31] }, wantErr: true},
		{name: "invalid timestamp", mutate: func(entry *CatalogEntry) { entry.PublishedAt = "not-a-time" }, wantErr: true},
		{name: "untrimmed tag", mutate: func(entry *CatalogEntry) { entry.Tags = []string{" tag"} }, wantErr: true},
		{name: "empty tag", mutate: func(entry *CatalogEntry) { entry.Tags = []string{""} }, wantErr: true},
		{name: "non canonical tag", mutate: func(entry *CatalogEntry) { entry.Tags = []string{"Not Canonical"} }, wantErr: true},
		{name: "too long tag", mutate: func(entry *CatalogEntry) { entry.Tags = []string{"abcdefghijklmnopqrstuvwxyzabcdefg"} }, wantErr: true},
		{name: "unsorted tags", mutate: func(entry *CatalogEntry) { entry.Tags = []string{"z", "a"} }, wantErr: true},
		{name: "duplicate tags", mutate: func(entry *CatalogEntry) { entry.Tags = []string{"a", "a"} }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := valid.Clone()
			if tt.mutate != nil {
				tt.mutate(&entry)
			}
			if err := entry.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestCatalogEntryCloneDoesNotShareTags(t *testing.T) {
	original := catalogEntryFixture()
	got := original.Clone()
	got.Tags[0] = "changed"
	if original.Tags[0] == "changed" {
		t.Fatal("CatalogEntry.Clone shares Tags")
	}
}

func TestCatalogEntryValidateRequiresNonNilTags(t *testing.T) {
	entry := catalogEntryFixture()
	entry.Tags = nil
	if err := entry.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want nil tags rejected")
	}
}

func catalogEntryFixture() CatalogEntry {
	return CatalogEntry{
		Ref:         PowerRef{Type: PowerTypeSkill, Name: "catalog-entry", Version: "v1.2.3"},
		Description: "Catalog entry",
		Message:     "Publish catalog entry",
		ContentType: "application/gzip",
		ContentSize: 1,
		ContentMD5:  "0123456789abcdef0123456789abcdef",
		PublishedAt: "2026-08-25T12:34:56Z",
		Tags:        []string{"catalog", "resource"},
	}
}
