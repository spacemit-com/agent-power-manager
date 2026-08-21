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

// Client 定义 Cloud 资源发现和获取边界。
// Skill 与 Agent 暂保留旧缓存下载语义；Plugin 只写入调用方目录，缓存与 marketplace 由 pluginresource 独占管理。
type Client interface {
	Catalog
	DownloadSkill(domain.PowerRef) error
	GetMCP(context.Context, domain.PowerRef) (domain.MCPServer, error)
	DownloadPlugin(context.Context, domain.PowerRef, string) error
	// DownloadAgent 将指定版本的 Agent yaml 原子复制到本地缓存。
	DownloadAgent(domain.PowerRef) error
}
