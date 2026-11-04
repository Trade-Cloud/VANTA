package screens

import (
	"context"
	"fmt"
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

// ---------- Processes ----------

// В стиле Resources: без заголовка, строки «куда подключены», кнопки
// «Обновить» и галочки. Автообновление — Settings › Настройки приложения
// (core.Store.AutoRefresh("processes")), фоновое обновление тихое.
// Сверху плашки-сводки, ниже карточка-таблица: клик по заголовку колонки
// сортирует, клик по строке выбирает процесс и показывает панель действий.

const (
	procEvery = 3   // секунд между фоновыми обновлениями
	procLimit = 200 // строк в таблице; остальное — через поиск
)

var procCols = []float32{-1, 64, 110, 64, 64, 84, 96}

type procRow struct {
	press                               *header.Pressable
	name, cmd, pid, user, cpu, mem, rss *header.Text
	time                                *header.Text
	key                                 int
}

type processesPage struct {
	*base
	obj     fyne.CanvasObject
	list    []host.Process
	loaded  bool
	loading bool

	sortKey string
	desc    bool
	heads   map[string]*header.Text
	labels  map[string]string

	filter *widget.Entry
	count  *header.Text
	more   *header.Text
	rows   *rowSet[*procRow]

	sel             int // PID выбранного процесса, 0 — нет
	selBox          fyne.CanvasObject
	selName, selSub *header.Text

	tTotal, tCPU, tMem, tUsers *statTile
}

func newProcessesPage(env *toolEnv) toolPage {
	p := &processesPage{base: newBase(env), sortKey: "cpu", desc: true}

	p.tTotal = newStatTile(theme.ListIcon(), theme.ColorNamePrimary, "ПРОЦЕССЫ")
	p.tCPU = newStatTile(icChip, theme.ColorNameWarning, "CPU, СУММАРНО")
	p.tMem = newStatTile(theme.StorageIcon(), theme.ColorNameSuccess, "ПАМЯТЬ (RSS)")
	p.tUsers = newStatTile(theme.AccountIcon(), theme.ColorNamePrimary, "ПОЛЬЗОВАТЕЛИ")

	p.filter = searchEntry("Поиск", func(string) { p.render() })
	p.count = muted("")
	p.more = muted("")
	p.more.Hide()

	// Заголовки колонок — кликабельные, со стрелкой сортировки.
	p.heads = map[string]*header.Text{}
	p.labels = map[string]string{}
	head := func(key, label string, num bool) fyne.CanvasObject {
		t := capsLabel(label)
		if num {
			right(t)
		}
		p.heads[key], p.labels[key] = t, label
		return newTapArea(t, func() { p.sortBy(key) })
	}
	cols := tableHead(procCols,
		head("name", "ПРОЦЕСС", false),
		head("pid", "PID", true),
		head("user", "ПОЛЬЗОВАТЕЛЬ", false),
		head("cpu", "CPU %", true),
		head("mem", "MEM %", true),
		head("rss", "RSS", true),
		head("time", "ВРЕМЯ", true),
	)
	p.markSort()

	p.rows = newRowSet(func() (*procRow, fyne.CanvasObject) {
		r := &procRow{
			name: truncated(txt("", theme.TextSize(), theme.ColorNameForeground, true)),
			cmd:  truncated(muted("")),
			pid:  right(monoCell()),
			user: truncated(txt("", theme.TextSize(), theme.ColorNameForeground, false)),
			cpu:  right(monoCell()),
			mem:  right(monoCell()),
			rss:  right(monoCell()),
			time: right(monoCell()),
		}
		r.press = rowPress(procCells(r), func() { p.pick(r.key) })
		return r, r.press
	})

	p.selBox = p.selectionBar()
	p.selBox.Hide()

	table := section("Процессы", p.count, fixedW(300, p.filter),
		p.selBox, cols, p.rows.box, p.more)

	p.obj = vstack(sp4,
		p.bar(),
		tiles(p.tTotal, p.tCPU, p.tMem, p.tUsers),
		table,
	)
	p.render()
	p.reload()
	p.every(procEvery, func() bool { return p.env.st.AutoRefresh("processes") && !p.loading }, p.reload)
	return p
}

func procCells(r *procRow) fyne.CanvasObject {
	return cols(procCols, sp3, vstack(2, r.name, r.cmd), r.pid, r.user, r.cpu, r.mem, r.rss, r.time)
}

func (p *processesPage) object() fyne.CanvasObject { return p.obj }

func (p *processesPage) reload() {
	p.loading = true
	var list []host.Process
	p.work(func(ctx context.Context) (err error) {
		list, err = p.env.h.Processes(ctx)
		return err
	}, func(err error) {
		p.loading = false
		p.setErr(err)
		if err == nil {
			p.list, p.loaded = list, true
			p.render()
		}
	})
}

// ---------- сортировка ----------

func (p *processesPage) sortBy(key string) {
	if p.sortKey == key {
		p.desc = !p.desc
	} else {
		// Числа — по убыванию, текст — по алфавиту.
		p.sortKey, p.desc = key, key != "name" && key != "user" && key != "pid"
	}
	p.markSort()
	p.render()
}

func (p *processesPage) markSort() {
	for k, t := range p.heads {
		label := p.labels[k]
		c := theme.ColorNamePlaceHolder
		if k == p.sortKey {
			c = theme.ColorNameForeground
			if p.desc {
				label += " ↓"
			} else {
				label += " ↑"
			}
		}
		setTC(t, label, c)
	}
}

func (p *processesPage) sorted() []host.Process {
	list := append([]host.Process(nil), p.list...)
	less := func(a, b host.Process) bool { return false }
	switch p.sortKey {
	case "name":
		less = func(a, b host.Process) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) }
	case "pid":
		less = func(a, b host.Process) bool { return a.PID < b.PID }
	case "user":
		less = func(a, b host.Process) bool { return a.User < b.User }
	case "cpu":
		less = func(a, b host.Process) bool { return a.CPU < b.CPU }
	case "mem":
		less = func(a, b host.Process) bool { return a.Mem < b.Mem }
	case "rss":
		less = func(a, b host.Process) bool { return a.RSS < b.RSS }
	case "time":
		less = func(a, b host.Process) bool { return etimeSec(a.Elapsed) < etimeSec(b.Elapsed) }
	}
	sort.SliceStable(list, func(i, j int) bool {
		if p.desc {
			return less(list[j], list[i])
		}
		return less(list[i], list[j])
	})
	return list
}

// etimeSec — «[[dd-]hh:]mm:ss» из ps в секунды.
func etimeSec(s string) int {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		days, _ = strconv.Atoi(d)
		s = rest
	}
	sec := 0
	for _, part := range strings.Split(s, ":") {
		n, _ := strconv.Atoi(part)
		sec = sec*60 + n
	}
	return days*86400 + sec
}

// ---------- отрисовка ----------

func (p *processesPage) render() {
	p.renderTiles()

	q := strings.ToLower(strings.TrimSpace(p.filter.Text))
	var vis []host.Process
	for _, pr := range p.sorted() {
		if q != "" && !strings.Contains(strings.ToLower(
			strconv.Itoa(pr.PID)+" "+pr.User+" "+pr.Name+" "+pr.Command), q) {
			continue
		}
		vis = append(vis, pr)
	}
	total := len(vis)
	if len(vis) > procLimit {
		vis = vis[:procLimit]
	}

	empty := "Нет процессов"
	switch {
	case !p.loaded:
		empty = "Читаем список процессов…"
	case q != "":
		empty = "Ничего не найдено по «" + strings.TrimSpace(p.filter.Text) + "»"
	}
	rows := p.rows.resize(len(vis), empty)
	for i, pr := range vis {
		r := rows[i]
		r.key = pr.PID
		setT(r.name, pr.Name)
		setT(r.cmd, pr.Command)
		setT(r.pid, strconv.Itoa(pr.PID))
		setT(r.user, pr.User)
		setTC(r.cpu, fmt.Sprintf("%.1f", pr.CPU), loadColor(pr.CPU))
		setT(r.mem, fmt.Sprintf("%.1f", pr.Mem))
		setT(r.rss, host.Bytes(pr.RSS))
		setT(r.time, pr.Elapsed)
		r.press.SetSelected(pr.PID == p.sel)
	}

	switch {
	case !p.loaded:
		setT(p.count, "")
	case q != "":
		setT(p.count, fmt.Sprintf("найдено %d из %d", total, len(p.list)))
	default:
		setT(p.count, plural(len(p.list), "процесс", "процесса", "процессов"))
	}
	if total > len(vis) {
		setT(p.more, fmt.Sprintf("Показаны первые %d из %d — уточните поиск, чтобы увидеть остальные.", len(vis), total))
		p.more.Show()
	} else {
		p.more.Hide()
	}
	p.renderSelection()
}

func (p *processesPage) renderTiles() {
	if !p.loaded {
		return
	}
	var running, sleeping, zombie int
	var cpu float64
	var rss uint64
	users := map[string]bool{}
	var topCPU, topMem host.Process
	for _, pr := range p.list {
		switch {
		case strings.HasPrefix(pr.State, "R"):
			running++
		case strings.HasPrefix(pr.State, "Z"):
			zombie++
		case strings.HasPrefix(pr.State, "S"), strings.HasPrefix(pr.State, "I"), strings.HasPrefix(pr.State, "D"):
			sleeping++
		}
		cpu += pr.CPU
		rss += pr.RSS
		users[pr.User] = true
		if pr.CPU > topCPU.CPU {
			topCPU = pr
		}
		if pr.RSS > topMem.RSS {
			topMem = pr
		}
	}
	p.tTotal.set(strconv.Itoa(len(p.list)), fmt.Sprintf("выполняются %d  ·  ждут %d", running, sleeping))
	sub := "все простаивают"
	if topCPU.PID != 0 {
		sub = fmt.Sprintf("больше всех: %s %.1f%%", topCPU.Name, topCPU.CPU)
	}
	p.tCPU.set(fmt.Sprintf("%.1f%%", cpu), sub)
	sub = ""
	if topMem.PID != 0 {
		sub = "больше всех: " + topMem.Name + " " + host.Bytes(topMem.RSS)
	}
	p.tMem.set(host.Bytes(rss), sub)
	z := "зомби нет"
	if zombie > 0 {
		z = "зомби: " + strconv.Itoa(zombie)
	}
	p.tUsers.set(strconv.Itoa(len(users)), z)
	c := theme.ColorNamePlaceHolder
	if zombie > 0 {
		c = theme.ColorNameWarning
	}
	setTC(p.tUsers.sub, z, c)
}

// ---------- выбор и действия ----------

func (p *processesPage) pick(pid int) {
	if p.sel == pid {
		pid = 0 // повторный клик снимает выбор
	}
	p.sel = pid
	p.render()
}

func (p *processesPage) selectionBar() fyne.CanvasObject {
	p.selName = truncated(txt("", theme.TextSize(), theme.ColorNameForeground, true))
	p.selSub = truncated(muted(""))
	kill := btn("Убить", theme.DeleteIcon(), func() { p.signal("KILL") })
	kill.Importance = widget.DangerImportance
	tools := hstack(sp2,
		btn("Подробно", theme.InfoIcon(), p.details),
		btn("Приоритет…", theme.MoveUpIcon(), p.askNice),
		btn("Сигнал…", theme.MailSendIcon(), p.askSignal),
		btn("Завершить", theme.CancelIcon(), func() { p.signal("TERM") }),
		kill,
		iconButton(theme.WindowCloseIcon(), func() { p.pick(p.sel) }),
	)
	left := hstack(sp3, iconTile(icChip, theme.ColorNamePrimary, 32), vstack(2, p.selName, p.selSub))
	left.Layout = &fillLast{gap: sp3}
	bg := header.NewRect(theme.ColorNamePrimary, "", 0, 0)
	bg.Alpha = 18
	return vstack(0, container.NewStack(bg, pad(sp4, sp2, sp3, sp2,
		container.NewBorder(nil, nil, nil, tools, left))), hline())
}

func (p *processesPage) renderSelection() {
	pr, ok := p.current()
	if !ok {
		if p.sel != 0 && p.loaded {
			p.sel = 0 // процесс завершился
		}
		p.selBox.Hide()
		return
	}
	setT(p.selName, fmt.Sprintf("%s  ·  PID %d", pr.Name, pr.PID))
	setT(p.selSub, fmt.Sprintf("%s  ·  родитель %d  ·  %s", pr.User, pr.PPID, pr.Command))
	p.selBox.Show()
}

func (p *processesPage) current() (host.Process, bool) {
	if p.sel == 0 {
		return host.Process{}, false
	}
	for _, pr := range p.list {
		if pr.PID == p.sel {
			return pr, true
		}
	}
	return host.Process{}, false
}

func (p *processesPage) signal(sig string) {
	pr, ok := p.current()
	if !ok {
		return
	}
	root := pr.User != p.env.h.User()
	p.act(true, "Сигнал "+sig+" процессу "+strconv.Itoa(pr.PID),
		fmt.Sprintf("Отправить SIG%s процессу %d (%s, пользователь %s)?\n\n%s", sig, pr.PID, pr.Name, pr.User, pr.Command),
		"Отправить", func(ctx context.Context) error { return p.env.h.Kill(ctx, pr.PID, sig, root) }, p.reload)
}

func (p *processesPage) askSignal() {
	pr, ok := p.current()
	if !ok {
		return
	}
	p.ask("Сигнал процессу "+strconv.Itoa(pr.PID), "Отправить", []askField{{Label: "Сигнал", Options: host.Signals}}, true,
		func(ctx context.Context, v []string) error {
			return p.env.h.Kill(ctx, pr.PID, v[0], pr.User != p.env.h.User())
		}, p.reload)
}

func (p *processesPage) askNice() {
	pr, ok := p.current()
	if !ok {
		return
	}
	p.ask("Приоритет процесса "+strconv.Itoa(pr.PID), "Изменить", []askField{{Label: "nice (-20 — выше, 19 — ниже)", Value: "0"}}, false,
		func(ctx context.Context, v []string) error {
			n, err := strconv.Atoi(v[0])
			if err != nil || n < -20 || n > 19 {
				return fmt.Errorf("nice — число от -20 до 19")
			}
			return p.env.h.Renice(ctx, pr.PID, n)
		}, p.reload)
}

func (p *processesPage) details() {
	pr, ok := p.current()
	if !ok {
		return
	}
	var out string
	p.load(func(ctx context.Context) (err error) {
		out, err = p.env.h.Sudo(ctx, fmt.Sprintf("cat /proc/%d/status 2>&1; echo; echo '--- cwd:'; readlink /proc/%d/cwd; echo '--- exe:'; readlink /proc/%d/exe; "+
			"echo '--- открытых файлов:'; ls /proc/%d/fd 2>/dev/null | wc -l", pr.PID, pr.PID, pr.PID, pr.PID))
		return err
	}, func() {
		showText(fmt.Sprintf("proc:%s:%d", p.env.sv.ID, pr.PID), fmt.Sprintf("Процесс %d · %s", pr.PID, pr.Name), pr.Command+"\n\n"+out)
	})
}
