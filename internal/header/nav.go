package header

import (
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"github.com/Trade-Cloud/vanta/internal/core"
)

// Поведение dropdown (спецификация, раздел 4):
//  1. наведение на пункт сразу открывает меню (без задержки — так решил Danya);
//  2. пока курсор над пунктом или над меню, меню открыто;
//  3. ушёл отовсюду — меню закрывается через closeDelay;
//  4. клик по пункту в меню — переход в раздел;
//  5. Escape закрывает;
//  6. клик по пункту с dropdown ничего не делает; с клавиатуры (Enter/↓,
//     Alt+2…7) меню открывается в modal-режиме слоя.
const (
	closeDelay = 260 * time.Millisecond
)

type navBar struct {
	h     *Header
	obj   fyne.CanvasObject
	items map[string]*navItem
	order []string

	// hover-состояние: где сейчас курсор (пункты и части открытого меню).
	inside    map[string]bool
	closeGen  int
	hoverMode bool // меню открыто наведением (а не кликом/клавиатурой)
}

type navItem struct {
	sec   core.Section
	btn   *FocusPressable
	label *vText
	bar   *vRect
}

func newNavBar(h *Header) *navBar {
	n := &navBar{h: h, items: map[string]*navItem{}, inside: map[string]bool{}}
	box := container.NewHBox()
	for _, sec := range core.Sections {
		it := n.newItem(sec)
		n.items[sec.ID] = it
		n.order = append(n.order, sec.ID)
		box.Add(it.obj())
	}
	n.obj = box
	h.on(core.TopicRoute, n.paint)
	n.paint()
	return n
}

func (n *navBar) newItem(sec core.Section) *navItem {
	it := &navItem{sec: sec}
	it.label = text(sec.Title, theme.TextSize(), theme.ColorNameForeground, false)
	content := it.label
	var c fyne.CanvasObject = container.NewCenter(content)
	if len(sec.Pages) > 0 {
		c = container.NewHBox(container.NewCenter(content), icon(theme.MenuDropDownIcon(), 14))
	}
	it.btn = NewFocusPressable(c, func() { n.click(it) })
	it.btn.Pad = fyne.NewSize(theme.Padding()*2, theme.Padding()*1.5)
	it.btn.OnHover = func(in bool) { n.hoverItem(it, in) }
	it.btn.OnKey = func(e *fyne.KeyEvent) bool { return n.itemKey(it, e) }
	it.bar = newVRect(theme.ColorNamePrimary)
	return it
}

// obj — пункт с полоской активного раздела снизу.
func (it *navItem) obj() fyne.CanvasObject {
	return container.NewBorder(nil, container.NewGridWrap(fyne.NewSize(0, 2), it.bar), nil, nil, it.btn)
}

func (n *navBar) key(id string) string { return keyNavPref + id }

// paint подсвечивает раздел текущего route полоской снизу (без жирного).
func (n *navBar) paint() {
	cur := core.SectionOf(n.h.st.Route())
	for id, it := range n.items {
		on := id == cur
		if on {
			it.bar.FillName = theme.ColorNamePrimary
		} else {
			it.bar.FillName = ""
		}
		it.bar.Refresh()
	}
}

// ---------- hover ----------

func (n *navBar) hoverItem(it *navItem, in bool) {
	k := "item:" + it.sec.ID
	if !in {
		delete(n.inside, k)
		n.scheduleClose()
		return
	}
	n.inside[k] = true
	layer := n.h.layer
	// Открыто модально (клик, Ctrl+K, уведомления) — hover не вмешивается.
	if layer.Modal() {
		return
	}
	if len(it.sec.Pages) == 0 {
		// Overview без dropdown: просто закрыть чужое меню после задержки.
		n.scheduleClose()
		return
	}
	if layer.IsOpen(n.key(it.sec.ID)) {
		n.closeGen++ // вернулись — отменить закрытие
		return
	}
	n.open(it, true, false)
}

func (n *navBar) hoverPanel(part string, in bool) {
	k := "panel:" + part
	if in {
		n.inside[k] = true
		n.closeGen++
		return
	}
	delete(n.inside, k)
	n.scheduleClose()
}

func (n *navBar) scheduleClose() {
	n.closeGen++
	g := n.closeGen
	after(closeDelay, func() {
		if g != n.closeGen || !n.hoverMode {
			return
		}
		if len(n.inside) > 0 && n.insideOpen() {
			return
		}
		if strings.HasPrefix(n.h.layer.Key(), keyNavPref) {
			n.h.layer.Close()
		}
	})
}

// insideOpen — курсор над пунктом открытого меню или над самим меню.
func (n *navBar) insideOpen() bool {
	open := strings.TrimPrefix(n.h.layer.Key(), keyNavPref)
	for k := range n.inside {
		if strings.HasPrefix(k, "panel:") || k == "item:"+open {
			return true
		}
	}
	return false
}

// ---------- клик и клавиатура ----------

func (n *navBar) click(it *navItem) {
	layer := n.h.layer
	if len(it.sec.Pages) == 0 {
		layer.Close()
		n.h.st.Navigate(core.RouteOverview)
		return
	}
	// Пункты с dropdown по клику ничего не делают: меню открывается наведением.
}

func (n *navBar) itemKey(it *navItem, e *fyne.KeyEvent) bool {
	switch e.Name {
	case fyne.KeyDown, fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		if len(it.sec.Pages) == 0 {
			n.click(it)
		} else {
			n.open(it, false, true)
		}
		return true
	case fyne.KeyLeft, fyne.KeyRight:
		if nb := n.neighbor(it.sec.ID, e.Name); nb != nil {
			n.h.w.Canvas().Focus(nb.btn)
		}
		return true
	}
	return n.h.escKey(e)
}

func (n *navBar) neighbor(id string, dir fyne.KeyName) *navItem {
	for i, x := range n.order {
		if x != id {
			continue
		}
		j := i + 1
		if dir == fyne.KeyLeft {
			j = i - 1
		}
		j = (j + len(n.order)) % len(n.order)
		return n.items[n.order[j]]
	}
	return nil
}

// openByKeyboard — Alt+2…7: открыть меню раздела с фокусом на нём.
func (n *navBar) openByKeyboard(id string) {
	if it, ok := n.items[id]; ok {
		n.open(it, false, true)
	}
}

// open показывает dropdown раздела. hover — режим наведения (без ловушки
// кликов); focus — передать клавиатуру в меню.
func (n *navBar) open(it *navItem, hover, focus bool) {
	h := n.h
	n.hoverMode = hover
	for k := range n.inside {
		if strings.HasPrefix(k, "panel:") {
			delete(n.inside, k)
		}
	}

	list := n.menu(it)
	o := Opts{
		Modal: !hover, Align: AlignStart,
		Owns:    []fyne.Focusable{list},
		OnClose: func() { n.hoverMode = false },
	}
	if hover {
		o.Hover = func(in bool) { n.hoverPanel("bg", in) }
	}
	if focus {
		o.Focus = list
	}
	h.layer.Open(n.key(it.sec.ID), it.btn, list, o)
}

// menu — плавающая панель «Services — описание …» без заголовка.
// Больше четырёх пунктов — в две колонки, чтобы было видно карту раздела.
func (n *navBar) menu(it *navItem) *navList {
	h := n.h
	grid := container.NewGridWithColumns(1)
	if len(it.sec.Pages) > 4 {
		grid = container.NewGridWithColumns(2)
	}
	list := newNavList(minWidth(240, grid))

	var rows []*Pressable
	var acts []func()
	cur := h.st.Route()
	for i, pg := range it.sec.Pages {
		pg := pg
		part := "row" + string(rune('0'+i))
		row := pageRow(pg)
		row.OnHover = func(in bool) {
			if n.hoverMode {
				n.hoverPanel(part, in)
			}
		}
		if pg.Route == cur {
			row.SetSelected(true)
		}
		rows = append(rows, row)
		acts = append(acts, func() {
			h.layer.Close()
			h.st.Navigate(pg.Route)
		})
		grid.Add(minWidth(220, row))
	}
	list.Set(rows, acts)
	list.OnKey = func(e *fyne.KeyEvent) bool {
		switch e.Name {
		case fyne.KeyLeft, fyne.KeyRight:
			// Соседний раздел, как в меню-баре.
			nb := n.neighbor(it.sec.ID, e.Name)
			for nb != nil && len(nb.sec.Pages) == 0 {
				nb = n.neighbor(nb.sec.ID, e.Name)
			}
			if nb != nil {
				n.open(nb, false, true)
			}
			return true
		case fyne.KeyEscape:
			h.layer.Close()
			h.w.Canvas().Focus(it.btn)
			return true
		}
		return false
	}
	return list
}

// after выполняет fn в UI-горутине через d.
func after(d time.Duration, fn func()) {
	time.AfterFunc(d, func() { fyne.Do(fn) })
}
