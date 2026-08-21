package cloud

import "errors"

// ErrNotFound 表示 Catalog 中不存在请求的精确能力标识。
var ErrNotFound = errors.New("cloud power not found")
