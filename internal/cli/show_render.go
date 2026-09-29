package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// showPowerJSON 是 show --json 的单对象记录：与 list --json 的发布元数据字段集一致，
// 含 v2 展示字段（空为 ""），供前端按精确版本读取展示名与中文描述（ADR-040）。
type showPowerJSON struct {
	Type          string   `json:"type"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	NameZH        string   `json:"name_zh"`
	NameEN        string   `json:"name_en"`
	DescriptionZH string   `json:"description_zh"`
}

// renderShow 输出单个 Power 的完整元数据，JSON 模式输出单对象而非数组。
func renderShow(writer io.Writer, result ShowResult, asJSON bool) error {
	if asJSON {
		power := result.Power
		record := showPowerJSON{
			Type:          string(power.Power.Type),
			Name:          power.Power.Name,
			Version:       power.Power.Version,
			Description:   power.Description,
			Tags:          power.Tags,
			NameZH:        power.NameZH,
			NameEN:        power.NameEN,
			DescriptionZH: power.DescriptionZH,
		}
		if record.Tags == nil {
			record.Tags = []string{}
		}
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("encode show JSON: %w", err)
		}
		return nil
	}
	p := result.Power
	tags := strings.Join(p.Tags, ", ")
	if _, err := fmt.Fprintf(writer, "type: %s\nname: %s\nversion: %s\ndescription: %s\n", p.Power.Type, p.Power.Name, p.Power.Version, p.Description); err != nil {
		return fmt.Errorf("write show result: %w", err)
	}
	if tags == "" {
		_, err := io.WriteString(writer, "tags:\n")
		if err != nil {
			return fmt.Errorf("write show tags: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(writer, "tags: %s\n", tags); err != nil {
		return fmt.Errorf("write show tags: %w", err)
	}
	return nil
}
