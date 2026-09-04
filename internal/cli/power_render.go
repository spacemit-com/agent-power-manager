package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// powerJSON 是 list/search JSON 输出的单条记录；description 与 tags 是否填充由 powerMetadata 决定。
type powerJSON struct {
	Agent       string   `json:"agent,omitempty"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// powerMetadata 控制 Power 文本行/JSON 是否输出 description 与 tags，二者可独立开关。
type powerMetadata struct {
	description bool
	tags        bool
}

// allMetadata 返回 search --all 的元数据策略：description 与 tags 同开同关。
func allMetadata(all bool) powerMetadata {
	return powerMetadata{description: all, tags: all}
}

// renderPowers 渲染 list 与 search 共用的 Power 记录；metadata 决定是否输出元数据，asJSON 切换为 JSON 输出。
func renderPowers(writer io.Writer, powers []ListedPower, metadata powerMetadata, asJSON bool) error {
	if asJSON {
		return renderPowersJSON(writer, powers, metadata)
	}
	for _, power := range powers {
		if err := renderPowerText(writer, power, metadata); err != nil {
			return err
		}
	}
	return nil
}

func renderPowersJSON(writer io.Writer, powers []ListedPower, metadata powerMetadata) error {
	records := make([]powerJSON, 0, len(powers))
	for _, power := range powers {
		records = append(records, toPowerJSON(power, metadata))
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		return fmt.Errorf("encode powers JSON: %w", err)
	}
	return nil
}

// toPowerJSON 将一条 Power 复制为 JSON 记录；description 与 tags 按 metadata 独立填充。
func toPowerJSON(power ListedPower, metadata powerMetadata) powerJSON {
	record := powerJSON{
		Agent:   string(power.Agent),
		Type:    string(power.Power.Type),
		Name:    power.Power.Name,
		Version: power.Power.Version,
	}
	if metadata.description {
		record.Description = power.Description
	}
	if metadata.tags {
		record.Tags = power.Tags
	}
	return record
}

func renderPowerText(writer io.Writer, power ListedPower, metadata powerMetadata) error {
	return renderPowerLine(writer, power, metadata, "")
}

// renderPowerLine 渲染一条不含 Agent 前缀的 Power 行；indent 用于分组模式下的小节缩进。
func renderPowerLine(writer io.Writer, power ListedPower, metadata powerMetadata, indent string) error {
	line := indent + string(power.Power.Type) + "/" + power.Power.Name + "@" + power.Power.Version + "\n"
	if _, err := io.WriteString(writer, line); err != nil {
		return fmt.Errorf("write power result: %w", err)
	}
	metaIndent := indent + "  "
	if metadata.description && power.Description != "" {
		if _, err := io.WriteString(writer, metaIndent+"description: "+power.Description+"\n"); err != nil {
			return fmt.Errorf("write power description: %w", err)
		}
	}
	if metadata.tags && len(power.Tags) > 0 {
		if _, err := io.WriteString(writer, metaIndent+"tags: "+strings.Join(power.Tags, ", ")+"\n"); err != nil {
			return fmt.Errorf("write power tags: %w", err)
		}
	}
	return nil
}
