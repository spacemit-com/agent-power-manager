package adapter

import "errors"

var (
	// ErrAgentNotInstalled 表示目标 Agent 根目录不可用于安装。
	ErrAgentNotInstalled = errors.New("agent is not installed")
	// ErrUnsupportedAdapter 表示没有匹配智能体和能力类型的 Adapter。
	ErrUnsupportedAdapter = errors.New("unsupported adapter")
	// ErrTargetConflict 表示最终安装路径已被文件系统条目占用。
	ErrTargetConflict = errors.New("target path already exists")
	// ErrInvalidSkill 表示 Skill 引用、staging 或元数据不符合安装约束。
	ErrInvalidSkill = errors.New("invalid skill")
	// ErrInvalidMCP 表示 MCP 引用或启动定义不符合约束。
	ErrInvalidMCP = errors.New("invalid MCP server")
	// ErrMCPStateUnknown 表示 Agent CLI 无法明确判断 MCP 是否存在。
	ErrMCPStateUnknown = errors.New("MCP state is unknown")
	// ErrMCPConflict 表示未被 APM 纳管的同名 MCP 已存在。
	ErrMCPConflict = errors.New("MCP server already exists")
	// ErrMCPMutationUnknown 表示 Agent CLI 变更可能已经发生但结果无法确认。
	ErrMCPMutationUnknown = errors.New("MCP mutation result is unknown")
	// ErrInvalidPlugin 表示 Plugin 定义、目标或 Agent 返回事实不符合约束。
	ErrInvalidPlugin = errors.New("invalid plugin")
	// ErrMarketplaceConflict 表示目标 Agent 已存在非 APM 管理的同名 marketplace。
	ErrMarketplaceConflict = errors.New("plugin marketplace already exists")
	// ErrPluginConflict 表示未被 APM 纳管的同名 Plugin 已存在。
	ErrPluginConflict = errors.New("plugin already exists")
	// ErrPluginStateUnknown 表示无法明确判断 Plugin 是否存在。
	ErrPluginStateUnknown = errors.New("plugin state is unknown")
	// ErrPluginMutationUnknown 表示 Plugin 变更可能已发生但结果无法确认。
	ErrPluginMutationUnknown = errors.New("plugin mutation result is unknown")
	// ErrAgentCLIUnavailable 表示目标 Agent CLI 无法解析或启动。
	ErrAgentCLIUnavailable = errors.New("agent CLI is unavailable")
	// ErrInvalidAgent 表示 Agent 引用、staging 或 agent.yaml 不符合安装约束。
	ErrInvalidAgent = errors.New("invalid agent")
)
