package header

import (
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// Binding — горячая клавиша header.
//
// Модификатор «Ctrl» записан как fyne.KeyModifierShortcutDefault: на macOS
// это Cmd, на остальных Ctrl. Таблица — единственное место, где заданы
// сочетания, поэтому пользовательскую настройку можно сделать подменой
// Bindings из настроек (хранение пока не сделано).
type Binding struct {
	ID    string
	Label string
	Key   fyne.KeyName
	Mod   fyne.KeyModifier
}

const (
	bindPalette  = "palette"
	bindSearch   = "search"
	bindOverview = "overview"
	bindNavPref  = "nav:" // nav:<section id> — открыть dropdown раздела
)

const ctrl = fyne.KeyModifierShortcutDefault

// Bindings — действующая раскладка.
var Bindings = []Binding{
	{ID: bindPalette, Label: "Command Palette", Key: fyne.KeyK, Mod: ctrl},
	{ID: bindSearch, Label: "Global Search", Key: fyne.KeyF, Mod: ctrl | fyne.KeyModifierShift},
	{ID: bindOverview, Label: "Overview", Key: fyne.Key1, Mod: fyne.KeyModifierAlt},
	{ID: bindNavPref + "monitor", Label: "Monitor", Key: fyne.Key2, Mod: fyne.KeyModifierAlt},
	{ID: bindNavPref + "system", Label: "System", Key: fyne.Key3, Mod: fyne.KeyModifierAlt},
	{ID: bindNavPref + "developer", Label: "Developer", Key: fyne.Key4, Mod: fyne.KeyModifierAlt},
	{ID: bindNavPref + "automation", Label: "Automation", Key: fyne.Key5, Mod: fyne.KeyModifierAlt},
	{ID: bindNavPref + "security", Label: "Security", Key: fyne.Key6, Mod: fyne.KeyModifierAlt},
	{ID: bindNavPref + "more", Label: "More", Key: fyne.Key7, Mod: fyne.KeyModifierAlt},
}

func (b Binding) Shortcut() *desktop.CustomShortcut {
	return &desktop.CustomShortcut{KeyName: b.Key, Modifier: b.Mod}
}

// Hint — подпись сочетания для текущей ОС: «⇧⌘F» на macOS, «Ctrl+Shift+F» на остальных.
func (b Binding) Hint() string {
	key := string(b.Key)
	if runtime.GOOS == "darwin" {
		var s strings.Builder
		if b.Mod&fyne.KeyModifierControl != 0 {
			s.WriteString("⌃")
		}
		if b.Mod&fyne.KeyModifierAlt != 0 {
			s.WriteString("⌥")
		}
		if b.Mod&fyne.KeyModifierShift != 0 {
			s.WriteString("⇧")
		}
		if b.Mod&fyne.KeyModifierSuper != 0 {
			s.WriteString("⌘")
		}
		return s.String() + key
	}
	var parts []string
	if b.Mod&fyne.KeyModifierControl != 0 {
		parts = append(parts, "Ctrl")
	}
	if b.Mod&fyne.KeyModifierSuper != 0 {
		parts = append(parts, "Win")
	}
	if b.Mod&fyne.KeyModifierAlt != 0 {
		parts = append(parts, "Alt")
	}
	if b.Mod&fyne.KeyModifierShift != 0 {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, key), "+")
}

func binding(id string) Binding {
	for _, b := range Bindings {
		if b.ID == id {
			return b
		}
	}
	return Binding{}
}

func hint(id string) string { return binding(id).Hint() }

// installKeys вешает горячие клавиши и Escape на окно.
func (h *Header) installKeys() {
	c := h.w.Canvas()
	for _, b := range Bindings {
		id := b.ID
		c.AddShortcut(b.Shortcut(), func(fyne.Shortcut) { h.runBinding(id) })
	}
	// Escape, когда фокуса нет ни у кого. Иначе Escape уходит в
	// сфокусированный виджет, и наши виджеты передают его в closeAll.
	c.SetOnTypedKey(func(e *fyne.KeyEvent) {
		if e.Name == fyne.KeyEscape {
			h.closeAll()
		}
	})
}

// globalShortcut — для полей ввода: сработала ли глобальная клавиша.
func (h *Header) globalShortcut(s fyne.Shortcut) bool {
	for _, b := range Bindings {
		if b.Shortcut().ShortcutName() == s.ShortcutName() {
			h.runBinding(b.ID)
			return true
		}
	}
	return false
}

func (h *Header) runBinding(id string) {
	// Открыт диалог Fyne (overlay) — header под ним, не трогаем.
	if h.w.Canvas().Overlays().Top() != nil {
		return
	}
	switch {
	case id == bindPalette:
		h.togglePalette()
	case id == bindSearch:
		h.layer.Close()
		h.w.Canvas().Focus(h.search)
		h.openSearch() // если поле уже было в фокусе, FocusGained не придёт
	case id == bindOverview:
		h.layer.Close()
		h.st.Navigate("overview")
	case strings.HasPrefix(id, bindNavPref):
		h.nav.openByKeyboard(strings.TrimPrefix(id, bindNavPref))
	}
}

// escKey — общий обработчик Escape для наших фокусируемых виджетов.
func (h *Header) escKey(e *fyne.KeyEvent) bool {
	if e.Name == fyne.KeyEscape {
		h.closeAll()
		return true
	}
	return false
}
