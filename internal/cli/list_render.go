package cli

import (
	"fmt"
	"io"
)

func renderList(writer io.Writer, result ListResult) error {
	for _, item := range result.Powers {
		prefix := ""
		if item.Agent != "" {
			prefix = string(item.Agent) + "/"
		}
		line := prefix + string(item.Power.Type) + "/" + item.Power.Name + "@" + item.Power.Version + "\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return fmt.Errorf("write list result: %w", err)
		}
	}
	return nil
}
