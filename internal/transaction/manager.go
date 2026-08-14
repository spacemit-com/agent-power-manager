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
	Inspector
	Recoverer
	InstallSkill(context.Context, SkillInstallRequest) error
	UninstallSkill(context.Context, SkillUninstallRequest) (UninstallResult, error)
	InstallMCP(context.Context, MCPInstallRequest) error
	UninstallMCP(context.Context, MCPUninstallRequest) (UninstallResult, error)
	InstallPlugin(context.Context, PluginInstallRequest) error
	UninstallPlugin(context.Context, PluginUninstallRequest) (UninstallResult, error)
}

// MCPAdapterResolver 根据 journal 中的 Agent 动态解析恢复所需的 MCP Adapter。
type MCPAdapterResolver interface {
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
}

// PluginAdapterResolver 根据 journal 中的 Agent 动态解析恢复所需的 Plugin Adapter。
type PluginAdapterResolver interface {
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
}

// AdapterResolver 组合 NewManager 恢复所有 CLI 类型所需的解析能力。
type AdapterResolver interface {
	MCPAdapterResolver
	PluginAdapterResolver
}

// SkillInstallRequest 描述单个 Agent 的已解析文件型 Skill 安装事务输入。
type SkillInstallRequest struct {
	Agent       domain.AgentID
	Power       domain.PowerRef
	Previous    *domain.PowerRef
	StagingRoot string
	Adapter     adapter.SkillAdapter
}

// SkillUninstallRequest 描述单个 Agent 的已解析文件型 Skill 卸载事务输入。
type SkillUninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Adapter adapter.SkillAdapter
}

// MCPInstallRequest 描述单个 Agent 的 MCP 安装或版本切换输入；定义允许为空以支持无副作用的冲突和漂移复查。
type MCPInstallRequest struct {
	Agent          domain.AgentID
	Power          domain.PowerRef
	Previous       *domain.PowerRef
	Server         *domain.MCPServer
	PreviousServer *domain.MCPServer
	Adapter        adapter.MCPAdapter
}

// MCPUninstallRequest 描述单个 Agent 的 MCP 卸载输入；Server 仅在 CLI 确认注册存在时必需。
type MCPUninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Server  *domain.MCPServer
	Adapter adapter.MCPAdapter
}

// PluginInstallRequest 描述 Plugin 首装、修复或版本切换的事务输入。
type PluginInstallRequest struct {
	Agent       domain.AgentID
	Power       domain.PowerRef
	Previous    *domain.PowerRef
	Definition  domain.PluginDefinition
	StagingRoot string
	Adapter     adapter.PluginAdapter
}

// PluginUninstallRequest 描述无需 Cloud 即可执行的 Plugin 卸载输入。
type PluginUninstallRequest struct {
	Agent      domain.AgentID
	Power      domain.PowerRef
	ActualName string
	Adapter    adapter.PluginAdapter
}

// UninstallResult 描述卸载事务已确认的目标状态；该事实可与后续错误同时返回。
type UninstallResult struct {
	TargetMissing bool
}

// ManifestWriter 定义提交后的清单写入，以及安装预检发现陈旧清单时不创建 journal 的单条移除操作。
type ManifestWriter interface {
	Set(domain.AgentID, domain.PowerRef) error
	Remove(domain.AgentID, domain.PowerType, string) error
}

// Service 实现集中 journal 驱动的事务管理。
type Service struct {
	manifests      ManifestWriter
	journals       JournalStore
	mcpAdapters    MCPAdapterResolver
	pluginAdapters PluginAdapterResolver
	files          fileOps
	newID          func() (string, error)
}

// newService 创建可注入存储与文件操作的事务服务。
func newService(manifests ManifestWriter, journals JournalStore, mcpAdapters ...MCPAdapterResolver) *Service {
	service := &Service{
		manifests: manifests,
		journals:  journals,
		files:     defaultFileOps(),
		newID:     newTransactionID,
	}
	if len(mcpAdapters) != 0 {
		service.mcpAdapters = mcpAdapters[0]
		if resolver, ok := mcpAdapters[0].(PluginAdapterResolver); ok {
			service.pluginAdapters = resolver
		}
	}
	return service
}

var _ Manager = (*Service)(nil)

// NewManager 创建使用集中事务日志的默认事务管理器。
func NewManager(manifests ManifestWriter, adapters AdapterResolver) (*Service, error) {
	if isNilInterface(manifests) {
		return nil, fmt.Errorf("create transaction manager with nil manifest writer: %w", ErrInvalidRequest)
	}
	if isNilInterface(adapters) {
		return nil, fmt.Errorf("create transaction manager with nil adapter resolver: %w", ErrInvalidRequest)
	}
	journalPath, err := paths.JournalPath()
	if err != nil {
		return nil, fmt.Errorf("resolve journal path: %w", err)
	}
	return newService(manifests, newFilesystemJournalStore(journalPath), adapters), nil
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
