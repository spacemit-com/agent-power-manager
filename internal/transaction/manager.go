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
	InspectFilesystemTarget(domain.PowerRef, FilesystemTarget) (TargetPresence, error)
	InstallSkill(context.Context, SkillInstallRequest) error
	UninstallSkill(context.Context, SkillUninstallRequest) (UninstallResult, error)
	InstallAgent(context.Context, AgentInstallRequest) error
	UninstallAgent(context.Context, AgentUninstallRequest) (UninstallResult, error)
	InstallMCP(context.Context, MCPInstallRequest) error
	UninstallMCP(context.Context, MCPUninstallRequest) (UninstallResult, error)
	InstallPlugin(context.Context, PluginInstallRequest) error
	UninstallPlugin(context.Context, PluginUninstallRequest) (UninstallResult, error)
}

// FilesystemTarget 提供文件型 Power 的最终目标路径，供只读目标探针使用。
type FilesystemTarget interface {
	TargetPath(domain.PowerRef) (string, error)
}

// TargetPresence 表示文件型 Power 目标路径的存在状态。
type TargetPresence string

const (
	// TargetAbsent 表示目标路径不存在。
	TargetAbsent TargetPresence = "absent"
	// TargetPresent 表示目标路径存在。
	TargetPresent TargetPresence = "present"
)

// MCPAdapterResolver 根据 journal 中的 Agent 动态解析恢复所需的 MCP Adapter。
type MCPAdapterResolver interface {
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
}

// PluginAdapterResolver 根据 journal 中的 Agent 动态解析恢复所需的 Plugin Adapter。
type PluginAdapterResolver interface {
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
}

// AgentAdapterResolver 根据 journal 中的 Agent 动态解析恢复所需的文件型 Agent Adapter。
type AgentAdapterResolver interface {
	ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error)
}

// AdapterResolver 组合 NewManager 恢复所有 CLI 类型所需的解析能力。
type AdapterResolver interface {
	MCPAdapterResolver
	PluginAdapterResolver
	AgentAdapterResolver
}

// installMetadata 是安装时从目录条目捕获的发布元数据，四类安装请求与 journal 共用；
// 它是纯元数据，不参与身份、幂等、缓存键或 recovery 判定（ADR-024、ADR-040）。
type installMetadata struct {
	Description   string
	Tags          []string
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// applyToJournal 把发布元数据填入 journal envelope，避免各 driver 逐个字段手工复制时漏项。
func (metadata installMetadata) applyToJournal(journal *Journal) {
	journal.Description = metadata.Description
	journal.Tags = metadata.Tags
	journal.NameZH = metadata.NameZH
	journal.NameEN = metadata.NameEN
	journal.DescriptionZH = metadata.DescriptionZH
}

// installedPower 把发布元数据与给定引用组合为清单条目。
func (metadata installMetadata) installedPower(ref domain.PowerRef) domain.InstalledPower {
	return domain.InstalledPower{
		Ref:           ref,
		Description:   metadata.Description,
		Tags:          metadata.Tags,
		NameZH:        metadata.NameZH,
		NameEN:        metadata.NameEN,
		DescriptionZH: metadata.DescriptionZH,
	}
}

// metadata 返回 Skill 安装请求捕获的发布元数据。
func (request SkillInstallRequest) metadata() installMetadata {
	return installMetadata{
		Description: request.Description, Tags: request.Tags,
		NameZH: request.NameZH, NameEN: request.NameEN, DescriptionZH: request.DescriptionZH,
	}
}

// metadata 返回 Agent 安装请求捕获的发布元数据。
func (request AgentInstallRequest) metadata() installMetadata {
	return installMetadata{
		Description: request.Description, Tags: request.Tags,
		NameZH: request.NameZH, NameEN: request.NameEN, DescriptionZH: request.DescriptionZH,
	}
}

// metadata 返回 MCP 安装请求捕获的发布元数据。
func (request MCPInstallRequest) metadata() installMetadata {
	return installMetadata{
		Description: request.Description, Tags: request.Tags,
		NameZH: request.NameZH, NameEN: request.NameEN, DescriptionZH: request.DescriptionZH,
	}
}

// metadata 返回 Plugin 安装请求捕获的发布元数据。
func (request PluginInstallRequest) metadata() installMetadata {
	return installMetadata{
		Description: request.Description, Tags: request.Tags,
		NameZH: request.NameZH, NameEN: request.NameEN, DescriptionZH: request.DescriptionZH,
	}
}

// SkillInstallRequest 描述单个 Agent 的已解析文件型 Skill 安装事务输入。
type SkillInstallRequest struct {
	Agent       domain.AgentID
	Power       domain.PowerRef
	Description string
	Tags        []string
	Previous    *domain.PowerRef
	StagingRoot string
	Adapter     adapter.SkillAdapter
	// v2 展示字段：安装时捕获、随 journal 持久化，供离线 recovery 重放写回清单（ADR-040）。
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// SkillUninstallRequest 描述单个 Agent 的已解析文件型 Skill 卸载事务输入。
type SkillUninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Adapter adapter.SkillAdapter
}

// AgentInstallRequest 描述单个 Agent 的已解析文件型 Agent 安装事务输入。
type AgentInstallRequest struct {
	Agent       domain.AgentID
	Power       domain.PowerRef
	Description string
	Tags        []string
	Previous    *domain.PowerRef
	StagingRoot string
	Adapter     adapter.AgentAdapter
	// v2 展示字段：安装时捕获、随 journal 持久化，供离线 recovery 重放写回清单（ADR-040）。
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// AgentUninstallRequest 描述单个 Agent 的已解析文件型 Agent 卸载事务输入。
type AgentUninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Adapter adapter.AgentAdapter
}

// MCPInstallRequest 描述单个 Agent 的 MCP 安装或版本切换输入；定义允许为空以支持无副作用的冲突和漂移复查。
type MCPInstallRequest struct {
	Agent          domain.AgentID
	Power          domain.PowerRef
	Description    string
	Tags           []string
	Previous       *domain.PowerRef
	Server         *domain.MCPServer
	PreviousServer *domain.MCPServer
	Adapter        adapter.MCPAdapter
	// v2 展示字段：安装时捕获、随 journal 持久化，供离线 recovery 重放写回清单（ADR-040）。
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// MCPUninstallRequest 描述单个 Agent 的 MCP 卸载输入；卸载只依赖本地清单和目标 CLI 状态。
type MCPUninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Adapter adapter.MCPAdapter
}

// PluginMarketplaceInstallRequest 描述本地 marketplace 首装或版本切换目标。
type PluginMarketplaceInstallRequest struct {
	Target   adapter.MarketplaceTarget
	Previous *adapter.MarketplaceTarget
	Adapter  adapter.MarketplacePluginAdapter
}

// PluginInstallRequest 描述已解析的 Plugin 首装或版本切换输入；不得携带 Cloud 或资源构建职责。
type PluginInstallRequest struct {
	Agent          domain.AgentID
	Power          domain.PowerRef
	Description    string
	Tags           []string
	Previous       *domain.PowerRef
	StagingRoot    string
	Adapter        adapter.PluginAdapter
	Target         adapter.PluginTarget
	PreviousTarget *adapter.PluginTarget
	Marketplace    *PluginMarketplaceInstallRequest
	// v2 展示字段：安装时捕获、随 journal 持久化，供离线 recovery 重放写回清单（ADR-040）。
	NameZH        string
	NameEN        string
	DescriptionZH string
}

// PluginUninstallRequest 描述无需 Cloud 即可执行的 Plugin 卸载输入。
type PluginUninstallRequest struct {
	Agent              domain.AgentID
	Power              domain.PowerRef
	Adapter            adapter.PluginAdapter
	Target             adapter.PluginTarget
	Marketplace        *adapter.MarketplaceTarget
	MarketplaceAdapter adapter.MarketplacePluginAdapter
}

// UninstallResult 描述卸载事务已确认的目标状态；该事实可与后续错误同时返回。
type UninstallResult struct {
	TargetMissing bool
}

// ManifestWriter 定义提交后的清单写入，以及安装预检发现陈旧清单时不创建 journal 的单条移除操作。
type ManifestWriter interface {
	Set(domain.AgentID, domain.InstalledPower) error
	Remove(domain.AgentID, domain.PowerType, string) error
}

// Service 实现集中 journal 驱动的事务管理。
type Service struct {
	manifests      ManifestWriter
	journals       JournalStore
	mcpAdapters    MCPAdapterResolver
	pluginAdapters PluginAdapterResolver
	agentAdapters  AgentAdapterResolver
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
		if resolver, ok := mcpAdapters[0].(AgentAdapterResolver); ok {
			service.agentAdapters = resolver
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
