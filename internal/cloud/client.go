// Package cloud 提供能力资源查询与获取接口。
package cloud

import (
	"context"

	"apm/internal/domain"
)

// Client 定义 Cloud 资源发现和获取边界。
// Skill 延续旧缓存下载语义；Plugin 只写入调用方目录，缓存与 marketplace 由 pluginresource 独占管理。
type Client interface {
	List() ([]domain.PowerRef, error)
	DownloadSkill(domain.PowerRef) error
	GetMCP(domain.PowerRef) (domain.MCPServer, error)
	DownloadPlugin(context.Context, domain.PowerRef, string) error
	// DownloadAgent 将指定版本的 Agent yaml 原子复制到本地缓存。
	DownloadAgent(domain.PowerRef) error
}
