package header

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/core"
)

const searchLimit = 12

// ---------- Global Search в Level 01 ----------

func (h *Header) buildSearch() fyne.CanvasObject {
	h.search = newKeyEntry("Поиск   " + hint(bindSearch))
	// Отступ справа, иначе лупа прилипает к краю поля.
	h.search.ActionItem = inset(theme.Padding()*2, 0, widget.NewIcon(theme.SearchIcon()))
	h.searchBody = container.NewVBox()
	h.searchList = newNavList(h.searchBody)

	h.search.OnChanged = func(string) { h.openSearch() }
	h.search.onShortcut = h.globalShortcut
	h.search.onFocus = func(in bool) {
		if in {
			h.openSearch()
		}
	}
	h.search.onKey = func(e *fyne.KeyEvent) bool {
		switch e.Name {
		case fyne.KeyDown:
			h.openSearch()
			h.searchList.Step(1)
		case fyne.KeyUp:
			h.searchList.Step(-1)
		case fyne.KeyReturn, fyne.KeyEnter:
			h.searchList.Activate()
		case fyne.KeyEscape:
			if h.layer.IsOpen(keySearch) {
				h.layer.Close()
			} else {
				h.search.SetText("")
				h.w.Canvas().Unfocus()
			}
		default:
			return false
		}
		return true
	}
	h.on(core.TopicHistory, func() {
		if h.layer.IsOpen(keySearch) {
			h.fillSearch()
		}
	})
	return h.search
}

// openSearch показывает выдачу под полем (или обновляет уже открытую).
func (h *Header) openSearch() {
	h.fillSearch()
	if !h.layer.IsOpen(keySearch) {
		h.layer.Open(keySearch, h.search, maxHeight(420, h.searchList), Opts{
			Modal: true, Align: AlignEnd, MatchAnchor: true,
		})
	}
	h.layer.Relayout()
}

func (h *Header) fillSearch() {
	q := h.search.Text
	run := func(r core.Result) {
		h.st.AddHistory(q)
		h.search.SetText("")
		h.layer.Close()
		h.w.Canvas().Unfocus()
		h.exec(r)
	}
	if q == "" {
		h.fillHistory(h.searchBody, h.searchList, func(s string) { h.search.SetText(s) })
		return
	}
	fillResults(h.searchBody, h.searchList, h.st.Search(q, searchLimit), run)
	if h.searchList.Len() > 0 {
		h.searchList.Select(0)
	}
}

// fillHistory — пустой запрос: история и подсказка.
func (h *Header) fillHistory(body *fyne.Container, list *navList, pick func(string)) {
	hist := h.st.History()
	var objs []fyne.CanvasObject
	var rows []*Pressable
	var acts []func()
	if len(hist) == 0 {
		objs = append(objs, inset(8, 8, container.NewVBox(
			line("Ищите разделы, серверы и настройки.", true),
			line(hint(bindPalette)+" — Command Palette", true),
		)))
	} else {
		clear := widget.NewButton("Очистить", h.st.ClearHistory)
		clear.Importance = widget.LowImportance
		objs = append(objs, container.NewBorder(nil, nil, inset(8, 4, caps("НЕДАВНИЕ ЗАПРОСЫ")), clear))
		for _, q := range hist {
			q := q
			row := NewPressable(container.NewHBox(icon(theme.HistoryIcon(), 16), widget.NewLabel(q)), nil)
			rows = append(rows, row)
			acts = append(acts, func() { pick(q) })
			objs = append(objs, row)
		}
	}
	body.Objects = objs
	body.Refresh()
	list.Set(rows, acts)
}

// fillResults — строки выдачи поиска.
func fillResults(body *fyne.Container, list *navList, res []core.Result, run func(core.Result)) {
	var objs []fyne.CanvasObject
	var rows []*Pressable
	var acts []func()
	if len(res) == 0 {
		objs = append(objs, inset(8, 12, line("Ничего не найдено", true)))
	}
	for _, r := range res {
		r := r
		kind := small(r.Kind.Label())
		info := container.NewVBox(text(r.Title, theme.TextSize(), theme.ColorNameForeground, true), line(r.Hint, true))
		row := NewPressable(container.NewBorder(nil, nil, icon(kindIcon(r.Kind), 18), container.NewCenter(kind), info), nil)
		rows = append(rows, row)
		acts = append(acts, func() { run(r) })
		objs = append(objs, row)
	}
	body.Objects = objs
	body.Refresh()
	list.Set(rows, acts)
}

func kindIcon(k core.ResultKind) fyne.Resource {
	switch k {
	case core.KindObject:
		return theme.ComputerIcon()
	case core.KindFunction:
		return theme.SettingsIcon()
	}
	return theme.ListIcon()
}

// exec выполняет найденное: переход или смена сервера.
func (h *Header) exec(r core.Result) {
	switch {
	case r.ServerID != "":
		h.st.SetActive(r.ServerID)
	case r.Route != "":
		h.st.Navigate(r.Route)
	}
}

// ---------- Command Palette (Ctrl/Cmd+K) ----------

func (h *Header) togglePalette() {
	if h.layer.IsOpen(keyPalette) {
		h.layer.Close()
		return
	}
	entry := newKeyEntry("Раздел, сервер или настройка…")
	body := container.NewVBox()
	list := newNavList(body)
	fill := func() {
		q := entry.Text
		var res []core.Result
		if q == "" {
			// Без запроса — разделы.
			for _, r := range h.st.Search("", 0) {
				if r.Kind == core.KindSection && len(res) < searchLimit {
					res = append(res, r)
				}
			}
		} else {
			res = h.st.Search(q, searchLimit)
		}
		fillResults(body, list, res, func(r core.Result) {
			h.st.AddHistory(q)
			h.layer.Close()
			h.exec(r)
		})
		list.Select(0)
		h.layer.Relayout()
	}
	entry.OnChanged = func(string) { fill() }
	entry.onShortcut = h.globalShortcut
	entry.onKey = func(e *fyne.KeyEvent) bool {
		switch e.Name {
		case fyne.KeyDown:
			list.Step(1)
		case fyne.KeyUp:
			list.Step(-1)
		case fyne.KeyReturn, fyne.KeyEnter:
			list.Activate()
		case fyne.KeyEscape:
			h.layer.Close()
		default:
			return false
		}
		return true
	}
	fill()

	head := container.NewBorder(nil, nil, inset(4, 0, caps("COMMAND PALETTE")), small(hint(bindPalette)))
	content := container.NewBorder(container.NewVBox(head, entry), nil, nil, nil, maxHeight(380, list))
	h.layer.Open(keyPalette, nil, content, Opts{
		Modal: true, Align: AlignCenter, Width: 560,
		Focus: entry, Owns: []fyne.Focusable{entry},
	})
}
