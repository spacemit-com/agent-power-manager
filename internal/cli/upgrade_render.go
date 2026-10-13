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
func renderUpgrade(writer io.Writer, result UpgradeResult, flags Flags) error {
	if flags.JSON {
		return renderUpgradeJSON(writer, result)
	}
	for _, item := range result.Items {
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
