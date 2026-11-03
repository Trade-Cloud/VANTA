package debugbridge

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	"github.com/Trade-Cloud/vanta/internal/debugproto"
)

// logHandler пишет в next (stderr) и параллельно шлёт запись в debugger.
type logHandler struct {
	b      *Bridge
	next   slog.Handler
	attrs  map[string]string
	prefix string // текущая группа WithGroup, с точкой на конце
}

func (h *logHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.next.Handle(ctx, r)

	attrs := make(map[string]string, len(h.attrs)+r.NumAttrs())
	for k, v := range h.attrs {
		attrs[k] = v
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(attrs, h.prefix, a)
		return true
	})
	entry := &debugproto.LogEntry{Level: r.Level.String(), Message: r.Message, Attrs: attrs}
	if r.PC != 0 {
		f, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
		if f.File != "" {
			entry.Source = fmt.Sprintf("%s:%d", shortPath(f.File), f.Line)
		}
	}
	h.b.broadcast(debugproto.Message{Type: debugproto.TypeLog, Time: r.Time, Log: entry})
	return err
}

func (h *logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	nh := h.clone()
	nh.next = h.next.WithAttrs(as)
	for _, a := range as {
		addAttr(nh.attrs, h.prefix, a)
	}
	return nh
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := h.clone()
	nh.next = h.next.WithGroup(name)
	nh.prefix = h.prefix + name + "."
	return nh
}

func (h *logHandler) clone() *logHandler {
	m := make(map[string]string, len(h.attrs))
	for k, v := range h.attrs {
		m[k] = v
	}
	return &logHandler{b: h.b, next: h.next, attrs: m, prefix: h.prefix}
}

func addAttr(dst map[string]string, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, ga := range a.Value.Group() {
			addAttr(dst, p, ga)
		}
		return
	}
	dst[prefix+a.Key] = a.Value.String()
}

// shortPath оставляет два последних элемента пути: "pkg/file.go".
func shortPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}
