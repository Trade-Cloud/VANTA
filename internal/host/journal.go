package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LogEntry — запись journald.
type LogEntry struct {
	At       time.Time
	Priority int // 0 emerg … 7 debug, -1 — нет
	Unit     string
	Ident    string
	PID      string
	Message  string
}

// PriorityNames — уровни journald по номеру.
var PriorityNames = []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}

func (e LogEntry) Level() string {
	if e.Priority >= 0 && e.Priority < len(PriorityNames) {
		return PriorityNames[e.Priority]
	}
	return ""
}

// JournalQuery — фильтры journalctl.
type JournalQuery struct {
	Unit     string // -u
	Priority int    // -p 0..7; <0 — все
	Since    string // --since ("1 hour ago", "today", "2026-11-01")
	Grep     string // -g
	Lines    int    // -n
	Boot     bool   // -b (только текущая загрузка)
	Kernel   bool   // -k
}

func (q JournalQuery) args() string {
	var a []string
	if q.Unit != "" {
		a = append(a, "-u "+Q(q.Unit))
	}
	if q.Priority >= 0 && q.Priority <= 7 {
		a = append(a, "-p "+strconv.Itoa(q.Priority))
	}
	if q.Since != "" {
		a = append(a, "--since "+Q(q.Since))
	}
	if q.Grep != "" {
		a = append(a, "-g "+Q(q.Grep))
	}
	if q.Boot {
		a = append(a, "-b")
	}
	if q.Kernel {
		a = append(a, "-k")
	}
	return strings.Join(a, " ")
}

// Journal — последние записи по фильтру, старые первыми.
func (h *Host) Journal(ctx context.Context, q JournalQuery) ([]LogEntry, error) {
	n := q.Lines
	if n <= 0 {
		n = 300
	}
	out, err := h.Sudo(ctx, fmt.Sprintf("journalctl --no-pager -o json -n %d %s", n, q.args()))
	if err != nil && out == "" {
		return nil, err
	}
	var list []LogEntry
	for _, l := range lines(out) {
		if e, ok := parseJournal(l); ok {
			list = append(list, e)
		}
	}
	return list, nil
}

// FollowJournal — journalctl -f: новые записи приходят в fn, пока не отменён ctx.
func (h *Host) FollowJournal(ctx context.Context, q JournalQuery, fn func(LogEntry)) error {
	var buf string
	return h.Stream(ctx, "journalctl --no-pager -o json -n 0 -f "+q.args(), true, func(chunk string) {
		buf += chunk
		for {
			i := strings.IndexByte(buf, '\n')
			if i < 0 {
				return
			}
			line := buf[:i]
			buf = buf[i+1:]
			if e, ok := parseJournal(line); ok {
				fn(e)
			}
		}
	})
}

func parseJournal(l string) (LogEntry, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(l), &m); err != nil {
		return LogEntry{}, false
	}
	str := func(k string) string {
		switch v := m[k].(type) {
		case string:
			return v
		case []any: // бинарное поле — массив байт
			b := make([]byte, 0, len(v))
			for _, x := range v {
				if f, ok := x.(float64); ok {
					b = append(b, byte(f))
				}
			}
			return string(b)
		}
		return ""
	}
	e := LogEntry{Priority: -1, Unit: str("_SYSTEMD_UNIT"), Ident: str("SYSLOG_IDENTIFIER"), PID: str("_PID"), Message: str("MESSAGE")}
	if us, err := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64); err == nil {
		e.At = time.UnixMicro(us)
	}
	if p, err := strconv.Atoi(str("PRIORITY")); err == nil {
		e.Priority = p
	}
	if e.Ident == "" {
		e.Ident = str("_COMM")
	}
	return e, true
}

// JournalDiskUsage — сколько места занимает журнал.
func (h *Host) JournalDiskUsage(ctx context.Context) (string, error) {
	out, err := h.Sudo(ctx, "journalctl --disk-usage 2>&1")
	return strings.TrimSpace(out), err
}

// VacuumJournal — удалить записи старше age ("7d", "2weeks").
func (h *Host) VacuumJournal(ctx context.Context, age string) (string, error) {
	return h.Sudo(ctx, "journalctl --vacuum-time="+Q(age)+" 2>&1")
}
