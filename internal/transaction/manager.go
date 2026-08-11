package transaction

import (
	"context"
	"fmt"
	"os"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
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

// ManifestWriter 定义提交后的清单写入，以及安装预检发现陈旧清单时不创建 journal 的单条移除操作。
type ManifestWriter interface {
	Set(domain.AgentID, domain.PowerRef) error
	Remove(domain.AgentID, domain.PowerType, string) error
}

// Service 实现集中 journal 驱动的事务管理。
type Service struct {
	manifests ManifestWriter
	journals  JournalStore
	files     fileOps
	newID     func() (string, error)
}

// newService 创建可注入存储与文件操作的事务服务。
func newService(manifests ManifestWriter, journals JournalStore) *Service {
	return &Service{
		manifests: manifests,
		journals:  journals,
		files:     defaultFileOps(),
		newID:     newTransactionID,
	}
}

var _ Manager = (*Service)(nil)

// NewManager 创建使用集中事务日志的默认事务管理器。
func NewManager(manifests ManifestWriter) (*Service, error) {
	if isNilInterface(manifests) {
		return nil, fmt.Errorf("create transaction manager with nil manifest writer: %w", ErrInvalidRequest)
	}
	journalPath, err := paths.JournalPath()
	if err != nil {
		return nil, fmt.Errorf("resolve journal path: %w", err)
	}
	return newService(manifests, newFilesystemJournalStore(journalPath)), nil
}

type fileOps struct {
	lstat     func(string) (os.FileInfo, error)
	mkdir     func(string, os.FileMode) error
	chmod     func(string, os.FileMode) error
	rename    func(string, string) error
	remove    func(string) error
	removeAll func(string) error
	readDir   func(string) ([]os.DirEntry, error)
}

func defaultFileOps() fileOps {
	return fileOps{
		lstat:     os.Lstat,
		mkdir:     os.Mkdir,
		chmod:     os.Chmod,
		rename:    os.Rename,
		remove:    os.Remove,
		removeAll: os.RemoveAll,
		readDir:   os.ReadDir,
	}
}
