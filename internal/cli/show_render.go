package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// renderShow 输出单个 Power 的完整元数据，JSON 模式输出单对象而非数组。
func renderShow(writer io.Writer, result ShowResult, asJSON bool) error {
	if asJSON {
		record := toPowerJSON(result.Power, powerMetadata{description: true, tags: true})
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("encode show JSON: %w", err)
		}
		return nil
	}
	return renderPowerLine(writer, result.Power, powerMetadata{description: true, tags: true}, "")
}
