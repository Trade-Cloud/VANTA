package debugbridge

import (
	"fmt"
	"image/color"
	"log/slog"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"github.com/Trade-Cloud/vanta/internal/debugproto"
)

const overlayInterval = 250 * time.Millisecond

// Цвета рамок по глубине вложенности.
var boundsPalette = []color.NRGBA{
	{0xFF, 0x4D, 0x00, 0xFF},
	{0x00, 0xA8, 0xFF, 0xFF},
	{0x2E, 0xCC, 0x71, 0xFF},
	{0xC0, 0x39, 0xFF, 0xFF},
	{0xF1, 0xC4, 0x0F, 0xFF},
}

// watched — окно, подключённое к мосту. Все поля трогаются только из UI-горутины.
type watched struct {
	id    int
	win   fyne.Window
	root  fyne.CanvasObject // контент приложения
	layer *fyne.Container   // слой оверлея поверх root
	stack *fyne.Container   // Stack(root, layer) — то, что реально лежит в окне

	bounds  bool
	showFPS bool
}

// Attach подключает окно к мосту: данные окна, дерево UI и оверлей.
// Вызывать из UI-горутины после w.SetContent и до Show. Закрытое окно
// мост убирает сам (через SetOnClosed — своё OnClosed у окна не ставить).
// Оверлей кладётся слоем внутрь контента, а не в Canvas().Overlays(),
// потому что верхний overlay забирает себе все клики.
func (b *Bridge) Attach(w fyne.Window) {
	if b == nil || w == nil {
		return
	}
	b.nextID++
	ww := &watched{id: b.nextID, win: w}
	ww.wrap(w.Content())
	b.windows = append(b.windows, ww)
	w.SetOnClosed(func() { b.detach(ww) })

	if b.fpsAnim == nil {
		b.fpsAnim = fyne.NewAnimation(time.Second, func(float32) { b.fps.tick() })
		b.fpsAnim.Curve = fyne.AnimationLinear
		b.fpsAnim.RepeatCount = fyne.AnimationRepeatForever
		b.fpsAnim.Start()
	}
	b.ready.Store(true)
	slog.Debug("окно подключено к debug-мосту", "id", ww.id, "title", w.Title())
}

// detach убирает закрытое окно и сообщает отладчику. UI-горутина.
func (b *Bridge) detach(ww *watched) {
	for i, w := range b.windows {
		if w == ww {
			b.windows = append(b.windows[:i], b.windows[i+1:]...)
			break
		}
	}
	b.ensureOverlayTicker()
	b.broadcast(debugproto.Message{
		Type:   debugproto.TypeClosed,
		Time:   time.Now(),
		Window: &debugproto.WindowInfo{ID: ww.id},
	})
	slog.Debug("окно отключено от debug-моста", "id", ww.id)
}

func (w *watched) wrap(content fyne.CanvasObject) {
	if content == nil {
		content = container.NewWithoutLayout()
	}
	w.root = content
	w.layer = container.NewWithoutLayout()
	w.stack = container.NewStack(content, w.layer)
	w.win.SetContent(w.stack)
}

// redraw перестраивает слой оверлея. Если приложение заменило контент окна
// через SetContent, заново оборачиваем новый контент.
func (w *watched) redraw(fps fpsSnap) {
	if w.win.Content() != w.stack {
		w.wrap(w.win.Content())
	}
	var objs []fyne.CanvasObject
	if w.bounds {
		objs = appendBounds(objs, w.root, fyne.NewPos(0, 0), 0)
	}
	if w.showFPS {
		objs = append(objs, fpsBadge(fps)...)
	}
	w.layer.Objects = objs
	w.layer.Refresh()
}

func appendBounds(dst []fyne.CanvasObject, obj fyne.CanvasObject, origin fyne.Position, depth int) []fyne.CanvasObject {
	if obj == nil || !obj.Visible() {
		return dst
	}
	pos := origin.Add(obj.Position())
	r := canvas.NewRectangle(color.Transparent)
	r.StrokeColor = boundsPalette[depth%len(boundsPalette)]
	r.StrokeWidth = 1
	r.Move(pos)
	r.Resize(obj.Size())
	dst = append(dst, r)
	if c, ok := obj.(*fyne.Container); ok {
		for _, ch := range c.Objects {
			dst = appendBounds(dst, ch, pos, depth+1)
		}
	}
	return dst
}

func fpsBadge(s fpsSnap) []fyne.CanvasObject {
	txt := canvas.NewText(fmt.Sprintf("FPS %.0f  avg %.1fms  max %.1fms", s.fps, s.avgMs, s.maxMs), color.NRGBA{0x39, 0xFF, 0x14, 0xFF})
	txt.TextSize = 12
	txt.TextStyle = fyne.TextStyle{Monospace: true}
	size := txt.MinSize()
	pad := float32(4)
	bg := canvas.NewRectangle(color.NRGBA{0, 0, 0, 0xB0})
	bg.CornerRadius = 4
	bg.Move(fyne.NewPos(pad, pad))
	bg.Resize(size.Add(fyne.NewSize(pad*2, pad*2)))
	txt.Move(fyne.NewPos(pad*2, pad*2))
	txt.Resize(size)
	return []fyne.CanvasObject{bg, txt}
}

func (w *watched) info() debugproto.WindowInfo {
	c := w.win.Canvas()
	size := c.Size()
	minSize := w.root.MinSize()
	focused := ""
	if f := c.Focused(); f != nil {
		focused = fmt.Sprintf("%T", f)
	}
	variant := "light"
	if fyne.CurrentApp().Settings().ThemeVariant() == theme.VariantDark {
		variant = "dark"
	}
	return debugproto.WindowInfo{
		ID:         w.id,
		Title:      w.win.Title(),
		Width:      size.Width,
		Height:     size.Height,
		Scale:      c.Scale(),
		FullScreen: w.win.FullScreen(),
		Padded:     w.win.Padded(),
		MinWidth:   minSize.Width,
		MinHeight:  minSize.Height,
		Focused:    focused,
		Theme:      variant,
		Objects:    countObjects(w.root),
		OverlayOn:  w.bounds,
		OverlayFPS: w.showFPS,
	}
}

// Внутрь виджетов не заходим: их рендереры не доступны через публичный API Fyne.
func countObjects(obj fyne.CanvasObject) int {
	if obj == nil {
		return 0
	}
	n := 1
	if c, ok := obj.(*fyne.Container); ok {
		for _, ch := range c.Objects {
			n += countObjects(ch)
		}
	}
	return n
}

func buildNode(obj fyne.CanvasObject) *debugproto.Node {
	if obj == nil {
		return nil
	}
	pos, size := obj.Position(), obj.Size()
	n := &debugproto.Node{
		Type:    fmt.Sprintf("%T", obj),
		X:       pos.X,
		Y:       pos.Y,
		W:       size.Width,
		H:       size.Height,
		Visible: obj.Visible(),
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, ch := range c.Objects {
			n.Children = append(n.Children, buildNode(ch))
		}
	}
	return n
}

// ensureOverlayTicker запускает/останавливает периодическую перерисовку оверлея.
// Вызывается из UI-горутины.
func (b *Bridge) ensureOverlayTicker() {
	on := false
	for _, w := range b.windows {
		on = on || w.bounds || w.showFPS
	}
	b.overlayOn.Store(on)
	if on && b.overlayLoopOn.CompareAndSwap(false, true) {
		go b.overlayLoop()
	}
}

func (b *Bridge) overlayLoop() {
	t := time.NewTicker(overlayInterval)
	defer t.Stop()
	for range t.C {
		if !b.overlayOn.Load() {
			b.overlayLoopOn.Store(false)
			// Оверлей могли включить между проверкой и Store — тогда продолжаем.
			if !b.overlayOn.Load() || !b.overlayLoopOn.CompareAndSwap(false, true) {
				return
			}
		}
		snap := b.fps.current()
		b.do(func() {
			for _, w := range b.windows {
				if w.bounds || w.showFPS {
					w.redraw(snap)
				}
			}
		})
	}
}

// fpsCounter считает тики рендер-цикла Fyne. Тикает через бесконечную
// fyne.Animation: драйвер вызывает её один раз за итерацию цикла перед отрисовкой.
type fpsCounter struct {
	mu     sync.Mutex
	last   time.Time
	since  time.Time
	frames int
	sum    time.Duration
	max    time.Duration
	cur    fpsSnap
}

type fpsSnap struct {
	fps, avgMs, maxMs float64
}

func (f *fpsCounter) tick() {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.last.IsZero() {
		d := now.Sub(f.last)
		f.frames++
		f.sum += d
		if d > f.max {
			f.max = d
		}
	}
	f.last = now
}

// snapshot закрывает текущий интервал и начинает новый.
func (f *fpsCounter) snapshot() fpsSnap {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	elapsed := statsInterval
	if !f.since.IsZero() {
		elapsed = now.Sub(f.since)
	}
	s := fpsSnap{}
	if f.frames > 0 && elapsed > 0 {
		s.fps = float64(f.frames) / elapsed.Seconds()
		s.avgMs = float64(f.sum) / float64(f.frames) / float64(time.Millisecond)
		s.maxMs = float64(f.max) / float64(time.Millisecond)
	}
	f.cur = s
	f.frames, f.sum, f.max = 0, 0, 0
	f.since = now
	return s
}

func (f *fpsCounter) current() fpsSnap {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}
