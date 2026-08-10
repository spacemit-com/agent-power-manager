package transaction

import (
	"context"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// Manager 定义 lifecycle 所需的高层事务操作。
type Manager interface {
	Recover(context.Context) (RecoveryResult, error)
	Install(context.Context, InstallRequest) error
	Uninstall(context.Context, UninstallRequest) error
}

// InstallRequest 描述单个 Agent 的已解析安装事务输入。
type InstallRequest struct {
	Agent       domain.AgentID
	Power       domain.PowerRef
	Previous    *domain.PowerRef
	StagingRoot string
	Adapter     adapter.Adapter
}

// UninstallRequest 描述单个 Agent 的已解析卸载事务输入。
type UninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Adapter adapter.Adapter
}

// ManifestWriter 定义提交点后所需的清单写操作。
type ManifestWriter interface {
	Set(domain.AgentID, domain.PowerRef) error
	Remove(domain.AgentID, domain.PowerType, string) error
}

// Service 实现集中 journal 驱动的事务管理。
type Service struct {
	manifests ManifestWriter
	journals  JournalStore
}

func newService(manifests ManifestWriter, journals JournalStore) *Service {
	return &Service{manifests: manifests, journals: journals}
}
