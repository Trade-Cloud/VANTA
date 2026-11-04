package screens

import (
	"context"
	"errors"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/core"
	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/host"
)

// Вкладки, которые работают с подключённым сервером. Каждая — своя
// страница поверх internal/host; визуал пока минимальный (стандартные
// виджеты Fyne), главное — функционал.

// toolPage — страница вкладки сервера.
type toolPage interface {
	object() fyne.CanvasObject
	dispose()
}

// toolEnv — то, с чем работает страница: подключённый сервер и окно.
type toolEnv struct {
	st  *core.Store
	w   fyne.Window
	h   *host.Host
	sv  agent.Server
	ctx context.Context // отменяется, когда страницу закрыли
}

var toolPages = map[core.Route]func(*toolEnv) toolPage{
	"monitor/resources":   newResourcesPage,
	"monitor/processes":   newProcessesPage,
	"monitor/network":     newNetworkPage,
	"monitor/storage":     newStoragePage,
	"system/services":     newServicesPage,
	"system/users":        newUsersPage,
	"system/journal":      newJournalPage,
	"system/packages":     newPackagesPage,
	"developer/terminal":  newTerminalPage,
	"developer/files":     newFilesPage,
	"developer/docker":    newDockerPage,
	"developer/databases": newDatabasesPage,
	"security/firewall":   newFirewallPage,
}

// ---------- общий каркас ----------

// base — общее для всех страниц: заголовок, строка состояния, фоновая работа.
type base struct {
	env     *toolEnv
	cancel  context.CancelFunc
	status  *widget.Label
	busy    *loadLine // бегущая линия загрузки (loader.go), высота постоянная
	closed  bool
	running int // сколько фоновых задач идёт (для полосы прогресса)
	timers  []func()
	// silent — сейчас идёт автообновление (every): work не показывает полосу
	// прогресса, иначе она каждые пару секунд сдвигает содержимое вниз и
	// обратно — данные «мигают».
	silent bool
}

func newBase(env *toolEnv) *base {
	ctx, cancel := context.WithCancel(env.ctx)
	env.ctx = ctx
	b := &base{env: env, cancel: cancel}
	b.status = widget.NewLabel("")
	b.status.Wrapping = fyne.TextWrapWord
	b.status.Hide()
	b.busy = newLoadLine()
	return b
}

func (b *base) dispose() {
	b.closed = true
	b.cancel()
	b.busy.Stop()
	for _, stop := range b.timers {
		stop()
	}
}

// head — заголовок вкладки с инструментами справа, строка ошибки и прогресс.
func (b *base) head(title string, tools ...fyne.CanvasObject) fyne.CanvasObject {
	h := widget.NewLabel(title)
	h.SizeName = theme.SizeNameHeadingText
	h.TextStyle = fyne.TextStyle{Bold: true}
	sub := widget.NewLabel(b.env.sv.Name + " · " + b.env.sv.Login())
	sub.SizeName = theme.SizeNameCaptionText
	right := container.NewHBox(tools...)
	return container.NewVBox(
		container.NewBorder(nil, nil, container.NewVBox(h, sub), right),
		b.busy,
		b.status,
	)
}

// bar — только полоса прогресса и строка ошибки, без заголовка (для
// страниц, где заголовок и «куда подключены» не нужны).
func (b *base) bar() fyne.CanvasObject {
	return container.NewVBox(b.busy, b.status)
}

// work выполняет fn в фоне; done — в UI-горутине (если страница ещё открыта).
func (b *base) work(fn func(ctx context.Context) error, done func(err error)) {
	shown := !b.silent
	if shown {
		b.running++
		b.busy.Start()
	}
	go func() {
		err := fn(b.env.ctx)
		fyne.Do(func() {
			if b.closed {
				return
			}
			if shown {
				if b.running--; b.running <= 0 {
					b.running = 0
					b.busy.Stop()
				}
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

// load — work, у которого ошибка сразу идёт в строку состояния.
func (b *base) load(fn func(ctx context.Context) error, ok func()) {
	b.work(fn, func(err error) {
		b.setErr(err)
		if err == nil && ok != nil {
			ok()
		}
	})
}

func (b *base) setErr(err error) {
	if err == nil {
		b.status.Hide()
		return
	}
	b.status.Importance = widget.DangerImportance
	b.status.SetText(err.Error())
	b.status.Show()
}

func (b *base) setInfo(s string) {
	if s == "" {
		b.status.Hide()
		return
	}
	b.status.Importance = widget.MediumImportance
	b.status.SetText(s)
	b.status.Show()
}

// every запускает fn раз в d, пока открыта страница и включено on().
func (b *base) every(d int, on func() bool, fn func()) {
	stop := make(chan struct{})
	b.timers = append(b.timers, func() { close(stop) })
	go func() {
		t := newTicker(d)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fyne.Do(func() {
					if !b.closed && (on == nil || on()) {
						b.silent = true
						fn()
						b.silent = false
					}
				})
			}
		}
	}()
}

// act — действие над сервером. danger — сначала окно подтверждения.
// После успеха — уведомление и after (обычно перезагрузка списка).
func (b *base) act(danger bool, title, question, confirm string, fn func(ctx context.Context) error, after func()) {
	run := func(f *formWin) {
		if f != nil {
			f.Busy("Выполняем…")
		}
		b.work(fn, func(err error) {
			if err != nil {
				if f != nil {
					f.Fail(err)
					return
				}
				b.setErr(err)
				return
			}
			if f != nil {
				f.Close()
			}
			b.setErr(nil)
			b.env.st.Notify(agent.SeveritySuccess, title, b.env.sv.Name)
			if after != nil {
				after()
			}
		})
	}
	if !danger {
		run(nil)
		return
	}
	q := widget.NewLabel(question)
	q.Wrapping = fyne.TextWrapWord
	showForm(formSpec{
		Key:       "confirm:" + title + question,
		Title:     title,
		Subtitle:  b.env.sv.Name + " · " + b.env.sv.Login(),
		Icon:      theme.WarningIcon(),
		IconColor: theme.ColorNameError,
		Body:      q,
		Confirm:   confirm,
		Danger:    true,
		OnConfirm: run,
	})
}

// confirm — окно подтверждения; yes вызывается после «да» (окно закрыто).
func (b *base) confirm(title, question, label string, yes func()) {
	q := widget.NewLabel(question)
	q.Wrapping = fyne.TextWrapWord
	showForm(formSpec{
		Key:       "confirm:" + title + question,
		Title:     title,
		Subtitle:  b.env.sv.Name + " · " + b.env.sv.Login(),
		Icon:      theme.WarningIcon(),
		IconColor: theme.ColorNameError,
		Body:      q,
		Confirm:   label,
		Danger:    true,
		OnConfirm: func(f *formWin) { f.Close(); yes() },
	})
}

// ask — окно с полями ввода; fn получает значения по порядку.
func (b *base) ask(title, confirm string, fields []askField, danger bool, fn func(ctx context.Context, vals []string) error, after func()) {
	entries := make([]fyne.CanvasObject, 0, len(fields))
	getters := make([]func() string, len(fields))
	var focus fyne.Focusable
	var subs []*widget.Entry
	for i, fl := range fields {
		switch {
		case len(fl.Options) > 0:
			s := widget.NewSelect(fl.Options, nil)
			s.SetSelected(fl.Value)
			if fl.Value == "" {
				s.SetSelectedIndex(0)
			}
			getters[i] = func() string { return s.Selected }
			entries = append(entries, field(fl.Label, s, nil))
		case fl.Check:
			c := widget.NewCheck(fl.Label, nil)
			c.SetChecked(fl.Value == "1")
			getters[i] = func() string {
				if c.Checked {
					return "1"
				}
				return ""
			}
			entries = append(entries, c)
		default:
			var e *widget.Entry
			switch {
			case fl.Password:
				e = widget.NewPasswordEntry()
			case fl.Multi:
				e = widget.NewMultiLineEntry()
				e.SetMinRowsVisible(5)
				e.TextStyle = fyne.TextStyle{Monospace: true}
			default:
				e = widget.NewEntry()
			}
			e.SetText(fl.Value)
			e.PlaceHolder = fl.Hint
			getters[i] = func() string { return e.Text }
			entries = append(entries, field(fl.Label, e, nil))
			if focus == nil {
				focus = e
			}
			if !fl.Multi {
				subs = append(subs, e)
			}
		}
	}
	var f *formWin
	f = showForm(formSpec{
		Key:      "ask:" + title,
		Title:    title,
		Subtitle: b.env.sv.Name + " · " + b.env.sv.Login(),
		Icon:     theme.DocumentCreateIcon(),
		Body:     container.NewVBox(entries...),
		Confirm:  confirm,
		Danger:   danger,
		Focus:    focus,
		OnConfirm: func(f *formWin) {
			vals := make([]string, len(getters))
			for i, g := range getters {
				vals[i] = strings.TrimSpace(g())
				if fields[i].Password || fields[i].Multi {
					vals[i] = g()
				}
			}
			f.Busy("Выполняем…")
			b.work(func(ctx context.Context) error { return fn(ctx, vals) }, func(err error) {
				if err != nil {
					f.Fail(err)
					return
				}
				f.Close()
				b.env.st.Notify(agent.SeveritySuccess, title, b.env.sv.Name)
				if after != nil {
					after()
				}
			})
		},
	})
	if f != nil {
		f.Submit(subs...)
	}
}

type askField struct {
	Label, Value, Hint string
	Password, Multi    bool
	Check              bool
	Options            []string
}

// ---------- окна вывода ----------

// textWin — отдельное окно с моноширинным текстом (лог, status, inspect).
// Повторный вызов с тем же key обновляет текст.
type textWin struct {
	w    fyne.Window
	grid *widget.TextGrid
	scr  *container.Scroll
	buf  strings.Builder
	stop func()
}

var openTexts = map[string]*textWin{}

func showText(key, title, text string) *textWin {
	if t := openTexts[key]; t != nil {
		t.set(text)
		t.w.RequestFocus()
		return t
	}
	t := &textWin{grid: widget.NewTextGrid()}
	t.w = fyne.CurrentApp().NewWindow(title + " · VANTA")
	t.scr = container.NewScroll(t.grid)
	copyBtn := widget.NewButtonWithIcon("Копировать", theme.ContentCopyIcon(), func() {
		fyne.CurrentApp().Clipboard().SetContent(t.buf.String())
	})
	closeBtn := widget.NewButton("Закрыть", func() { t.w.Close() })
	t.w.SetContent(container.NewBorder(nil, container.NewHBox(copyBtn, closeBtn), nil, nil, t.scr))
	t.w.Resize(fyne.NewSize(860, 560))
	t.w.SetOnClosed(func() {
		delete(openTexts, key)
		if t.stop != nil {
			t.stop()
		}
	})
	openTexts[key] = t
	t.set(text)
	if header.WindowHook != nil {
		header.WindowHook(t.w)
	}
	t.w.Show()
	return t
}

const textMax = 400_000

func (t *textWin) set(s string) {
	t.buf.Reset()
	t.buf.WriteString(stripANSI(s))
	t.grid.SetText(t.buf.String())
}

// add дописывает кусок и прокручивает вниз (для потоков).
func (t *textWin) add(s string) {
	t.buf.WriteString(stripANSI(s))
	text := t.buf.String()
	if len(text) > textMax {
		text = text[len(text)-textMax:]
		t.buf.Reset()
		t.buf.WriteString(text)
	}
	t.grid.SetText(text)
	t.scr.ScrollToBottom()
}

// stream — окно, куда идёт вывод долгой команды. detached=false — команда
// живёт, пока открыты окно и страница (логи -f); detached=true — доводится до
// конца в любом случае (apt: прерванный dpkg хуже долгого ожидания).
func (b *base) stream(key, title string, detached bool, fn func(ctx context.Context, out func(string)) error, after func()) {
	t := showText(key, title, "")
	parent := b.env.ctx
	if detached {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	t.stop = nil
	if !detached {
		t.stop = cancel
	}
	out := func(s string) { fyne.Do(func() { t.add(s) }) }
	go func() {
		defer cancel()
		err := fn(ctx, out)
		fyne.Do(func() {
			if errors.Is(err, context.Canceled) {
				return
			}
			if err != nil {
				t.add("\n✗ " + err.Error() + "\n")
				b.env.st.Notify(agent.SeverityError, title+": ошибка", err.Error())
			} else {
				t.add("\n✓ Готово\n")
				b.env.st.Notify(agent.SeveritySuccess, title, b.env.sv.Name)
			}
			if after != nil && !b.closed {
				after()
			}
		})
	}()
}

// stripANSI убирает escape-последовательности цвета и \r из вывода.
func stripANSI(s string) string {
	if !strings.ContainsAny(s, "\x1b\r") {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' {
			continue
		}
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

// ---------- таблица ----------

// grid — таблица со строкой заголовков, фильтром и выбором строки по ключу.
type grid struct {
	t      *widget.Table
	cols   []string
	all    [][]string
	keys   []string
	vis    []int
	query  string
	sel    string
	selRow int // строка, подсвеченная в таблице (для sel)
	OnPick func(key string)
}

func newGrid(cols []string, widths []float32) *grid {
	g := &grid{cols: cols, selRow: -1}
	g.t = widget.NewTableWithHeaders(
		func() (int, int) { return len(g.vis), len(g.cols) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			l := o.(*widget.Label)
			if id.Row < len(g.vis) {
				row := g.all[g.vis[id.Row]]
				if id.Col < len(row) {
					l.SetText(row[id.Col])
					return
				}
			}
			l.SetText("")
		})
	g.t.ShowHeaderColumn = false
	g.t.CreateHeader = func() fyne.CanvasObject {
		l := widget.NewLabel("")
		l.TextStyle = fyne.TextStyle{Bold: true}
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}
	g.t.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		if id.Col >= 0 && id.Col < len(g.cols) {
			o.(*widget.Label).SetText(g.cols[id.Col])
		}
	}
	for i, w := range widths {
		g.t.SetColumnWidth(i, w)
	}
	g.t.OnSelected = func(id widget.TableCellID) {
		if id.Row >= 0 && id.Row < len(g.vis) {
			g.sel, g.selRow = g.keys[g.vis[id.Row]], id.Row
			if g.OnPick != nil {
				g.OnPick(g.sel)
			}
		}
	}
	return g
}

// set — новые строки; keys[i] — ключ строки (сохраняет выбор при обновлении).
func (g *grid) set(rows [][]string, keys []string) {
	g.all, g.keys = rows, keys
	g.apply()
}

func (g *grid) filter(q string) {
	g.query = strings.ToLower(strings.TrimSpace(q))
	g.apply()
}

func (g *grid) apply() {
	g.vis = g.vis[:0]
	selRow := -1
	for i, r := range g.all {
		if g.query != "" && !strings.Contains(strings.ToLower(strings.Join(r, " ")), g.query) {
			continue
		}
		if g.keys[i] == g.sel {
			selRow = len(g.vis)
		}
		g.vis = append(g.vis, i)
	}
	switch {
	case selRow < 0 && g.sel != "":
		g.sel, g.selRow = "", -1
		g.t.UnselectAll()
		if g.OnPick != nil {
			g.OnPick("")
		}
	case selRow >= 0 && selRow != g.selRow:
		// Строка уехала (сортировка, фильтр) — подсветка едет за ней,
		// чтобы действие шло ровно над тем, что видно выбранным.
		g.selRow = selRow
		g.t.Select(widget.TableCellID{Row: selRow, Col: 0})
	}
	g.t.Refresh()
}

// selected — ключ выбранной строки ("" — ничего).
func (g *grid) selected() string { return g.sel }

// filterEntry — поле поиска по таблице.
func filterEntry(g *grid) *widget.Entry {
	e := widget.NewEntry()
	e.PlaceHolder = "Фильтр"
	e.OnChanged = g.filter
	return e
}

// needSel — кнопка, активная только при выбранной строке.
type selButtons struct{ list []*widget.Button }

func (s *selButtons) add(b *widget.Button) *widget.Button {
	b.Disable()
	s.list = append(s.list, b)
	return b
}

func (s *selButtons) set(on bool) {
	for _, b := range s.list {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
}

// noServer — что показывать вкладке сервера без подключения.
func noServer(st *core.Store, title string) fyne.CanvasObject {
	h := widget.NewLabel(title)
	h.SizeName = theme.SizeNameHeadingText
	h.TextStyle = fyne.TextStyle{Bold: true}
	msg := "Сервер не выбран. Выберите сервер в header — VANTA подключится к нему по SSH."
	if sv, ok := st.Active(); ok {
		switch hl := st.Health(sv.ID); {
		case hl.Status == core.StatusConnecting:
			msg = "Подключаемся к " + sv.Name + "…"
		case st.ConnError() != nil:
			msg = "Нет подключения к " + sv.Name + ": " + st.ConnError().Error()
		case st.Connected():
			msg = "Этот источник серверов не умеет выполнять команды (нужно SSH-подключение)."
		}
	}
	l := widget.NewLabel(msg)
	l.Wrapping = fyne.TextWrapWord
	return container.NewVBox(h, widget.NewCard("", "", l))
}

// btn — обычная кнопка с иконкой.
func btn(label string, ic fyne.Resource, fn func()) *widget.Button {
	return widget.NewButtonWithIcon(label, ic, fn)
}

func newTicker(sec int) *time.Ticker { return time.NewTicker(time.Duration(sec) * time.Second) }
