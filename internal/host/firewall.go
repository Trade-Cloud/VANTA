package host

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Фаерволы, которыми умеем управлять.
const (
	FWUfw       = "ufw"
	FWFirewalld = "firewalld"
	FWNft       = "nftables"
	FWIptables  = "iptables"
)

// FWRule — правило. Для ufw Num — номер для удаления.
type FWRule struct {
	Num    int
	To     string // порт/протокол или сервис
	Action string // ALLOW IN / DENY / REJECT / LIMIT
	From   string
	Raw    string
}

// Firewall — состояние фаервола.
type Firewall struct {
	Kind    string // FWUfw… "" — не найден
	Active  bool
	Summary string // статус по-человечески (ufw verbose / firewall-cmd --list-all)
	Rules   []FWRule
	Raw     string // полный ruleset (nft / iptables-save) для просмотра
	Zone    string // firewalld: зона по умолчанию
}

// GetFirewall определяет фаервол и читает правила (нужен root).
func (h *Host) GetFirewall(ctx context.Context) (*Firewall, error) {
	out, err := h.Sudo(ctx, `if command -v ufw >/dev/null; then echo ufw; `+
		`elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then echo firewalld; `+
		`elif command -v nft >/dev/null; then echo nftables; `+
		`elif command -v iptables >/dev/null; then echo iptables; fi`)
	if err != nil {
		return nil, err
	}
	fw := &Firewall{Kind: strings.TrimSpace(out)}
	switch fw.Kind {
	case FWUfw:
		st, err := h.Sudo(ctx, "ufw status verbose; echo ---; ufw status numbered")
		if err != nil {
			return nil, err
		}
		verbose, numbered, _ := strings.Cut(st, "---\n")
		fw.Summary = strings.TrimSpace(verbose)
		fw.Active = strings.Contains(verbose, "Status: active")
		fw.Rules = parseUfwNumbered(numbered)
	case FWFirewalld:
		st, err := h.Sudo(ctx, "firewall-cmd --get-default-zone; echo ---; firewall-cmd --list-all")
		if err != nil {
			return nil, err
		}
		zone, all, _ := strings.Cut(st, "---\n")
		fw.Zone = strings.TrimSpace(zone)
		fw.Summary = strings.TrimSpace(all)
		fw.Active = true
		for _, l := range lines(all) {
			k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch k {
			case "services":
				for _, s := range strings.Fields(v) {
					fw.Rules = append(fw.Rules, FWRule{To: s, Action: "service", From: "any", Raw: "service:" + s})
				}
			case "ports":
				for _, s := range strings.Fields(v) {
					fw.Rules = append(fw.Rules, FWRule{To: s, Action: "port", From: "any", Raw: "port:" + s})
				}
			}
			if strings.HasPrefix(strings.TrimSpace(l), "rule ") {
				r := strings.TrimSpace(l)
				fw.Rules = append(fw.Rules, FWRule{To: r, Action: "rich", Raw: "rich:" + r})
			}
		}
	case FWNft:
		st, err := h.Sudo(ctx, "nft list ruleset")
		if err != nil {
			return nil, err
		}
		fw.Raw = st
		fw.Active = strings.TrimSpace(st) != ""
		fw.Summary = "nftables: правила только для просмотра. Для управления поставьте ufw или firewalld."
	case FWIptables:
		st, err := h.Sudo(ctx, "iptables -S; echo; ip6tables -S 2>/dev/null")
		if err != nil {
			return nil, err
		}
		fw.Raw = st
		fw.Active = true
		fw.Summary = "iptables: правила только для просмотра. Для управления поставьте ufw или firewalld."
	default:
		fw.Summary = "Фаервол не найден (ufw, firewalld, nftables, iptables)."
	}
	return fw, nil
}

// [ 1] 22/tcp                     ALLOW IN    Anywhere
var ufwRuleRe = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s{2,}(ALLOW|DENY|REJECT|LIMIT)(?:\s+(IN|OUT|FWD))?\s+(.+?)\s*$`)

func parseUfwNumbered(s string) []FWRule {
	var out []FWRule
	for _, l := range lines(s) {
		m := ufwRuleRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		act := m[3]
		if m[4] != "" {
			act += " " + m[4]
		}
		out = append(out, FWRule{Num: n, To: strings.TrimSpace(m[2]), Action: act, From: m[5], Raw: strings.TrimSpace(l)})
	}
	return out
}

// NewRule — правило из формы.
type NewRule struct {
	Action string // allow / deny / reject / limit
	Port   string // 22, 8000:8100, или имя сервиса (OpenSSH, http)
	Proto  string // tcp / udp / "" — оба
	From   string // IP/CIDR, "" — откуда угодно
}

var (
	portRe  = regexp.MustCompile(`^\d{1,5}([:-]\d{1,5})?$`)
	svcRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _.-]{0,40}$`)
	cidrRe  = regexp.MustCompile(`^[0-9A-Fa-f:.]+(/\d{1,3})?$`)
	actions = map[string]bool{"allow": true, "deny": true, "reject": true, "limit": true}
)

func (r NewRule) validate() error {
	if !actions[r.Action] {
		return errors.New("действие: allow, deny, reject или limit")
	}
	if !portRe.MatchString(r.Port) && !svcRe.MatchString(r.Port) {
		return errors.New("порт: число, диапазон 8000:8100 или имя сервиса")
	}
	if r.Proto != "" && r.Proto != "tcp" && r.Proto != "udp" {
		return errors.New("протокол: tcp или udp")
	}
	if r.From != "" && !cidrRe.MatchString(r.From) {
		return errors.New("источник: IP-адрес или подсеть (1.2.3.0/24)")
	}
	return nil
}

// AddRule добавляет правило.
func (h *Host) AddRule(ctx context.Context, kind string, r NewRule) error {
	if err := r.validate(); err != nil {
		return err
	}
	isPort := portRe.MatchString(r.Port)
	switch kind {
	case FWUfw:
		var cmd string
		if r.From == "" && (!isPort || r.Proto == "") && !strings.ContainsAny(r.Port, ":-") {
			target := r.Port
			if r.Proto != "" {
				target += "/" + r.Proto
			}
			cmd = "ufw " + r.Action + " " + Q(target)
		} else {
			from := "any"
			if r.From != "" {
				from = r.From
			}
			cmd = "ufw " + r.Action + " from " + Q(from) + " to any"
			if isPort {
				cmd += " port " + Q(strings.ReplaceAll(r.Port, "-", ":"))
				proto := r.Proto
				if proto == "" && strings.ContainsAny(r.Port, ":-") {
					proto = "tcp" // диапазон в ufw только с протоколом
				}
				if proto != "" {
					cmd += " proto " + proto
				}
			} else {
				cmd += " app " + Q(r.Port)
			}
		}
		_, err := h.Sudo(ctx, cmd)
		return err
	case FWFirewalld:
		if r.Action != "allow" || r.From != "" {
			// Всё, кроме простого «открыть порт», — rich rule.
			fam := "ipv4"
			if strings.Contains(r.From, ":") {
				fam = "ipv6"
			}
			rule := "rule family=" + fam
			if r.From != "" {
				rule += ` source address="` + r.From + `"`
			}
			if isPort {
				proto := r.Proto
				if proto == "" {
					proto = "tcp"
				}
				rule += ` port port="` + strings.ReplaceAll(r.Port, ":", "-") + `" protocol="` + proto + `"`
			} else {
				rule += ` service name="` + r.Port + `"`
			}
			act := map[string]string{"allow": "accept", "deny": "drop", "reject": "reject", "limit": `accept limit value="6/m"`}[r.Action]
			rule += " " + act
			_, err := h.Sudo(ctx, "firewall-cmd --permanent --add-rich-rule="+Q(rule)+" && firewall-cmd --reload")
			return err
		}
		if isPort {
			proto := r.Proto
			if proto == "" {
				proto = "tcp"
			}
			_, err := h.Sudo(ctx, "firewall-cmd --permanent --add-port="+Q(strings.ReplaceAll(r.Port, ":", "-")+"/"+proto)+" && firewall-cmd --reload")
			return err
		}
		_, err := h.Sudo(ctx, "firewall-cmd --permanent --add-service="+Q(r.Port)+" && firewall-cmd --reload")
		return err
	}
	return fmt.Errorf("добавление правил для %s не поддерживается", kind)
}

// DeleteRule удаляет правило.
func (h *Host) DeleteRule(ctx context.Context, kind string, r FWRule) error {
	switch kind {
	case FWUfw:
		if r.Num <= 0 {
			return errors.New("нет номера правила")
		}
		_, err := h.Sudo(ctx, fmt.Sprintf("ufw --force delete %d", r.Num))
		return err
	case FWFirewalld:
		typ, val, _ := strings.Cut(r.Raw, ":")
		var opt string
		switch typ {
		case "service":
			opt = "--remove-service="
		case "port":
			opt = "--remove-port="
		case "rich":
			opt = "--remove-rich-rule="
		default:
			return errors.New("неизвестное правило")
		}
		_, err := h.Sudo(ctx, "firewall-cmd --permanent "+opt+Q(val)+" && firewall-cmd --reload")
		return err
	}
	return fmt.Errorf("удаление правил для %s не поддерживается", kind)
}

// SetFirewall включает/выключает ufw. Перед включением добавляет правило
// для порта SSH, чтобы не потерять доступ.
func (h *Host) SetFirewall(ctx context.Context, kind string, on bool, sshPort int) error {
	switch kind {
	case FWUfw:
		if on {
			if sshPort <= 0 {
				sshPort = 22
			}
			_, err := h.Sudo(ctx, fmt.Sprintf("ufw allow %d/tcp && ufw --force enable", sshPort))
			return err
		}
		_, err := h.Sudo(ctx, "ufw disable")
		return err
	case FWFirewalld:
		if on {
			_, err := h.Sudo(ctx, "systemctl enable --now firewalld")
			return err
		}
		_, err := h.Sudo(ctx, "systemctl disable --now firewalld")
		return err
	}
	return fmt.Errorf("для %s не поддерживается", kind)
}
