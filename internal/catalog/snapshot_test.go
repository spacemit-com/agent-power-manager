package catalog

import (
	"errors"
	"testing"

	"apm/internal/domain"
)

func TestSnapshotQueriesReturnClonesAndResolveVersions(t *testing.T) {
	snapshot, err := Decode(validCatalogJSON())
	if err != nil {
		t.Fatal(err)
	}

	skills := snapshot.List(domain.PowerTypeSkill)
	if len(skills) != 3 {
		t.Fatalf("List(skill) length = %d, want 3", len(skills))
	}
	skills[0].Tags[0] = "changed"
	if again := snapshot.List(domain.PowerTypeSkill); again[0].Tags[0] == "changed" {
		t.Fatal("List() shares entry Tags with snapshot")
	}
	if got := snapshot.List(domain.PowerTypePlugin); len(got) != 1 || got[0].Ref.Type != domain.PowerTypePlugin {
		t.Fatalf("List(plugin) = %#v", got)
	}

	exact, err := snapshot.Resolve(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v1.0.0"})
	if err != nil || exact.Ref.Version != "v1.0.0" {
		t.Fatalf("Resolve(exact) = %#v, %v", exact, err)
	}
	latest, err := snapshot.Resolve(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha"})
	if err != nil || latest.Ref.Version != "v2.0.0" {
		t.Fatalf("Resolve(latest) = %#v, %v", latest, err)
	}
	latest.Tags[0] = "changed"
	if again, err := snapshot.Resolve(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha"}); err != nil || again.Tags[0] == "changed" {
		t.Fatalf("Resolve() shares entry Tags or failed: %#v, %v", again, err)
	}

	metadata, err := snapshot.Metadata(domain.PowerRef{Type: domain.PowerTypePlugin, Name: "plugin", Version: "v1.0.0"})
	if err != nil || metadata.Ref.Type != domain.PowerTypePlugin {
		t.Fatalf("Metadata() = %#v, %v", metadata, err)
	}
	metadata.Tags[0] = "changed"
	if again, err := snapshot.Metadata(metadata.Ref); err != nil || again.Tags[0] == "changed" {
		t.Fatalf("Metadata() shares entry Tags or failed: %#v, %v", again, err)
	}

	if _, err := snapshot.Resolve(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := snapshot.Metadata(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Metadata(non-exact) error = %v, want ErrNotFound", err)
	}
}

func TestSnapshotResolveRejectsCrossTypeAmbiguity(t *testing.T) {
	entries := []domain.CatalogEntry{
		catalogEntry(domain.PowerTypeSkill, "shared", "v1.0.0"),
		catalogEntry(domain.PowerTypePlugin, "shared", "v1.0.0"),
	}
	data, err := Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Resolve(domain.PowerRef{Name: "shared"}); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Resolve(cross-type) error = %v, want ErrAmbiguous", err)
	}
}

func catalogEntry(powerType domain.PowerType, name, version string) domain.CatalogEntry {
	contentType := "application/gzip"
	if powerType == domain.PowerTypeMCP {
		contentType = "application/json"
	}
	return domain.CatalogEntry{Ref: domain.PowerRef{Type: powerType, Name: name, Version: version}, Description: "description", Message: "message", ContentType: contentType, ContentSize: 1, ContentMD5: "0123456789abcdef0123456789abcdef", PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{"tag"}}
}
