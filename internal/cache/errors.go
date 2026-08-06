package cache

import "errors"

// ErrMiss 表示缓存中不存在请求的能力版本。
var ErrMiss = errors.New("power cache miss")
