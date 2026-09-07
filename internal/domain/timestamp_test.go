package domain

import (
	"testing"
	"time"
)

func TestParseAPCTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
		// wantUTCValue 是成功用例解析结果规范化到 UTC 后的 RFC3339Nano 表示。
		wantUTCValue string
		// wantLocationUTC 要求无时区用例的解析结果位置为 UTC。
		wantLocationUTC bool
	}{
		{name: "RFC3339 Z", value: "2026-08-24T10:00:00Z", wantUTCValue: "2026-08-24T10:00:00Z"},
		{name: "RFC3339 offset", value: "2026-08-24T10:00:00+08:00", wantUTCValue: "2026-08-24T02:00:00Z"},
		{name: "RFC3339 fractional seconds", value: "2026-08-24T10:00:00.123Z", wantUTCValue: "2026-08-24T10:00:00.123Z"},
		{name: "RFC3339 nano seconds", value: "2026-08-24T10:00:00.123456789Z", wantUTCValue: "2026-08-24T10:00:00.123456789Z"},
		{name: "no timezone", value: "2026-09-07T10:17:58", wantUTCValue: "2026-09-07T10:17:58Z", wantLocationUTC: true},
		{name: "no timezone fractional seconds", value: "2026-09-07T10:17:58.123456789", wantUTCValue: "2026-09-07T10:17:58.123456789Z", wantLocationUTC: true},
		{name: "empty", value: "", wantErr: true},
		{name: "missing seconds", value: "2026-09-07T10:17", wantErr: true},
		{name: "space separator", value: "2026-09-07 10:17:58", wantErr: true},
		{name: "impossible month", value: "2026-13-07T10:17:58", wantErr: true},
		{name: "impossible day", value: "2026-09-32T10:17:58", wantErr: true},
		{name: "garbage", value: "yesterday", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAPCTimestamp(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseAPCTimestamp(%q) error = %v, wantErr %t", tt.value, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.UTC().Format(time.RFC3339Nano) != tt.wantUTCValue {
				t.Fatalf("ParseAPCTimestamp(%q) = %q, want %q", tt.value, got.UTC().Format(time.RFC3339Nano), tt.wantUTCValue)
			}
			if tt.wantLocationUTC && got.Location() != time.UTC {
				t.Fatalf("ParseAPCTimestamp(%q) location = %v, want UTC", tt.value, got.Location())
			}
		})
	}
}
