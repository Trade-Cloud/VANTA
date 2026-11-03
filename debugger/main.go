// VANTA Debugger — отдельное приложение для отладки VANTA. В основную программу не входит.
//
// Подключается к debug-мосту основного приложения (internal/debugbridge) по TCP
// и показывает логи, данные окон, FPS/рантайм и дерево UI, а также включает
// оверлеи (рамки виджетов, FPS-бейдж) прямо в окне VANTA.
//
// Запуск: VANTA_DEBUG=1 go run .   (основное приложение)
//
//	go run ./debugger           (отладчик; адрес можно задать -addr)
package main

import (
	"flag"
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/debugproto"
	"github.com/Trade-Cloud/vanta/internal/prefs"
)

const maxLogs = 5000

var levels = []string{"ВСЕ", "DEBUG", "INFO", "WARN", "ERROR"}

type ui struct {
	win fyne.Window
	cl  *Client

	statusDot *canvas.Circle
	status    *widget.Label
	addr      *widget.Entry
	boundsOn  bool
	fpsOn     bool
	resetBtn  *widget.Button

	logs      []debugproto.Message
	filtered  []int
	minLevel  int
	query     string
	autoTail  bool
	logList   *widget.List
	logDetail *widget.Label

	windows  map[int]debugproto.WindowInfo
	winLabel *widget.Label

	fpsText   *canvas.Text
	spark     *sparkline
	perfLabel *widget.Label

	trees     map[int]*debugproto.Tree
	treeLabel *widget.Label
}

func main() {
	addr := flag.String("addr", debugproto.DefaultAddr, "адрес debug-моста VANTA")
	flag.Parse()

	a := app.NewWithID("org.trade-cloud.vanta.debugger")
	u := &ui{
		win:      a.NewWindow("VANTA Debugger"),
		cl:       NewClient(*addr),
		autoTail: true,
		windows:  map[int]debugproto.WindowInfo{},
		trees:    map[int]*debugproto.Tree{},
	}
	u.cl.OnState = func(ok bool, err error) { fyne.Do(func() { u.setState(ok, err) }) }
	u.cl.OnMessage = func(m debugproto.Message) { fyne.Do(func() { u.handle(m) }) }

	u.win.SetContent(u.build(*addr))
	u.win.Resize(fyne.NewSize(1000, 700))

	go u.cl.Run()
	u.win.ShowAndRun()
}

func (u *ui) build(addr string) fyne.CanvasObject {
	u.statusDot = canvas.NewCircle(color.NRGBA{0xC0, 0x30, 0x30, 0xFF})
	dot := container.NewGridWrap(fyne.NewSize(12, 12), u.statusDot)
	u.status = widget.NewLabel("Подключение…")

	u.addr = widget.NewEntry()
	u.addr.SetText(addr)
	u.addr.OnSubmitted = func(s string) { u.cl.SetAddr(strings.TrimSpace(s)) }
	connect := widget.NewButton("Подключиться", func() { u.cl.SetAddr(strings.TrimSpace(u.addr.Text)) })

	bounds := widget.NewCheck("Рамки виджетов", func(on bool) {
		u.boundsOn = on
		u.send(debugproto.Command{Name: debugproto.CmdOverlayBounds, Bool: on})
	})
	fps := widget.NewCheck("FPS в окне", func(on bool) {
		u.fpsOn = on
		u.send(debugproto.Command{Name: debugproto.CmdOverlayFPS, Bool: on})
	})

	// Сброс первого запуска — офлайн-параметр: правит файл состояния VANTA,
	// поэтому доступен только пока VANTA не подключена (выключена).
	u.resetBtn = widget.NewButton("Сбросить первый запуск", u.resetOnboarding)

	top := container.NewBorder(nil, nil,
		container.NewHBox(container.NewCenter(dot), u.status),
		container.NewHBox(bounds, fps, u.resetBtn, connect),
		u.addr,
	)

	tabs := container.NewAppTabs(
		container.NewTabItem("Логи", u.buildLogs()),
		container.NewTabItem("Окна", u.buildWindows()),
		container.NewTabItem("Производительность", u.buildPerf()),
		container.NewTabItem("Дерево UI", u.buildTree()),
	)
	return container.NewBorder(container.NewVBox(top, widget.NewSeparator()), nil, nil, nil, tabs)
}

// ---------- Логи ----------

func (u *ui) buildLogs() fyne.CanvasObject {
	u.logList = widget.NewList(
		func() int { return len(u.filtered) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle = fyne.TextStyle{Monospace: true}
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= len(u.filtered) {
				return
			}
			m := u.logs[u.filtered[id]]
			l := o.(*widget.Label)
			l.Importance = levelImportance(m.Log.Level)
			l.SetText(formatLog(m, false))
		},
	)
	u.logDetail = widget.NewLabel("Выбери строку, чтобы увидеть детали")
	u.logDetail.TextStyle = fyne.TextStyle{Monospace: true}
	u.logDetail.Wrapping = fyne.TextWrapWord
	u.logList.OnSelected = func(id widget.ListItemID) {
		if id < len(u.filtered) {
			u.logDetail.SetText(formatLog(u.logs[u.filtered[id]], true))
		}
	}

	level := widget.NewSelect(levels, func(s string) {
		u.minLevel = levelRank(s)
		u.refilter()
	})
	level.SetSelected(levels[0])
	search := widget.NewEntry()
	search.SetPlaceHolder("Поиск по тексту и атрибутам")
	search.OnChanged = func(s string) {
		u.query = strings.ToLower(s)
		u.refilter()
	}
	tail := widget.NewCheck("Автопрокрутка", func(on bool) { u.autoTail = on })
	tail.SetChecked(true)
	clear := widget.NewButton("Очистить", func() {
		u.logs = nil
		u.refilter()
	})

	bar := container.NewBorder(nil, nil, level, container.NewHBox(tail, clear), search)
	split := container.NewVSplit(u.logList, container.NewVScroll(u.logDetail))
	split.Offset = 0.8
	return container.NewBorder(bar, nil, nil, nil, split)
}

func (u *ui) addLog(m debugproto.Message) {
	u.logs = append(u.logs, m)
	if len(u.logs) > maxLogs {
		u.logs = u.logs[len(u.logs)-maxLogs:]
		u.refilter()
		return
	}
	if u.matches(m) {
		u.filtered = append(u.filtered, len(u.logs)-1)
		u.logList.Refresh()
		if u.autoTail {
			u.logList.ScrollToBottom()
		}
	}
}

func (u *ui) refilter() {
	u.filtered = u.filtered[:0]
	for i, m := range u.logs {
		if u.matches(m) {
			u.filtered = append(u.filtered, i)
		}
	}
	u.logList.UnselectAll()
	u.logList.Refresh()
	if u.autoTail {
		u.logList.ScrollToBottom()
	}
}

func (u *ui) matches(m debugproto.Message) bool {
	if levelRank(m.Log.Level) < u.minLevel {
		return false
	}
	return u.query == "" || strings.Contains(strings.ToLower(formatLog(m, true)), u.query)
}

func formatLog(m debugproto.Message, full bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %-5s  %s", m.Time.Local().Format("15:04:05.000"), m.Log.Level, m.Log.Message)
	keys := make([]string, 0, len(m.Log.Attrs))
	for k := range m.Log.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sep := "  "
	if full {
		sep = "\n  "
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "%s%s=%s", sep, k, m.Log.Attrs[k])
	}
	if full && m.Log.Source != "" {
		fmt.Fprintf(&b, "\n  source: %s", m.Log.Source)
	}
	return b.String()
}

// levelRank: "INFO+2" и т.п. считаем по префиксу.
func levelRank(l string) int {
	switch {
	case strings.HasPrefix(l, "ERROR"):
		return 4
	case strings.HasPrefix(l, "WARN"):
		return 3
	case strings.HasPrefix(l, "INFO"):
		return 2
	case strings.HasPrefix(l, "DEBUG"):
		return 1
	}
	return 0
}

func levelImportance(l string) widget.Importance {
	switch levelRank(l) {
	case 4:
		return widget.DangerImportance
	case 3:
		return widget.WarningImportance
	case 1:
		return widget.LowImportance
	}
	return widget.MediumImportance
}

// ---------- Окна ----------

func (u *ui) buildWindows() fyne.CanvasObject {
	u.winLabel = widget.NewLabel("Нет данных")
	u.winLabel.TextStyle = fyne.TextStyle{Monospace: true}
	return container.NewVScroll(u.winLabel)
}

func (u *ui) renderWindows() {
	if len(u.windows) == 0 {
		u.winLabel.SetText("Нет данных")
		return
	}
	ids := make([]int, 0, len(u.windows))
	for id := range u.windows {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var b strings.Builder
	for _, id := range ids {
		w := u.windows[id]
		fmt.Fprintf(&b, "Окно #%d  «%s»\n", w.ID, w.Title)
		fmt.Fprintf(&b, "  размер        %.0f × %.0f  (scale %.2f, в пикселях %.0f × %.0f)\n",
			w.Width, w.Height, w.Scale, w.Width*w.Scale, w.Height*w.Scale)
		fmt.Fprintf(&b, "  min size      %.0f × %.0f\n", w.MinWidth, w.MinHeight)
		fmt.Fprintf(&b, "  fullscreen    %v\n", w.FullScreen)
		fmt.Fprintf(&b, "  padded        %v\n", w.Padded)
		fmt.Fprintf(&b, "  тема          %s\n", w.Theme)
		fmt.Fprintf(&b, "  фокус         %s\n", orDash(w.Focused))
		fmt.Fprintf(&b, "  объектов      %d\n", w.Objects)
		fmt.Fprintf(&b, "  оверлей       рамки=%v fps=%v\n\n", w.OverlayOn, w.OverlayFPS)
	}
	u.winLabel.SetText(b.String())
}

// ---------- Производительность ----------

func (u *ui) buildPerf() fyne.CanvasObject {
	u.fpsText = canvas.NewText("— FPS", color.NRGBA{0x39, 0xFF, 0x14, 0xFF})
	u.fpsText.TextSize = 40
	u.fpsText.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	u.spark = newSparkline(60)
	u.perfLabel = widget.NewLabel("Нет данных")
	u.perfLabel.TextStyle = fyne.TextStyle{Monospace: true}
	note := widget.NewLabel("FPS — частота тиков рендер-цикла Fyne (общая на все окна). Серая линия — 60 FPS.")
	note.Importance = widget.LowImportance
	note.Wrapping = fyne.TextWrapWord
	return container.NewBorder(
		container.NewVBox(u.fpsText, note),
		u.perfLabel, nil, nil,
		u.spark,
	)
}

func (u *ui) renderStats(s *debugproto.Stats) {
	u.fpsText.Text = fmt.Sprintf("%.0f FPS", s.FPS)
	u.fpsText.Refresh()
	u.spark.Push(s.FPS)
	u.perfLabel.SetText(fmt.Sprintf(
		"кадр avg %.2f ms   max %.2f ms\n"+
			"горутин %d   heap %.1f / %.1f MB   GC %d\n"+
			"аптайм %s",
		s.FrameAvgMs, s.FrameMaxMs,
		s.Goroutines, s.HeapAllocMB, s.HeapSysMB, s.NumGC,
		(time.Duration(s.UptimeSec) * time.Second).String(),
	))
}

// ---------- Дерево UI ----------

func (u *ui) buildTree() fyne.CanvasObject {
	u.treeLabel = widget.NewLabel("Нажми «Обновить», чтобы получить дерево объектов")
	u.treeLabel.TextStyle = fyne.TextStyle{Monospace: true}
	refresh := widget.NewButton("Обновить", func() {
		u.send(debugproto.Command{Name: debugproto.CmdTreeGet})
	})
	note := widget.NewLabel("Показаны контейнеры и виджеты верхнего уровня: внутренности виджетов Fyne наружу не отдаёт.")
	note.Importance = widget.LowImportance
	note.Wrapping = fyne.TextWrapWord
	return container.NewBorder(container.NewBorder(nil, nil, nil, refresh, note), nil, nil, nil,
		container.NewScroll(u.treeLabel))
}

func (u *ui) renderTrees() {
	ids := make([]int, 0, len(u.trees))
	for id := range u.trees {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "Окно #%d\n", id)
		writeNode(&b, u.trees[id].Root, 1)
		b.WriteString("\n")
	}
	u.treeLabel.SetText(b.String())
}

func writeNode(b *strings.Builder, n *debugproto.Node, depth int) {
	if n == nil {
		return
	}
	hidden := ""
	if !n.Visible {
		hidden = "  [hidden]"
	}
	fmt.Fprintf(b, "%s%s  (%.0f,%.0f %.0f×%.0f)%s\n",
		strings.Repeat("  ", depth), n.Type, n.X, n.Y, n.W, n.H, hidden)
	for _, ch := range n.Children {
		writeNode(b, ch, depth+1)
	}
}

// ---------- Состояние VANTA ----------

func (u *ui) resetOnboarding() {
	path, _ := prefs.Path()
	dialog.ShowConfirm("Сбросить первый запуск?",
		"При следующем запуске VANTA откроет окно «Начало».\n"+path,
		func(ok bool) {
			if !ok {
				return
			}
			if err := prefs.ResetOnboarding(); err != nil {
				dialog.ShowError(err, u.win)
				return
			}
			dialog.ShowInformation("Готово", "Первый запуск сброшен.", u.win)
		}, u.win)
}

// ---------- Соединение ----------

func (u *ui) send(cmd debugproto.Command) {
	if err := u.cl.Send(cmd); err != nil && err != errNotConnected {
		u.status.SetText("Ошибка отправки: " + err.Error())
	}
}

func (u *ui) setState(ok bool, err error) {
	// Подключились — значит VANTA запущена, сбрасывать нельзя.
	// Без VANTA_DEBUG отладчик запущенную VANTA не видит: тогда сброс сработает,
	// но VANTA его не заметит до следующего старта.
	if ok {
		u.resetBtn.Disable()
	} else {
		u.resetBtn.Enable()
	}
	if ok {
		u.statusDot.FillColor = color.NRGBA{0x2E, 0xCC, 0x71, 0xFF}
		u.status.SetText("Подключено к " + u.cl.Addr())
	} else {
		u.statusDot.FillColor = color.NRGBA{0xC0, 0x30, 0x30, 0xFF}
		msg := "Ожидание VANTA на " + u.cl.Addr()
		if err != nil {
			msg += " (" + shortErr(err) + ")"
		}
		u.status.SetText(msg)
	}
	u.statusDot.Refresh()
}

func (u *ui) handle(m debugproto.Message) {
	switch m.Type {
	case debugproto.TypeHello:
		h := m.Hello
		// Новое подключение: мост заново пришлёт последние логи, старые убираем.
		u.logs = nil
		u.refilter()
		u.windows = map[int]debugproto.WindowInfo{}
		u.trees = map[int]*debugproto.Tree{}
		u.spark.Clear()
		u.status.SetText(fmt.Sprintf("Подключено к %s · %s pid %d · %s/%s · %s",
			u.cl.Addr(), h.App, h.PID, h.GOOS, h.GOARCH, h.GoVersion))
		if h.Version != debugproto.Version {
			u.status.SetText(u.status.Text + fmt.Sprintf(" · протокол v%d ≠ v%d", h.Version, debugproto.Version))
		}
		// Восстанавливаем оверлеи после переподключения/перезапуска VANTA.
		if u.boundsOn {
			u.send(debugproto.Command{Name: debugproto.CmdOverlayBounds, Bool: true})
		}
		if u.fpsOn {
			u.send(debugproto.Command{Name: debugproto.CmdOverlayFPS, Bool: true})
		}
		u.send(debugproto.Command{Name: debugproto.CmdTreeGet})
	case debugproto.TypeLog:
		if m.Log != nil {
			u.addLog(m)
		}
	case debugproto.TypeWindow:
		if m.Window != nil {
			u.windows[m.Window.ID] = *m.Window
			u.renderWindows()
		}
	case debugproto.TypeClosed:
		if m.Window != nil {
			delete(u.windows, m.Window.ID)
			delete(u.trees, m.Window.ID)
			u.renderWindows()
			u.renderTrees()
		}
	case debugproto.TypeStats:
		if m.Stats != nil {
			u.renderStats(m.Stats)
		}
	case debugproto.TypeTree:
		if m.Tree != nil {
			u.trees[m.Tree.WindowID] = m.Tree
			u.renderTrees()
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}
