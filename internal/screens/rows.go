package screens

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/header"
)

// Кирпичики страниц Processes / Network / Storage в стиле Resources:
// плашки-сводки, карточки-секции, таблицы с разделителями. Всё создаётся
// один раз, при обновлении меняются только значения — без мигания.

// setT — новый текст; перерисовка только если он поменялся.
func setT(t *header.Text, s string) {
	if t.Text != s {
		t.Text = s
		t.Refresh()
	}
}

// setTC — новый текст и цвет темы.
func setTC(t *header.Text, s string, c fyne.ThemeColorName) {
	if t.Text != s || t.ColorName != c {
		t.Text, t.ColorName = s, c
		t.Refresh()
	}
}

// right — текст, прижатый вправо (числа в таблицах).
func right(t *header.Text) *header.Text {
	t.Align = fyne.TextAlignTrailing
	return t
}

// monoCell — моноширинное число в ячейке таблицы.
func monoCell() *header.Text { return truncated(mono("", theme.TextSize())) }

// ---------- плашка-сводка ----------

// statTile — «иконка | ПОДПИСЬ / крупное значение / пояснение».
type statTile struct {
	obj      fyne.CanvasObject
	val, sub *header.Text
}

func newStatTile(res fyne.Resource, c fyne.ThemeColorName, label string) *statTile {
	t := &statTile{
		val: txt("—", theme.TextSize()+6, theme.ColorNameForeground, true),
		sub: truncated(muted("")),
	}
	t.val.Mono = true
	row := hstack(sp3, iconTile(res, c, 36), vstack(2, capsLabel(label), truncated(t.val), t.sub))
	row.Layout = &fillLast{gap: sp3}
	t.obj = card(padXY(sp3, sp3, row))
	return t
}

func (t *statTile) set(val, sub string) {
	setT(t.val, val)
	setT(t.sub, sub)
}

func tiles(ts ...*statTile) fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, len(ts))
	for i, t := range ts {
		objs[i] = t.obj
	}
	return container.NewGridWithColumns(len(ts), objs...)
}

// ---------- карточка-секция ----------

// section — карточка с заголовком (и подписью/инструментами справа),
// линией под ним и содержимым.
func section(name string, sub *header.Text, tools fyne.CanvasObject, body ...fyne.CanvasObject) fyne.CanvasObject {
	left := fyne.CanvasObject(txt(name, theme.TextSize()+1, theme.ColorNameForeground, true))
	if sub != nil {
		left = hstack(sp2, left, sub)
	}
	top := left
	if tools != nil {
		top = container.NewBorder(nil, nil, left, tools)
	}
	items := append([]fyne.CanvasObject{pad(sp4, sp3, sp4, sp3, top), hline()}, body...)
	return card(vstack(0, items...))
}

// tableHead — строка подписей колонок с теми же отступами, что у строк.
func tableHead(widths []float32, cells ...fyne.CanvasObject) fyne.CanvasObject {
	return vstack(0, pad(sp4, sp2, sp4, sp2, cols(widths, sp3, cells...)), hline())
}

// searchEntry — поле поиска с лупой, как на странице SSH.
func searchEntry(hint string, on func(string)) *widget.Entry {
	e := widget.NewEntry()
	e.PlaceHolder = hint
	e.ActionItem = pad(sp2, 0, sp2, 0, square(16, widget.NewIcon(theme.SearchIcon())))
	e.OnChanged = on
	return e
}

// ---------- строки таблицы ----------

// rowSet — строки таблицы, которые переиспользуются по позиции: при
// обновлении меняются только тексты, новые строки создаются лишь когда
// их стало больше. Пустой список — строка-заглушка.
type rowSet[R any] struct {
	box   *fyne.Container
	mk    func() (R, fyne.CanvasObject)
	rows  []R
	objs  []fyne.CanvasObject
	shown int
	empty *header.Text
	isEmp bool
}

func newRowSet[R any](mk func() (R, fyne.CanvasObject)) *rowSet[R] {
	return &rowSet[R]{box: vstack(0), mk: mk, shown: -1, empty: muted("")}
}

// resize — ровно n строк; emptyText показывается при n == 0.
func (s *rowSet[R]) resize(n int, emptyText string) []R {
	if n == 0 {
		setT(s.empty, emptyText)
		if !s.isEmp {
			s.isEmp, s.shown = true, 0
			s.box.Objects = []fyne.CanvasObject{pad(sp4, sp5, sp4, sp5, container.NewCenter(s.empty))}
			s.box.Refresh()
		}
		return nil
	}
	for len(s.rows) < n {
		r, o := s.mk()
		s.rows = append(s.rows, r)
		s.objs = append(s.objs, o)
	}
	if n != s.shown || s.isEmp {
		s.isEmp, s.shown = false, n
		objs := make([]fyne.CanvasObject, 0, 2*n)
		for i := 0; i < n; i++ {
			if i > 0 {
				objs = append(objs, hline())
			}
			objs = append(objs, s.objs[i])
		}
		s.box.Objects = objs
		s.box.Refresh()
	}
	return s.rows[:n]
}

// rowPress — кликабельная строка с подсветкой при наведении и выборе.
func rowPress(content fyne.CanvasObject, tap func()) *header.Pressable {
	p := header.NewPressable(content, tap)
	p.Pad = fyne.NewSize(sp4, sp2)
	return p
}

// rowPlain — строка без клика, с теми же отступами.
func rowPlain(content fyne.CanvasObject) fyne.CanvasObject {
	return pad(sp4, sp2, sp4, sp2, content)
}

// ---------- кликабельная подпись (сортировка) ----------

type tapArea struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
}

func newTapArea(content fyne.CanvasObject, fn func()) *tapArea {
	t := &tapArea{content: content, onTap: fn}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tapArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

func (t *tapArea) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *tapArea) Cursor() desktop.Cursor { return desktop.PointerCursor }

// ---------- полоска заполнения ----------

// meter — тонкая полоска 0…100 % цвета темы.
type meter struct {
	widget.BaseWidget
	value float64
	color fyne.ThemeColorName
}

func newMeter() *meter {
	m := &meter{color: theme.ColorNamePrimary}
	m.ExtendBaseWidget(m)
	return m
}

func (m *meter) set(v float64, c fyne.ThemeColorName) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	if v == m.value && c == m.color {
		return
	}
	m.value, m.color = v, c
	m.Refresh()
}

func (m *meter) CreateRenderer() fyne.WidgetRenderer {
	track := header.NewRect(theme.ColorNameSeparator, "", 0, 3)
	fill := header.NewRect(m.color, "", 0, 3)
	return &meterRenderer{m: m, track: track, fill: fill}
}

type meterRenderer struct {
	m           *meter
	track, fill *header.Rect
}

const meterH float32 = 6

func (r *meterRenderer) Layout(s fyne.Size) {
	y := (s.Height - meterH) / 2
	r.track.Move(fyne.NewPos(0, y))
	r.track.Resize(fyne.NewSize(s.Width, meterH))
	w := s.Width * float32(r.m.value) / 100
	if r.m.value > 0 && w < meterH {
		w = meterH
	}
	r.fill.Move(fyne.NewPos(0, y))
	r.fill.Resize(fyne.NewSize(w, meterH))
}

func (r *meterRenderer) MinSize() fyne.Size { return fyne.NewSize(40, meterH) }

func (r *meterRenderer) Refresh() {
	r.fill.FillName = r.m.color
	r.track.Refresh()
	r.fill.Refresh()
	r.Layout(r.m.Size())
}

func (r *meterRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill}
}

func (r *meterRenderer) Destroy() {}

// usageColor — цвет заполненности: обычный, «внимание», «плохо».
func usageColor(p float64) fyne.ThemeColorName {
	switch {
	case p >= 90:
		return theme.ColorNameError
	case p >= 75:
		return theme.ColorNameWarning
	}
	return theme.ColorNamePrimary
}

// loadColor — цвет числа нагрузки (CPU %): обычный текст до 50 %.
func loadColor(p float64) fyne.ThemeColorName {
	switch {
	case p >= 90:
		return theme.ColorNameError
	case p >= 50:
		return theme.ColorNameWarning
	}
	return theme.ColorNameForeground
}

// ---------- плашка-метка, которую можно менять ----------

type vchip struct {
	obj   fyne.CanvasObject
	frame *header.Rect
	t     *header.Text
}

func newVChip() *vchip {
	c := &vchip{
		frame: header.NewRect("", "", 1, radiusChip),
		t:     txt("", theme.CaptionTextSize(), theme.ColorNameForeground, true),
	}
	c.frame.Alpha = 22
	c.obj = container.NewHBox(container.NewStack(c.frame, pad(sp2-1, 2, sp2-1, 2, c.t)))
	return c
}

func (c *vchip) set(s string, col fyne.ThemeColorName) {
	if c.t.Text == s && c.t.ColorName == col {
		return
	}
	c.frame.FillName, c.frame.StrokeName = col, col
	c.frame.Refresh()
	c.t.Text, c.t.ColorName = s, col
	c.t.Refresh()
}

// ---------- иконки ----------

var (
	icChip  = svg("memory.svg", "M15 9H9v6h6V9zm-2 4h-2v-2h2v2zm8-2V9h-2V7c0-1.1-.9-2-2-2h-2V3h-2v2h-2V3H9v2H7c-1.1 0-2 .9-2 2v2H3v2h2v2H3v2h2v2c0 1.1.9 2 2 2h2v2h2v-2h2v2h2v-2h2c1.1 0 2-.9 2-2v-2h2v-2h-2v-2h2zm-4 6H7V7h10v10z")
	icLan   = svg("lan.svg", "M13 22h8v-7h-3v-4h-5V9h3V2H8v7h3v2H6v4H3v7h8v-7H8v-2h8v2h-3z")
	icSwap  = svg("swap.svg", "M6.99 11L3 15l3.99 4v-3H14v-2H6.99v-3zM21 9l-3.99-4v3H10v2h7.01v3L21 9z")
	icDisk  = svg("disk.svg", "M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 14.5c-2.49 0-4.5-2.01-4.5-4.5S9.51 7.5 12 7.5s4.5 2.01 4.5 4.5-2.01 4.5-4.5 4.5zm0-5.5c-.55 0-1 .45-1 1s.45 1 1 1 1-.45 1-1-.45-1-1-1z")
	icPie   = svg("pie.svg", "M11 2v20c-5.07-.5-9-4.79-9-10s3.93-9.5 9-10zm2.03 0v8.99H22c-.47-4.74-4.24-8.52-8.97-8.99zm0 11.01V22c4.74-.47 8.5-4.25 8.97-8.99h-8.97z")
	icRoute = svg("route.svg", "M19 15.18V7c0-2.21-1.79-4-4-4s-4 1.79-4 4v10c0 1.1-.9 2-2 2s-2-.9-2-2V8.82C8.16 8.4 9 7.3 9 6c0-1.66-1.34-3-3-3S3 4.34 3 6c0 1.3.84 2.4 2 2.82V17c0 2.21 1.79 4 4 4s4-1.79 4-4V7c0-1.1.9-2 2-2s2 .9 2 2v8.18c-1.16.41-2 1.51-2 2.82 0 1.66 1.34 3 3 3s3-1.34 3-3c0-1.3-.84-2.4-2-2.82z")
)
