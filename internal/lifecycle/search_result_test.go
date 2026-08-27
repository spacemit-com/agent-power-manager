package lifecycle

import (
	"reflect"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func listedPowersFromRefs(refs []domain.PowerRef) []cli.ListedPower {
	powers := make([]cli.ListedPower, len(refs))
	for index, ref := range refs {
		powers[index] = cli.ListedPower{Power: ref}
	}
	return powers
}

func TestFilterSearchPowersMatchesNameSelectsLatestAndSorts(t *testing.T) {
	flags := cli.Flags{Keyword: "  STRASSE  "}
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "StraßeTool", Version: "v1.9.0"},
		{Type: domain.PowerTypeSkill, Name: "StraßeTool", Version: "v1.10.0"},
		{Type: domain.PowerTypeSkill, Name: "StraßeAddon", Version: "v3.0.0"},
		{Type: domain.PowerTypePlugin, Name: "StraßePlugin", Version: "v2.0.0"},
		{Type: domain.PowerTypeMCP, Name: "unrelated", Version: "v9.0.0"},
	}
	want := listedPowersFromRefs([]domain.PowerRef{
		{Type: domain.PowerTypePlugin, Name: "StraßePlugin", Version: "v2.0.0"},
		{Type: domain.PowerTypeSkill, Name: "StraßeAddon", Version: "v3.0.0"},
		{Type: domain.PowerTypeSkill, Name: "StraßeTool", Version: "v1.10.0"},
	})

	if got := filterSearchPowers(flags, listedPowersFromRefs(refs)); !reflect.DeepEqual(got, want) {
		t.Fatalf("filterSearchPowers() = %#v, want %#v", got, want)
	}
}

func TestFilterSearchPowersAppliesExactType(t *testing.T) {
	flags := cli.Flags{Keyword: "demo", Type: "skill"}
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "demo-skill", Version: "v1.0.0"},
		{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v1.0.0"},
	}
	want := listedPowersFromRefs([]domain.PowerRef{{Type: domain.PowerTypeSkill, Name: "demo-skill", Version: "v1.0.0"}})

	if got := filterSearchPowers(flags, listedPowersFromRefs(refs)); !reflect.DeepEqual(got, want) {
		t.Fatalf("filterSearchPowers() = %#v, want %#v", got, want)
	}
}

func TestFilterSearchPowersMatchesOnlyName(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "named-power", Version: "v1.2.3"},
		{Type: domain.PowerTypePlugin, Name: "other-power", Version: "v2.0.0"},
	}

	if got := filterSearchPowers(cli.Flags{Keyword: "skill"}, listedPowersFromRefs(refs)); len(got) != 0 {
		t.Fatalf("keyword matching type returned %#v, want no matches", got)
	}
	if got := filterSearchPowers(cli.Flags{Keyword: "v1.2.3"}, listedPowersFromRefs(refs)); len(got) != 0 {
		t.Fatalf("keyword matching version returned %#v, want no matches", got)
	}
}

func TestFilterSearchPowersKeepsSameNameInDifferentTypes(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "shared", Version: "v1.0.0"},
		{Type: domain.PowerTypePlugin, Name: "shared", Version: "v2.0.0"},
	}
	want := listedPowersFromRefs([]domain.PowerRef{
		{Type: domain.PowerTypePlugin, Name: "shared", Version: "v2.0.0"},
		{Type: domain.PowerTypeSkill, Name: "shared", Version: "v1.0.0"},
	})

	if got := filterSearchPowers(cli.Flags{Keyword: "shared"}, listedPowersFromRefs(refs)); !reflect.DeepEqual(got, want) {
		t.Fatalf("filterSearchPowers() = %#v, want %#v", got, want)
	}
}

func TestFilterSearchPowersKeepsLatestDescriptionAndTags(t *testing.T) {
	powers := []cli.ListedPower{
		{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.9.0"}, Description: "old", Tags: []string{"old-tag"}},
		{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.10.0"}, Description: "new", Tags: []string{"new-tag"}},
	}
	want := []cli.ListedPower{
		{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.10.0"}, Description: "new", Tags: []string{"new-tag"}},
	}

	if got := filterSearchPowers(cli.Flags{Keyword: "demo"}, powers); !reflect.DeepEqual(got, want) {
		t.Fatalf("filterSearchPowers() = %#v, want %#v", got, want)
	}
}

func TestFilterSearchPowersReturnsNonNilEmptySlice(t *testing.T) {
	got := filterSearchPowers(cli.Flags{Keyword: "missing"}, []cli.ListedPower{})
	if got == nil {
		t.Fatal("filterSearchPowers() returned nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("filterSearchPowers() = %#v, want empty slice", got)
	}
}

func TestFilterSearchPowersDoesNotModifyInput(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
	}
	powers := listedPowersFromRefs(refs)
	want := append([]cli.ListedPower(nil), powers...)

	_ = filterSearchPowers(cli.Flags{Keyword: "demo"}, powers)

	if !reflect.DeepEqual(powers, want) {
		t.Fatalf("filterSearchPowers() modified input: got %#v, want %#v", powers, want)
	}
}
