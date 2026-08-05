package logging

import (
	"log/slog"

	"apm/internal/domain"
)

// Agent 创建包含智能体标识的结构化日志字段。
func Agent(agent domain.AgentID) slog.Attr {
	return slog.String("agent", string(agent))
}

// Power 创建包含能力名称的结构化日志字段。
func Power(power string) slog.Attr {
	return slog.String("power", power)
}

// Version 创建包含版本号的结构化日志字段。
func Version(version string) slog.Attr {
	return slog.String("version", version)
}

// Phase 创建包含执行阶段的结构化日志字段。
func Phase(phase string) slog.Attr {
	return slog.String("phase", phase)
}
