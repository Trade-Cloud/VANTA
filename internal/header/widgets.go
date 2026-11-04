package header

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/core"
)

// ---------- Pressable: кликабельная область с подсветкой при наведении ----------

// Pressable — содержимое с фоном при hover/выделении и OnTap.
// В отличие от widget.Button, внутрь можно положить что угодно (строку
// меню, триггер сервера), а OnHover нужен для hover-dropdown.
// Не получает фокус по Tab; для этого есть FocusPressable.
type Pressable struct {
	widget.BaseWidget

	OnTap   func()
	OnHover func(in bool)
	// Pad — внутренние отступы; ноль — по умолчанию.
	Pad fyne.Size
	// Disabled — не реагирует на клик, контент приглушать снаружи.
	Disabled bool

	content  fyne.CanvasObject
	selected bool
	hovered  bool
	focused  bool
}

func NewPressable(content fyne.CanvasObject, onTap func()) *Pressable {
	p := &Pressable{content: content, OnTap: onTap}
	p.ExtendBaseWidget(p)
	return p
}

func (p *Pressable) SetContent(c fyne.CanvasObject) {
	p.content = c
	p.Refresh()
}

// SetSelected — подсветка «активен/выбран клавиатурой».
func (p *Pressable) SetSelected(on bool) {
	if p.selected != on {
		p.selected = on
		p.Refresh()
	}
}

func (p *Pressable) Tapped(*fyne.PointEvent) {
	if !p.Disabled && p.OnTap != nil {
		p.OnTap()
	}
}

func (p *Pressable) MouseIn(*desktop.MouseEvent) {
	p.hovered = true
	p.Refresh()
	if p.OnHover != nil {
		p.OnHover(true)
	}
}

func (p *Pressable) MouseMoved(*desktop.MouseEvent) {}

func (p *Pressable) MouseOut() {
	p.hovered = false
	p.Refresh()
	if p.OnHover != nil {
		p.OnHover(false)
	}
}

func (p *Pressable) Cursor() desktop.Cursor {
	if p.Disabled {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (p *Pressable) pad() fyne.Size {
	if p.Pad.IsZero() {
		return fyne.NewSize(theme.Padding()*2, theme.Padding())
	}
	return p.Pad
}

func (p *Pressable) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = theme.InputRadiusSize()
	return &pressRenderer{p: p, bg: bg}
}

type pressRenderer struct {
	p  *Pressable
	bg *canvas.Rectangle
}

func (r *pressRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	if r.p.content != nil {
		pad := r.p.pad()
		r.p.content.Move(fyne.NewPos(pad.Width, pad.Height))
		r.p.content.Resize(size.Subtract(fyne.NewSize(pad.Width*2, pad.Height*2)))
	}
}

func (r *pressRenderer) MinSize() fyne.Size {
	pad := r.p.pad()
	min := fyne.NewSize(pad.Width*2, pad.Height*2)
	if r.p.content != nil {
		min = min.Add(r.p.content.MinSize())
	}
	return min
}

func (r *pressRenderer) Refresh() {
	switch {
	case r.p.selected:
		r.bg.FillColor = theme.Color(theme.ColorNameSelection)
	case r.p.hovered && !r.p.Disabled:
		r.bg.FillColor = theme.Color(theme.ColorNameHover)
	default:
		r.bg.FillColor = color.Transparent
	}
	if r.p.focused {
		r.bg.StrokeColor = theme.Color(theme.ColorNameFocus)
		r.bg.StrokeWidth = 2
	} else {
		r.bg.StrokeWidth = 0
	}
	r.bg.Refresh()
	r.Layout(r.p.Size())
	if r.p.content != nil {
		r.p.content.Refresh()
	}
}

func (r *pressRenderer) Objects() []fyne.CanvasObject {
	if r.p.content == nil {
		return []fyne.CanvasObject{r.bg}
	}
	return []fyne.CanvasObject{r.bg, r.p.content}
}

func (r *pressRenderer) Destroy() {}

// FocusPressable — Pressable, до которого можно дойти Tab'ом.
// Enter/Space — клик; Escape и прочее — в OnKey.
type FocusPressable struct {
	Pressable
	// OnKey получает клавиши первым; true — обработано.
	OnKey func(*fyne.KeyEvent) bool
}

func NewFocusPressable(content fyne.CanvasObject, onTap func()) *FocusPressable {
	f := &FocusPressable{}
	f.content, f.OnTap = content, onTap
	f.ExtendBaseWidget(f)
	return f
}

func (f *FocusPressable) FocusGained()   { f.focused = true; f.Refresh() }
func (f *FocusPressable) FocusLost()     { f.focused = false; f.Refresh() }
func (f *FocusPressable) TypedRune(rune) {}

func (f *FocusPressable) TypedKey(e *fyne.KeyEvent) {
	if f.OnKey != nil && f.OnKey(e) {
		return
	}
	switch e.Name {
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		f.Tapped(nil)
	}
}

// ---------- мелкие кирпичики ----------

// fixed — объект ровно заданного размера, по центру доступного места.
func fixed(size fyne.Size, o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewCenter(container.NewGridWrap(size, o))
}

func text(s string, size float32, c fyne.ThemeColorName, bold bool) *vText {
	return newVText(s, size, c, bold)
}

func small(s string) *vText {
	return text(s, theme.CaptionTextSize(), theme.ColorNamePlaceHolder, false)
}

// line — строка, которая обрезается многоточием, а не распирает панель.
func line(s string, caption bool) *widget.Label {
	l := widget.NewLabel(s)
	l.Truncation = fyne.TextTruncateEllipsis
	if caption {
		// Не LowImportance: Fyne красит её цветом Disabled, в тёмной теме
		// он почти чёрный. Мельче — и достаточно.
		l.SizeName = theme.SizeNameCaptionText
	}
	return l
}

func caps(s string) *vText {
	t := text(s, theme.CaptionTextSize()-1, theme.ColorNamePlaceHolder, true)
	return t
}

func icon(res fyne.Resource, size float32) fyne.CanvasObject {
	return fixed(fyne.NewSquareSize(size), widget.NewIcon(res))
}

func vsep() fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(1, 20), newVRect(theme.ColorNameSeparator))
}

func hsep() fyne.CanvasObject {
	return widget.NewSeparator()
}

// skeleton — серый прямоугольник вместо ещё не загруженных данных.
func skeleton(w, h float32) fyne.CanvasObject {
	r := newVRect(theme.ColorNameDisabledButton)
	r.Radius = 0
	return fixed(fyne.NewSize(w, h), r)
}

// badge — счётчик в правом верхнем углу объекта.
func badge(base fyne.CanvasObject, n int) fyne.CanvasObject {
	if n <= 0 {
		return base
	}
	s := fmt.Sprint(n)
	if n > 99 {
		s = "99+"
	}
	t := text(s, 9, theme.ColorNameForegroundOnError, true)
	t.Align = fyne.TextAlignCenter
	bg := newVRect(theme.ColorNameError)
	bg.Radius = 0
	b := container.NewStack(bg, container.NewCenter(t))
	return container.New(&badgeLayout{}, base, b)
}

type badgeLayout struct{}

func (badgeLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(size)
	w := fyne.Max(14, objs[1].MinSize().Width+6)
	objs[1].Resize(fyne.NewSize(w, 14))
	objs[1].Move(fyne.NewPos(size.Width-w+4, -4))
}

func (badgeLayout) MinSize(objs []fyne.CanvasObject) fyne.Size { return objs[0].MinSize() }

// ---------- статусы ----------

func statusColorName(s core.Status) fyne.ThemeColorName {
	switch s {
	case core.StatusOnline:
		return theme.ColorNameSuccess
	case core.StatusDegraded:
		return theme.ColorNameWarning
	case core.StatusOffline, core.StatusNoResponse, core.StatusFailed:
		return theme.ColorNameError
	}
	return theme.ColorNamePlaceHolder
}

// statusText — «Online», «Нет ответа · 42 с», «Подключение…».
// reachText — ping сервера: «Online · 23 мс», «Offline», «Проверка…».
func reachText(st *core.Store, id string) (string, fyne.ThemeColorName) {
	r, ok := st.Reach(id)
	switch {
	case !ok:
		return "Проверка…", theme.ColorNamePlaceHolder
	case r.Online:
		return fmt.Sprintf("Online · %d мс", r.RTT.Milliseconds()), theme.ColorNameSuccess
	}
	return "Offline", theme.ColorNameError
}

func statusText(h core.Health) string {
	if h.Status == core.StatusNoResponse {
		if h.LastSeen.IsZero() {
			return "Нет ответа"
		}
		return "Нет ответа · " + ago(h.LastSeen)
	}
	return h.Status.Label()
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "никогда"
	}
	d := time.Since(t)
	switch {
	case d < 5*time.Second:
		return "сейчас"
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч", int(d.Hours()))
	}
	return t.Format("02.01 15:04")
}

func severityIcon(s agent.Severity) fyne.Resource {
	switch s {
	case agent.SeveritySuccess:
		return theme.NewColoredResource(theme.ConfirmIcon(), theme.ColorNameSuccess)
	case agent.SeverityWarning:
		return theme.NewColoredResource(theme.WarningIcon(), theme.ColorNameWarning)
	case agent.SeverityError:
		return theme.NewColoredResource(theme.ErrorIcon(), theme.ColorNameError)
	}
	return theme.NewColoredResource(theme.InfoIcon(), theme.ColorNamePrimary)
}

func envColor(e agent.Env) fyne.ThemeColorName {
	switch e {
	case agent.EnvProduction:
		return theme.ColorNameError
	case agent.EnvStaging:
		return theme.ColorNameWarning
	}
	return theme.ColorNamePrimary
}

// envBadge — маленькая плашка окружения: PROD / STAGE / DEV.
func envBadge(e agent.Env) fyne.CanvasObject {
	short := map[agent.Env]string{agent.EnvProduction: "PROD", agent.EnvStaging: "STAGE", agent.EnvDevelopment: "DEV"}[e]
	if short == "" {
		short = string(e)
	}
	t := text(short, 9, envColor(e), true)
	bg := newVRect("")
	bg.StrokeName, bg.StrokeWidth, bg.Radius = envColor(e), 1, 0
	return container.NewCenter(container.NewStack(bg, container.New(&insetLayout{4, 1}, t)))
}

// insetLayout — отступы по краям одного объекта.
type insetLayout struct{ x, y float32 }

func (l *insetLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(l.x, l.y))
		o.Resize(size.Subtract(fyne.NewSize(l.x*2, l.y*2)))
	}
}

func (l *insetLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, o := range objs {
		m = m.Max(o.MinSize())
	}
	return m.Add(fyne.NewSize(l.x*2, l.y*2))
}

func inset(x, y float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&insetLayout{x, y}, o)
}

// maxHeight — скролл, который растёт по контенту, но не выше h.
func maxHeight(h float32, content fyne.CanvasObject) fyne.CanvasObject {
	s := container.NewVScroll(content)
	return container.New(&capLayout{h: h}, s)
}

type capLayout struct{ h float32 }

func (l *capLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l *capLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	s := objs[0].(*container.Scroll)
	m := s.Content.MinSize()
	return fyne.NewSize(m.Width, fyne.Min(m.Height, l.h))
}

// minWidth — не уже w.
func minWidth(w float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&widthLayout{w}, o)
}

type widthLayout struct{ w float32 }

func (l *widthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l *widthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	m := objs[0].MinSize()
	return fyne.NewSize(fyne.Max(m.Width, l.w), m.Height)
}

// ---------- иконки, которых нет в теме Fyne ----------

func svgIcon(name, path string) fyne.Resource {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000000" d="` + path + `"/></svg>`
	return theme.NewThemedResource(fyne.NewStaticResource(name, []byte(svg)))
}

var (
	iconBell        = svgIcon("bell.svg", "M12 22c1.1 0 2-.9 2-2h-4c0 1.1.89 2 2 2zm6-6v-5c0-3.07-1.64-5.64-4.5-6.32V4c0-.83-.67-1.5-1.5-1.5s-1.5.67-1.5 1.5v.68C7.63 5.36 6 7.92 6 11v5l-2 2v1h16v-1l-2-2z")
	iconStar        = svgIcon("star.svg", "M12 17.27L18.18 21l-1.64-7.03L22 9.24l-7.19-.61L12 2 9.19 8.63 2 9.24l5.46 4.73L5.82 21z")
	iconStarOutline = svgIcon("star-outline.svg", "M22 9.24l-7.19-.62L12 2 9.19 8.63 2 9.24l5.46 4.73L5.82 21 12 17.27 18.18 21l-1.63-7.03L22 9.24zM12 15.4l-3.76 2.27 1-4.28-3.32-2.88 4.38-.38L12 6.1l1.71 4.04 4.38.38-3.32 2.88 1 4.28L12 15.4z")
)

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func starIcon(on bool) fyne.Resource {
	if on {
		return theme.NewColoredResource(iconStar, theme.ColorNameWarning)
	}
	return iconStarOutline
}

// fixedWidth — ровно w в ширину, высота по контенту.
func fixedWidth(w float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&fixedWidthLayout{w}, o)
}

type fixedWidthLayout struct{ w float32 }

func (l *fixedWidthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l *fixedWidthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		h = fyne.Max(h, o.MinSize().Height)
	}
	return fyne.NewSize(l.w, h)
}
