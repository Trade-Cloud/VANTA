package header

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/apptheme"
	"github.com/Trade-Cloud/vanta/internal/core"
)

// ---------- Notifications ----------

type noteFilter int

const (
	filterAll noteFilter = iota
	filterUnread
	filterErrors
	filterWarnings
	filterServer
)

var noteFilters = []struct {
	f     noteFilter
	label string
}{
	{filterAll, "Все"}, {filterUnread, "Непрочитанные"}, {filterErrors, "Ошибки"},
	{filterWarnings, "Предупреждения"}, {filterServer, "Этот сервер"},
}

func (h *Header) buildBell() fyne.CanvasObject {
	h.bellBtn = h.iconButton(nil, h.toggleNotes)
	refresh := func() {
		h.bellBtn.SetContent(container.NewCenter(badge(icon(iconBell, iconSize), h.st.Unread())))
	}
	h.on(core.TopicNotifications, refresh)
	refresh()
	return h.bellBtn
}

func (h *Header) toggleNotes() {
	h.toggle(keyNotes, h.bellBtn, func() (fyne.CanvasObject, Opts) {
		filter := filterAll
		body := container.NewVBox()
		chips := container.NewHBox()

		var fill func()
		fill = func() {
			// Фильтры.
			chips.Objects = nil
			for _, nf := range noteFilters {
				nf := nf
				b := widget.NewButton(nf.label, func() { filter = nf.f; fill() })
				b.Importance = widget.LowImportance
				if nf.f == filter {
					b.Importance = widget.HighImportance
				}
				chips.Objects = append(chips.Objects, b)
			}
			chips.Refresh()

			list, loaded := h.st.Notifications()
			active, _ := h.st.Active()
			var objs []fyne.CanvasObject
			if !loaded {
				for i := 0; i < 3; i++ {
					objs = append(objs, inset(8, 8, container.NewHBox(skeleton(16, 16), container.NewVBox(skeleton(180, 12), skeleton(240, 10)))))
				}
			}
			shown := 0
			for _, n := range list {
				switch filter {
				case filterUnread:
					if n.Read {
						continue
					}
				case filterErrors:
					if n.Severity != agent.SeverityError {
						continue
					}
				case filterWarnings:
					if n.Severity != agent.SeverityWarning {
						continue
					}
				case filterServer:
					if n.ServerID != active.ID {
						continue
					}
				}
				shown++
				objs = append(objs, h.noteRow(n))
			}
			if loaded && shown == 0 {
				objs = append(objs, inset(8, 12, line("Здесь пока пусто", true)))
			}
			body.Objects = objs
			body.Refresh()
			h.layer.Relayout()
		}
		off := h.st.On(core.TopicNotifications, fill)
		fill()

		readAll := widget.NewButton("Прочитать все", h.st.MarkAllRead)
		readAll.Importance = widget.LowImportance
		clear := widget.NewButton("Очистить историю", h.st.ClearNotifications)
		clear.Importance = widget.LowImportance
		head := container.NewBorder(nil, nil, inset(4, 0, caps("УВЕДОМЛЕНИЯ")), readAll)
		top := container.NewVBox(head, container.NewHScroll(chips))
		content := container.NewBorder(top, container.NewVBox(hsep(), container.NewCenter(clear)), nil, nil, maxHeight(380, body))
		return content, Opts{Modal: true, Align: AlignEnd, Width: 420, OnClose: off}
	})
}

func (h *Header) noteRow(n agent.Notification) fyne.CanvasObject {
	title := text(n.Title, theme.TextSize(), theme.ColorNameForeground, !n.Read)
	where := "VANTA"
	if sv, ok := h.st.Server(n.ServerID); ok {
		where = sv.Name
	}
	meta := small(where + " · " + ago(n.At))
	info := container.NewVBox(container.NewBorder(nil, nil, nil, meta, title), line(n.Body, true))
	left := icon(severityIcon(n.Severity), 18)
	id := n.ID
	return NewPressable(container.NewBorder(nil, nil, left, nil, info), func() { h.st.MarkRead(id) })
}

// ---------- Settings ----------

func (h *Header) buildGear() fyne.CanvasObject {
	h.gearBtn = h.iconButton(icon(theme.SettingsIcon(), iconSize), func() { h.toggleSettings(false) })
	h.gearBtn.OnKey = func(e *fyne.KeyEvent) bool {
		if e.Name == fyne.KeyDown {
			h.toggleSettings(true)
			return true
		}
		return h.escKey(e)
	}
	return h.gearBtn
}

func (h *Header) toggleSettings(keyboard bool) {
	h.toggle(keySettings, h.gearBtn, func() (fyne.CanvasObject, Opts) {
		body := container.NewVBox(inset(8, 4, caps("SETTINGS")))
		list := newNavList(body)
		list.OnKey = h.escKey
		var rows []*Pressable
		var acts []func()
		for _, pg := range core.SettingsPages {
			pg := pg
			row := pageRow(pg)
			rows = append(rows, row)
			acts = append(acts, func() { h.layer.Close(); h.st.Navigate(pg.Route) })
			body.Add(row)
		}
		// Тема — прямо в меню, без перехода.
		body.Add(hsep())
		body.Add(inset(8, 4, caps("ТЕМА")))
		for _, mode := range []string{apptheme.System, apptheme.Light, apptheme.Dark} {
			mode := mode
			mark := theme.RadioButtonIcon()
			if h.st.Theme() == mode {
				mark = theme.RadioButtonCheckedIcon()
			}
			row := NewPressable(container.NewHBox(icon(mark, 16), widget.NewLabel(apptheme.Label(mode))), nil)
			rows = append(rows, row)
			acts = append(acts, func() { h.layer.Close(); h.st.SetTheme(mode) })
			body.Add(row)
		}
		list.Set(rows, acts)
		o := Opts{Modal: true, Align: AlignEnd, Width: 280, Owns: []fyne.Focusable{list}}
		if keyboard {
			o.Focus = list
		}
		return list, o
	})
}

// pageRow — «Название / описание».
func pageRow(pg core.Page) *Pressable {
	return NewPressable(container.NewVBox(
		text(pg.Title, theme.TextSize(), theme.ColorNameForeground, true),
		line(pg.Description, true),
	), nil)
}
