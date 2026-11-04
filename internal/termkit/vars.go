package termkit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// Встроенные переменные: считаются в момент запуска.
var builtins = []string{"server", "host", "user", "date", "time", "datetime", "last_exit", "last_output"}

// Builtin — имя встроенной переменной.
func Builtin(name string) bool {
	for _, b := range builtins {
		if b == name {
			return true
		}
	}
	return false
}

// Builtins — список встроенных переменных с описанием (для подсказки в UI).
func Builtins() [][2]string {
	return [][2]string{
		{"server", "название сервера в VANTA"}, {"host", "адрес сервера"}, {"user", "пользователь SSH"},
		{"date", "2026-11-03"}, {"time", "22:40:05"}, {"datetime", "20261103-224005"},
		{"last_exit", "код выхода прошлой команды очереди"}, {"last_output", "вывод прошлой команды очереди"},
	}
}

// Env — значения для подстановки: пользовательские + встроенные.
type Env struct {
	Vars                 map[string]string
	Server, Host, User   string
	LastExit, LastOutput string
}

func (e Env) lookup(name string) (string, bool) {
	now := time.Now()
	switch name {
	case "server":
		return e.Server, true
	case "host":
		return e.Host, true
	case "user":
		return e.User, true
	case "date":
		return now.Format("2006-01-02"), true
	case "time":
		return now.Format("15:04:05"), true
	case "datetime":
		return now.Format("20060102-150405"), true
	case "last_exit":
		return e.LastExit, true
	case "last_output":
		return e.LastOutput, true
	}
	v, ok := e.Vars[name]
	return v, ok
}

// Params — имена {{…}} в тексте по порядку, без повторов.
func Params(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// Missing — параметры, для которых нет значения (их спросят перед запуском).
func (e Env) Missing(s string) []string {
	var out []string
	for _, p := range Params(s) {
		if _, ok := e.lookup(p); !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Expand подставляет {{имя}}. Неизвестное имя — ошибка.
func (e Env) Expand(s string) (string, error) {
	var missing []string
	out := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := placeholderRe.FindStringSubmatch(m)[1]
		v, ok := e.lookup(name)
		if !ok {
			missing = append(missing, name)
			return m
		}
		return v
	})
	if len(missing) > 0 {
		return out, fmt.Errorf("нет значения для: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// ExpandOnly подставляет только имена из vals, остальные {{…}} оставляет
// как есть (их подставят при запуске).
func ExpandOnly(s string, vals map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := vals[placeholderRe.FindStringSubmatch(m)[1]]; ok {
			return v
		}
		return m
	})
}
