package lifecycle

import (
	"reflect"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestMakeCloudListResultFiltersTypeKeepsAllVersionsAndSorts(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.9.0"},
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.10.0"},
		{Type: domain.PowerTypePlugin, Name: "alpha", Version: "v2.0.0"},
	}

	got := makeCloudListResult(cli.Flags{}, refs)
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: refs[2]},
		{Power: refs[1]},
		{Power: refs[0]},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCloudListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCloudListResultAppliesExactTypeFilter(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "demo-skill", Version: "v1.0.0"},
		{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v1.0.0"},
	}
	want := cli.ListResult{Powers: []cli.ListedPower{{Power: refs[0]}}}

	if got := makeCloudListResult(cli.Flags{Type: "skill"}, refs); !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCloudListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCloudListResultKeepsCompleteDuplicatesStable(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"},
		{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"},
		{Type: domain.PowerTypeSkill, Name: "second", Version: "v1.0.0"},
	}

	got := makeCloudListResult(cli.Flags{}, refs)
	want := cli.ListResult{Powers: []cli.ListedPower{
		{Power: refs[0]},
		{Power: refs[1]},
		{Power: refs[2]},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeCloudListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeCloudListResultReturnsNonNilEmptySlice(t *testing.T) {
	got := makeCloudListResult(cli.Flags{}, nil)
	if got.Powers == nil {
		t.Fatal("makeCloudListResult() returned nil Powers, want non-nil empty slice")
	}
	if len(got.Powers) != 0 {
		t.Fatalf("makeCloudListResult() returned %#v, want empty Powers", got.Powers)
	}
}

func TestMakeCloudListResultDoesNotModifyInput(t *testing.T) {
	refs := []domain.PowerRef{
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
		{Type: domain.PowerTypePlugin, Name: "alpha", Version: "v2.0.0"},
	}
	want := append([]domain.PowerRef(nil), refs...)

	_ = makeCloudListResult(cli.Flags{}, refs)

	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("makeCloudListResult() modified input: got %#v, want %#v", refs, want)
	}
}

func TestMakeInstalledListResultAddsAgentFiltersAndSorts(t *testing.T) {
	input := []agentPowerGroups{
		{agent: domain.AgentCodex, groups: domain.PowerGroups{
			domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
		}},
		{agent: domain.AgentClaudeCode, groups: domain.PowerGroups{
			domain.PowerTypeSkill: {
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.9.0"},
				{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.10.0"},
			},
		}},
		{agent: domain.AgentClaudeCode, groups: domain.PowerGroups{
			domain.PowerTypePlugin: {{Type: domain.PowerTypePlugin, Name: "demo", Version: "v1.0.0"}},
		}},
	}

	got := makeInstalledListResult(cli.Flags{Installed: true, Agent: "all"}, input)
	want := []cli.ListedPower{
		{Agent: domain.AgentClaudeCode, Power: input[2].groups[domain.PowerTypePlugin][0]},
		{Agent: domain.AgentClaudeCode, Power: input[1].groups[domain.PowerTypeSkill][1]},
		{Agent: domain.AgentClaudeCode, Power: input[1].groups[domain.PowerTypeSkill][0]},
		{Agent: domain.AgentCodex, Power: input[0].groups[domain.PowerTypeSkill][0]},
	}
	if !reflect.DeepEqual(got.Powers, want) {
		t.Fatalf("makeInstalledListResult().Powers = %#v, want %#v", got.Powers, want)
	}
}

func TestMakeInstalledListResultAppliesExactTypeFilter(t *testing.T) {
	input := []agentPowerGroups{{agent: domain.AgentCodex, groups: domain.PowerGroups{
		domain.PowerTypeSkill:  {{Type: domain.PowerTypeSkill, Name: "skill", Version: "v1.0.0"}},
		domain.PowerTypePlugin: {{Type: domain.PowerTypePlugin, Name: "plugin", Version: "v1.0.0"}},
	}}}

	got := makeInstalledListResult(cli.Flags{Type: "plugin"}, input)
	want := cli.ListResult{Powers: []cli.ListedPower{{
		Agent: domain.AgentCodex,
		Power: input[0].groups[domain.PowerTypePlugin][0],
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("makeInstalledListResult() = %#v, want %#v", got, want)
	}
}

func TestMakeInstalledListResultKeepsCompleteDuplicatesStable(t *testing.T) {
	first := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	second := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	input := []agentPowerGroups{{agent: domain.AgentCodex, groups: domain.PowerGroups{
		domain.PowerTypeSkill: {first, second},
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
	input := []agentPowerGroups{{agent: domain.AgentCodex, groups: domain.PowerGroups{
		domain.PowerTypeSkill: {
			{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
			{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v2.0.0"},
		},
	}}}
	want := append([]agentPowerGroups(nil), input...)
	want[0].groups = domain.PowerGroups{
		domain.PowerTypeSkill: append([]domain.PowerRef(nil), input[0].groups[domain.PowerTypeSkill]...),
	}

	_ = makeInstalledListResult(cli.Flags{}, input)

	if !reflect.DeepEqual(input, want) {
		t.Fatalf("makeInstalledListResult() modified input: got %#v, want %#v", input, want)
	}
}
