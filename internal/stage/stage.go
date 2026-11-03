// Package stage — этапы приложения. Каждый этап — отдельное окно со своим
// размером и правилами. Переход на этап открывает его окно и закрывает
// окно предыдущего.
//
// Новый этап = константа ID + Register со Spec.
package stage

import (
	"fmt"
	"log/slog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

type ID string

const (
	Start ID = "start" // «Начало» (первый запуск): кнопка «Let's go»
	Main  ID = "main"  // «Основа»
)

// Spec описывает окно этапа.
type Spec struct {
	Title   string
	Size    fyne.Size                             // стартовый размер
	MinSize fyne.Size                             // минимальный; ноль — без ограничения сверх контента
	Fixed   bool                                  // запретить менять размер (тогда окно ровно Size)
	Build   func(w fyne.Window) fyne.CanvasObject // w — окно этапа (шорткаты, диалоги)
}

type Manager struct {
	app   fyne.App
	specs map[ID]Spec

	cur   fyne.Window
	curID ID

	// OnWindow вызывается для каждого нового окна до Show
	// (например, подключить его к debug-мосту).
	OnWindow func(fyne.Window)
}

func NewManager(a fyne.App) *Manager {
	return &Manager{app: a, specs: map[ID]Spec{}}
}

func (m *Manager) Register(id ID, s Spec) {
	m.specs[id] = s
}

func (m *Manager) Current() ID {
	return m.curID
}

// Go открывает окно этапа id и закрывает предыдущее. Вызывать из UI-горутины.
// Новое окно показывается раньше, чем закрывается старое, иначе Fyne
// завершит приложение, когда закроется последнее окно.
func (m *Manager) Go(id ID) {
	s, ok := m.specs[id]
	if !ok {
		panic(fmt.Sprintf("stage: этап %q не зарегистрирован", id))
	}

	w := m.app.NewWindow(s.Title)
	content := s.Build(w)
	if !s.MinSize.IsZero() {
		content = container.New(minSizeLayout{min: s.MinSize}, content)
	}
	w.SetContent(content)
	w.Resize(s.Size)
	w.SetFixedSize(s.Fixed)
	w.CenterOnScreen()
	if m.OnWindow != nil {
		m.OnWindow(w)
	}
	w.Show()

	prev, prevID := m.cur, m.curID
	m.cur, m.curID = w, id
	if prev != nil {
		prev.Close()
	}
	slog.Info("этап", "from", string(prevID), "to", string(id),
		"size", fmt.Sprintf("%.0fx%.0f", s.Size.Width, s.Size.Height), "fixed", s.Fixed)
}

// minSizeLayout растягивает контент на всё окно и не даёт окну стать
// меньше min: минимальный размер окна в Fyne = MinSize его контента.
type minSizeLayout struct{ min fyne.Size }

func (l minSizeLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l minSizeLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	min := l.min
	for _, o := range objs {
		min = min.Max(o.MinSize())
	}
	return min
}
