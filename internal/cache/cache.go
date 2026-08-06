// Package cache 提供能力缓存接口。
package cache

import "apm/internal/domain"

// Content 表示缓存内容的位置。
type Content struct {
	Path string
}

// PowerCache 定义能力缓存的读取操作。
type PowerCache interface {
	Fetch(domain.PowerRef) (Content, error)
}
