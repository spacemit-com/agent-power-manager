package logging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// warnedGroupWritableDirectories 记录本进程已提示过的目录，避免同一目录在单次命令中重复告警。
var warnedGroupWritableDirectories sync.Map

// WarnGroupWritableDirectory 对按信任策略放行的组可写目录输出一次 warning。
// 用途：Cloud Remote 与 Catalog Repository 两条目录链都会逐级检查同一个本地目录，
// 甚至同一目录仅在单次命令中就会被两条链各检查一次；本函数按目录路径去重，
// 使每条命令对每个目录只输出一行，且不暴露检查来自哪一条链。
func WarnGroupWritableDirectory(path string, mode uint32) {
	if _, loaded := warnedGroupWritableDirectories.LoadOrStore(path, struct{}{}); loaded {
		return
	}
	slog.LogAttrs(
		context.Background(),
		slog.LevelWarn,
		"local directory permits group entry mutation",
		slog.String("path", path),
		slog.String("mode", fmt.Sprintf("%04o", mode)),
	)
}
