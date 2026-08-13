// Package cloud 提供能力资源查询与获取接口。
package cloud

import "apm/internal/domain"

// Client 定义云端能力查询操作。
type Client interface {
	List() ([]domain.PowerRef, error)
	Get(domain.PowerRef) error
	GetMCP(domain.PowerRef) (domain.MCPServer, error)
	GetPlugin(domain.PowerRef) (domain.PluginDefinition, error)
}
