// Package cache 提供能力缓存的占位接口。
package cache

import "apm/internal/domain"

// Content 表示缓存内容的位置和校验和。
type Content struct {
	Path     string
	Checksum string
}

// PowerCache 定义能力缓存的读取和写入操作。
type PowerCache interface {
	Fetch(domain.PowerRef) (Content, error)
	Put(domain.PowerRef, []byte) error
}
