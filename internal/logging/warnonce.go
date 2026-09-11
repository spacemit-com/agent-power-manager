package logging

import (
	"context"
	"log/slog"
	"sync"
)

// warnedOnceKeys 记录本进程已输出过的去重 key；仅用于抑制重复告警，不做跨命令状态。
var warnedOnceKeys sync.Map

// WarnOnce 对同一 key 只输出一次 warning 级日志。
// 用途：目录信任检查在单次命令中会多次走到同一祖先目录（如 ~/.cache），
// 逐次输出会刷屏；key 应能唯一标识被检查的对象与条件。
func WarnOnce(key, message string, attributes ...slog.Attr) {
	if _, loaded := warnedOnceKeys.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	slog.LogAttrs(context.Background(), slog.LevelWarn, message, attributes...)
}
