package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"apm/internal/domain"
)

var canonicalInstallVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Flags 保存命令解析后的公共选项和位置参数。
type Flags struct {
	Agent     string
	Type      string
	Name      string
	Version   string
	Keyword   string
	Installed bool
	All       bool   // All 在 list/search 中追加输出 description 与 tags。
	Message   string // Message 仅用于 submit，进入 lifecycle 前会去除首尾空白。
	Output    string // Output 预留给查询命令绑定输出格式。
	JSON      bool
	Status    string
	Limit     int
	DryRun    bool // DryRun 仅用于 upgrade：只输出升级计划，不写本地状态。
}

func validateCommon(flags *Flags) error {
	if isExpertType(flags.Type) {
		return validateExpertTypeFlags(flags)
	}
	if strings.TrimSpace(flags.Agent) == "" {
		return errors.New("--agent is required")
	}
	if err := validateAgentValue(*flags); err != nil {
		return err
	}
	return resolvePowerType(flags)
}

// validateExpertTypeFlags 约束 expert power 仅适用于 Bibit；空 --agent 由 lifecycle 默认 bibit。
func validateExpertTypeFlags(flags *Flags) error {
	if err := resolvePowerType(flags); err != nil {
		return err
	}
	if err := validateAgentValue(*flags); err != nil {
		return err
	}
	switch flags.Agent {
	case "", "bibit":
		return nil
	default:
		return fmt.Errorf("--type expert only supports --agent bibit, got %q", flags.Agent)
	}
}

// validateAgentValue 校验 --agent 取值为已知智能体标识，非法时列出可选值；空值交给调用方的 required/empty 检查。
func validateAgentValue(flags Flags) error {
	agent := strings.TrimSpace(flags.Agent)
	if agent == "" || domain.IsKnownAgentID(domain.AgentID(agent)) {
		return nil
	}
	return fmt.Errorf("invalid --agent %q: must be %s", flags.Agent, agentOptionList())
}

// agentOptionList 返回 --agent 可选值的展示文本，all 在前、其余按字典序，末尾用 "or" 连接。
func agentOptionList() string {
	ids := domain.KnownAgentIDs()
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", or " + parts[len(parts)-1]
}

// isExpertType 判断原始的 --type 取值是否指向 Expert 类别，改名前的拼写也算。
//
// 单独抽出它是因为 normalize 必须在判断之后才能发生：各命令的报错顺序依赖
// 「先看是不是 Expert 类型，再看 --agent」，顺序一乱就改变了既有的报错优先级。
func isExpertType(rawType string) bool {
	return rawType == string(domain.PowerTypeExpert) || rawType == legacyAgentTypeValue
}

// legacyAgentTypeValue 是 Expert 类别改名前的 wire 取值。APC 至今仍用它（见
// APC-API.md 与 ADR-044），但 apm 自己的 CLI 只把它当作旧拼写接受，并在下一轮
// 移除；翻译到 APC 的那份映射在 internal/cloud/remote 里。
const legacyAgentTypeValue = "agent"

// resolvePowerType 校验 --type 并把改名前的拼写就地改成规范取值。
//
// 每个命令都必须在读取 flags.Type 之前调用它：原始字符串会一路流进
// internal/lifecycle 并最终参与 domain.PowerType 比较与过滤，留着 "agent"
// 会让 Expert 类别凭空消失（过滤为空、清单查不到）。
//
// --json 下不提示弃用：那种模式的 stdout 是机器契约，而这是一句提醒而不是结果。
func resolvePowerType(flags *Flags) error {
	switch flags.Type {
	case "", string(domain.PowerTypeSkill), string(domain.PowerTypePlugin), string(domain.PowerTypeMCP), string(domain.PowerTypeExpert):
		return nil
	case legacyAgentTypeValue:
		flags.Type = string(domain.PowerTypeExpert)
		if !flags.JSON {
			slog.Warn("--type agent is deprecated and will be removed; use --type expert",
				slog.String("type", string(domain.PowerTypeExpert)))
		}
		return nil
	default:
		return fmt.Errorf("invalid --type %q: must be skill, plugin, mcp, or expert", flags.Type)
	}
}

func parsePower(value string) (string, string, error) {
	name, version, hasVersion := strings.Cut(value, "=")
	if strings.TrimSpace(name) == "" {
		return "", "", errors.New("power name must not be empty")
	}
	if hasVersion && strings.TrimSpace(version) == "" {
		return "", "", errors.New("power version must not be empty")
	}
	return name, version, nil
}

func validateInstallVersion(version string) error {
	if version == "" {
		return nil
	}
	if !canonicalInstallVersion.MatchString(version) {
		return fmt.Errorf("invalid power version %q: must be vMAJOR.MINOR.PATCH", version)
	}
	return nil
}
