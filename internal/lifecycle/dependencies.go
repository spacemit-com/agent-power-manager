package lifecycle

import (
	"reflect"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/transaction"
)

// AdapterRegistry 定义智能体适配器注册表所需的操作。
type AdapterRegistry interface {
	ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error)
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
	RefreshInstalled() (domain.AgentSnapshot, error)
}

// ConfigRepository 是配置清单 Repository 的别名。
type ConfigRepository = config.Repository

// TransactionManager 是 lifecycle 调用的高层事务管理接口别名。
type TransactionManager = transaction.Manager

// Locker 是进程间锁的别名。
type Locker = lock.Locker

// PowerCache 是能力缓存的别名。
type PowerCache = cache.PowerCache

// CloudClient 是云端能力客户端的别名。
type CloudClient = cloud.Client

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
