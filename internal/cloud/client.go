// Package cloud 提供云端能力服务的占位接口。
package cloud

import "apm/internal/domain"

// Client 定义云端能力查询操作。
type Client interface {
	List(Query) ([]domain.PowerRef, error)
	Get(domain.PowerRef) (PowerMeta, error)
}
