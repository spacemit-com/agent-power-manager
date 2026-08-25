// Package cloud 提供能力资源查询与获取接口。
package cloud

import (
	"context"

	"apm/internal/domain"
)

// Catalog 定义可取消的 Cloud 能力目录查询边界。
// List 返回稳定展示顺序；Resolve 仅按精确类型和名称解析，不得将不同类型的同名能力混合。
type Catalog interface {
	List(context.Context) ([]domain.PowerRef, error)
	Resolve(context.Context, domain.PowerRef) (domain.PowerRef, error)
}

// CatalogSource 定义目录版本与完整目录下载边界；destination 必须由调用方提供、绝对且尚不存在。
// DownloadCatalog 仅在 destination 本地无法安全使用或清理时包装 ErrCatalogDestination；传输、协议和内容错误不得使用该标记。
type CatalogSource interface {
	CatalogVersion(context.Context) (domain.CatalogVersion, error)
	DownloadCatalog(context.Context, string) error
}

// SkillSource 定义向调用方私有目录下载精确版本 Skill 的边界；destination 必须不存在或是空物理目录。
type SkillSource interface {
	DownloadSkill(context.Context, domain.PowerRef, string) error
}

// PluginSource 定义向调用方私有目录下载精确版本 Plugin 的边界；缓存和 marketplace 由 pluginresource 独占管理。
type PluginSource interface {
	DownloadPlugin(context.Context, domain.PowerRef, string) error
}

// MCPSource 定义读取精确版本 MCP 领域值的边界，不写入本地缓存。
type MCPSource interface {
	GetMCP(context.Context, domain.PowerRef) (domain.MCPServer, error)
}

// AgentSource 定义向调用方私有文件下载精确版本 Agent 的边界；destination 必须不存在。
type AgentSource interface {
	DownloadAgent(context.Context, domain.PowerRef, string) error
}

// Client 定义 Cloud 资源发现、各类精确版本获取及提交边界。
type Client interface {
	Catalog
	SkillSource
	PluginSource
	MCPSource
	AgentSource
	Submission
}
