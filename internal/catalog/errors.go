// Package catalog 提供版本化资源目录的严格编解码与不可变查询快照。
package catalog

import "errors"

var (
	// ErrInvalidCatalog 表示目录字节或目录数据不满足 v1 契约。
	ErrInvalidCatalog = errors.New("invalid catalog")
	// ErrNotFound 表示快照中不存在所请求的资源。
	ErrNotFound = errors.New("catalog entry not found")
	// ErrAmbiguous 表示未指定类型的名称匹配到多个 Power 类型。
	ErrAmbiguous = errors.New("catalog entry type is ambiguous")
	// ErrCatalogUnavailable 表示本地没有可用目录且本次同步未能完成。
	ErrCatalogUnavailable = errors.New("catalog unavailable")
)
