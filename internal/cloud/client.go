// Package cloud 提供能力资源查询与获取接口。
package cloud

import "apm/internal/domain"

// Client 定义各类云端 Power 的查询与下载能力。
// 文件型资源负责下载到本地缓存，定义型资源只返回安装所需元数据。
type Client interface {
	List() ([]domain.PowerRef, error)
	DownloadSkill(domain.PowerRef) error
	GetMCP(domain.PowerRef) (domain.MCPServer, error)
	GetPlugin(domain.PowerRef) (domain.PluginDefinition, error)
	DownloadPlugin(domain.PowerRef) error
}
