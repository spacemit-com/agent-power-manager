package domain

import (
	"fmt"
	"regexp"
	"time"
)

// apcTimestampWithoutZone 匹配测试环境返回的无时区 ISO 8601 时间戳（可带 1-9 位小数秒）。
var apcTimestampWithoutZone = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?$`)

// ParseAPCTimestamp 解析 APC 返回的时间戳：接受 RFC3339（含小数秒与 UTC/偏移量）或测试环境
// 实际返回的无时区 ISO 8601 时间戳（如 2026-09-07T10:17:58，可带 1-9 位小数秒）。无时区值
// 按 UTC 解释，保证 submission list/timeline 排序比较的确定性。
func ParseAPCTimestamp(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	if apcTimestampWithoutZone.MatchString(value) {
		return time.Parse("2006-01-02T15:04:05", value)
	}
	return time.Time{}, fmt.Errorf("timestamp %q must be RFC3339 or ISO 8601 without timezone", value)
}
