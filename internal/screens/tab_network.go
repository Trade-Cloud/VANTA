package screens

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/host"
)

// ---------- Network ----------

// В стиле Resources: без заголовка, «куда подключены», «Обновить» и
// галочки (автообновление — Settings › Настройки приложения,
// core.Store.AutoRefresh("network")). Сверху плашки: приём, отдача,
// открытые порты, соединения; ниже карточки интерфейсов, портов,
// маршрутов и DNS.

const netEvery = 3

var (
	ifaceCols = []float32{-1, 84, 150, 56, netSpeedCol, netSpeedCol}
	portCols  = []float32{64, -1, 72, -1}
)

type ifaceRow struct {
	name, addrs, mac, mtu, rx, tx *header.Text
	state                         *vchip
}

type portRow struct {
	proto      *vchip
	addr, port *header.Text
	proc, pid  *header.Text
}

type lineRow struct{ t *header.Text }

type networkPage struct {
	*base
	obj     fyne.CanvasObject
	last    *host.Network
	loading bool

	tRx, tTx, tPorts, tConn *statTile

	ifaces  *rowSet[*ifaceRow]
	ifCount *header.Text

	ports     *rowSet[*portRow]
	portCount *header.Text
	portHint  *header.Text
	filter    *widget.Entry

	routes *rowSet[*lineRow]
	dns    *rowSet[*lineRow]
}

func newNetworkPage(env *toolEnv) toolPage {
	p := &networkPage{base: newBase(env)}

	p.tRx = newStatTile(theme.DownloadIcon(), theme.ColorNameSuccess, "ПРИЁМ")
	p.tTx = newStatTile(theme.UploadIcon(), theme.ColorNamePrimary, "ОТДАЧА")
	p.tPorts = newStatTile(icLan, theme.ColorNameWarning, "ОТКРЫТЫЕ ПОРТЫ")
	p.tConn = newStatTile(icSwap, theme.ColorNamePrimary, "TCP-СОЕДИНЕНИЯ")

	// Интерфейсы.
	p.ifCount = muted("")
	p.ifaces = newRowSet(func() (*ifaceRow, fyne.CanvasObject) {
		r := &ifaceRow{
			name:  truncated(txt("", theme.TextSize(), theme.ColorNameForeground, true)),
			addrs: truncated(muted("")),
			state: newVChip(),
			mac:   monoCell(),
			mtu:   right(monoCell()),
			rx:    mono("", theme.TextSize()),
			tx:    mono("", theme.TextSize()),
		}
		speed := func(res fyne.Resource, c fyne.ThemeColorName, t *header.Text) fyne.CanvasObject {
			return hstack(sp1, square(12, widget.NewIcon(tinted(res, c))), t)
		}
		return r, rowPlain(cols(ifaceCols, sp3,
			vstack(2, r.name, r.addrs), r.state.obj, r.mac, r.mtu,
			speed(theme.DownloadIcon(), theme.ColorNameSuccess, r.rx),
			speed(theme.UploadIcon(), theme.ColorNamePrimary, r.tx)))
	})
	ifaces := section("Интерфейсы", p.ifCount, nil,
		tableHead(ifaceCols, capsLabel("ИНТЕРФЕЙС"), capsLabel("СОСТОЯНИЕ"), capsLabel("MAC"),
			right(capsLabel("MTU")), capsLabel("ПРИЁМ"), capsLabel("ОТДАЧА")),
		p.ifaces.box)

	// Порты.
	p.portCount = muted("")
	p.portHint = muted("Имена процессов видны только с правами root (sudo без пароля).")
	p.portHint.Hide()
	p.filter = searchEntry("Поиск: порт, адрес, процесс", func(string) { p.renderPorts() })
	p.ports = newRowSet(func() (*portRow, fyne.CanvasObject) {
		r := &portRow{
			proto: newVChip(),
			addr:  monoCell(),
			port:  mono("", theme.TextSize()),
			proc:  txt("", theme.TextSize(), theme.ColorNameForeground, true),
			pid:   muted(""),
		}
		r.port.Bold = true
		return r, rowPlain(cols(portCols, sp3, r.proto.obj, r.addr, r.port, hstack(sp2, r.proc, r.pid)))
	})
	ports := section("Открытые порты", p.portCount, fixedW(280, p.filter),
		tableHead(portCols, capsLabel("ПРОТОКОЛ"), capsLabel("АДРЕС"), capsLabel("ПОРТ"), capsLabel("ПРОЦЕСС")),
		p.ports.box,
		pad(sp4, 0, sp4, 0, p.portHint))

	// Маршруты и DNS.
	line := func() (*lineRow, fyne.CanvasObject) {
		r := &lineRow{t: monoCell()}
		return r, rowPlain(r.t)
	}
	p.routes = newRowSet(line)
	p.dns = newRowSet(line)
	ping := btn("Ping…", theme.MediaPlayIcon(), p.ping)
	bottom := container.NewGridWithColumns(2,
		section("Маршруты", nil, nil, p.routes.box),
		section("DNS", nil, ping, p.dns.box),
	)

	p.obj = vstack(sp4,
		p.bar(),
		tiles(p.tRx, p.tTx, p.tPorts, p.tConn),
		ifaces,
		ports,
		bottom,
	)
	p.render()
	p.reload()
	p.every(netEvery, func() bool { return p.env.st.AutoRefresh("network") && !p.loading }, p.reload)
	return p
}

func (p *networkPage) object() fyne.CanvasObject { return p.obj }

func (p *networkPage) reload() {
	p.loading = true
	prev := p.last
	var n *host.Network
	p.work(func(ctx context.Context) (err error) {
		n, err = p.env.h.GetNetwork(ctx, prev, p.env.h.IsRoot(ctx))
		return err
	}, func(err error) {
		p.loading = false
		p.setErr(err)
		if err != nil {
			return
		}
		p.last = n
		p.render()
	})
}

func (p *networkPage) ping() {
	p.ask("Ping с сервера", "Проверить", []askField{{Label: "Адрес", Value: "1.1.1.1"}}, false,
		func(ctx context.Context, v []string) error {
			out, err := p.env.h.Ping(ctx, v[0])
			if err != nil {
				return err
			}
			fyne.Do(func() { showText("ping:"+p.env.sv.ID, "Ping "+v[0], out) })
			return nil
		}, nil)
}

var (
	reEstab = regexp.MustCompile(`estab (\d+)`)
	reTCP   = regexp.MustCompile(`TCP:\s+(\d+)`)
	reTW    = regexp.MustCompile(`timewait (\d+)`)
)

func (p *networkPage) render() {
	n := p.last
	if n == nil {
		p.ifaces.resize(0, "Читаем сеть…")
		p.ports.resize(0, "Читаем сеть…")
		p.routes.resize(0, "…")
		p.dns.resize(0, "…")
		return
	}

	// Плашки. Мосты и оверлеи контейнеров в сумму не берём — их трафик
	// уже прошёл через физический интерфейс (как в Resources).
	var rx, tx float64
	var rxT, txT uint64
	known := false
	for _, i := range n.Ifaces {
		if virtualIface(i.Name) || i.Name == "lo" {
			continue
		}
		rxT, txT = rxT+i.RxBytes, txT+i.TxBytes
		if i.RxRate >= 0 {
			known = true
			rx, tx = rx+i.RxRate, tx+i.TxRate
		}
	}
	if known {
		p.tRx.set(rate(rx), "всего "+host.Bytes(rxT))
		p.tTx.set(rate(tx), "всего "+host.Bytes(txT))
	} else {
		p.tRx.set("—", "всего "+host.Bytes(rxT))
		p.tTx.set("—", "всего "+host.Bytes(txT))
	}
	var tcp, udp int
	ports := map[string]bool{}
	for _, l := range n.Listeners {
		if strings.HasPrefix(l.Proto, "tcp") {
			tcp++
		} else {
			udp++
		}
		ports[l.Proto+":"+l.Port] = true
	}
	p.tPorts.set(strconv.Itoa(len(ports)), "tcp "+strconv.Itoa(tcp)+"  ·  udp "+strconv.Itoa(udp))
	estab, total, tw := "—", "", ""
	if m := reEstab.FindStringSubmatch(n.Summary); m != nil {
		estab = m[1]
	}
	if m := reTCP.FindStringSubmatch(n.Summary); m != nil {
		total = "всего " + m[1]
	}
	if m := reTW.FindStringSubmatch(n.Summary); m != nil {
		tw = "TIME_WAIT " + m[1]
	}
	p.tConn.set(estab, strings.Trim(total+"  ·  "+tw, " ·"))

	// Интерфейсы.
	setT(p.ifCount, plural(len(n.Ifaces), "интерфейс", "интерфейса", "интерфейсов"))
	rows := p.ifaces.resize(len(n.Ifaces), "Интерфейсов нет")
	for k, i := range n.Ifaces {
		r := rows[k]
		setT(r.name, i.Name)
		addrs := strings.Join(i.Addrs, "  ·  ")
		if addrs == "" {
			addrs = "нет адресов"
		}
		setT(r.addrs, addrs)
		st := strings.ToUpper(i.State)
		switch st {
		case "UP":
			r.state.set("UP", theme.ColorNameSuccess)
		case "DOWN", "LOWERLAYERDOWN":
			r.state.set("DOWN", theme.ColorNameError)
		case "", "UNKNOWN":
			r.state.set("—", theme.ColorNamePlaceHolder)
		default:
			r.state.set(st, theme.ColorNameWarning)
		}
		setT(r.mac, orDash(i.MAC))
		mtu := "—"
		if i.MTU > 0 {
			mtu = strconv.Itoa(i.MTU)
		}
		setT(r.mtu, mtu)
		setT(r.rx, host.Rate(i.RxRate))
		setT(r.tx, host.Rate(i.TxRate))
	}

	p.renderPorts()

	// Маршруты и DNS.
	routes := p.routes.resize(len(n.Routes), "Маршрутов нет")
	for k, s := range n.Routes {
		setT(routes[k].t, s)
	}
	dns := p.dns.resize(len(n.DNS), "Не указаны в /etc/resolv.conf")
	for k, s := range n.DNS {
		setT(dns[k].t, s)
	}
}

func (p *networkPage) renderPorts() {
	n := p.last
	if n == nil {
		return
	}
	list := append([]host.Listener(nil), n.Listeners...)
	sort.SliceStable(list, func(i, j int) bool {
		a, _ := strconv.Atoi(list[i].Port)
		b, _ := strconv.Atoi(list[j].Port)
		if a != b {
			return a < b
		}
		return list[i].Proto < list[j].Proto
	})
	q := strings.ToLower(strings.TrimSpace(p.filter.Text))
	var vis []host.Listener
	named := false
	for _, l := range list {
		if l.Process != "" {
			named = true
		}
		if q != "" && !strings.Contains(strings.ToLower(l.Proto+" "+l.Local+" "+l.Process), q) {
			continue
		}
		vis = append(vis, l)
	}
	if q != "" {
		setT(p.portCount, "найдено "+strconv.Itoa(len(vis))+" из "+strconv.Itoa(len(list)))
	} else {
		setT(p.portCount, plural(len(list), "порт", "порта", "портов"))
	}
	if !named && len(list) > 0 {
		p.portHint.Show()
	} else {
		p.portHint.Hide()
	}
	empty := "Ничего не слушает"
	if q != "" {
		empty = "Ничего не найдено по «" + strings.TrimSpace(p.filter.Text) + "»"
	}
	rows := p.ports.resize(len(vis), empty)
	for k, l := range vis {
		r := rows[k]
		proto := strings.ToLower(l.Proto)
		c := theme.ColorNamePrimary
		if strings.HasPrefix(proto, "udp") {
			c = theme.ColorNameWarning
		}
		r.proto.set(proto, c)
		addr := strings.TrimSuffix(l.Local, ":"+l.Port)
		switch addr {
		case "0.0.0.0", "*", "[::]", "::":
			addr += "  (все)"
		}
		setT(r.addr, addr)
		setT(r.port, l.Port)
		name, pid, _ := strings.Cut(l.Process, "/")
		setT(r.proc, orDash(name))
		if pid != "" {
			pid = "pid " + pid
		}
		setT(r.pid, pid)
	}
}
