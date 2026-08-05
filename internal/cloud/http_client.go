package cloud

import "apm/internal/domain"

// HTTPClient 是 HTTP 云端能力客户端的零值占位实现。
type HTTPClient struct{}

// NewHTTPClient 创建零值 HTTP 云端能力客户端。
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{}
}

// List 列出符合查询条件的能力引用。
func (c *HTTPClient) List(Query) ([]domain.PowerRef, error) {
	return nil, nil
}

// Get 获取指定能力引用的元数据。
func (c *HTTPClient) Get(domain.PowerRef) (PowerMeta, error) {
	return PowerMeta{}, nil
}
