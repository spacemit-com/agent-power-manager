package cli

import (
	"errors"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestRenderUpgradeWritesExactOutcomeLines(t *testing.T) {
	targetA := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v0.1.0"}
	targetB := domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.1.0"}
	result := UpgradeResult{Items: []UpgradeItem{
		{Agent: domain.AgentCodex, Current: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v0.0.1"}, Target: targetA, Outcome: UpgradeOutcomeUpgraded},
		{Agent: domain.AgentCodex, Current: targetA, Target: targetA, Outcome: UpgradeOutcomeUpToDate},
		{Agent: domain.AgentClaudeCode, Current: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "gone", Version: "v0.0.1"}, Target: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "gone", Version: "v0.0.1"}, Outcome: UpgradeOutcomeSkipped, Skip: UpgradeSkipNotInCatalog},
		{Agent: domain.AgentHermes, Current: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.2.0"}, Target: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.2.0"}, Outcome: UpgradeOutcomeSkipped, Skip: UpgradeSkipNewerLocal},
		{Agent: domain.AgentCodex, Current: targetB, Target: targetB, Outcome: UpgradeOutcomeFailed},
		{Agent: domain.AgentHermes, Current: targetB, Target: targetB, Outcome: UpgradeOutcomeUnexecuted},
	}}

	var output strings.Builder
	if err := renderUpgrade(&output, result, Flags{}); err != nil {
		t.Fatal(err)
	}
	// up-to-date 不再逐条打印，其计数由末尾汇总行承接。
	want := "codex/skill/demo@v0.1.0 upgraded\n" +
		"claude-code/skill/gone@v0.0.1 skipped skip=not-in-catalog\n" +
		"hermes-agent/mcp/filesystem@v0.2.0 skipped skip=newer-local\n" +
		"codex/mcp/filesystem@v0.1.0 failed\n" +
		"hermes-agent/mcp/filesystem@v0.1.0 unexecuted\n" +
		"1 upgraded, 1 up-to-date, 2 skipped, 1 failed, 1 unexecuted.\n"
	if output.String() != want {
		t.Fatalf("renderUpgrade() = %q, want %q", output.String(), want)
	}
}

func TestRenderUpgradeSummaryLine(t *testing.T) {
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	tests := []struct {
		name  string
		items []UpgradeItem
		flags Flags
		want  string
	}{
		{
			name:  "nothing installed",
			items: nil,
			want:  "nothing to upgrade\n",
		},
		{
			name: "all up to date keeps the count visible",
			items: []UpgradeItem{
				{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeUpToDate},
				{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeUpToDate},
			},
			want: "2 up-to-date.\n",
		},
		{
			name: "dry run itemizes planned entries and summarises",
			items: []UpgradeItem{
				{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomePlanned},
				{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeUpToDate},
			},
			flags: Flags{DryRun: true},
			want:  "codex/skill/demo@v1.0.0 planned\n1 planned, 1 up-to-date.\n",
		},
		{
			name: "skipped is itemized with its reason and counted in the summary",
			items: []UpgradeItem{
				{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeSkipped, Skip: UpgradeSkipNewerLocal},
			},
			want: "codex/skill/demo@v1.0.0 skipped skip=newer-local\n1 skipped.\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			if err := renderUpgrade(&output, UpgradeResult{Items: test.items}, test.flags); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("renderUpgrade() = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestRenderUpgradeJSONKeepsUpToDateRecords(t *testing.T) {
	// --json 是完整对账记录：文本省略 up-to-date，但机器契约不得缩水。
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v0.1.0"}
	result := UpgradeResult{Items: []UpgradeItem{
		{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeUpToDate},
		{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeUpgraded},
	}}

	var output strings.Builder
	if err := renderUpgrade(&output, result, Flags{JSON: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), `"outcome": "up-to-date"`); got != 1 {
		t.Fatalf("JSON up-to-date records = %d, want 1 in %q", got, output.String())
	}
	// JSON 模式不追加文本汇总行。
	if strings.Contains(output.String(), "nothing to upgrade") {
		t.Fatalf("JSON output = %q, want no text summary line", output.String())
	}
}

func TestRenderUpgradeJSONWritesStableRecords(t *testing.T) {
	result := UpgradeResult{
		DryRun: true,
		Items: []UpgradeItem{{
			Agent:   domain.AgentCodex,
			Current: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v0.0.1"},
			Target:  domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v0.1.0"},
			Outcome: UpgradeOutcomePlanned,
		}},
	}

	var output strings.Builder
	if err := renderUpgrade(&output, result, Flags{JSON: true}); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"agent": "codex"`,
		`"type": "skill"`,
		`"name": "demo"`,
		`"current_version": "v0.0.1"`,
		`"target_version": "v0.1.0"`,
		`"outcome": "planned"`,
		`"skip": ""`,
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("renderUpgrade(JSON) = %q, want fragment %q", output.String(), fragment)
		}
	}
}

func TestRenderUpgradeWrapsWriteFailureAndPreservesFirstLine(t *testing.T) {
	cause := errors.New("disk full")
	writer := &upgradeFailOnWrite{failAt: 2, err: cause}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	result := UpgradeResult{Items: []UpgradeItem{
		{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeUpgraded},
		{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: UpgradeOutcomeUpgraded},
	}}

	err := renderUpgrade(writer, result, Flags{})
	if err == nil || !strings.Contains(err.Error(), "write upgrade result") {
		t.Fatalf("renderUpgrade() error = %v, want wrapped write upgrade result error", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("renderUpgrade() error = %v, want cause %v", err, cause)
	}
	if got, want := writer.output.String(), "codex/skill/demo@v1.0.0 upgraded\n"; got != want {
		t.Fatalf("renderUpgrade() output = %q, want completed prefix %q", got, want)
	}
}

type upgradeFailOnWrite struct {
	output strings.Builder
	failAt int
	writes int
	err    error
}

func (w *upgradeFailOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.output.Write(p)
}

func TestUpgradeOutcomeFromInstallKeepsInstallSemantics(t *testing.T) {
	// 升级路径复用安装结果，必须如实映射而不是一律记作 upgraded。
	tests := []struct {
		install InstallOutcome
		want    UpgradeOutcome
	}{
		{InstallOutcomeUpgraded, UpgradeOutcomeUpgraded},
		{InstallOutcomeInstalled, UpgradeOutcomeInstalled},
		{InstallOutcomeDowngraded, UpgradeOutcomeDowngraded},
		{InstallOutcomeUnchanged, UpgradeOutcomeUpToDate},
	}
	for _, test := range tests {
		if got := UpgradeOutcomeFromInstall(test.install); got != test.want {
			t.Fatalf("UpgradeOutcomeFromInstall(%q) = %q, want %q", test.install, got, test.want)
		}
	}
}

func TestUpgradeBatchErrorUnwrapPreservesEveryCause(t *testing.T) {
	first := errors.New("manifest write failed")
	second := errors.New("plugin cache missing")
	err := &UpgradeBatchError{Failures: []UpgradeFailure{
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Err: first},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v0.1.0"}, Err: second},
	}}

	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("errors.Is(%v) must match both causes", err)
	}
	message := err.Error()
	if !strings.Contains(message, "upgrade failed for 2 power(s)") ||
		!strings.Contains(message, "codex/skill/demo@v1.0.0") ||
		!strings.Contains(message, "hermes-agent/plugin/demo-plugin@v0.1.0") {
		t.Fatalf("Error() = %q, want both failures named", message)
	}
}
