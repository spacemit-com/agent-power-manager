package cloud

import "errors"

// ErrNotFound 表示请求的云端能力不存在。
var ErrNotFound = errors.New("cloud power not found")
