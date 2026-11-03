package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

const sparkPoints = 120 // две минуты при одном замере в секунду

// sparkline — простой линейный график последних значений FPS.
type sparkline struct {
	widget.BaseWidget
	values []float64
	target float64 // ориентир (60 FPS), рисуется серой линией
}

func newSparkline(target float64) *sparkline {
	s := &sparkline{target: target}
	s.ExtendBaseWidget(s)
	return s
}

func (s *sparkline) Push(v float64) {
	s.values = append(s.values, v)
	if len(s.values) > sparkPoints {
		s.values = s.values[len(s.values)-sparkPoints:]
	}
	s.Refresh()
}

func (s *sparkline) Clear() {
	s.values = nil
	s.Refresh()
}

func (s *sparkline) CreateRenderer() fyne.WidgetRenderer {
	r := &sparkRenderer{
		s:      s,
		bg:     canvas.NewRectangle(color.NRGBA{0x18, 0x18, 0x1B, 0xFF}),
		target: canvas.NewLine(color.NRGBA{0x80, 0x80, 0x80, 0x80}),
	}
	r.rebuild()
	return r
}

type sparkRenderer struct {
	s      *sparkline
	bg     *canvas.Rectangle
	target *canvas.Line
	lines  []*canvas.Line
	objs   []fyne.CanvasObject
	size   fyne.Size
}

func (r *sparkRenderer) Layout(size fyne.Size) {
	r.size = size
	r.rebuild()
}

func (r *sparkRenderer) MinSize() fyne.Size { return fyne.NewSize(300, 140) }

func (r *sparkRenderer) Refresh() {
	r.rebuild()
	for _, o := range r.objs {
		o.Refresh()
	}
}

func (r *sparkRenderer) Objects() []fyne.CanvasObject { return r.objs }

func (r *sparkRenderer) Destroy() {}

func (r *sparkRenderer) rebuild() {
	w, h := r.size.Width, r.size.Height
	r.bg.Resize(r.size)

	scale := r.s.target * 1.25
	for _, v := range r.s.values {
		if v > scale {
			scale = v
		}
	}
	y := func(v float64) float32 { return h - float32(v/scale)*h }
	x := func(i int) float32 { return float32(i) / float32(sparkPoints-1) * w }

	ty := y(r.s.target)
	r.target.Position1 = fyne.NewPos(0, ty)
	r.target.Position2 = fyne.NewPos(w, ty)

	n := len(r.s.values) - 1
	for len(r.lines) < n {
		l := canvas.NewLine(color.NRGBA{0x39, 0xFF, 0x14, 0xFF})
		l.StrokeWidth = 2
		r.lines = append(r.lines, l)
	}
	// Прижимаем график к правому краю, чтобы новые точки появлялись справа.
	off := sparkPoints - len(r.s.values)
	r.objs = []fyne.CanvasObject{r.bg, r.target}
	for i := 0; i < n; i++ {
		l := r.lines[i]
		l.Position1 = fyne.NewPos(x(off+i), y(r.s.values[i]))
		l.Position2 = fyne.NewPos(x(off+i+1), y(r.s.values[i+1]))
		r.objs = append(r.objs, l)
	}
}
