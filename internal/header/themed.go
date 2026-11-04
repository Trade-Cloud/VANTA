package header

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// vText — текст, у которого цвет задан ИМЕНЕМ цвета темы и берётся из
// темы при каждой отрисовке. canvas.Text запоминает готовый цвет при
// создании и после смены темы/варианта остаётся старым (чёрный на тёмном).
type vText struct {
	widget.BaseWidget
	Text      string
	TextSize  float32
	ColorName fyne.ThemeColorName
	Bold      bool
	Align     fyne.TextAlign
	// Mono — моноширинный шрифт (отпечатки, ключи).
	Mono bool
	// Truncate — не шире отведённого места: хвост режется «…».
	Truncate bool
}

func newVText(s string, size float32, c fyne.ThemeColorName, bold bool) *vText {
	t := &vText{Text: s, TextSize: size, ColorName: c, Bold: bold}
	t.ExtendBaseWidget(t)
	return t
}

func (t *vText) CreateRenderer() fyne.WidgetRenderer {
	r := &vTextRenderer{t: t, c: canvas.NewText("", color.Transparent)}
	r.Refresh()
	return r
}

type vTextRenderer struct {
	t *vText
	c *canvas.Text
}

func (r *vTextRenderer) sync() {
	r.c.Text = r.t.Text
	r.c.TextSize = r.t.TextSize
	r.c.TextStyle = fyne.TextStyle{Bold: r.t.Bold, Monospace: r.t.Mono}
	r.c.Alignment = r.t.Align
	r.c.Color = theme.ColorForWidget(r.t.ColorName, r.t)
	if r.t.Truncate {
		r.c.Text = fitText(r.t.Text, r.t.Size().Width, r.c.TextSize, r.c.TextStyle)
	}
}

// fitText — s, обрезанная с «…», чтобы влезть в w.
func fitText(s string, w, size float32, st fyne.TextStyle) string {
	if w <= 0 || fyne.MeasureText(s, size, st).Width <= w {
		return s
	}
	rs := []rune(s)
	lo, hi := 0, len(rs)
	for lo < hi { // самый длинный префикс, который влезает вместе с «…»
		mid := (lo + hi + 1) / 2
		if fyne.MeasureText(string(rs[:mid])+"…", size, st).Width <= w {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(rs[:lo]) + "…"
}

func (r *vTextRenderer) Layout(s fyne.Size) {
	r.c.Resize(s)
	if r.t.Truncate {
		r.sync()
		r.c.Refresh()
	}
}

func (r *vTextRenderer) MinSize() fyne.Size {
	r.sync()
	m := r.c.MinSize()
	if r.t.Truncate {
		m = fyne.NewSize(fyne.MeasureText("…", r.c.TextSize, r.c.TextStyle).Width, m.Height)
	}
	return m
}

func (r *vTextRenderer) Refresh()                     { r.sync(); r.c.Refresh() }
func (r *vTextRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.c} }
func (r *vTextRenderer) Destroy()                     {}

// vRect — прямоугольник с цветами по именам темы. Alpha > 0 — заливка
// цветом FillName с этой прозрачностью (подложки предупреждений).
type vRect struct {
	widget.BaseWidget
	FillName    fyne.ThemeColorName // "" — прозрачная заливка
	StrokeName  fyne.ThemeColorName // "" — без рамки
	StrokeWidth float32
	Radius      float32
	Alpha       uint8
}

func newVRect(fill fyne.ThemeColorName) *vRect {
	r := &vRect{FillName: fill}
	r.ExtendBaseWidget(r)
	return r
}

func (v *vRect) CreateRenderer() fyne.WidgetRenderer {
	r := &vRectRenderer{v: v, r: canvas.NewRectangle(color.Transparent)}
	r.Refresh()
	return r
}

type vRectRenderer struct {
	v *vRect
	r *canvas.Rectangle
}

func (r *vRectRenderer) Layout(s fyne.Size) { r.r.Resize(s) }
func (r *vRectRenderer) MinSize() fyne.Size { return fyne.Size{} }
func (r *vRectRenderer) Refresh() {
	v := r.v
	r.r.FillColor = color.Transparent
	if v.FillName != "" {
		c := theme.ColorForWidget(v.FillName, v)
		if v.Alpha > 0 {
			cr, cg, cb, _ := c.RGBA()
			c = color.NRGBA{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), v.Alpha}
		}
		r.r.FillColor = c
	}
	r.r.StrokeWidth = 0
	if v.StrokeName != "" {
		r.r.StrokeColor = theme.ColorForWidget(v.StrokeName, v)
		r.r.StrokeWidth = v.StrokeWidth
	}
	// Радиус не больше радиуса из темы VANTA (сейчас 0 — строгий стиль):
	// так один параметр темы управляет углами всех наших плашек и рамок,
	// даже если вызывающий код передал своё скругление.
	r.r.CornerRadius = min(v.Radius, theme.InputRadiusSize())
	r.r.Refresh()
}
func (r *vRectRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.r} }
func (r *vRectRenderer) Destroy()                     {}

// ThemedRect — прямоугольник цвета темы name для других пакетов (фон
// шапок окон). Цвет обновляется вместе с темой.
func ThemedRect(name fyne.ThemeColorName) fyne.CanvasObject { return newVRect(name) }

// Text и Rect — те же vText/vRect для других пакетов (страницы панели):
// цвет задаётся именем и берётся из темы при каждой отрисовке.
type (
	Text = vText
	Rect = vRect
)

// NewText — текст цвета темы c.
func NewText(s string, size float32, c fyne.ThemeColorName, bold bool) *Text {
	return newVText(s, size, c, bold)
}

// NewRect — прямоугольник: заливка fill, рамка stroke толщиной width, скругление radius.
// Пустое имя — без заливки/рамки.
func NewRect(fill, stroke fyne.ThemeColorName, width, radius float32) *Rect {
	r := newVRect(fill)
	r.StrokeName, r.StrokeWidth, r.Radius = stroke, width, radius
	return r
}
