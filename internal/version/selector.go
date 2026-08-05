// Package version 提供版本选择的占位类型。
package version

// Selector 是版本选择器的零值占位实现。
type Selector struct{}

// NewSelector 创建零值版本选择器。
func NewSelector() *Selector {
	return &Selector{}
}

// Select 从候选版本中选择匹配请求的版本。
func (s *Selector) Select([]string, string) (string, error) {
	return "", nil
}
