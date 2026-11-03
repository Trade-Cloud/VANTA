package header

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Layer — слой плавающих панелей поверх контента окна.
//
// Почему не widget.PopUp: попап Fyne живёт в Canvas().Overlays(), а пока
// есть overlay, события мыши ищутся только в нём. Тогда при открытом
// dropdown не работает hover по соседним пунктам навигации. Наш слой лежит
// внутри контента (Stack поверх страницы), поэтому hover и клики видят и
// навигацию, и панель.
//
// Одновременно открыта одна панель. Modal — под панелью ловушка кликов на
// всё окно: клик мимо закрывает панель. Без Modal (hover-меню) клики мимо
// уходят в приложение, а закрытием управляет владелец.
type Layer struct {
	root *fyne.Container

	key     string
	opts    Opts
	anchor  fyne.CanvasObject
	panel   *shield
	catcher *catcher
	offset  float32
	anim    *fyne.Animation
}

type Align int

const (
	AlignStart  Align = iota // левый край панели по левому краю якоря
	AlignEnd                 // правый край по правому краю якоря
	AlignCenter              // по центру окна сверху (Command Palette)
)

type Opts struct {
	Modal bool
	Align Align
	// Width — ширина панели; 0 — по контенту. MatchAnchor — как у якоря.
	Width       float32
	MatchAnchor bool
	// Hover — курсор вошёл/вышел с фона панели (для hover-меню).
	Hover func(in bool)
	// Focus — что сфокусировать после открытия.
	Focus fyne.Focusable
	// Owns — что ещё в панели может получить фокус (поля ввода, списки).
	// При закрытии фокус с них снимается.
	Owns []fyne.Focusable
	// OnClose — панель закрылась (любым способом).
	OnClose func()
}

const (
	panelGap   = 4
	edgeMargin = 8
	animShift  = 6
	animTime   = 120 * time.Millisecond
)

func NewLayer() *Layer {
	l := &Layer{}
	l.root = container.New(l)
	return l
}

func (l *Layer) Object() fyne.CanvasObject { return l.root }

// Key — ключ открытой панели, "" — ничего не открыто.
func (l *Layer) Key() string { return l.key }

func (l *Layer) IsOpen(key string) bool { return l.key != "" && l.key == key }

func (l *Layer) Modal() bool { return l.key != "" && l.opts.Modal }

// Open показывает content под якорем. Уже открытая панель заменяется.
func (l *Layer) Open(key string, anchor, content fyne.CanvasObject, o Opts) {
	l.closeQuiet()
	l.key, l.opts, l.anchor = key, o, anchor
	l.panel = newShield(content, o.Hover)
	objs := []fyne.CanvasObject{}
	if o.Modal {
		l.catcher = newCatcher(l.Close)
		objs = append(objs, l.catcher)
	}
	l.root.Objects = append(objs, l.panel)

	// Плавное появление: панель съезжает на место за animTime.
	l.offset = -animShift
	l.anim = fyne.NewAnimation(animTime, func(f float32) {
		l.offset = -animShift * (1 - f)
		l.place()
	})
	l.anim.Curve = fyne.AnimationEaseOut
	l.root.Refresh()
	l.anim.Start()

	if o.Focus != nil {
		if c := fyne.CurrentApp().Driver().CanvasForObject(l.root); c != nil {
			c.Focus(o.Focus)
		}
	}
}

// Close закрывает открытую панель.
func (l *Layer) Close() {
	if l.key == "" {
		return
	}
	onClose := l.opts.OnClose
	l.closeQuiet()
	l.root.Refresh()
	if onClose != nil {
		onClose()
	}
}

func (l *Layer) closeQuiet() {
	if l.anim != nil {
		l.anim.Stop()
		l.anim = nil
	}
	if l.key != "" {
		// Фокус внутри закрытой панели оставлять нельзя: клавиши уйдут в никуда.
		if c := fyne.CurrentApp().Driver().CanvasForObject(l.root); c != nil && c.Focused() != nil {
			for _, f := range append([]fyne.Focusable{l.opts.Focus}, l.opts.Owns...) {
				if f != nil && f == c.Focused() {
					c.Unfocus()
					break
				}
			}
		}
	}
	l.key, l.anchor, l.panel, l.catcher = "", nil, nil, nil
	l.opts = Opts{}
	l.root.Objects = nil
}

// Relayout — контент панели поменял размер.
func (l *Layer) Relayout() { l.place() }

// Layout — fyne.Layout для root: ловушку на всё окно, панель — к якорю.
func (l *Layer) Layout(_ []fyne.CanvasObject, _ fyne.Size) { l.place() }

func (l *Layer) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (l *Layer) place() {
	if l.panel == nil {
		return
	}
	size := l.root.Size()
	if l.catcher != nil {
		l.catcher.Move(fyne.NewPos(0, 0))
		l.catcher.Resize(size)
	}

	d := fyne.CurrentApp().Driver()
	origin := d.AbsolutePositionForObject(l.root)
	var apos fyne.Position
	var asize fyne.Size
	if l.anchor != nil {
		apos = d.AbsolutePositionForObject(l.anchor).Subtract(origin)
		asize = l.anchor.Size()
	}

	min := l.panel.MinSize()
	w := min.Width
	if l.opts.MatchAnchor && asize.Width > w {
		w = asize.Width
	}
	if l.opts.Width > w {
		w = l.opts.Width
	}
	w = fyne.Min(w, size.Width-edgeMargin*2)

	var x, y float32
	switch l.opts.Align {
	case AlignEnd:
		x, y = apos.X+asize.Width-w, apos.Y+asize.Height+panelGap
	case AlignCenter:
		x, y = (size.Width-w)/2, fyne.Max(48, size.Height*0.12)
	default:
		x, y = apos.X, apos.Y+asize.Height+panelGap
	}
	x = fyne.Max(edgeMargin, fyne.Min(x, size.Width-w-edgeMargin))
	h := fyne.Min(min.Height, size.Height-y-edgeMargin)

	l.panel.Resize(fyne.NewSize(w, fyne.Max(h, 0)))
	l.panel.Move(fyne.NewPos(x, y+l.offset))
}

// ---------- фон панели ----------

// shield — подложка панели: тень, фон, рамка. Ловит клики и hover по
// пустым местам панели, чтобы они не проваливались в страницу под ней.
type shield struct {
	widget.BaseWidget
	content fyne.CanvasObject
	hover   func(bool)
}

func newShield(content fyne.CanvasObject, hover func(bool)) *shield {
	s := &shield{content: content, hover: hover}
	s.ExtendBaseWidget(s)
	return s
}

func (s *shield) Tapped(*fyne.PointEvent)          {}
func (s *shield) TappedSecondary(*fyne.PointEvent) {}
func (s *shield) Scrolled(*fyne.ScrollEvent)       {}
func (s *shield) MouseIn(*desktop.MouseEvent) {
	if s.hover != nil {
		s.hover(true)
	}
}
func (s *shield) MouseMoved(*desktop.MouseEvent) {}
func (s *shield) MouseOut() {
	if s.hover != nil {
		s.hover(false)
	}
}

func (s *shield) CreateRenderer() fyne.WidgetRenderer {
	shadow := canvas.NewRectangle(theme.Color(theme.ColorNameShadow))
	shadow.CornerRadius = theme.InputRadiusSize() * 2
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameMenuBackground))
	bg.CornerRadius = theme.InputRadiusSize() * 2
	bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	bg.StrokeWidth = 1
	return &shieldRenderer{s: s, shadow: shadow, bg: bg}
}

type shieldRenderer struct {
	s          *shield
	shadow, bg *canvas.Rectangle
}

const shieldPad = 6

func (r *shieldRenderer) Layout(size fyne.Size) {
	r.shadow.Move(fyne.NewPos(0, 3))
	r.shadow.Resize(size)
	r.bg.Resize(size)
	r.s.content.Move(fyne.NewPos(shieldPad, shieldPad))
	r.s.content.Resize(size.Subtract(fyne.NewSquareSize(shieldPad * 2)))
}

func (r *shieldRenderer) MinSize() fyne.Size {
	return r.s.content.MinSize().Add(fyne.NewSquareSize(shieldPad * 2))
}

func (r *shieldRenderer) Refresh() {
	r.bg.FillColor = theme.Color(theme.ColorNameMenuBackground)
	r.bg.StrokeColor = theme.Color(theme.ColorNameSeparator)
	r.shadow.FillColor = theme.Color(theme.ColorNameShadow)
	r.shadow.Refresh()
	r.bg.Refresh()
	r.s.content.Refresh()
}

func (r *shieldRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.shadow, r.bg, r.s.content}
}

func (r *shieldRenderer) Destroy() {}

// catcher — прозрачная ловушка кликов на всё окно под модальной панелью.
type catcher struct {
	widget.BaseWidget
	onTap func()
}

func newCatcher(onTap func()) *catcher {
	c := &catcher{onTap: onTap}
	c.ExtendBaseWidget(c)
	return c
}

func (c *catcher) Tapped(*fyne.PointEvent)          { c.onTap() }
func (c *catcher) TappedSecondary(*fyne.PointEvent) { c.onTap() }
func (c *catcher) Scrolled(*fyne.ScrollEvent)       {}

func (c *catcher) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
