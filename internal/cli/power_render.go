package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// powerJSON 是 list/search JSON 输出的单条记录；description 与 tags 仅在 --all 时填充。
type powerJSON struct {
	Agent       string   `json:"agent,omitempty"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// renderPowers 渲染 list 与 search 共用的 Power 记录；all 追加 description/tags，asJSON 切换为 JSON 输出。
func renderPowers(writer io.Writer, powers []ListedPower, all, asJSON bool) error {
	if asJSON {
		return renderPowersJSON(writer, powers, all)
	}
	for _, power := range powers {
		if err := renderPowerText(writer, power, all); err != nil {
			return err
		}
	}
	return nil
}

func renderPowersJSON(writer io.Writer, powers []ListedPower, all bool) error {
	records := make([]powerJSON, 0, len(powers))
	for _, power := range powers {
		records = append(records, toPowerJSON(power, all))
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		return fmt.Errorf("encode powers JSON: %w", err)
	}
	return nil
}

// toPowerJSON 将一条 Power 复制为 JSON 记录；description 与 tags 仅在 all 时填充。
func toPowerJSON(power ListedPower, all bool) powerJSON {
	record := powerJSON{
		Agent:   string(power.Agent),
		Type:    string(power.Power.Type),
		Name:    power.Power.Name,
		Version: power.Power.Version,
	}
	if all {
		record.Description = power.Description
		record.Tags = power.Tags
	}
	return record
}

func renderPowerText(writer io.Writer, power ListedPower, all bool) error {
	return renderPowerLine(writer, power, all, "")
}

// renderPowerLine 渲染一条不含 Agent 前缀的 Power 行；indent 用于分组模式下的小节缩进。
func renderPowerLine(writer io.Writer, power ListedPower, all bool, indent string) error {
	line := indent + string(power.Power.Type) + "/" + power.Power.Name + "@" + power.Power.Version + "\n"
	if _, err := io.WriteString(writer, line); err != nil {
		return fmt.Errorf("write power result: %w", err)
	}
	if !all {
		return nil
	}
	metaIndent := indent + "  "
	if power.Description != "" {
		if _, err := io.WriteString(writer, metaIndent+"description: "+power.Description+"\n"); err != nil {
			return fmt.Errorf("write power description: %w", err)
		}
	}
	if len(power.Tags) > 0 {
		if _, err := io.WriteString(writer, metaIndent+"tags: "+strings.Join(power.Tags, ", ")+"\n"); err != nil {
			return fmt.Errorf("write power tags: %w", err)
		}
	}
	return nil
}
