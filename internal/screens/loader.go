package screens

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// loadLine — индикатор загрузки страницы: тонкая линия, по которой
// бежит светящийся отрезок цвета Primary (края плавно гаснут). Высота
// постоянная, в покое линия прозрачная — содержимое под ней не прыгает,
// как было с ProgressBarInfinite, который появлялся и пропадал.
// Цвета берутся из темы при каждой отрисовке.
type loadLine struct {
	widget.BaseWidget
	running bool
	pos     float32 // 0…1 — где сейчас отрезок
	anim    *fyne.Animation
}

const (
	loadLineH   float32 = 2
	loadLineSeg float32 = 0.32 // длина отрезка от ширины
	loadLineDur         = 1100 * time.Millisecond
)

func newLoadLine() *loadLine {
	l := &loadLine{}
	l.ExtendBaseWidget(l)
	return l
}

// Start — показать бегущий отрезок (повторный вызов ничего не делает).
func (l *loadLine) Start() {
	if l.running {
		return
	}
	l.running = true
	l.anim = fyne.NewAnimation(loadLineDur, func(f float32) {
		l.pos = f
		l.Refresh()
	})
	l.anim.Curve = fyne.AnimationEaseInOut
	l.anim.RepeatCount = fyne.AnimationRepeatForever
	l.anim.Start()
	l.Refresh()
}

// Stop — погасить линию; место под ней остаётся.
func (l *loadLine) Stop() {
	if !l.running {
		return
	}
	l.running = false
	if l.anim != nil {
		l.anim.Stop()
		l.anim = nil
	}
	l.Refresh()
}

func (l *loadLine) CreateRenderer() fyne.WidgetRenderer {
	r := &loadLineRenderer{
		l:     l,
		track: canvas.NewRectangle(color.Transparent),
		head:  canvas.NewHorizontalGradient(color.Transparent, color.Transparent),
		tail:  canvas.NewHorizontalGradient(color.Transparent, color.Transparent),
	}
	r.Refresh()
	return r
}

type loadLineRenderer struct {
	l          *loadLine
	track      *canvas.Rectangle
	head, tail *canvas.LinearGradient // левая половина отрезка разгорается, правая гаснет
}

func (r *loadLineRenderer) Layout(s fyne.Size) {
	y := (s.Height - loadLineH) / 2
	r.track.Move(fyne.NewPos(0, y))
	r.track.Resize(fyne.NewSize(s.Width, loadLineH))
	seg := s.Width * loadLineSeg
	x := -seg + r.l.pos*(s.Width+seg)
	// Не выходим за края страницы: отрезок обрезается у границ.
	left, mid, rightX := x, x+seg/2, x+seg
	clip := func(v float32) float32 { return fyne.Max(0, fyne.Min(s.Width, v)) }
	r.head.Move(fyne.NewPos(clip(left), y))
	r.head.Resize(fyne.NewSize(clip(mid)-clip(left), loadLineH))
	r.tail.Move(fyne.NewPos(clip(mid), y))
	r.tail.Resize(fyne.NewSize(clip(rightX)-clip(mid), loadLineH))
}

func (r *loadLineRenderer) MinSize() fyne.Size { return fyne.NewSize(0, loadLineH) }

func (r *loadLineRenderer) Refresh() {
	l := r.l
	on := l.running
	r.head.Hidden, r.tail.Hidden = !on, !on
	r.track.FillColor = color.Transparent
	if on {
		c := theme.ColorForWidget(theme.ColorNamePrimary, l)
		cr, cg, cb, _ := c.RGBA()
		solid := color.NRGBA{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), 255}
		clear := color.NRGBA{solid.R, solid.G, solid.B, 0}
		r.head.StartColor, r.head.EndColor = clear, solid
		r.tail.StartColor, r.tail.EndColor = solid, clear
		r.track.FillColor = color.NRGBA{solid.R, solid.G, solid.B, 40}
	}
	r.Layout(l.Size())
	r.track.Refresh()
	r.head.Refresh()
	r.tail.Refresh()
}

func (r *loadLineRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.head, r.tail}
}

func (r *loadLineRenderer) Destroy() {
	if r.l.anim != nil {
		r.l.anim.Stop()
		r.l.anim, r.l.running = nil, false
	}
}
