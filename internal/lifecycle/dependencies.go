package lifecycle

import (
	"context"
	"log/slog"
	"reflect"

	"apm/internal/adapter"
	"apm/internal/catalog"
	"apm/internal/domain"
)

// AdapterRegistry 定义智能体适配器注册表所需的操作。
type AdapterRegistry interface {
	ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error)
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
	ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error)
	SupportedAgents() []domain.AgentID
	RefreshInstalled() (domain.AgentSnapshot, error)
}

// VersionSelector 定义 Install 在可信候选中选择目标版本的最小接口。
type VersionSelector interface {
	Select([]string, string) (string, error)
}

// CatalogManager 定义只读命令获取不可变目录快照的最小同步边界。
// Snapshot 自行处理非阻塞全局锁；SnapshotLocked 仅供已经持锁的写路径使用。
type CatalogManager interface {
	Snapshot(context.Context) (catalog.Snapshot, catalog.SyncStatus, error)
	SnapshotLocked(context.Context) (catalog.Snapshot, catalog.SyncStatus, error)
}

// warnStaleCatalog 仅将一次过期快照降级写入诊断日志，不污染命令业务结果。
func warnStaleCatalog(command string, status catalog.SyncStatus) {
	if !status.Stale {
		return
	}
	slog.Warn("catalog snapshot is stale", slog.String("command", command), slog.Any("error", status.Cause))
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
