package logging

import (
	"context"
	"log/slog"
)

type debugEnabledFunc func() bool

type debugHandler struct {
	handler slog.Handler
	enabled debugEnabledFunc
}

func (h *debugHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.enabled != nil && h.enabled() && h.handler.Enabled(ctx, level)
}

func (h *debugHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.handler.Handle(ctx, record)
}

func (h *debugHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &debugHandler{handler: h.handler.WithAttrs(attrs), enabled: h.enabled}
}

func (h *debugHandler) WithGroup(name string) slog.Handler {
	return &debugHandler{handler: h.handler.WithGroup(name), enabled: h.enabled}
}

type levelRouter struct {
	debug slog.Handler
	info  slog.Handler
	warn  slog.Handler
	error slog.Handler
}

func (h *levelRouter) target(level slog.Level) slog.Handler {
	switch {
	case level < slog.LevelInfo:
		return h.debug
	case level < slog.LevelWarn:
		return h.info
	case level < slog.LevelError:
		return h.warn
	default:
		return h.error
	}
}

func (h *levelRouter) Enabled(ctx context.Context, level slog.Level) bool {
	return h.target(level).Enabled(ctx, level)
}

func (h *levelRouter) Handle(ctx context.Context, record slog.Record) error {
	return h.target(record.Level).Handle(ctx, record)
}

func (h *levelRouter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelRouter{
		debug: h.debug.WithAttrs(attrs),
		info:  h.info.WithAttrs(attrs),
		warn:  h.warn.WithAttrs(attrs),
		error: h.error.WithAttrs(attrs),
	}
}

func (h *levelRouter) WithGroup(name string) slog.Handler {
	return &levelRouter{
		debug: h.debug.WithGroup(name),
		info:  h.info.WithGroup(name),
		warn:  h.warn.WithGroup(name),
		error: h.error.WithGroup(name),
	}
}
