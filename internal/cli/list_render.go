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
	if flags.JSON {
		return renderListPowersJSON(writer, result.Powers)
	}
	return renderPowers(writer, result.Powers, powerMetadata{}, false)
}

// listPowerJSON 是 list --json 的机器记录：description 与 tags 恒定存在（空为 ""/[]），
// 与 search 的精简记录解耦，供前端稳定读取元数据。
type listPowerJSON struct {
	Agent       string   `json:"agent,omitempty"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// toListPowerJSON 将一条 Power 复制为 list JSON 记录；nil tags 归一化为空数组。
func toListPowerJSON(power ListedPower) listPowerJSON {
	tags := power.Tags
	if tags == nil {
		tags = []string{}
	}
	return listPowerJSON{
		Agent:       string(power.Agent),
		Type:        string(power.Power.Type),
		Name:        power.Power.Name,
		Version:     power.Power.Version,
		Description: power.Description,
		Tags:        tags,
	}
}

func renderListPowersJSON(writer io.Writer, powers []ListedPower) error {
	records := make([]listPowerJSON, 0, len(powers))
	for _, power := range powers {
		records = append(records, toListPowerJSON(power))
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		return fmt.Errorf("encode list powers JSON: %w", err)
	}
	return nil
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

// renderInstalledAgentsJSON 输出顶层数组：未安装/空清单为状态记录，能力为带元数据的 power 对象，按 Agent 顺序交错。
func renderInstalledAgentsJSON(writer io.Writer, result ListResult) error {
	byAgent := groupPowersByAgent(result.Powers)
	records := make([]any, 0, len(result.Agents)+len(result.Powers))
	for _, summary := range result.Agents {
		switch summary.State {
		case InstalledAgentNotInstalled, InstalledAgentEmpty:
			records = append(records, agentStateJSON{Agent: string(summary.Agent), State: string(summary.State)})
		case InstalledAgentListed:
			for _, power := range byAgent[summary.Agent] {
				records = append(records, toListPowerJSON(power))
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
