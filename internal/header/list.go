package header

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// navList — список строк с выбором стрелками и Enter.
//
// Если список сам в фокусе (меню навигации, Settings), клавиши приходят ему.
// В панелях с полем ввода (поиск, серверы) фокус у поля, и оно зовёт
// Step/Activate само.
type navList struct {
	widget.BaseWidget

	content fyne.CanvasObject
	rows    []*Pressable
	acts    []func()
	idx     int // -1 — ничего не выделено
	// OnKey — до обработки по умолчанию; true — обработано.
	OnKey func(*fyne.KeyEvent) bool
}

func newNavList(content fyne.CanvasObject) *navList {
	l := &navList{content: content, idx: -1}
	l.ExtendBaseWidget(l)
	return l
}

// Set — новые строки; acts[i] выполняется по Enter/клику на rows[i].
func (l *navList) Set(rows []*Pressable, acts []func()) {
	l.rows, l.acts = rows, acts
	for i, r := range rows {
		act := acts[i]
		r.OnTap = act
	}
	if l.idx >= len(rows) {
		l.idx = len(rows) - 1
	}
	l.paint()
}

func (l *navList) Len() int { return len(l.rows) }

// Step сдвигает выделение, по кругу.
func (l *navList) Step(d int) {
	n := len(l.rows)
	if n == 0 {
		return
	}
	switch {
	case l.idx < 0 && d > 0:
		l.idx = 0
	case l.idx < 0:
		l.idx = n - 1
	default:
		l.idx = ((l.idx+d)%n + n) % n
	}
	l.paint()
}

// Select ставит выделение на i (или снимает при -1).
func (l *navList) Select(i int) {
	if i >= len(l.rows) {
		i = len(l.rows) - 1
	}
	l.idx = i
	l.paint()
}

// Activate выполняет выделенную строку (или первую, если ничего не выделено).
func (l *navList) Activate() bool {
	i := l.idx
	if i < 0 {
		i = 0
	}
	if i >= len(l.acts) || l.rows[i].Disabled {
		return false
	}
	l.acts[i]()
	return true
}

func (l *navList) paint() {
	for i, r := range l.rows {
		r.SetSelected(i == l.idx)
	}
}

func (l *navList) FocusGained() {
	if l.idx < 0 && len(l.rows) > 0 {
		l.idx = 0
	}
	l.paint()
}

func (l *navList) FocusLost() {
	l.idx = -1
	l.paint()
}

func (l *navList) TypedRune(rune) {}

func (l *navList) TypedKey(e *fyne.KeyEvent) {
	if l.OnKey != nil && l.OnKey(e) {
		return
	}
	switch e.Name {
	case fyne.KeyDown:
		l.Step(1)
	case fyne.KeyUp:
		l.Step(-1)
	case fyne.KeyHome:
		l.Select(0)
	case fyne.KeyEnd:
		l.Select(len(l.rows) - 1)
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		l.Activate()
	}
}

func (l *navList) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.content)
}

// keyEntry — поле ввода, которое отдаёт стрелки/Enter/Escape и глобальные
// горячие клавиши наверх. Обычный Entry съедает все shortcuts, пока он в
// фокусе, и Ctrl+K в поле поиска не сработал бы.
type keyEntry struct {
	widget.Entry
	onKey      func(*fyne.KeyEvent) bool
	onShortcut func(fyne.Shortcut) bool
	onFocus    func(bool)
}

func newKeyEntry(placeholder string) *keyEntry {
	e := &keyEntry{}
	e.PlaceHolder = placeholder
	e.ExtendBaseWidget(e)
	return e
}

func (e *keyEntry) TypedKey(k *fyne.KeyEvent) {
	if e.onKey != nil && e.onKey(k) {
		return
	}
	e.Entry.TypedKey(k)
}

func (e *keyEntry) TypedShortcut(s fyne.Shortcut) {
	if e.onShortcut != nil && e.onShortcut(s) {
		return
	}
	e.Entry.TypedShortcut(s)
}

func (e *keyEntry) FocusGained() {
	e.Entry.FocusGained()
	if e.onFocus != nil {
		e.onFocus(true)
	}
}

func (e *keyEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocus != nil {
		e.onFocus(false)
	}
}
