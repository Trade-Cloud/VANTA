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
	r.c.TextStyle = fyne.TextStyle{Bold: r.t.Bold}
	r.c.Alignment = r.t.Align
	r.c.Color = theme.ColorForWidget(r.t.ColorName, r.t)
}

func (r *vTextRenderer) Layout(s fyne.Size)           { r.c.Resize(s) }
func (r *vTextRenderer) MinSize() fyne.Size           { r.sync(); return r.c.MinSize() }
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
	r.r.CornerRadius = v.Radius
	r.r.Refresh()
}
func (r *vRectRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.r} }
func (r *vRectRenderer) Destroy()                     {}
