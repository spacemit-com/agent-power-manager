package cli

import (
	"fmt"
	"io"
)

func renderInstall(writer io.Writer, result InstallResult) error {
	for _, item := range result.Items {
		line := string(item.Agent) + "/" + string(item.Power.Type) + "/" + item.Power.Name + "@" + item.Power.Version + " " + string(item.Outcome) + "\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return fmt.Errorf("write install result: %w", err)
		}
	}
	return nil
}
