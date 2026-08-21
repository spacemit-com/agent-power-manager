// Package contentpolicy 提供 Local 与 Remote Cloud provider 共用的纯内容安全策略。
package contentpolicy

import (
	"bytes"
	"unicode/utf8"
)

// IsUnsafeText 拒绝可执行文件、归档伪装以及不满足 UTF-8 文本约束的内容。
func IsUnsafeText(contents []byte) bool {
	for _, signature := range [][]byte{
		{'P', 'K', 0x03, 0x04},
		{'P', 'K', 0x05, 0x06},
		{'P', 'K', 0x07, 0x08},
		{0x1f, 0x8b},
		{'B', 'Z', 'h'},
		{0xfd, '7', 'z', 'X', 'Z', 0x00},
		{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c},
		{'R', 'a', 'r', '!', 0x1a, 0x07},
		{'!', '<', 'a', 'r', 'c', 'h', '>', '\n'},
		{0x7f, 'E', 'L', 'F'},
		{'M', 'Z'},
		{0x00, 'a', 's', 'm'},
		{0xfe, 0xed, 0xfa, 0xce},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xcf, 0xfa, 0xed, 0xfe},
		{0xca, 0xfe, 0xba, 0xbe},
		{0xbe, 0xba, 0xfe, 0xca},
		{0xca, 0xfe, 0xba, 0xbf},
		{0xbf, 0xba, 0xfe, 0xca},
	} {
		if bytes.HasPrefix(contents, signature) {
			return true
		}
	}
	if len(contents) >= 262 && bytes.Equal(contents[257:262], []byte("ustar")) {
		return true
	}
	if !utf8.Valid(contents) || bytes.IndexByte(contents, 0) >= 0 {
		return true
	}
	for _, character := range contents {
		if character < 0x20 && character != '\n' && character != '\r' && character != '\t' && character != '\f' {
			return true
		}
	}
	return false
}
