package lifecycle

import (
	"reflect"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestMakeCatalogListResultKeepsLatestPerNameAndSorts(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.9.0"},
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.10.0"},
		{Type: domain.PowerTypePlugin, Name: "alpha", Version: "v2.0.0"},
	}

	got := makeCatalogListResult(cli.Flags{}, catalogEntriesFromRefs(refs))
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: refs[2], Description: "test power", Tags: []string{}},
		{Power: refs[1], Description: "test power", Tags: []string{}},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCatalogListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCatalogListResultAppliesExactTypeFilter(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "demo-skill", Version: "v1.0.0"},
		{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v1.0.0"},
	}
	want := cli.ListResult{Powers: []cli.ListedPower{{Power: refs[0], Description: "test power", Tags: []string{}}}}

	if got := makeCatalogListResult(cli.Flags{Type: "skill"}, catalogEntriesFromRefs(refs)); !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCatalogListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCatalogListResultCollapsesDuplicateVersions(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"},
		{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"},
		{Type: domain.PowerTypeSkill, Name: "second", Version: "v1.0.0"},
	}

	got := makeCatalogListResult(cli.Flags{}, catalogEntriesFromRefs(refs))
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: refs[0], Description: "test power", Tags: []string{}},
		{Power: refs[2], Description: "test power", Tags: []string{}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCatalogListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCatalogListResultReturnsNonNilEmptySlice(t *testing.T) {
	got := makeCatalogListResult(cli.Flags{}, nil)
	if got.Powers == nil {
		t.Fatal("makeCatalogListResult() returned nil Powers, want non-nil empty slice")
	}
	if len(got.Powers) != 0 {
		t.Fatalf("makeCatalogListResult() returned %#v, want empty Powers", got.Powers)
	}
}

func TestMakeCatalogListResultDoesNotModifyInput(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
		{Type: domain.PowerTypePlugin, Name: "alpha", Version: "v2.0.0"},
	}
	entries := catalogEntriesFromRefs(refs)
	want := append([]domain.CatalogEntry(nil), entries...)

	_ = makeCatalogListResult(cli.Flags{}, entries)

	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("makeCatalogListResult() modified input: got %#v, want %#v", entries, want)
	}
}

func TestMakeCatalogListResultKeepsLatestRegardlessOfInputOrder(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"},
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.5.0"},
	}

	got := makeCatalogListResult(cli.Flags{}, catalogEntriesFromRefs(refs))
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: refs[0], Description: "test power", Tags: []string{}},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCatalogListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCatalogListResultKeepsLatestDescriptionAndTags(t *testing.T) {
	entries := []domain.CatalogEntry{
		catalogEntryFromRef(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.9.0"}),
		catalogEntryFromRef(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.10.0"}),
	}
	entries[0].Description = "old"
	entries[0].Tags = []string{"old-tag"}
	entries[1].Description = "new"
	entries[1].Tags = []string{"new-tag"}

	got := makeCatalogListResult(cli.Flags{}, entries)
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.10.0"}, Description: "new", Tags: []string{"new-tag"}},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCatalogListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCatalogListResultFiltersByName(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
		{Type: domain.PowerTypeMCP, Name: "alpha", Version: "v2.0.0"},
		{Type: domain.PowerTypeSkill, Name: "other", Version: "v1.0.0"},
	}

	got := makeCatalogListResult(cli.Flags{Name: "beta"}, catalogEntriesFromRefs(refs))
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: refs[0], Description: "test power", Tags: []string{}},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCatalogListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCatalogListResultFiltersByNameKeepsLatest(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.9.0"},
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.10.0"},
		{Type: domain.PowerTypeSkill, Name: "other", Version: "v3.0.0"},
	}

	got := makeCatalogListResult(cli.Flags{Name: "beta"}, catalogEntriesFromRefs(refs))
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: refs[1], Description: "test power", Tags: []string{}},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCatalogListResult() = %#v, want %#v", got, want)
	}
}

func catalogEntriesFromRefs(refs []domain.PowerRef) []domain.CatalogEntry {
	entries := make([]domain.CatalogEntry, 0, len(refs))
	for _, ref := range refs {
		entries = append(entries, catalogEntryFromRef(ref))
	}
	return entries
}

func TestMakeInstalledListResultAddsAgentFiltersAndSorts(t *testing.T) {
	input := []agentPowerGroups{
		{agent: domain.AgentCodex, groups: domain.InstalledPowerGroups{
			domain.PowerTypeSkill: {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}}},
		}},
		{agent: domain.AgentClaudeCode, groups: domain.InstalledPowerGroups{
			domain.PowerTypeSkill: {
				{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.9.0"}},
				{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.10.0"}},
			},
		}},
		{agent: domain.AgentClaudeCode, groups: domain.InstalledPowerGroups{
			domain.PowerTypePlugin: {{Ref: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo", Version: "v1.0.0"}}},
		}},
	}

	got := makeInstalledListResult(cli.Flags{Installed: true, Agent: "all"}, input)
	want := []cli.ListedPower{
		{Agent: domain.AgentClaudeCode, Power: input[2].groups[domain.PowerTypePlugin][0].Ref},
		{Agent: domain.AgentClaudeCode, Power: input[1].groups[domain.PowerTypeSkill][1].Ref},
		{Agent: domain.AgentClaudeCode, Power: input[1].groups[domain.PowerTypeSkill][0].Ref},
		{Agent: domain.AgentCodex, Power: input[0].groups[domain.PowerTypeSkill][0].Ref},
	}
	if !reflect.DeepEqual(got.Powers, want) {
		t.Fatalf("makeInstalledListResult().Powers = %#v, want %#v", got.Powers, want)
	}
}

func TestMakeInstalledListResultAppliesExactTypeFilter(t *testing.T) {
	input := []agentPowerGroups{{agent: domain.AgentCodex, groups: domain.InstalledPowerGroups{
		domain.PowerTypeSkill:  {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "skill", Version: "v1.0.0"}}},
		domain.PowerTypePlugin: {{Ref: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "plugin", Version: "v1.0.0"}}},
	}}}

	got := makeInstalledListResult(cli.Flags{Type: "plugin"}, input)
	want := cli.ListResult{Powers: []cli.ListedPower{{
		Agent: domain.AgentCodex,
		Power: input[0].groups[domain.PowerTypePlugin][0].Ref,
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeInstalledListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeInstalledListResultFiltersByName(t *testing.T) {
	input := []agentPowerGroups{
		{agent: domain.AgentCodex, groups: domain.InstalledPowerGroups{
			domain.PowerTypeSkill: {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"}}},
		}},
		{agent: domain.AgentCodex, groups: domain.InstalledPowerGroups{
			domain.PowerTypeMCP: {{Ref: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "other", Version: "v1.0.0"}}},
		}},
		{agent: domain.AgentClaudeCode, groups: domain.InstalledPowerGroups{
			domain.PowerTypeSkill: {{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v2.0.0"}}},
		}},
	}

	got := makeInstalledListResult(cli.Flags{Installed: true, Agent: "all", Name: "beta"}, input)
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Agent: domain.AgentClaudeCode, Power: input[2].groups[domain.PowerTypeSkill][0].Ref},
		{Agent: domain.AgentCodex, Power: input[0].groups[domain.PowerTypeSkill][0].Ref},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeInstalledListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeInstalledListResultKeepsCompleteDuplicatesStable(t *testing.T) {
	first := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	second := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	input := []agentPowerGroups{{agent: domain.AgentCodex, groups: domain.InstalledPowerGroups{
		domain.PowerTypeSkill: {domain.InstalledPower{Ref: first}, domain.InstalledPower{Ref: second}},
	}}}

	got := makeInstalledListResult(cli.Flags{}, input)
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Agent: domain.AgentCodex, Power: first},
		{Agent: domain.AgentCodex, Power: second},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeInstalledListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeInstalledListResultReturnsNonNilEmptySlice(t *testing.T) {
	got := makeInstalledListResult(cli.Flags{}, nil)
	if got.Powers == nil {
		t.Fatal("makeInstalledListResult() returned nil Powers, want non-nil empty slice")
	}
	if len(got.Powers) != 0 {
		t.Fatalf("makeInstalledListResult() returned %#v, want empty Powers", got.Powers)
	}
}

func TestMakeInstalledListResultDoesNotModifyInput(t *testing.T) {
	input := []agentPowerGroups{{agent: domain.AgentCodex, groups: domain.InstalledPowerGroups{
		domain.PowerTypeSkill: {
			{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"}},
			{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v2.0.0"}},
		},
	}}}
	want := append([]agentPowerGroups(nil), input...)
	want[0].groups = domain.InstalledPowerGroups{
		domain.PowerTypeSkill: append([]domain.InstalledPower(nil), input[0].groups[domain.PowerTypeSkill]...),
	}

	_ = makeInstalledListResult(cli.Flags{}, input)

	if !reflect.DeepEqual(input, want) {
		t.Fatalf("makeInstalledListResult() modified input: got %#v, want %#v", input, want)
	}
}
