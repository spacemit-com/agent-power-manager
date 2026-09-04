package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// renderShow 输出单个 Power 的完整元数据，JSON 模式输出单对象而非数组。
func renderShow(writer io.Writer, result ShowResult, asJSON bool) error {
	if asJSON {
		record := struct {
			Type        string   `json:"type"`
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		}{string(result.Power.Power.Type), result.Power.Power.Name, result.Power.Power.Version, result.Power.Description, result.Power.Tags}
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
	if len(p.Tags) == 0 {
		tags = "[]"
	}
	if _, err := fmt.Fprintf(writer, "type: %s\nname: %s\nversion: %s\ndescription: %s\ntags: %s\n", p.Power.Type, p.Power.Name, p.Power.Version, p.Description, tags); err != nil {
		return fmt.Errorf("write show result: %w", err)
	}
	return nil
}
