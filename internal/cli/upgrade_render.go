package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// upgradeJSON 是 upgrade --json 的机器记录；字段恒定存在，空值为 ""。
type upgradeJSON struct {
	Agent          string `json:"agent"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version"`
	Outcome        string `json:"outcome"`
	Skip           string `json:"skip"`
}

// toUpgradeJSON 将一条升级结果复制为 JSON 记录。
func toUpgradeJSON(item UpgradeItem) upgradeJSON {
	return upgradeJSON{
		Agent:          string(item.Agent),
		Type:           string(item.Target.Type),
		Name:           item.Target.Name,
		CurrentVersion: item.Current.Version,
		TargetVersion:  item.Target.Version,
		Outcome:        string(item.Outcome),
		Skip:           item.Skip,
	}
}

// renderUpgrade 渲染全量升级的计划或结果；--json 切换为机器契约，--dry-run 只改变语义不改变形状。
//
// 文本模式省略逐条 up-to-date：它们不产生动作，逐行打印只会淹没真正需要关注的条目；
// 其计数由末尾汇总行承接。--json 仍是完整记录，机器可自行统计（ADR-045）。
func renderUpgrade(writer io.Writer, result UpgradeResult, flags Flags) error {
	if flags.JSON {
		return renderUpgradeJSON(writer, result)
	}
	if err := renderUpgradeItems(writer, result); err != nil {
		return err
	}
	return writeUpgradeSummary(writer, result)
}

// renderUpgradeItems 逐行输出有动作的结果，跳过 up-to-date。
func renderUpgradeItems(writer io.Writer, result UpgradeResult) error {
	for _, item := range result.Items {
		if item.Outcome == UpgradeOutcomeUpToDate {
			continue
		}
		line := string(item.Agent) + "/" + string(item.Target.Type) + "/" + item.Target.Name +
			"@" + item.Target.Version + " " + string(item.Outcome)
		// 跳过原因与结果同行输出，保证 --dry-run 的纯文本仍然自解释。
		if item.Skip != "" {
			line += " skip=" + item.Skip
		}
		if _, err := io.WriteString(writer, line+"\n"); err != nil {
			return fmt.Errorf("write upgrade result: %w", err)
		}
	}
	return nil
}

// writeUpgradeSummary 追加计数汇总行，接住被省略的 up-to-date。
func writeUpgradeSummary(writer io.Writer, result UpgradeResult) error {
	summary := upgradeSummaryLine(result.Items, 0, 0)
	if _, err := io.WriteString(writer, summary+"\n"); err != nil {
		return fmt.Errorf("write upgrade summary: %w", err)
	}
	return nil
}

func renderUpgradeJSON(writer io.Writer, result UpgradeResult) error {
	records := make([]upgradeJSON, 0, len(result.Items))
	for _, item := range result.Items {
		records = append(records, toUpgradeJSON(item))
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		return fmt.Errorf("encode upgrade JSON: %w", err)
	}
	return nil
}
