package host

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Iface — сетевой интерфейс.
type Iface struct {
	Name    string
	State   string // UP / DOWN / UNKNOWN
	MAC     string
	MTU     int
	Addrs   []string // CIDR
	RxBytes uint64
	TxBytes uint64
	RxRate  float64 // байт/с, <0 — неизвестно
	TxRate  float64
}

// Listener — открытый порт.
type Listener struct {
	Proto   string // tcp / udp
	Local   string // адрес:порт
	Port    string
	Process string // users:(("sshd",pid=1,fd=3)) → sshd/1
}

// Network — снимок сети.
type Network struct {
	Ifaces    []Iface
	Listeners []Listener
	Routes    []string
	DNS       []string
	Summary   string // ss -s
	At        time.Time
}

const networkCmd = `ip -o link 2>/dev/null; echo ---; ip -o addr 2>/dev/null; echo ---; cat /proc/net/dev; echo ---; ` +
	`ip route 2>/dev/null; ip -6 route 2>/dev/null | grep -v '^fe80'; echo ---; grep -E '^nameserver' /etc/resolv.conf 2>/dev/null; echo ---; ss -s 2>/dev/null | head -n 3; echo ---; `

// GetNetwork снимает состояние сети. prev — прошлый снимок для скорости.
// root — порты читать через sudo (с именами процессов).
func (h *Host) GetNetwork(ctx context.Context, prev *Network, root bool) (*Network, error) {
	cmd := networkCmd + "ss -tulpnH 2>/dev/null || ss -tulpn 2>/dev/null | tail -n +2"
	var out string
	var err error
	if root {
		out, err = h.Sudo(ctx, cmd)
	} else {
		out, err = h.Run(ctx, cmd)
	}
	if err != nil {
		return nil, err
	}
	n := &Network{At: time.Now()}
	parts := strings.Split(out, "---\n")
	for len(parts) < 7 {
		parts = append(parts, "")
	}
	byName := map[string]*Iface{}
	order := []string{}
	get := func(name string) *Iface {
		name = strings.TrimSuffix(name, ":")
		if at := strings.Index(name, "@"); at > 0 {
			name = name[:at]
		}
		if i := byName[name]; i != nil {
			return i
		}
		i := &Iface{Name: name, RxRate: -1, TxRate: -1}
		byName[name] = i
		order = append(order, name)
		return i
	}
	// 2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc ... state UP mode DEFAULT ... link/ether aa:bb brd ...
	for _, l := range lines(parts[0]) {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		i := get(f[1])
		for k := 2; k < len(f)-1; k++ {
			switch f[k] {
			case "mtu":
				i.MTU, _ = strconv.Atoi(f[k+1])
			case "state":
				i.State = f[k+1]
			case "link/ether", "link/loopback", "link/none":
				if f[k] == "link/ether" {
					i.MAC = f[k+1]
				}
			}
		}
	}
	// 2: eth0    inet 1.2.3.4/24 brd ...
	for _, l := range lines(parts[1]) {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		if f[2] == "inet" || f[2] == "inet6" {
			i := get(f[1])
			i.Addrs = append(i.Addrs, f[3])
		}
	}
	for _, l := range lines(parts[2]) {
		name, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		i := get(strings.TrimSpace(name))
		i.RxBytes, _ = strconv.ParseUint(f[0], 10, 64)
		i.TxBytes, _ = strconv.ParseUint(f[8], 10, 64)
	}
	for _, name := range order {
		n.Ifaces = append(n.Ifaces, *byName[name])
	}
	if prev != nil {
		dt := n.At.Sub(prev.At).Seconds()
		for k := range n.Ifaces {
			for _, p := range prev.Ifaces {
				if p.Name == n.Ifaces[k].Name && dt > 0 && n.Ifaces[k].RxBytes >= p.RxBytes && n.Ifaces[k].TxBytes >= p.TxBytes {
					n.Ifaces[k].RxRate = float64(n.Ifaces[k].RxBytes-p.RxBytes) / dt
					n.Ifaces[k].TxRate = float64(n.Ifaces[k].TxBytes-p.TxBytes) / dt
				}
			}
		}
	}
	n.Routes = lines(parts[3])
	for _, l := range lines(parts[4]) {
		if f := strings.Fields(l); len(f) >= 2 {
			n.DNS = append(n.DNS, f[1])
		}
	}
	n.Summary = strings.TrimSpace(parts[5])
	// udp UNCONN 0 0 0.0.0.0:68 0.0.0.0:* users:(("dhclient",pid=1,fd=6))
	for _, l := range lines(parts[6]) {
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		ls := Listener{Proto: f[0], Local: f[4]}
		if i := strings.LastIndex(ls.Local, ":"); i >= 0 {
			ls.Port = ls.Local[i+1:]
		}
		if len(f) > 6 {
			ls.Process = parseSSUsers(strings.Join(f[6:], " "))
		}
		n.Listeners = append(n.Listeners, ls)
	}
	return n, nil
}

// users:(("nginx",pid=10,fd=6),("nginx",pid=11,fd=6)) → nginx/10
func parseSSUsers(s string) string {
	i := strings.Index(s, `(("`)
	if i < 0 {
		return ""
	}
	s = s[i+3:]
	name, rest, _ := strings.Cut(s, `"`)
	pid := ""
	if j := strings.Index(rest, "pid="); j >= 0 {
		pid = rest[j+4:]
		if k := strings.IndexAny(pid, ",)"); k >= 0 {
			pid = pid[:k]
		}
	}
	if pid != "" {
		return name + "/" + pid
	}
	return name
}

// Ping — ping -c 4 до адреса с сервера.
func (h *Host) Ping(ctx context.Context, target string) (string, error) {
	out, err := h.Run(ctx, "ping -c 4 -W 2 "+Q(target)+" 2>&1")
	if err != nil && out != "" {
		return out, nil
	}
	return out, err
}

// Rate — скорость для человека: 1.2 MiB/s.
func Rate(bps float64) string {
	if bps < 0 {
		return "—"
	}
	return Bytes(uint64(bps)) + "/s"
}
