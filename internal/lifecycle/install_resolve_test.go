package lifecycle

import (
	"reflect"
	"strings"
	"testing"

	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/domain"
)

func TestResolveCatalogEntryReturnsExactSnapshotMetadata(t *testing.T) {
	entries := []domain.CatalogEntry{
		catalogEntryForInstallTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, "old metadata"),
		catalogEntryForInstallTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, "authoritative metadata"),
	}
	snapshot := catalogSnapshotForInstallTest(t, entries)
	selector := &recordingVersionSelector{selected: "v2.0.0"}

	got, err := resolveCatalogEntry(cli.Flags{Name: "demo", Type: "skill"}, snapshot, selector)
	if err != nil {
		t.Fatalf("resolveCatalogEntry() error = %v", err)
	}
	if !reflect.DeepEqual(got, entries[1]) {
		t.Fatalf("resolveCatalogEntry() = %#v, want exact snapshot entry %#v", got, entries[1])
	}
}

func catalogEntryForInstallTest(ref domain.PowerRef, message string) domain.CatalogEntry {
	contentType := "application/gzip"
	if ref.Type == domain.PowerTypeMCP {
		contentType = "application/json"
	} else if ref.Type == domain.PowerTypeAgent {
		contentType = "application/yaml"
	}
	return domain.CatalogEntry{
		Ref: ref, Description: "test entry", Message: message, ContentType: contentType,
		ContentSize: 1, ContentMD5: "0123456789abcdef0123456789abcdef",
		PublishedAt: "2026-08-25T00:00:00Z", Tags: []string{},
	}
}

func catalogSnapshotForInstallTest(t *testing.T, entries []domain.CatalogEntry) catalog.Snapshot {
	t.Helper()
	raw, err := catalog.Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

type recordingVersionSelector struct {
	candidates []string
	requested  string
	selected   string
	err        error
	calls      int
}

func (s *recordingVersionSelector) Select(candidates []string, requested string) (string, error) {
	s.calls++
	s.candidates = append([]string(nil), candidates...)
	s.requested = requested
	return s.selected, s.err
}

func TestResolveInstallPower(t *testing.T) {
	tests := []struct {
		name       string
		flags      cli.Flags
		refs       []domain.PowerRef
		selected   string
		want       domain.PowerRef
		wantArgs   []string
		wantErr    string
		wantNoCall bool
	}{
		{name: "unique inferred type latest", flags: cli.Flags{Name: "demo"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, {Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}}, selected: "v2.0.0", want: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}, wantArgs: []string{"v1.0.0", "v2.0.0"}},
		{name: "explicit exact type and version", flags: cli.Flags{Name: "demo", Type: "plugin", Version: "v1.0.0"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, {Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}}, selected: "v1.0.0", want: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}, wantArgs: []string{"v1.0.0"}},
		{name: "case sensitive name", flags: cli.Flags{Name: "Demo"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}, wantErr: `power "Demo" not found`, wantNoCall: true},
		{name: "cross type ambiguity before explicit version", flags: cli.Flags{Name: "demo", Version: "v1.0.0"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, {Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}}, wantErr: "multiple power types", wantNoCall: true},
		{name: "explicit type does not fall back", flags: cli.Flags{Name: "demo", Type: "mcp"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}, wantErr: `type "mcp" not found`, wantNoCall: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := append([]domain.PowerRef(nil), tt.refs...)
			before := append([]domain.PowerRef(nil), refs...)
			selector := &recordingVersionSelector{selected: tt.selected}

			got, err := resolveInstallPower(tt.flags, refs, selector)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveInstallPower() error = %v", err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("resolveInstallPower() = %#v, want %#v", got, tt.want)
				}
				if selector.calls != 1 {
					t.Fatalf("selector calls = %d, want 1", selector.calls)
				}
				if !reflect.DeepEqual(selector.candidates, tt.wantArgs) {
					t.Fatalf("selector candidates = %#v, want %#v", selector.candidates, tt.wantArgs)
				}
				if selector.requested != tt.flags.Version {
					t.Fatalf("selector requested = %q, want %q", selector.requested, tt.flags.Version)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolveInstallPower() error = %v, want substring %q", err, tt.wantErr)
			}
			if tt.wantNoCall && selector.calls != 0 {
				t.Fatalf("selector calls = %d, want 0", selector.calls)
			}
			if !reflect.DeepEqual(refs, before) {
				t.Fatalf("resolveInstallPower() mutated refs: got %#v, before %#v", refs, before)
			}
		})
	}
}
