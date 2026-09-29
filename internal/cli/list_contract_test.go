package cli

import (
	"errors"
	"io"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestRenderListWritesExactCloudAndInstalledLines(t *testing.T) {
	result := ListResult{Powers: []ListedPower{
		{Power: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "cloud", Version: "v2.0.0"}},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "local", Version: "v1.0.0"}},
	}}
	var output strings.Builder
	if err := renderList(&output, result, Flags{}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "mcp/cloud@v2.0.0\nskill/local@v1.0.0\n"; got != want {
		t.Fatalf("renderList() = %q, want %q", got, want)
	}
}

func TestRenderListGroupedInstalledAgents(t *testing.T) {
	result := ListResult{
		Powers: []ListedPower{{
			Agent: domain.AgentCodex,
			Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		}},
		Agents: []InstalledAgentSummary{
			{Agent: domain.AgentBibit, State: InstalledAgentNotInstalled},
			{Agent: domain.AgentClaudeCode, State: InstalledAgentEmpty},
			{Agent: domain.AgentCodex, State: InstalledAgentListed},
		},
	}
	var output strings.Builder
	if err := renderList(&output, result, Flags{}); err != nil {
		t.Fatal(err)
	}
	const want = "bibit\n  not installed\nclaude-code\n  no powers installed\ncodex\n  skill/demo@v1.0.0\n"
	if got := output.String(); got != want {
		t.Fatalf("renderList(grouped) = %q, want %q", got, want)
	}
}

func TestRenderListWritesNothingForEmptyResult(t *testing.T) {
	var output strings.Builder
	if err := renderList(&output, ListResult{}, Flags{}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "" {
		t.Fatalf("renderList() = %q, want empty output", got)
	}
}

func TestRenderListAllWritesIdentityOnly(t *testing.T) {
	result := ListResult{Powers: []ListedPower{{
		Power:       domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2.3"},
		Description: "a demo skill",
		Tags:        []string{"alpha", "beta"},
	}}}
	var output strings.Builder
	if err := renderList(&output, result, Flags{All: true}); err != nil {
		t.Fatal(err)
	}
	const want = "skill/demo@v1.2.3\n"
	if got := output.String(); got != want {
		t.Fatalf("renderList(all) = %q, want %q", got, want)
	}
}

func TestRenderListAllOmitsEmptyDescriptionAndTags(t *testing.T) {
	result := ListResult{Powers: []ListedPower{{
		Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "bare", Version: "v1.0.0"},
	}}}
	var output strings.Builder
	if err := renderList(&output, result, Flags{All: true}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "skill/bare@v1.0.0\n"; got != want {
		t.Fatalf("renderList(all, empty metadata) = %q, want %q", got, want)
	}
}

func TestRenderListJSONIncludesDescriptionAndTags(t *testing.T) {
	result := ListResult{Powers: []ListedPower{{
		Power:       domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
		Description: "a demo skill",
		Tags:        []string{"alpha", "beta"},
	}}}
	var output strings.Builder
	if err := renderList(&output, result, Flags{JSON: true}); err != nil {
		t.Fatal(err)
	}
	const want = "[\n  {\n    \"type\": \"skill\",\n    \"name\": \"beta\",\n    \"version\": \"v1.0.0\",\n    \"description\": \"a demo skill\",\n    \"tags\": [\n      \"alpha\",\n      \"beta\"\n    ],\n    \"name_zh\": \"\",\n    \"name_en\": \"\",\n    \"description_zh\": \"\"\n  }\n]\n"
	if got := output.String(); got != want {
		t.Fatalf("renderList(json) = %q, want %q", got, want)
	}
}

func TestRenderListJSONWithAllIncludesDescriptionAndTags(t *testing.T) {
	result := ListResult{Powers: []ListedPower{{
		Agent:       domain.AgentCodex,
		Power:       domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
		Description: "a demo skill",
		Tags:        []string{"alpha", "beta"},
	}}}
	var output strings.Builder
	if err := renderList(&output, result, Flags{All: true, JSON: true}); err != nil {
		t.Fatal(err)
	}
	const want = "[\n  {\n    \"agent\": \"codex\",\n    \"type\": \"skill\",\n    \"name\": \"beta\",\n    \"version\": \"v1.0.0\",\n    \"description\": \"a demo skill\",\n    \"tags\": [\n      \"alpha\",\n      \"beta\"\n    ],\n    \"name_zh\": \"\",\n    \"name_en\": \"\",\n    \"description_zh\": \"\"\n  }\n]\n"
	if got := output.String(); got != want {
		t.Fatalf("renderList(json+all) = %q, want %q", got, want)
	}
}

func TestRenderListJSONNormalizesEmptyDescriptionAndTags(t *testing.T) {
	result := ListResult{Powers: []ListedPower{{
		Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "bare", Version: "v1.0.0"},
	}}}
	var output strings.Builder
	if err := renderList(&output, result, Flags{JSON: true}); err != nil {
		t.Fatal(err)
	}
	const want = "[\n  {\n    \"type\": \"skill\",\n    \"name\": \"bare\",\n    \"version\": \"v1.0.0\",\n    \"description\": \"\",\n    \"tags\": [],\n    \"name_zh\": \"\",\n    \"name_en\": \"\",\n    \"description_zh\": \"\"\n  }\n]\n"
	if got := output.String(); got != want {
		t.Fatalf("renderList(json, empty metadata) = %q, want %q", got, want)
	}
}

func TestRenderListWrapsSecondWriteFailureAndPreservesFirstLine(t *testing.T) {
	cause := errors.New("disk full")
	writer := &failOnWrite{failAt: 2, err: cause}
	result := ListResult{Powers: []ListedPower{
		{Power: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "first", Version: "v1.0.0"}},
		{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "second", Version: "v1.0.0"}},
	}}

	err := renderList(writer, result, Flags{})
	if err == nil || !strings.Contains(err.Error(), "write power result") {
		t.Fatalf("renderList() error = %v, want wrapped write power result error", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("renderList() error = %v, want cause %v", err, cause)
	}
	if got, want := writer.output.String(), "mcp/first@v1.0.0\n"; got != want {
		t.Fatalf("renderList() output = %q, want completed prefix %q", got, want)
	}
}

func TestPartialListErrorReportsAgentsAndUnwrapsCauses(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	err := &PartialListError{Failures: []AgentFailure{
		{Agent: domain.AgentClaudeCode, Err: first},
		{Agent: domain.AgentCodex, Err: second},
	}}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("missing cause: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "claude-code") || !strings.Contains(got, "codex") {
		t.Fatalf("Error() = %q, want both agents", got)
	}
}

type failOnWrite struct {
	output strings.Builder
	failAt int
	writes int
	err    error
}

func (w *failOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.output.Write(p)
}

var _ io.Writer = (*failOnWrite)(nil)

// TestRenderListJSONCarriesDisplayFields 锁定 list --json 的展示字段通道：
// 三个字段恒存在（空为 ""），前端据此决定卡片语言；缺失的旧记录回退到 name/description。
func TestRenderListJSONCarriesDisplayFields(t *testing.T) {
	result := ListResult{Powers: []ListedPower{{
		Power:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "brief-status", Version: "v0.0.1"},
		Description:   "Use when writing a concise progress status update.",
		Tags:          []string{"official"},
		NameZH:        "简报状态",
		NameEN:        "Brief Status",
		DescriptionZH: "用于撰写简洁的进度状态更新。",
	}}}
	var output strings.Builder
	if err := renderList(&output, result, Flags{JSON: true}); err != nil {
		t.Fatal(err)
	}
	const want = "[\n  {\n    \"type\": \"skill\",\n    \"name\": \"brief-status\",\n    \"version\": \"v0.0.1\",\n    \"description\": \"Use when writing a concise progress status update.\",\n    \"tags\": [\n      \"official\"\n    ],\n    \"name_zh\": \"简报状态\",\n    \"name_en\": \"Brief Status\",\n    \"description_zh\": \"用于撰写简洁的进度状态更新。\"\n  }\n]\n"
	if got := output.String(); got != want {
		t.Fatalf("renderList(json) = %q, want %q", got, want)
	}
}
