package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"apm/internal/domain"
)

func renderList(writer io.Writer, result ListResult, flags Flags) error {
	if result.Agents != nil {
		return renderInstalledAgents(writer, result, flags.JSON)
	}
	return renderPowers(writer, result.Powers, powerMetadata{}, flags.JSON)
}

// agentStateJSON 是 installed all 模式 JSON 输出中未安装/空清单 Agent 的状态记录。
type agentStateJSON struct {
	Agent string `json:"agent"`
	State string `json:"state"`
}

func renderInstalledAgents(writer io.Writer, result ListResult, asJSON bool) error {
	if asJSON {
		return renderInstalledAgentsJSON(writer, result)
	}
	return renderInstalledAgentsText(writer, result)
}

// renderInstalledAgentsText 按 Agent 顺序输出小节标题，未安装/空清单输出状态，已安装输出缩进能力行。
func renderInstalledAgentsText(writer io.Writer, result ListResult) error {
	metadata := powerMetadata{}
	byAgent := groupPowersByAgent(result.Powers)
	for _, summary := range result.Agents {
		if _, err := io.WriteString(writer, string(summary.Agent)+"\n"); err != nil {
			return fmt.Errorf("write agent header: %w", err)
		}
		switch summary.State {
		case InstalledAgentNotInstalled:
			if _, err := io.WriteString(writer, "  not installed\n"); err != nil {
				return fmt.Errorf("write agent state: %w", err)
			}
		case InstalledAgentEmpty:
			if _, err := io.WriteString(writer, "  no powers installed\n"); err != nil {
				return fmt.Errorf("write agent state: %w", err)
			}
		case InstalledAgentListed:
			for _, power := range byAgent[summary.Agent] {
				if err := renderPowerLine(writer, power, metadata, "  "); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// renderInstalledAgentsJSON 输出顶层数组：未安装/空清单为状态记录，能力为原 power 对象，按 Agent 顺序交错。
func renderInstalledAgentsJSON(writer io.Writer, result ListResult) error {
	metadata := powerMetadata{}
	byAgent := groupPowersByAgent(result.Powers)
	records := make([]any, 0, len(result.Agents)+len(result.Powers))
	for _, summary := range result.Agents {
		switch summary.State {
		case InstalledAgentNotInstalled, InstalledAgentEmpty:
			records = append(records, agentStateJSON{Agent: string(summary.Agent), State: string(summary.State)})
		case InstalledAgentListed:
			for _, power := range byAgent[summary.Agent] {
				records = append(records, toPowerJSON(power, metadata))
			}
		}
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		return fmt.Errorf("encode installed agents JSON: %w", err)
	}
	return nil
}

func groupPowersByAgent(powers []ListedPower) map[domain.AgentID][]ListedPower {
	byAgent := make(map[domain.AgentID][]ListedPower)
	for _, power := range powers {
		byAgent[power.Agent] = append(byAgent[power.Agent], power)
	}
	return byAgent
}
