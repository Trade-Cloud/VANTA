package host

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Service — systemd-юнит типа service.
type Service struct {
	Unit        string // nginx.service
	Load        string // loaded / not-found / masked
	Active      string // active / inactive / failed
	Sub         string // running / exited / dead
	Description string
	Enabled     string // enabled / disabled / static / masked / ""
}

// Name — без суффикса .service.
func (s Service) Name() string { return strings.TrimSuffix(s.Unit, ".service") }

// HasSystemd — сервер управляется systemd.
func (h *Host) HasSystemd(ctx context.Context) bool {
	_, err := h.Run(ctx, "test -d /run/systemd/system && command -v systemctl >/dev/null")
	return err == nil
}

// Services — все сервисы (и запущенные, и нет) с флагом автозапуска.
func (h *Host) Services(ctx context.Context) ([]Service, error) {
	out, err := h.Run(ctx, "systemctl list-units --type=service --all --no-legend --no-pager --plain; echo ---; "+
		"systemctl list-unit-files --type=service --no-legend --no-pager")
	if err != nil {
		return nil, err
	}
	units, files, _ := strings.Cut(out, "---\n")
	enabled := map[string]string{}
	for _, l := range lines(files) {
		f := strings.Fields(l)
		if len(f) >= 2 {
			enabled[f[0]] = f[1]
		}
	}
	seen := map[string]bool{}
	var list []Service
	for _, l := range lines(units) {
		l = strings.TrimLeft(l, "●* ")
		f := strings.Fields(l)
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		s := Service{Unit: f[0], Load: f[1], Active: f[2], Sub: f[3], Enabled: enabled[f[0]]}
		if len(f) > 4 {
			s.Description = strings.Join(f[4:], " ")
		}
		seen[s.Unit] = true
		list = append(list, s)
	}
	// Установленные, но ни разу не загруженные юниты list-units не показывает.
	for unit, en := range enabled {
		if !seen[unit] && !strings.Contains(unit, "@.") {
			list = append(list, Service{Unit: unit, Load: "—", Active: "inactive", Sub: "dead", Enabled: en})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Unit < list[j].Unit })
	return list, nil
}

// ServiceActions — действия над сервисом. Danger — нужно подтверждение.
var ServiceActions = []struct {
	Verb, Label string
	Danger      bool
}{
	{"start", "Запустить", false},
	{"stop", "Остановить", true},
	{"restart", "Перезапустить", true},
	{"reload", "Перечитать конфиг", false},
	{"enable", "Включить автозапуск", false},
	{"disable", "Выключить автозапуск", true},
	{"mask", "Замаскировать", true},
	{"unmask", "Снять маску", false},
}

// ServiceAction — systemctl <verb> <unit>.
func (h *Host) ServiceAction(ctx context.Context, verb, unit string) error {
	ok := false
	for _, a := range ServiceActions {
		ok = ok || a.Verb == verb
	}
	if !ok {
		return fmt.Errorf("неизвестное действие %q", verb)
	}
	_, err := h.Sudo(ctx, "systemctl "+verb+" -- "+Q(unit))
	return err
}

// ServiceStatus — systemctl status с последними строками лога.
func (h *Host) ServiceStatus(ctx context.Context, unit string) (string, error) {
	out, err := h.Sudo(ctx, "systemctl status --no-pager -l -n 40 -- "+Q(unit)+" 2>&1")
	if out != "" {
		return out, nil // status возвращает ненулевой код для остановленных
	}
	return out, err
}

// UnitFile — systemctl cat (текст юнита и drop-in).
func (h *Host) UnitFile(ctx context.Context, unit string) (string, error) {
	return h.Run(ctx, "systemctl cat --no-pager -- "+Q(unit)+" 2>&1")
}

// DaemonReload — systemctl daemon-reload.
func (h *Host) DaemonReload(ctx context.Context) error {
	_, err := h.Sudo(ctx, "systemctl daemon-reload")
	return err
}
