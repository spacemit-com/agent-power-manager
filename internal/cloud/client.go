// Package cloud 提供能力资源查询与获取接口。
package cloud

import (
	"context"

	"apm/internal/domain"
)

// CatalogSource 定义目录版本与完整目录下载边界；destination 必须由调用方提供、绝对且尚不存在。
// DownloadCatalog 仅在 destination 本地无法安全使用或清理时包装 ErrCatalogDestination；传输、协议和内容错误不得使用该标记。
type CatalogSource interface {
	CatalogVersion(context.Context) (domain.CatalogVersion, error)
	DownloadCatalog(context.Context, string) error
}

// SkillSource 使用已验证 CatalogEntry 向调用方私有目录下载精确版本 Skill；
// entry 是内容 metadata 的唯一权威，destination 必须不存在或是空物理目录。
type SkillSource interface {
	DownloadSkill(context.Context, domain.CatalogEntry, string) error
}

// PluginSource 使用已验证 CatalogEntry 下载精确版本 Plugin；不得按 Ref 二次查询 metadata，
// 缓存和 marketplace 由 pluginresource 独占管理。
type PluginSource interface {
	DownloadPlugin(context.Context, domain.CatalogEntry, string) error
}

// MCPSource 使用已验证 CatalogEntry 读取精确版本 MCP 领域值，不重建 metadata，也不写入本地缓存。
type MCPSource interface {
	GetMCP(context.Context, domain.CatalogEntry) (domain.MCPServer, error)
}

// AgentSource 使用已验证 CatalogEntry 下载精确版本 Agent；不得二次查询 metadata，destination 必须不存在。
type AgentSource interface {
	DownloadAgent(context.Context, domain.CatalogEntry, string) error
}

// Client 聚合唯一目录快照、各类精确内容与提交边界；
// 资源查询只能在本地 Catalog Snapshot 上完成，Provider 不得暴露分页或 metadata 解析接口。
type Client interface {
	CatalogSource
	SkillSource
	PluginSource
	MCPSource
	AgentSource
	Submission
}
