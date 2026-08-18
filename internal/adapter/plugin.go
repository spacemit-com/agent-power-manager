package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"apm/internal/domain"
)

// PluginPresence 表示目标 Agent 中指定 Plugin 的存在状态。
type PluginPresence string

const (
	// PluginPresent 表示目标 Agent 明确确认 Plugin 已安装。
	PluginPresent PluginPresence = "present"
	// PluginAbsent 表示目标 Agent 明确确认 Plugin 未安装。
	PluginAbsent PluginPresence = "absent"
)

// PluginFact 是 Adapter 从目标 Agent 或文件系统读取的安装事实。
type PluginFact struct {
	Presence   PluginPresence `json:"presence"`
	ActualName string         `json:"actual_name"`
	Selector   string         `json:"selector"`
}

// MarketplacePresence 表示目标 Agent 中指定 marketplace 的存在状态。
type MarketplacePresence string

const (
	// MarketplacePresent 表示目标 Agent 明确返回唯一的本地 marketplace。
	MarketplacePresent MarketplacePresence = "present"
	// MarketplaceAbsent 表示目标 Agent 明确未找到指定 marketplace。
	MarketplaceAbsent MarketplacePresence = "absent"
)

// MarketplaceTarget 描述 marketplace CLI 写操作使用的固定名称和本地绝对目录。
type MarketplaceTarget struct {
	Name   string
	Source string
}

// MarketplaceFact 是 Adapter 从目标 Agent 读取的 marketplace 注册事实。
type MarketplaceFact struct {
	Presence MarketplacePresence
	Name     string
	Source   string
}

// PluginTarget 描述一次安装或卸载所使用的稳定实际名和 CLI selector。
type PluginTarget struct {
	ActualName string `json:"actual_name"`
	Selector   string `json:"selector"`
}

// PluginMutationState 表示 Agent CLI 写操作产生副作用的确定程度。
type PluginMutationState string

const (
	// PluginMutationApplied 表示 CLI 已明确完成变更。
	PluginMutationApplied PluginMutationState = "applied"
	// PluginMutationNotApplied 表示 CLI 明确没有产生变更。
	PluginMutationNotApplied PluginMutationState = "not-applied"
	// PluginMutationUnknown 表示无法判断 CLI 是否已产生变更，调用方必须保留 journal。
	PluginMutationUnknown PluginMutationState = "unknown"
)

// PluginMutationResult 描述一次 Plugin 写操作的副作用事实。
type PluginMutationResult struct {
	State PluginMutationState
}

// PluginAdapter 隔离各 Agent 的基础 Plugin 事实与安装卸载；不隐式管理 marketplace。
type PluginAdapter interface {
	DeploymentMode() domain.PluginDeploymentMode
	Inspect(context.Context, string) (PluginFact, error)
	Add(context.Context, PluginTarget) (PluginMutationResult, error)
	Remove(context.Context, PluginTarget) (PluginMutationResult, error)
	TargetPath(string) (string, error)
}

// MarketplacePluginAdapter 为支持 marketplace 的 Agent 组合显式注册能力。
// Source 必须保持为绝对 clean 本地路径，Adapter 不负责清洗调用方输入。
type MarketplacePluginAdapter interface {
	PluginAdapter
	InspectMarketplace(context.Context, string) (MarketplaceFact, error)
	RegisterMarketplace(context.Context, MarketplaceTarget) (PluginMutationResult, error)
	UnregisterMarketplace(context.Context, MarketplaceTarget) (PluginMutationResult, error)
}

func validateMarketplaceTarget(target MarketplaceTarget) error {
	if !isSafeMarketplaceName(target.Name) || !isAbsoluteCleanMarketplaceSource(target.Source) {
		return fmt.Errorf("marketplace target %+v: %w", target, ErrInvalidPlugin)
	}
	return nil
}

func validateMarketplaceFact(fact MarketplaceFact) error {
	if !isSafeMarketplaceName(fact.Name) {
		return fmt.Errorf("marketplace fact %+v: %w", fact, ErrInvalidPlugin)
	}
	switch fact.Presence {
	case MarketplaceAbsent:
		if fact.Source != "" {
			return fmt.Errorf("absent marketplace %q has source: %w", fact.Name, ErrInvalidPlugin)
		}
	case MarketplacePresent:
		if !isAbsoluteCleanMarketplaceSource(fact.Source) {
			return fmt.Errorf("present marketplace %q has invalid source %q: %w", fact.Name, fact.Source, ErrInvalidPlugin)
		}
	default:
		return fmt.Errorf("marketplace %q has presence %q: %w", fact.Name, fact.Presence, ErrInvalidPlugin)
	}
	return nil
}

func isSafeMarketplaceName(name string) bool {
	logical, found := strings.CutPrefix(name, "apm-")
	return found && isSafePluginActualName(logical)
}

func isAbsoluteCleanMarketplaceSource(source string) bool {
	return source != "" && strings.TrimSpace(source) == source && filepath.IsAbs(source) && filepath.Clean(source) == source
}

func validatePluginFact(fact PluginFact) error {
	if !isSafePluginActualName(fact.ActualName) {
		return fmt.Errorf("plugin fact actual name %q: %w", fact.ActualName, ErrInvalidPlugin)
	}
	switch fact.Presence {
	case PluginAbsent:
		if fact.Selector != "" {
			return fmt.Errorf("absent plugin %q has selector: %w", fact.ActualName, ErrInvalidPlugin)
		}
	case PluginPresent:
		if strings.TrimSpace(fact.Selector) == "" {
			return fmt.Errorf("present plugin %q has no selector: %w", fact.ActualName, ErrInvalidPlugin)
		}
	default:
		return fmt.Errorf("plugin %q has presence %q: %w", fact.ActualName, fact.Presence, ErrInvalidPlugin)
	}
	return nil
}

func validatePluginTarget(target PluginTarget) error {
	if !isSafePluginActualName(target.ActualName) || strings.TrimSpace(target.Selector) == "" {
		return fmt.Errorf("plugin target %+v: %w", target, ErrInvalidPlugin)
	}
	return nil
}

func validatePluginDeployment(mode domain.PluginDeploymentMode) error {
	switch mode {
	case domain.PluginDeploymentMarketplace, domain.PluginDeploymentFilesystem:
		return nil
	default:
		return fmt.Errorf("plugin deployment mode %q: %w", mode, ErrInvalidPlugin)
	}
}

func isSafePluginActualName(name string) bool {
	trimmed := strings.TrimSpace(name)
	return trimmed != "" && trimmed == name && trimmed != "." && trimmed != ".." &&
		!filepath.IsAbs(trimmed) && filepath.Base(trimmed) == trimmed &&
		!strings.ContainsAny(trimmed, `/\\`)
}

func pluginMutationFromCommand(agent domain.AgentID, operation string, target PluginTarget, result agentCLIResult, runErr error, success bool) (PluginMutationResult, error) {
	return pluginResourceMutationFromCommand(agent, "Plugin", operation, target.ActualName, result, runErr, success)
}

func marketplaceMutationFromCommand(agent domain.AgentID, operation string, target MarketplaceTarget, result agentCLIResult, runErr error) (PluginMutationResult, error) {
	return pluginResourceMutationFromCommand(agent, "marketplace", operation, target.Name, result, runErr, runErr == nil)
}

func pluginResourceMutationFromCommand(agent domain.AgentID, resource, operation, identity string, result agentCLIResult, runErr error, success bool) (PluginMutationResult, error) {
	if !result.Started {
		return PluginMutationResult{State: PluginMutationNotApplied}, fmt.Errorf(
			"%s %s %s %q did not start: %w", agent, resource, operation, identity, runErr,
		)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		runErr = errors.Join(runErr, fmt.Errorf(
			"Agent CLI output was truncated (stdout=%t, stderr=%t)",
			result.StdoutTruncated,
			result.StderrTruncated,
		))
		success = false
	}
	if runErr == nil && success {
		return PluginMutationResult{State: PluginMutationApplied}, nil
	}
	diagnostic := strings.TrimSpace(result.Stderr)
	if diagnostic == "" {
		diagnostic = strings.TrimSpace(result.Stdout)
	}
	return PluginMutationResult{State: PluginMutationUnknown}, fmt.Errorf(
		"%s %s %s %q via %q has unknown result (exit=%d, diagnostic=%q): %w",
		agent, resource, operation, identity, result.Path, result.ExitCode, diagnostic,
		errors.Join(ErrPluginMutationUnknown, runErr),
	)
}

func decodePluginJSON(document string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(document))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return err
	}
	return nil
}

func normalizePluginMarketplaceSource(source string, local bool) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("empty marketplace source: %w", ErrPluginStateUnknown)
	}
	if !local {
		return source, nil
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve marketplace source %q: %w", source, err)
	}
	return filepath.Clean(absolute), nil
}

func isLocalPluginMarketplaceSource(source string) bool {
	trimmed := strings.TrimSpace(source)
	return filepath.IsAbs(trimmed) || trimmed == "." || trimmed == ".." ||
		strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../")
}

func pluginStateUnknown(agent domain.AgentID, operation string, result agentCLIResult, cause error) error {
	diagnostic := strings.TrimSpace(result.Stderr)
	if diagnostic == "" {
		diagnostic = strings.TrimSpace(result.Stdout)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		cause = errors.Join(cause, fmt.Errorf(
			"Agent CLI output was truncated (stdout=%t, stderr=%t)",
			result.StdoutTruncated,
			result.StderrTruncated,
		))
	}
	return fmt.Errorf(
		"inspect %s Plugin %s via %q (exit=%d, diagnostic=%q): %w",
		agent, operation, result.Path, result.ExitCode, diagnostic,
		errors.Join(ErrPluginStateUnknown, cause),
	)
}
