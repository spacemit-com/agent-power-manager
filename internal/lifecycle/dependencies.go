package lifecycle

import (
	"reflect"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// AdapterRegistry 定义智能体适配器注册表所需的操作。
type AdapterRegistry interface {
	ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error)
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
	RefreshInstalled() (domain.AgentSnapshot, error)
}

// VersionSelector 定义 Install 在可信候选中选择目标版本的最小接口。
type VersionSelector interface {
	Select([]string, string) (string, error)
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
