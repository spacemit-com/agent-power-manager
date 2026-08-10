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
)
