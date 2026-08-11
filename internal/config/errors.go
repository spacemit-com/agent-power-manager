package config

import "errors"

// ErrInvalidPower 表示能力引用的类别、名称或版本无效。
var ErrInvalidPower = errors.New("invalid power reference")
