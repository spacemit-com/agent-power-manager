package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"apm/internal/domain"
)

func testPrompt() UpgradePrompt {
	return UpgradePrompt{
		Upgrades: []UpgradeItem{
			{
				Agent:   domain.AgentCodex,
				Current: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.0.1"},
				Target:  domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.1.0"},
				Outcome: UpgradeOutcomePlanned,
			},
			{
				Agent:   domain.AgentHermes,
				Current: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v0.1.0"},
				Target:  domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v0.2.0"},
				Outcome: UpgradeOutcomePlanned,
			},
		},
		UpToDate: 3,
		Skipped:  1,
	}
}

func TestRenderUpgradePromptWritesAptStyleConfirmation(t *testing.T) {
	var output strings.Builder
	if err := renderUpgradePrompt(&output, testPrompt()); err != nil {
		t.Fatal(err)
	}
	want := "The following powers will be upgraded:\n" +
		"  codex/skill/foo-before-summary@v0.0.1 -> v0.1.0\n" +
		"  hermes-agent/plugin/demo-plugin@v0.1.0 -> v0.2.0\n" +
		"2 planned, 3 up-to-date, 1 skipped.\n"
	if output.String() != want {
		t.Fatalf("renderUpgradePrompt() = %q, want %q", output.String(), want)
	}
}

func TestAskUpgradeConfirmationParsesAnswers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "empty enter defaults to yes", input: "\n", want: true},
		{name: "y", input: "y\n", want: true},
		{name: "uppercase Y", input: "Y\n", want: true},
		{name: "yes", input: "yes\n", want: true},
		{name: "mixed case with spaces", input: "  YeS  \n", want: true},
		{name: "n", input: "n\n", want: false},
		{name: "no", input: "NO\n", want: false},
		{name: "retries then accepts", input: "maybe\ny\n", want: true},
		{name: "retries then rejects", input: "what?\nn\n", want: false},
		{name: "immediate EOF is a refusal", input: "", want: false},
		{name: "invalid then EOF is a refusal", input: "hmm", want: false},
		{name: "yes without newline still accepts", input: "y", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			got, err := askUpgradeConfirmation(strings.NewReader(test.input), &output, testPrompt())
			if err != nil {
				t.Fatalf("askUpgradeConfirmation() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("askUpgradeConfirmation(%q) = %t, want %t", test.input, got, test.want)
			}
			if !strings.Contains(output.String(), "Do you want to continue? [Y/n] ") {
				t.Fatalf("prompt output = %q, want the confirmation question", output.String())
			}
		})
	}
}

func TestAskUpgradeConfirmationRepromptsOnInvalidAnswer(t *testing.T) {
	var output strings.Builder
	if _, err := askUpgradeConfirmation(strings.NewReader("nope\ny\n"), &output, testPrompt()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "Do you want to continue?"); got != 2 {
		t.Fatalf("questions asked = %d, want 2 after one invalid answer", got)
	}
	if !strings.Contains(output.String(), "Please answer 'y' or 'n'.") {
		t.Fatalf("prompt output = %q, want the retry hint", output.String())
	}
}

func TestAskUpgradeConfirmationReportsWriteFailure(t *testing.T) {
	cause := errors.New("stderr closed")
	writer := &upgradeFailOnWrite{failAt: 1, err: cause}

	_, err := askUpgradeConfirmation(strings.NewReader("y\n"), writer, testPrompt())
	if err == nil || !errors.Is(err, cause) {
		t.Fatalf("askUpgradeConfirmation() error = %v, want wrapped cause %v", err, cause)
	}
}

func TestIsInteractiveInputRejectsNonTerminalReaders(t *testing.T) {
	// 测试替身与管道都不是终端；只有 *os.File 才可能交互。
	for _, reader := range []interface {
		Read([]byte) (int, error)
	}{strings.NewReader("y\n"), &bytes.Buffer{}} {
		if isInteractiveInput(reader) {
			t.Fatalf("isInteractiveInput(%T) = true, want false", reader)
		}
	}
}

func TestUpgradeNeedsConfirmation(t *testing.T) {
	// --dry-run 不产生变更、--yes 显式跳过、--json 的调用方无人值守。
	tests := []struct {
		name  string
		flags Flags
		want  bool
	}{
		{name: "plain interactive", flags: Flags{}, want: true},
		{name: "yes skips", flags: Flags{Yes: true}, want: false},
		{name: "dry run never asks", flags: Flags{DryRun: true}, want: false},
		{name: "json must be explicit", flags: Flags{JSON: true}, want: true},
		{name: "yes with json", flags: Flags{Yes: true, JSON: true}, want: false},
		{name: "yes with dry run", flags: Flags{Yes: true, DryRun: true}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := upgradeNeedsConfirmation(test.flags); got != test.want {
				t.Fatalf("upgradeNeedsConfirmation(%#v) = %t, want %t", test.flags, got, test.want)
			}
		})
	}
}

func TestRenderUpgradeProgress(t *testing.T) {
	tests := []struct {
		name     string
		progress UpgradeProgress
		want     string
	}{
		{
			name:     "stage line",
			progress: UpgradeProgress{Stage: UpgradeStageSyncingCatalog},
			want:     "upgrade: syncing-catalog\n",
		},
		{
			name: "executing line is 1-based and carries the version transition",
			progress: UpgradeProgress{
				Stage:   UpgradeStageExecuting,
				Agent:   domain.AgentCodex,
				Power:   domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v0.2.0"},
				Current: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v0.1.0"},
				Index:   1,
				Total:   4,
			},
			want: "upgrade: [2/4] codex/skill/demo@v0.1.0 -> v0.2.0\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			if err := renderUpgradeProgress(&output, test.progress); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("renderUpgradeProgress() = %q, want %q", output.String(), test.want)
			}
		})
	}
}
