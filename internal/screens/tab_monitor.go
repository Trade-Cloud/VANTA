package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/host"
)

// ---------- Resources ----------

// Без заголовка, строки «куда подключены», кнопки «Обновить» и галочки:
// автообновление включается в Settings › Настройки приложения
// (core.Store.ResourcesAuto). Все виджеты создаются один раз и при
// обновлении только меняют значения — иначе данные мигают.
type resourcesPage struct {
	*base
	obj  fyne.CanvasObject
	last *host.Resources

	cpu, mem, swap   *widget.ProgressBar
	cpuL, memL, swpL *widget.Label
	cpuInfo, memInfo *widget.Label
	cores            *barRows
	disks            *barRows
	io               *barRows
	net              *netView
	info, extra      *widget.Label
	temps            *barRows
	tempCard         *widget.Card
	loading          bool
}

func newResourcesPage(env *toolEnv) toolPage {
	p := &resourcesPage{base: newBase(env)}
	p.cpu, p.mem, p.swap = newBar(), newBar(), newBar()
	p.cpuL, p.memL, p.swpL = widget.NewLabel("CPU"), widget.NewLabel("Память"), widget.NewLabel("Swap")
	p.cpuInfo, p.memInfo = wrapLabel(), wrapLabel()
	p.cores = newBarRows(4)
	p.disks = newBarRows(1)
	p.io = newBarRows(1)
	p.net = newNetView()
	p.temps = newBarRows(2)
	p.info = widget.NewLabel("")
	p.info.TextStyle = fyne.TextStyle{Monospace: true}
	p.extra = widget.NewLabel("")
	p.extra.TextStyle = fyne.TextStyle{Monospace: true}

	cpuCard := widget.NewCard("Процессор", "", container.NewVBox(p.cpuL, p.cpu, p.cpuInfo, p.cores.box))
	memCard := widget.NewCard("Память", "", container.NewVBox(p.memL, p.mem, p.swpL, p.swap, p.memInfo))
	p.tempCard = widget.NewCard("Температура", "", p.temps.box)
	p.tempCard.Hide()
	p.obj = container.NewVBox(
		p.bar(),
		container.NewGridWithColumns(2, cpuCard, memCard),
		widget.NewCard("Диски", "", p.disks.box),
		widget.NewCard("Диски: чтение / запись", "", p.io.box),
		widget.NewCard("Сеть", "", p.net.obj),
		container.NewGridWithColumns(2,
			widget.NewCard("Система", "", p.info),
			widget.NewCard("Ресурсы ядра", "", p.extra)),
		p.tempCard,
	)
	p.reload()
	p.every(2, func() bool { return p.env.st.ResourcesAuto() && !p.loading }, p.reload)
	return p
}

func (p *resourcesPage) object() fyne.CanvasObject { return p.obj }

func (p *resourcesPage) reload() {
	p.loading = true
	var r *host.Resources
	prev := p.last
	p.work(func(ctx context.Context) (err error) {
		r, err = p.env.h.GetResources(ctx, prev)
		return err
	}, func(err error) {
		p.loading = false
		p.setErr(err)
		if err != nil {
			return
		}
		p.last = r
		p.render(r)
	})
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(used) / float64(total)
}

func rate(bps float64) string { return host.Bytes(uint64(bps)) + "/s" }

func (p *resourcesPage) render(r *host.Resources) {
	// Процессор.
	head := fmt.Sprintf("%d ядер", r.CPUs)
	if r.CPUModel != "" {
		head += "  ·  " + r.CPUModel
	}
	if r.CPUPercent >= 0 {
		p.cpu.SetValue(r.CPUPercent)
		head = fmt.Sprintf("Загрузка %.1f%%  ·  ", r.CPUPercent) + head
		p.cpuInfo.SetText(fmt.Sprintf("user %.1f%%  ·  system %.1f%%  ·  iowait %.1f%%  ·  steal %.1f%%",
			r.CPUUser, r.CPUSystem, r.CPUIOWait, r.CPUSteal))
	}
	p.cpuL.SetText(head)
	if len(r.Cores) > 1 {
		keys := make([]string, len(r.Cores))
		for i := range r.Cores {
			keys[i] = strconv.Itoa(i)
		}
		p.cores.set(keys, func(i int, row *barRow) {
			row.set(fmt.Sprintf("cpu%d  %.0f%%", i, r.Cores[i]), r.Cores[i])
		})
	}

	// Память.
	memPct := pct(r.MemUsed, r.MemTotal)
	p.mem.SetValue(memPct)
	p.memL.SetText(fmt.Sprintf("ОЗУ: %s из %s (%.0f%%)", host.Bytes(r.MemUsed), host.Bytes(r.MemTotal), memPct))
	if r.SwapTotal > 0 {
		sp := pct(r.SwapUsed, r.SwapTotal)
		p.swap.SetValue(sp)
		p.swpL.SetText(fmt.Sprintf("Swap: %s из %s (%.0f%%)", host.Bytes(r.SwapUsed), host.Bytes(r.SwapTotal), sp))
	} else {
		p.swap.SetValue(0)
		p.swpL.SetText("Swap: нет")
	}
	p.memInfo.SetText(fmt.Sprintf("доступно %s  ·  кэш %s  ·  буферы %s  ·  shared %s  ·  slab %s  ·  dirty %s",
		host.Bytes(r.MemAvailable), host.Bytes(r.Cached), host.Bytes(r.Buffers),
		host.Bytes(r.Shmem), host.Bytes(r.Slab), host.Bytes(r.Dirty)))

	// Диски.
	keys := make([]string, len(r.Disks))
	for i, d := range r.Disks {
		keys[i] = d.Mount
	}
	p.disks.set(keys, func(i int, row *barRow) {
		d := r.Disks[i]
		s := fmt.Sprintf("%s  ·  %s  ·  %s    %s из %s (%.0f%%)  ·  свободно %s",
			d.Mount, d.Device, d.FS, host.Bytes(d.Used), host.Bytes(d.Size), pct(d.Used, d.Size), host.Bytes(d.Avail))
		if d.Inodes > 0 {
			s += fmt.Sprintf("  ·  inodes %.0f%%", pct(d.InodesUsed, d.Inodes))
		}
		row.set(s, pct(d.Used, d.Size))
	})
	if len(r.Disks) == 0 {
		p.disks.empty("Нет данных (df недоступен)")
	}

	// Скорости.
	keys = make([]string, len(r.DiskIO))
	for i, d := range r.DiskIO {
		keys[i] = d.Name
	}
	p.io.setText(keys, func(i int) string {
		d := r.DiskIO[i]
		return fmt.Sprintf("%s:  чтение %s  ·  запись %s\n      всего %s / %s",
			d.Name, rate(d.ReadPS), rate(d.WritePS), host.Bytes(d.ReadTotal), host.Bytes(d.WriteTotal))
	})
	if len(r.DiskIO) == 0 {
		p.io.empty("Нет данных")
	}
	p.net.set(r.Net)

	// Система.
	p.info.SetText(fmt.Sprintf("Хост:       %s\nОС:         %s\nЯдро:       %s\nАптайм:     %s\nLoad avg:   %.2f  %.2f  %.2f\nСеансов:    %d",
		r.Hostname, r.OS, r.Kernel, host.Duration(r.Uptime), r.Load[0], r.Load[1], r.Load[2], r.Users))
	files := strconv.FormatUint(r.FilesOpen, 10)
	if r.FilesMax > 0 && r.FilesMax < 1<<62 {
		files += fmt.Sprintf(" из %d", r.FilesMax)
	}
	p.extra.SetText(fmt.Sprintf("Процессов:  %d\nПотоков:    %d (готовы %d)\nTCP:        %d (TIME_WAIT %d)\nUDP:        %d\nОткр. файлов: %s",
		r.Procs, r.Threads, r.Running, r.TCP, r.TCPTimeWait, r.UDP, files))

	// Температура — только если датчики есть (на VPS обычно нет).
	if len(r.Temps) == 0 {
		p.tempCard.Hide()
		return
	}
	keys = make([]string, len(r.Temps))
	for i, t := range r.Temps {
		keys[i] = strconv.Itoa(i) + t.Name
	}
	p.temps.set(keys, func(i int, row *barRow) {
		t := r.Temps[i]
		row.set(fmt.Sprintf("%s  %.1f °C", t.Name, t.C), t.C)
	})
	p.tempCard.Show()
}

func newBar() *widget.ProgressBar {
	b := widget.NewProgressBar()
	b.Max = 100
	b.TextFormatter = func() string { return "" }
	return b
}

func wrapLabel() *widget.Label {
	l := widget.NewLabel("")
	l.Wrapping = fyne.TextWrapWord
	return l
}

// barRows — список строк «подпись + полоса» с ключами. Пока набор ключей
// тот же, строки не пересоздаются, а только обновляются.
type barRows struct {
	box  *fyne.Container
	keys []string
	rows []*barRow
}

type barRow struct {
	l *widget.Label
	b *widget.ProgressBar // nil — строка только с текстом
}

func (r *barRow) set(text string, v float64) {
	r.l.SetText(text)
	if r.b != nil {
		r.b.SetValue(v)
	}
}

func newBarRows(cols int) *barRows {
	return &barRows{box: container.NewGridWithColumns(cols)}
}

func (br *barRows) same(keys []string) bool {
	if len(keys) != len(br.keys) {
		return false
	}
	for i := range keys {
		if keys[i] != br.keys[i] {
			return false
		}
	}
	return true
}

func (br *barRows) rebuild(keys []string, bars bool) {
	br.keys = append([]string(nil), keys...)
	br.rows = make([]*barRow, len(keys))
	objs := make([]fyne.CanvasObject, len(keys))
	for i := range keys {
		row := &barRow{l: widget.NewLabel("")}
		row.l.Truncation = fyne.TextTruncateEllipsis
		if bars {
			row.b = newBar()
			objs[i] = container.NewVBox(row.l, row.b)
		} else {
			objs[i] = row.l
		}
		br.rows[i] = row
	}
	br.box.Objects = objs
	br.box.Refresh()
}

// set — строки с полосой.
func (br *barRows) set(keys []string, fill func(i int, row *barRow)) {
	if !br.same(keys) || (len(br.rows) > 0 && br.rows[0].b == nil) {
		br.rebuild(keys, true)
	}
	for i, row := range br.rows {
		fill(i, row)
	}
}

// setText — строки только с текстом.
func (br *barRows) setText(keys []string, text func(i int) string) {
	if !br.same(keys) || (len(br.rows) > 0 && br.rows[0].b != nil) {
		br.rebuild(keys, false)
	}
	for i, row := range br.rows {
		row.l.SetText(text(i))
	}
}

// empty — одна строка-заглушка вместо списка.
func (br *barRows) empty(text string) {
	br.setText([]string{"\x00empty"}, func(int) string { return text })
}

// ---------- блок «Сеть» ----------

// netView — сверху суммарная скорость приёма/отдачи крупно, под ней
// таблица интерфейсов: имя и «всего» слева, скорости в своих колонках.
// Строки пересоздаются только при смене набора интерфейсов.
type netView struct {
	obj      fyne.CanvasObject
	rx, tx   *header.Text
	rxT, txT *header.Text
	list     *fyne.Container
	empty    *header.Text
	keys     []string
	rows     []netRow
}

type netRow struct {
	name, total, rx, tx *header.Text
}

const netSpeedCol float32 = 104

func newNetView() *netView {
	v := &netView{}
	big := theme.TextSize() + 6
	v.rx = txt("—", big, theme.ColorNameForeground, true)
	v.tx = txt("—", big, theme.ColorNameForeground, true)
	v.rx.Mono, v.tx.Mono = true, true
	v.rxT, v.txT = muted(""), muted("")
	tile := func(res fyne.Resource, c fyne.ThemeColorName, label string, val, sub *header.Text) fyne.CanvasObject {
		return card(padXY(sp3, sp3, hstack(sp3, iconTile(res, c, 36),
			vstack(2, capsLabel(label), val, sub))))
	}
	summary := container.NewGridWithColumns(2,
		tile(theme.DownloadIcon(), theme.ColorNameSuccess, "ПРИЁМ", v.rx, v.rxT),
		tile(theme.UploadIcon(), theme.ColorNamePrimary, "ОТДАЧА", v.tx, v.txT),
	)
	head := cols([]float32{-1, netSpeedCol, netSpeedCol}, sp2,
		capsLabel("ИНТЕРФЕЙС"), capsLabel("ПРИЁМ"), capsLabel("ОТДАЧА"))
	v.list = vstack(0)
	v.empty = muted("Нет данных")
	v.empty.Hide()
	v.obj = vstack(sp3, summary, vstack(sp2, pad(sp1, 0, sp1, 0, head), hline(), v.list, v.empty))
	return v
}

// virtualIface — мосты и оверлеи контейнеров: их трафик уже прошёл через
// физический интерфейс, в сумму не берём.
func virtualIface(name string) bool {
	for _, p := range []string{"docker", "br-", "virbr", "cni", "flannel", "cali", "vxlan", "kube"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func (v *netView) set(list []host.NetRate) {
	var rx, tx float64
	var rxT, txT uint64
	for _, n := range list {
		if virtualIface(n.Name) {
			continue
		}
		rx, tx = rx+n.RxPS, tx+n.TxPS
		rxT, txT = rxT+n.RxTotal, txT+n.TxTotal
	}
	v.rx.Text, v.tx.Text = rate(rx), rate(tx)
	v.rxT.Text, v.txT.Text = "всего "+host.Bytes(rxT), "всего "+host.Bytes(txT)
	for _, t := range []*header.Text{v.rx, v.tx, v.rxT, v.txT} {
		t.Refresh()
	}

	keys := make([]string, len(list))
	for i, n := range list {
		keys[i] = n.Name
	}
	if !sameKeys(keys, v.keys) {
		v.keys = keys
		v.rows = make([]netRow, len(list))
		objs := make([]fyne.CanvasObject, 0, 2*len(list))
		for i := range list {
			r := netRow{
				name:  truncated(txt("", theme.TextSize(), theme.ColorNameForeground, true)),
				total: truncated(muted("")),
				rx:    mono("", theme.TextSize()),
				tx:    mono("", theme.TextSize()),
			}
			v.rows[i] = r
			speed := func(res fyne.Resource, c fyne.ThemeColorName, t *header.Text) fyne.CanvasObject {
				return hstack(sp1, square(12, widget.NewIcon(tinted(res, c))), t)
			}
			if i > 0 {
				objs = append(objs, hline())
			}
			objs = append(objs, pad(sp1, sp2, sp1, sp2, cols([]float32{-1, netSpeedCol, netSpeedCol}, sp2,
				vstack(2, r.name, r.total),
				speed(theme.DownloadIcon(), theme.ColorNameSuccess, r.rx),
				speed(theme.UploadIcon(), theme.ColorNamePrimary, r.tx))))
		}
		v.list.Objects = objs
		v.list.Refresh()
	}
	for i, n := range list {
		r := v.rows[i]
		r.name.Text = n.Name
		r.total.Text = "всего ↓ " + host.Bytes(n.RxTotal) + "  ·  ↑ " + host.Bytes(n.TxTotal)
		r.rx.Text, r.tx.Text = rate(n.RxPS), rate(n.TxPS)
		for _, t := range []*header.Text{r.name, r.total, r.rx, r.tx} {
			t.Refresh()
		}
	}
	if len(list) == 0 {
		v.empty.Show()
	} else {
		v.empty.Hide()
	}
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
