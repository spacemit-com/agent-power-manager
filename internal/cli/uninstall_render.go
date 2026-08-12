package cli

import (
	"fmt"
	"io"
)

func renderUninstall(writer io.Writer, result UninstallResult) error {
	for _, item := range result.Items {
		prefix := string(item.Agent)
		if item.Power != nil {
			prefix += "/" + string(item.Power.Type) + "/" + item.Power.Name + "@" + item.Power.Version
		}
		if _, err := io.WriteString(writer, prefix+" "+string(item.Outcome)+"\n"); err != nil {
			return fmt.Errorf("write uninstall result: %w", err)
		}
	}
	return nil
}
