package screens

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	terminal "github.com/fyne-io/terminal"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/host"
	"github.com/Trade-Cloud/vanta/internal/termkit"
)

// Вкладка Terminal: сколько угодно PTY-сессий во вкладках, очередь команд,
// группы, зависимости, чекпоинты, переменные VANTA, книга команд, снимки
// вывода, сохранение в файл и напоминание о конце команды.
//
// Сессии живут, пока живо подключение (а не страница): уход в другой
// раздел и обратно их не закрывает.

// termTab — одна сессия.
type termTab struct {
	n     int
	sess  *termkit.Session
	term  *terminal.Terminal
	cfg   chan terminal.Config
	ended bool
	err   string
}

func (t *termTab) title() string { return fmt.Sprintf("Session %d", t.n) }

func (t *termTab) close() {
	if t.term != nil && t.cfg != nil {
		t.term.RemoveListener(t.cfg) // сам закрывает канал
		t.cfg = nil
	}
	if t.sess != nil {
		t.sess.Close()
	}
}

// termState — сессии и очередь одного подключения.
type termState struct {
	h      *host.Host
	tabs   []*termTab
	active int // индекс в tabs, -1 — нет
	next   int
	queue  *termkit.Queue
	remind bool // напомнить о конце очереди
	panel  bool // боковая панель открыта
	tab    int  // вкладка боковой панели
}

var termStates = map[*host.Host]*termState{}

// stateFor — состояние для подключения; сессии прошлых подключений закрываются.
func stateFor(h *host.Host) *termState {
	for k, st := range termStates {
		if k != h {
			for _, t := range st.tabs {
				t.close()
			}
			st.queue.Stop()
			delete(termStates, k)
		}
	}
	if st := termStates[h]; st != nil {
		return st
	}
	st := &termState{h: h, active: -1, next: 1, queue: &termkit.Queue{}}
	termStates[h] = st
	return st
}

type terminalPage struct {
	*base
	st   *termState
	ks   *termkit.Store
	obj  *fyne.Container
	bar  *fyne.Container
	area *fyne.Container
	side *fyne.Container
	tabs *container.AppTabs

	tools    *fyne.Container
	toolsRow *fyne.Container // линия + инструменты внизу

	queueList  *widget.List
	queueSel   string
	queueItems []termkit.Item
	runBtn     *widget.Button
	stopBtn    *widget.Button

	bookList  *widget.List
	book      []termkit.Entry
	bookSel   string
	bookCat   *widget.Select
	bookQuery *widget.Entry

	varList *widget.List
	vars    []string
	varSel  string

	snapList *widget.List
	snaps    []termkit.Snapshot
	snapSel  string
}

func newTerminalPage(env *toolEnv) toolPage {
	p := &terminalPage{base: newBase(env), st: stateFor(env.h), ks: termkit.Default()}
	p.st.queue.OnChange = func() { fyne.Do(p.refreshQueue) }
	p.bar = container.NewHBox()
	p.area = container.NewStack()
	p.side = container.NewStack()
	p.tools = p.buildTools()
	p.toolsRow = container.NewVBox(hline(), container.NewHScroll(p.tools))
	// Сверху — только вкладки сессий; все функции терминала — снизу.
	top := container.NewVBox(container.NewHScroll(p.bar), hline())
	bottom := container.NewVBox(
		p.side,
		p.busy,
		p.status,
		p.toolsRow,
	)
	p.obj = container.NewBorder(top, bottom, nil, nil, p.area)
	p.buildSide()
	p.render()
	return p
}

func (p *terminalPage) object() fyne.CanvasObject { return p.obj }

func (p *terminalPage) dispose() {
	// Сессии не закрываем: они принадлежат подключению.
	if p.st.queue.OnChange != nil {
		p.st.queue.OnChange = nil
	}
	p.base.dispose()
}

// ---------- вкладки сессий ----------

func (p *terminalPage) render() {
	p.bar.Objects = nil
	for i, t := range p.st.tabs {
		p.bar.Add(p.tabButton(i, t))
	}
	plus := header.NewPressable(container.NewCenter(square(16, widget.NewIcon(theme.ContentAddIcon()))), p.newSession)
	plus.Pad = fyne.NewSize(sp3, sp2)
	p.bar.Add(plus)
	p.bar.Refresh()

	if len(p.st.tabs) == 0 {
		p.toolsRow.Hide()
		p.side.Hide()
		empty := widget.NewLabel("Пусто")
		empty.Alignment = fyne.TextAlignCenter
		p.area.Objects = []fyne.CanvasObject{container.NewCenter(empty)}
		p.area.Refresh()
		return
	}
	p.toolsRow.Show()
	if p.st.panel {
		p.side.Show()
	} else {
		p.side.Hide()
	}
	if p.st.active < 0 || p.st.active >= len(p.st.tabs) {
		p.st.active = len(p.st.tabs) - 1
	}
	t := p.st.tabs[p.st.active]
	var content fyne.CanvasObject = t.term
	if t.err != "" {
		l := widget.NewLabel(t.err)
		l.Wrapping = fyne.TextWrapWord
		content = container.NewBorder(l, nil, nil, nil, t.term)
	}
	p.area.Objects = []fyne.CanvasObject{content}
	p.area.Refresh()
	p.env.w.Canvas().Focus(t.term)
}

func (p *terminalPage) tabButton(i int, t *termTab) fyne.CanvasObject {
	name := t.title()
	if t.ended {
		name += " · завершена"
	}
	color := theme.ColorNamePlaceHolder
	if i == p.st.active {
		color = theme.ColorNameForeground
	}
	label := header.NewPressable(txt(name, theme.TextSize(), color, i == p.st.active), func() {
		p.st.active = i
		p.render()
	})
	label.Pad = fyne.NewSize(sp3, sp2)
	x := header.NewPressable(square(12, widget.NewIcon(theme.CancelIcon())), func() { p.closeSession(t) })
	x.Pad = fyne.NewSize(sp1, sp2)
	row := container.NewHBox(label, x)
	if i == p.st.active {
		bg := header.NewRect(theme.ColorNameInputBackground, theme.ColorNameSeparator, 1, radiusChip)
		return container.NewStack(bg, row)
	}
	return row
}

func (p *terminalPage) newSession() {
	t := &termTab{n: p.st.next, term: terminal.New()}
	p.st.next++
	p.st.tabs = append(p.st.tabs, t)
	p.st.active = len(p.st.tabs) - 1
	p.render()
	h := p.env.h
	go func() {
		sess, err := termkit.Open(h, 120, 32)
		fyne.Do(func() {
			if err != nil {
				t.err = "Не удалось открыть сессию: " + err.Error()
				t.ended = true
				if !p.closed {
					p.render()
				}
				return
			}
			if t.ended { // вкладку закрыли, пока открывалась
				sess.Close()
				return
			}
			t.sess = sess
			cfg := make(chan terminal.Config, 4)
			t.cfg = cfg
			t.term.AddListener(cfg)
			go func() {
				for c := range cfg {
					sess.Resize(int(c.Columns), int(c.Rows))
				}
			}()
			go func() {
				_ = t.term.RunWithConnection(sess, sess.Out)
				fyne.Do(func() {
					t.ended = true
					if !p.closed && p.st.tabs != nil {
						p.render()
					}
				})
			}()
		})
	}()
}

func (p *terminalPage) closeSession(t *termTab) {
	do := func() {
		t.ended = true
		t.close()
		for i, x := range p.st.tabs {
			if x == t {
				p.st.tabs = append(p.st.tabs[:i], p.st.tabs[i+1:]...)
				if p.st.active >= i && p.st.active > 0 {
					p.st.active--
				}
				break
			}
		}
		p.render()
	}
	if t.sess != nil && !t.ended && (!t.sess.Idle() || p.st.queue.Running()) {
		p.confirm("Закрыть "+t.title(), "В "+t.title()+" ещё идёт команда. Закрыть сессию и прервать её?", "Закрыть", do)
		return
	}
	do()
}

func (p *terminalPage) current() (*termTab, error) {
	if p.st.active < 0 || p.st.active >= len(p.st.tabs) {
		return nil, errors.New("нет открытой сессии: нажмите +")
	}
	t := p.st.tabs[p.st.active]
	if t.sess == nil || t.ended {
		return nil, errors.New(t.title() + " не активна")
	}
	return t, nil
}

func (p *terminalPage) env0() termkit.Env {
	return termkit.Env{Vars: p.ks.Vars(), Server: p.env.sv.Name, Host: p.env.sv.IP, User: p.env.sv.SSHUser()}
}

// ---------- инструменты ----------

func (p *terminalPage) buildTools() *fyne.Container {
	panel := func(label string, ic fyne.Resource, tab int) *widget.Button {
		return btn(label, ic, func() {
			if p.st.panel && p.st.tab == tab {
				p.st.panel = false
			} else {
				p.st.panel, p.st.tab = true, tab
				p.tabs.SelectIndex(tab)
			}
			p.render()
		})
	}
	snap := btn("Снимок", theme.MediaPhotoIcon(), p.snapshotTerminal)
	toFile := btn("В файл", theme.DocumentSaveIcon(), func() {
		t, err := p.current()
		if err != nil {
			p.setErr(err)
			return
		}
		saveTextToFile(t.title()+".txt", t.term.Text(), p.setErr)
	})
	remind := btn("Напомнить", theme.InfoIcon(), p.remind)
	return container.NewHBox(
		panel("Очередь", theme.ListIcon(), 0),
		panel("Книга", theme.FileTextIcon(), 1),
		panel("Переменные", theme.SettingsIcon(), 2),
		panel("Снимки", theme.HistoryIcon(), 3),
		snap, toFile, remind,
	)
}

func (p *terminalPage) snapshotTerminal() {
	t, err := p.current()
	if err != nil {
		p.setErr(err)
		return
	}
	_, err = p.ks.AddSnapshot(termkit.Snapshot{
		Title:  t.title() + " · " + time.Now().Format("15:04:05"),
		Server: p.env.sv.Name, Text: strings.TrimRight(t.term.Text(), "\n "),
	})
	p.setErr(err)
	if err == nil {
		p.setInfo("Снимок вывода " + t.title() + " сохранён (панель «Снимки»).")
		p.refreshSnaps()
	}
}

// remind — уведомление, когда закончится текущая (или следующая) команда.
func (p *terminalPage) remind() {
	t, err := p.current()
	if err != nil {
		p.setErr(err)
		return
	}
	name, server := t.title(), p.env.sv.Name
	st := p.env.st
	err = t.sess.Remind(func(r termkit.Result) {
		took := r.Finished.Sub(r.Started).Round(time.Second)
		body := fmt.Sprintf("%s · %s · код %d · %s", server, name, r.Exit, took)
		fyne.Do(func() {
			sev := agent.SeveritySuccess
			if r.Exit != 0 {
				sev = agent.SeverityError
			}
			st.Notify(sev, "Команда выполнена", body)
			fyne.CurrentApp().SendNotification(fyne.NewNotification("VANTA: команда выполнена", body))
		})
	})
	if err != nil {
		p.setErr(err)
		return
	}
	if t.sess.Idle() {
		p.setInfo("Напомню, когда закончится следующая команда в " + name + ".")
	} else {
		p.setInfo("Напомню, когда закончится текущая команда в " + name + ".")
	}
}

// ---------- боковая панель ----------

func (p *terminalPage) buildSide() {
	p.tabs = container.NewAppTabs(
		container.NewTabItem("Очередь", p.queuePanel()),
		container.NewTabItem("Книга", p.bookPanel()),
		container.NewTabItem("Переменные", p.varsPanel()),
		container.NewTabItem("Снимки", p.snapsPanel()),
	)
	p.tabs.SelectIndex(p.st.tab)
	p.tabs.OnSelected = func(*container.TabItem) { p.st.tab = p.tabs.SelectedIndex() }
	p.side.Objects = []fyne.CanvasObject{fixedH(320, container.NewBorder(hline(), nil, nil, nil, p.tabs))}
	p.refreshQueue()
	p.refreshBook()
	p.refreshVars()
	p.refreshSnaps()
}

// fixedH — содержимое фиксированной высоты (панель под терминалом).
func fixedH(h float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&fixedHLayout{h: h}, o)
}

type fixedHLayout struct{ h float32 }

func (l *fixedHLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(size.Width, l.h))
	}
}

func (l *fixedHLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	w := float32(0)
	for _, o := range objs {
		w = fyne.Max(w, o.MinSize().Width)
	}
	return fyne.NewSize(w, l.h)
}

// ----- очередь -----

func (p *terminalPage) queuePanel() fyne.CanvasObject {
	p.queueList = widget.NewList(
		func() int { return len(p.queueItems) },
		func() fyne.CanvasObject {
			a := widget.NewLabel("")
			a.Truncation = fyne.TextTruncateEllipsis
			a.TextStyle = fyne.TextStyle{Monospace: true}
			b := widget.NewLabel("")
			b.SizeName = theme.SizeNameCaptionText
			b.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(a, b)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.queueItems) {
				return
			}
			it := p.queueItems[i]
			box := o.(*fyne.Container)
			cmd := fmt.Sprintf("%d. %s", i+1, it.Step.Command)
			box.Objects[0].(*widget.Label).SetText(cmd)
			box.Objects[1].(*widget.Label).SetText(stepMeta(it))
		})
	p.queueList.OnSelected = func(i widget.ListItemID) {
		if i < len(p.queueItems) {
			p.queueSel = p.queueItems[i].ID
		}
	}
	add := widget.NewEntry()
	add.PlaceHolder = "Команда, Enter — в очередь ({{переменная}})"
	onlyOK := widget.NewCheck("После успеха предыдущей", nil)
	onlyOK.SetChecked(true)
	addFn := func(s string) {
		if strings.TrimSpace(s) == "" {
			return
		}
		p.st.queue.Add(termkit.Step{Command: s, OnlyIfOK: onlyOK.Checked})
		add.SetText("")
	}
	add.OnSubmitted = addFn

	up := btn("", theme.MoveUpIcon(), func() { p.st.queue.Move(p.queueSel, -1); p.reselectQueue() })
	down := btn("", theme.MoveDownIcon(), func() { p.st.queue.Move(p.queueSel, 1); p.reselectQueue() })
	edit := btn("", theme.DocumentCreateIcon(), p.editStep)
	del := btn("", theme.DeleteIcon(), func() { p.st.queue.Remove(p.queueSel); p.queueSel = "" })
	out := btn("Вывод", theme.VisibilityIcon(), p.stepOutput)
	outSnap := btn("", theme.MediaPhotoIcon(), p.stepSnapshot)
	clear := btn("Очистить", theme.ContentClearIcon(), func() {
		if len(p.queueItems) > 0 {
			p.confirm("Очистить очередь", "Убрать из очереди все команды?", "Очистить", p.st.queue.Clear)
		}
	})
	asGroup := btn("В книгу группой", theme.ContentAddIcon(), p.queueToGroup)
	p.runBtn = btn("Запустить", theme.MediaPlayIcon(), p.runQueue)
	p.runBtn.Importance = widget.HighImportance
	p.stopBtn = btn("Стоп", theme.MediaStopIcon(), p.st.queue.Stop)
	remind := widget.NewCheck("Напомнить, когда очередь закончится", func(on bool) { p.st.remind = on })
	remind.SetChecked(p.st.remind)

	// Панель под терминалом: слева список, справа действия.
	top := container.NewBorder(nil, nil, nil,
		container.NewHBox(onlyOK, btn("", theme.ContentAddIcon(), func() { addFn(add.Text) })), add)
	side := container.NewVBox(
		container.NewHBox(up, down, edit, del, out, outSnap),
		container.NewHBox(clear, asGroup),
		layout.NewSpacer(),
		remind,
		container.NewGridWithColumns(2, p.runBtn, p.stopBtn),
	)
	return container.NewBorder(top, nil, nil, side, p.queueList)
}

func stepMeta(it termkit.Item) string {
	var m []string
	m = append(m, it.State.Label())
	if it.Result != nil {
		m = append(m, fmt.Sprintf("код %d", it.Result.Exit), it.Result.Finished.Sub(it.Result.Started).Round(100*time.Millisecond).String())
	}
	if it.Step.Group != "" {
		m = append(m, "группа «"+it.Step.Group+"»")
	}
	if it.Step.OnlyIfOK {
		m = append(m, "после успеха")
	}
	if it.Step.Checkpoint {
		m = append(m, "чекпоинт")
	}
	if it.Step.SaveAs != "" {
		m = append(m, "→ {{"+it.Step.SaveAs+"}}")
	}
	if it.Err != "" {
		m = append(m, it.Err)
	}
	return strings.Join(m, " · ")
}

func (p *terminalPage) refreshQueue() {
	if p.closed || p.queueList == nil {
		return
	}
	p.queueItems = p.st.queue.Items()
	p.queueList.Refresh()
	running := p.st.queue.Running()
	if running {
		p.runBtn.Disable()
		p.stopBtn.Enable()
	} else {
		p.runBtn.Enable()
		p.stopBtn.Disable()
	}
}

func (p *terminalPage) reselectQueue() {
	for i, it := range p.st.queue.Items() {
		if it.ID == p.queueSel {
			p.queueList.Select(i)
		}
	}
}

func (p *terminalPage) queueItem() (termkit.Item, bool) {
	for _, it := range p.queueItems {
		if it.ID == p.queueSel {
			return it, true
		}
	}
	return termkit.Item{}, false
}

func stepFields(s termkit.Step) []askField {
	b := func(v bool) string {
		if v {
			return "1"
		}
		return ""
	}
	return []askField{
		{Label: "Команда", Value: s.Command},
		{Label: "Только после успеха предыдущей", Check: true, Value: b(s.OnlyIfOK)},
		{Label: "Чекпоинт: сохранить результат и остаток очереди", Check: true, Value: b(s.Checkpoint)},
		{Label: "Сохранить вывод в переменную (пусто — нет)", Value: s.SaveAs, Hint: "VERSION"},
	}
}

func stepFrom(v []string, group string) (termkit.Step, error) {
	s := termkit.Step{Command: v[0], OnlyIfOK: v[1] == "1", Checkpoint: v[2] == "1", SaveAs: v[3], Group: group}
	if strings.TrimSpace(s.Command) == "" {
		return s, errors.New("пустая команда")
	}
	if s.SaveAs != "" && !termkit.ValidVar(s.SaveAs) {
		return s, errors.New("имя переменной: латиница, цифры и _")
	}
	return s, nil
}

func (p *terminalPage) editStep() {
	it, ok := p.queueItem()
	if !ok {
		return
	}
	p.ask("Команда очереди", "Сохранить", stepFields(it.Step), false, func(ctx context.Context, v []string) error {
		s, err := stepFrom(v, it.Step.Group)
		if err != nil {
			return err
		}
		return p.st.queue.Update(it.ID, s)
	}, nil)
}

func (p *terminalPage) stepOutput() {
	it, ok := p.queueItem()
	if !ok || it.Result == nil {
		p.setInfo("У выбранной команды ещё нет вывода.")
		return
	}
	text := termkit.StripANSI(it.Result.Output)
	showText("qout:"+it.ID, it.Ran, text)
}

// stepSnapshot — снимок вывода выбранной команды очереди.
func (p *terminalPage) stepSnapshot() {
	it, ok := p.queueItem()
	if !ok || it.Result == nil {
		p.setInfo("У выбранной команды ещё нет вывода.")
		return
	}
	code := it.Result.Exit
	_, err := p.ks.AddSnapshot(termkit.Snapshot{Title: "Вывод: " + it.Ran, Server: p.env.sv.Name,
		Text: strings.TrimSpace(termkit.StripANSI(it.Result.Output)), Command: it.Ran, Exit: &code})
	p.setErr(err)
	if err == nil {
		p.setInfo("Снимок вывода сохранён.")
		p.refreshSnaps()
	}
}

func (p *terminalPage) runQueue() {
	t, err := p.current()
	if err != nil {
		p.setErr(err)
		return
	}
	if len(p.st.queue.Items()) == 0 {
		p.setInfo("Очередь пуста: добавьте команды.")
		return
	}
	p.runIn(t, p.st.queue, "Очередь")
}

// runIn выполняет очередь q в сессии t; итог — в строку состояния и,
// если просили, в уведомление.
func (p *terminalPage) runIn(t *termTab, q *termkit.Queue, title string) {
	sess, env, ks := t.sess, p.env0(), p.ks
	remind := p.st.remind
	st, name, server := p.env.st, t.title(), p.env.sv.Name
	p.setInfo(title + " выполняется в " + name + "…")
	p.env.w.Canvas().Focus(t.term)
	go func() {
		sum, err := q.Run(context.Background(), sess, ks, env)
		fyne.Do(func() {
			body := server + " · " + name + " · " + sum.String()
			if err != nil {
				body = server + " · " + name + " · " + err.Error()
			}
			sev := agent.SeveritySuccess
			if err != nil || sum.Failed > 0 {
				sev = agent.SeverityError
			}
			st.Notify(sev, title+" завершена", body)
			if remind {
				fyne.CurrentApp().SendNotification(fyne.NewNotification("VANTA: "+title+" завершена", body))
			}
			if !p.closed {
				if err != nil {
					p.setErr(err)
				} else {
					p.setInfo(title + ": " + sum.String())
				}
				p.refreshVars()
				p.refreshSnaps()
			}
		})
	}()
}

func (p *terminalPage) queueToGroup() {
	steps := p.st.queue.Steps()
	if len(steps) == 0 {
		p.setInfo("Очередь пуста.")
		return
	}
	var cmds []string
	onlyOK := true
	for _, s := range steps {
		cmds = append(cmds, s.Command)
		onlyOK = onlyOK && s.OnlyIfOK
	}
	p.editEntry(termkit.Entry{Commands: cmds, OnlyIfOK: onlyOK, Category: "Группы"})
}

// ----- книга -----

func (p *terminalPage) bookPanel() fyne.CanvasObject {
	p.bookCat = widget.NewSelect(nil, func(string) { p.refreshBook() })
	p.bookQuery = widget.NewEntry()
	p.bookQuery.PlaceHolder = "Поиск"
	p.bookQuery.OnChanged = func(string) { p.refreshBook() }
	p.bookList = widget.NewList(
		func() int { return len(p.book) },
		func() fyne.CanvasObject {
			a := widget.NewLabel("")
			a.TextStyle = fyne.TextStyle{Bold: true}
			a.Truncation = fyne.TextTruncateEllipsis
			b := widget.NewLabel("")
			b.SizeName = theme.SizeNameCaptionText
			b.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(a, b)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.book) {
				return
			}
			e := p.book[i]
			box := o.(*fyne.Container)
			name := e.Name
			if e.IsGroup() {
				name += fmt.Sprintf("  [группа · %d]", len(e.Commands))
			}
			box.Objects[0].(*widget.Label).SetText(name)
			sub := e.Description
			if sub == "" {
				sub = e.Commands[0]
			}
			if e.Category != "" {
				sub = e.Category + " · " + sub
			}
			box.Objects[1].(*widget.Label).SetText(sub)
		})
	p.bookList.OnSelected = func(i widget.ListItemID) {
		if i < len(p.book) {
			p.bookSel = p.book[i].ID
		}
	}
	add := btn("Новая", theme.ContentAddIcon(), func() { p.editEntry(termkit.Entry{}) })
	edit := btn("", theme.DocumentCreateIcon(), func() {
		if e, ok := p.entry(); ok {
			p.editEntry(e)
		}
	})
	del := btn("", theme.DeleteIcon(), func() {
		e, ok := p.entry()
		if !ok {
			return
		}
		p.confirm("Удалить из книги", "Удалить «"+e.Name+"» из книги команд?", "Удалить", func() {
			p.setErr(p.ks.DeleteEntry(e.ID))
			p.bookSel = ""
			p.refreshBook()
		})
	})
	run := btn("Запустить", theme.MediaPlayIcon(), func() { p.useEntry(false) })
	run.Importance = widget.HighImportance
	toQueue := btn("В очередь", theme.ListIcon(), func() { p.useEntry(true) })
	top := container.NewBorder(nil, nil, nil, container.NewHBox(add, edit, del),
		container.NewGridWithColumns(2, p.bookCat, p.bookQuery))
	side := container.NewVBox(run, toQueue)
	return container.NewBorder(top, nil, nil, side, p.bookList)
}

func (p *terminalPage) refreshBook() {
	if p.bookList == nil {
		return
	}
	cats := append([]string{"Все категории"}, p.ks.Categories()...)
	p.bookCat.Options = cats
	if p.bookCat.Selected == "" {
		p.bookCat.Selected = cats[0]
	}
	q := strings.ToLower(strings.TrimSpace(p.bookQuery.Text))
	p.book = nil
	for _, e := range p.ks.Book() {
		if p.bookCat.Selected != "" && p.bookCat.Selected != cats[0] && e.Category != p.bookCat.Selected {
			continue
		}
		hay := strings.ToLower(e.Name + " " + e.Description + " " + e.Category + " " + strings.Join(e.Commands, " "))
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		p.book = append(p.book, e)
	}
	p.bookCat.Refresh()
	p.bookList.UnselectAll()
	p.bookSel = ""
	p.bookList.Refresh()
}

func (p *terminalPage) entry() (termkit.Entry, bool) {
	for _, e := range p.book {
		if e.ID == p.bookSel {
			return e, true
		}
	}
	return termkit.Entry{}, false
}

func (p *terminalPage) editEntry(e termkit.Entry) {
	title := "Новая команда"
	if e.ID != "" {
		title = "Команда «" + e.Name + "»"
	}
	only := ""
	if e.OnlyIfOK || e.ID == "" {
		only = "1"
	}
	p.ask(title, "Сохранить", []askField{
		{Label: "Название", Value: e.Name, Hint: "Перезапуск nginx"},
		{Label: "Категория", Value: e.Category, Hint: "Веб-сервер"},
		{Label: "Описание", Value: e.Description},
		{Label: "Команды — по одной на строку (несколько строк = группа). Параметры: {{имя}}", Multi: true,
			Value: strings.Join(e.Commands, "\n"), Hint: "systemctl restart {{service}}"},
		{Label: "В группе: каждая следующая только после успеха предыдущей", Check: true, Value: only},
	}, false, func(ctx context.Context, v []string) error {
		e.Name, e.Category, e.Description = v[0], v[1], v[2]
		e.Commands = strings.Split(v[3], "\n")
		e.OnlyIfOK = v[4] == "1"
		_, err := p.ks.SaveEntry(e)
		return err
	}, func() { p.refreshBook(); p.st.panel, p.st.tab = true, 1; p.tabs.SelectIndex(1); p.render() })
}

// useEntry — запустить запись книги в текущей сессии (toQueue — добавить в
// очередь). Параметры без значения спрашиваются в окне.
func (p *terminalPage) useEntry(toQueue bool) {
	e, ok := p.entry()
	if !ok {
		p.setInfo("Выберите команду в книге.")
		return
	}
	missing := p.env0().Missing(strings.Join(e.Commands, "\n"))
	apply := func(vals map[string]string) {
		var steps []termkit.Step
		group := ""
		if e.IsGroup() {
			group = e.Name
		}
		for i, c := range e.Commands {
			steps = append(steps, termkit.Step{Command: termkit.ExpandOnly(c, vals), Group: group, OnlyIfOK: e.OnlyIfOK && i > 0})
		}
		if toQueue {
			p.st.queue.Add(steps...)
			p.st.panel, p.st.tab = true, 0
			p.tabs.SelectIndex(0)
			p.render()
			return
		}
		t, err := p.current()
		if err != nil {
			p.setErr(err)
			return
		}
		if !e.IsGroup() {
			cmd, err := p.env0().Expand(steps[0].Command)
			if err != nil {
				p.setErr(err)
				return
			}
			p.setErr(t.sess.Send(cmd))
			p.env.w.Canvas().Focus(t.term)
			return
		}
		// Группа — общий запуск своей отдельной очередью.
		gq := &termkit.Queue{}
		gq.Add(steps...)
		p.runIn(t, gq, "Группа «"+e.Name+"»")
	}
	if len(missing) == 0 {
		apply(nil)
		return
	}
	var fields []askField
	for _, m := range missing {
		fields = append(fields, askField{Label: "{{" + m + "}}"})
	}
	p.ask("Параметры «"+e.Name+"»", "Продолжить", fields, false, func(ctx context.Context, v []string) error {
		vals := map[string]string{}
		for i, m := range missing {
			vals[m] = v[i]
		}
		fyne.Do(func() { apply(vals) })
		return nil
	}, nil)
}

// ----- переменные -----

func (p *terminalPage) varsPanel() fyne.CanvasObject {
	p.varList = widget.NewList(
		func() int { return len(p.vars) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle = fyne.TextStyle{Monospace: true}
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(p.vars) {
				o.(*widget.Label).SetText(p.vars[i])
			}
		})
	p.varList.OnSelected = func(i widget.ListItemID) {
		if i < len(p.vars) {
			p.varSel, _, _ = strings.Cut(p.vars[i], " = ")
		}
	}
	add := btn("Новая", theme.ContentAddIcon(), func() { p.editVar("", "") })
	edit := btn("", theme.DocumentCreateIcon(), func() {
		if p.varSel != "" {
			p.editVar(p.varSel, p.ks.Vars()[p.varSel])
		}
	})
	del := btn("", theme.DeleteIcon(), func() {
		name := p.varSel
		if name == "" {
			return
		}
		p.confirm("Удалить переменную", "Удалить {{"+name+"}}?", "Удалить", func() {
			p.setErr(p.ks.DeleteVar(name))
			p.varSel = ""
			p.refreshVars()
		})
	})
	var help []string
	for _, b := range termkit.Builtins() {
		help = append(help, "{{"+b[0]+"}} — "+b[1])
	}
	hl := widget.NewLabel("Встроенные:\n" + strings.Join(help, "\n"))
	hl.SizeName = theme.SizeNameCaptionText
	hl.Wrapping = fyne.TextWrapWord
	return container.NewBorder(container.NewHBox(add, edit, del), nil, nil,
		fixedW(320, container.NewVScroll(hl)), p.varList)
}

func (p *terminalPage) editVar(name, value string) {
	title := "Новая переменная"
	if name != "" {
		title = "Переменная " + name
	}
	p.ask(title, "Сохранить", []askField{{Label: "Имя", Value: name, Hint: "APP_DIR"}, {Label: "Значение", Value: value, Multi: true}}, false,
		func(ctx context.Context, v []string) error {
			n := strings.TrimSpace(v[0])
			if name != "" && n != name {
				if err := p.ks.DeleteVar(name); err != nil {
					return err
				}
			}
			return p.ks.SetVar(n, strings.TrimRight(v[1], "\n"))
		}, p.refreshVars)
}

func (p *terminalPage) refreshVars() {
	if p.varList == nil {
		return
	}
	vars := p.ks.Vars()
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	p.vars = p.vars[:0]
	for _, k := range names {
		v := strings.ReplaceAll(vars[k], "\n", "⏎")
		p.vars = append(p.vars, k+" = "+v)
	}
	p.varList.UnselectAll()
	p.varSel = ""
	p.varList.Refresh()
}

// ----- снимки и чекпоинты -----

func (p *terminalPage) snapsPanel() fyne.CanvasObject {
	p.snapList = widget.NewList(
		func() int { return len(p.snaps) },
		func() fyne.CanvasObject {
			a := widget.NewLabel("")
			a.Truncation = fyne.TextTruncateEllipsis
			b := widget.NewLabel("")
			b.SizeName = theme.SizeNameCaptionText
			b.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(a, b)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.snaps) {
				return
			}
			s := p.snaps[i]
			box := o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(s.Title)
			sub := s.Server + " · " + s.At.Format("2006-01-02 15:04:05")
			if s.Checkpoint {
				sub += fmt.Sprintf(" · чекпоинт · осталось команд: %d", len(s.Remaining))
			}
			box.Objects[1].(*widget.Label).SetText(sub)
		})
	p.snapList.OnSelected = func(i widget.ListItemID) {
		if i < len(p.snaps) {
			p.snapSel = p.snaps[i].ID
		}
	}
	open := btn("Открыть", theme.VisibilityIcon(), func() {
		if s, ok := p.snap(); ok {
			text := s.Text
			if s.Command != "" {
				text = "$ " + s.Command + "\n\n" + text
			}
			showText("snap:"+s.ID, s.Title, text)
		}
	})
	toFile := btn("В файл", theme.DocumentSaveIcon(), func() {
		if s, ok := p.snap(); ok {
			saveTextToFile("vanta-"+s.At.Format("20060102-150405")+".txt", s.Text, p.setErr)
		}
	})
	del := btn("", theme.DeleteIcon(), func() {
		s, ok := p.snap()
		if !ok {
			return
		}
		p.confirm("Удалить снимок", "Удалить «"+s.Title+"»?", "Удалить", func() {
			p.setErr(p.ks.DeleteSnapshot(s.ID))
			p.refreshSnaps()
		})
	})
	resume := btn("Продолжить с чекпоинта", theme.MediaSkipNextIcon(), func() {
		s, ok := p.snap()
		if !ok || !s.Checkpoint {
			p.setInfo("Выберите чекпоинт.")
			return
		}
		p.confirm("Продолжить с чекпоинта", fmt.Sprintf("Заменить очередь оставшимися командами (%d) и вернуть переменные на момент чекпоинта?", len(s.Remaining)),
			"Продолжить", func() {
				if err := p.st.queue.Restore(p.ks, s); err != nil {
					p.setErr(err)
					return
				}
				p.refreshVars()
				p.st.panel, p.st.tab = true, 0
				p.tabs.SelectIndex(0)
				p.render()
				p.setInfo("Очередь восстановлена из чекпоинта — нажмите «Запустить».")
			})
	})
	return container.NewBorder(container.NewHBox(open, toFile, del, layout.NewSpacer(), resume), nil, nil, nil, p.snapList)
}

func (p *terminalPage) refreshSnaps() {
	if p.snapList == nil {
		return
	}
	p.snaps = p.ks.Snapshots()
	p.snapList.UnselectAll()
	p.snapSel = ""
	p.snapList.Refresh()
}

func (p *terminalPage) snap() (termkit.Snapshot, bool) {
	for _, s := range p.snaps {
		if s.ID == p.snapSel {
			return s, true
		}
	}
	return termkit.Snapshot{}, false
}

// saveTextToFile — «Сохранить как» в отдельном окне.
func saveTextToFile(name, text string, onErr func(error)) {
	pw := pickerWindow("Сохранить вывод")
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		pw.Close()
		if err != nil || wc == nil {
			if err != nil {
				onErr(err)
			}
			return
		}
		_, werr := wc.Write([]byte(termkit.StripANSI(text)))
		cerr := wc.Close()
		if werr == nil {
			werr = cerr
		}
		onErr(werr)
	}, pw.w)
	d.SetFileName(name)
	d.Resize(fyne.NewSize(800, 540))
	d.SetOnClosed(pw.Close)
	d.Show()
}
