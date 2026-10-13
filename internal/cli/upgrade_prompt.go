package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// isInteractiveInput 报告 reader 是否连接到终端。
// 只有 *os.File 才可能交互；测试替身（bytes.Buffer、strings.Reader）一律视为非交互，
// 使「非交互必须有 --yes」的判定确定可复现。
func isInteractiveInput(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	return isTerminalFile(file)
}

// renderUpgradePrompt 渲染 apt 风格的确认文本：先列出将要升级的条目，再给出计数与提问。
// 文本写入 stderr，保证 stdout 只承载业务结果。
func renderUpgradePrompt(writer io.Writer, prompt UpgradePrompt) error {
	if _, err := io.WriteString(writer, "The following powers will be upgraded:\n"); err != nil {
		return fmt.Errorf("write upgrade prompt header: %w", err)
	}
	for _, item := range prompt.Upgrades {
		line := "  " + string(item.Agent) + "/" + string(item.Target.Type) + "/" + item.Target.Name +
			"@" + item.Current.Version + " -> " + item.Target.Version + "\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return fmt.Errorf("write upgrade prompt entry: %w", err)
		}
	}
	summary := upgradeSummaryLine(prompt.Upgrades, prompt.UpToDate, prompt.Skipped)
	if _, err := io.WriteString(writer, summary+"\n"); err != nil {
		return fmt.Errorf("write upgrade prompt summary: %w", err)
	}
	return nil
}

// askUpgradeConfirmation 打印确认文本并读取一次答复。
// 空回车与 y/yes 视为同意，n/no 视为拒绝，其它输入重新提问；EOF 视为拒绝——
// 绝不把「输入结束」当作同意。
func askUpgradeConfirmation(reader io.Reader, writer io.Writer, prompt UpgradePrompt) (bool, error) {
	if err := renderUpgradePrompt(writer, prompt); err != nil {
		return false, err
	}
	buffered := bufio.NewReader(reader)
	for {
		if _, err := io.WriteString(writer, "Do you want to continue? [Y/n] "); err != nil {
			return false, fmt.Errorf("write upgrade confirmation: %w", err)
		}
		line, err := buffered.ReadString('\n')
		// 完全没有输入（EOF/读取失败）：保守拒绝，绝不当作同意。
		if line == "" && err != nil {
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			// 输入已耗尽且该行未以换行结束，无法再次提问。
			if err != nil {
				return false, nil
			}
			if _, writeErr := io.WriteString(writer, "Please answer 'y' or 'n'.\n"); writeErr != nil {
				return false, fmt.Errorf("write upgrade confirmation retry: %w", writeErr)
			}
		}
	}
}

// upgradeSummaryLine 生成计数汇总行，只列出计数非零的结果，顺序固定。
// 它同时用于执行后的文本汇总与确认询问，使两处计数口径一致。
func upgradeSummaryLine(items []UpgradeItem, upToDate, skipped int) string {
	counts := map[UpgradeOutcome]int{}
	for _, item := range items {
		counts[item.Outcome]++
	}
	// 逐条 up-to-date 不在文本中显示，但汇总必须接住，故由参数补入。
	counts[UpgradeOutcomeUpToDate] += upToDate
	counts[UpgradeOutcomeSkipped] += skipped

	order := []UpgradeOutcome{
		UpgradeOutcomePlanned,
		UpgradeOutcomeInstalled,
		UpgradeOutcomeUpgraded,
		UpgradeOutcomeDowngraded,
		UpgradeOutcomeUpToDate,
		UpgradeOutcomeSkipped,
		UpgradeOutcomeFailed,
		UpgradeOutcomeUnexecuted,
	}
	parts := make([]string, 0, len(order))
	for _, outcome := range order {
		if count := counts[outcome]; count != 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, outcome))
		}
	}
	if len(parts) == 0 {
		return "nothing to upgrade"
	}
	return strings.Join(parts, ", ") + "."
}
