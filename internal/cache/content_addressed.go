package cache

import "apm/internal/domain"

// ContentAddressed 是内容寻址缓存的零值占位实现。
type ContentAddressed struct{}

// NewContentAddressed 创建零值内容寻址缓存。
func NewContentAddressed() *ContentAddressed {
	return &ContentAddressed{}
}

// Fetch 获取指定能力引用的缓存内容。
func (c *ContentAddressed) Fetch(domain.PowerRef) (Content, error) {
	return Content{}, nil
}

// Put 写入指定能力引用的缓存内容。
func (c *ContentAddressed) Put(domain.PowerRef, []byte) error {
	return nil
}
