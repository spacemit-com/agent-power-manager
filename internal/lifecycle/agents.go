package lifecycle

import (
	"fmt"
	"strings"

	"apm/internal/cli"
	"apm/internal/domain"
)

// availableAgentSuffix 为“未安装”报错追加当前已安装智能体提示。
// `--type agent` 仅支持 bibit，无其它可选值，因此不追加提示。
func availableAgentSuffix(flags cli.Flags, snapshot domain.AgentSnapshot) string {
	if flags.Type == string(domain.PowerTypeAgent) {
		return ""
	}
	installed := snapshot.Ordered()
	if len(installed) == 0 {
		return ""
	}
	parts := make([]string, len(installed))
	for i, agent := range installed {
		parts[i] = string(agent)
	}
	return fmt.Sprintf(" (available agents: %s)", strings.Join(parts, ", "))
}
