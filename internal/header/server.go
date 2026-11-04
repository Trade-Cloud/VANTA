package header

import (
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/core"
)

// ---------- триггер в header: «production-01 · Online ▾» ----------

func (h *Header) buildServerTrigger() fyne.CanvasObject {
	h.serverBtn = NewFocusPressable(nil, h.toggleServers)
	h.serverBtn.OnKey = func(e *fyne.KeyEvent) bool {
		if e.Name == fyne.KeyDown {
			if !h.layer.IsOpen(keyServers) {
				h.toggleServers()
			}
			return true
		}
		return h.escKey(e)
	}
	h.on(core.TopicServers, h.refreshServerTrigger)
	h.on(core.TopicActive, h.refreshServerTrigger)
	h.on(core.TopicHealth, h.refreshServerTrigger)
	h.refreshServerTrigger()
	return h.serverBtn
}

func (h *Header) refreshServerTrigger() {
	list, loaded, loading, err := h.st.ServersState()
	arrow := icon(theme.MenuDropDownIcon(), 16)
	var c fyne.CanvasObject
	switch {
	case !loaded && (loading || err == nil):
		// Skeleton, пока список не пришёл ни разу.
		c = container.NewHBox(skeleton(110, 12), skeleton(48, 10))
	case !loaded && err != nil:
		c = container.NewHBox(icon(theme.NewColoredResource(theme.ErrorIcon(), theme.ColorNameError), 16),
			text("Серверы не загружены", theme.TextSize(), theme.ColorNameError, false), arrow)
	case len(list) == 0:
		c = container.NewHBox(icon(theme.ContentAddIcon(), 16), text("Добавить сервер", theme.TextSize(), theme.ColorNameForeground, false))
	default:
		sv, ok := h.st.Active()
		if !ok {
			// При старте сервер не выбран: куда подключаться, решает пользователь.
			c = container.NewHBox(text("Сервер не выбран", theme.TextSize(), theme.ColorNamePlaceHolder, false), arrow)
			break
		}
		hl := h.st.Health(sv.ID)
		st := text(statusText(hl), theme.CaptionTextSize(), theme.ColorNamePlaceHolder, false)
		st.ColorName = statusColorName(hl.Status)
		c = container.NewHBox(
			text(sv.Name, theme.TextSize(), theme.ColorNameForeground, true),
			container.NewCenter(st), arrow)
	}
	h.serverBtn.SetContent(c)
}

// ---------- список серверов ----------

type serverPanel struct {
	h       *Header
	entry   *keyEntry
	list    *navList
	body    *fyne.Container
	footer  *fyne.Container
	byEnv   bool
	updates []func() // обновить статусы строк без пересборки
	offs    []func()
}

func (h *Header) toggleServers() {
	h.toggle(keyServers, h.serverBtn, func() (fyne.CanvasObject, Opts) {
		p := &serverPanel{h: h}
		content := p.build()
		return content, Opts{
			Modal: true, Align: AlignStart, Width: 440,
			Focus: p.entry, Owns: []fyne.Focusable{p.entry},
			OnClose: func() {
				for _, off := range p.offs {
					off()
				}
			},
		}
	})
}

func (p *serverPanel) build() fyne.CanvasObject {
	h := p.h
	p.entry = newKeyEntry("Поиск серверов…")
	p.entry.OnChanged = func(string) { p.rebuild() }
	p.entry.onShortcut = h.globalShortcut
	p.entry.onKey = func(e *fyne.KeyEvent) bool {
		switch e.Name {
		case fyne.KeyDown:
			p.list.Step(1)
		case fyne.KeyUp:
			p.list.Step(-1)
		case fyne.KeyReturn, fyne.KeyEnter:
			p.list.Activate()
		case fyne.KeyEscape:
			h.layer.Close()
		default:
			return false
		}
		return true
	}

	groupBtn := widget.NewButton("", nil)
	groupBtn.Importance = widget.LowImportance
	setGroupLabel := func() {
		if p.byEnv {
			groupBtn.SetText("По окружению")
		} else {
			groupBtn.SetText("По группам")
		}
	}
	groupBtn.OnTapped = func() { p.byEnv = !p.byEnv; setGroupLabel(); p.rebuild() }
	setGroupLabel()

	p.body = container.NewVBox()
	p.list = newNavList(p.body)
	p.footer = container.NewVBox()

	add := widget.NewButtonWithIcon("Добавить сервер", theme.ContentAddIcon(), func() {
		h.layer.Close()
		h.editServer(agent.Server{Env: agent.EnvProduction})
	})
	reload := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), h.st.ReloadServers)
	reload.Importance = widget.LowImportance
	disconnect := widget.NewButtonWithIcon("Отключиться", theme.LogoutIcon(), func() {
		h.layer.Close()
		h.st.Disconnect()
	})
	disconnect.Importance = widget.LowImportance
	if _, ok := h.st.Active(); !ok {
		disconnect.Hide()
	}

	p.offs = append(p.offs,
		h.st.On(core.TopicServers, p.rebuild),
		h.st.On(core.TopicActive, p.rebuild),
		h.st.On(core.TopicHealth, p.tick),
	)
	p.rebuild()

	top := container.NewBorder(nil, nil, nil, groupBtn, p.entry)
	bottom := container.NewVBox(p.footer, hsep(), container.NewBorder(nil, nil, add, container.NewHBox(disconnect, reload)))
	return container.NewBorder(top, bottom, nil, nil, maxHeight(360, p.list))
}

func (p *serverPanel) tick() {
	for _, u := range p.updates {
		u()
	}
}

func (p *serverPanel) rebuild() {
	h := p.h
	list, loaded, loading, err := h.st.ServersState()
	q := strings.ToLower(strings.TrimSpace(p.entry.Text))
	p.updates = nil

	// Подвал: ошибки и загрузка.
	var foot []fyne.CanvasObject
	if loading {
		foot = append(foot, line("Обновление списка…", true))
	}
	if err != nil {
		foot = append(foot, line("Ошибка: "+err.Error()+". Показан кэш.", true))
	}
	p.footer.Objects = foot
	p.footer.Refresh()

	if !loaded {
		var sk []fyne.CanvasObject
		for i := 0; i < 4; i++ {
			sk = append(sk, inset(8, 6, container.NewHBox(skeleton(140, 12), spacer(), skeleton(60, 10))))
		}
		p.body.Objects = sk
		p.body.Refresh()
		p.list.Set(nil, nil)
		h.layer.Relayout()
		return
	}

	var match []agent.Server
	for _, sv := range list {
		hay := strings.ToLower(sv.Name + " " + sv.Login() + " " + string(sv.Env) + " " + sv.Group)
		if q == "" || strings.Contains(hay, q) {
			match = append(match, sv)
		}
	}

	type group struct {
		title string
		items []agent.Server
	}
	var groups []group
	if q == "" {
		var fav []agent.Server
		for _, sv := range match {
			if sv.Favorite {
				fav = append(fav, sv)
			}
		}
		if len(fav) > 0 {
			groups = append(groups, group{"Избранные", fav})
		}
	}
	idx := map[string]int{}
	for _, sv := range match {
		g := sv.Group
		if p.byEnv {
			g = sv.Env.Label()
		}
		if g == "" {
			g = "Без группы"
		}
		i, ok := idx[g]
		if !ok {
			i = len(groups)
			idx[g] = i
			groups = append(groups, group{title: g})
		}
		groups[i].items = append(groups[i].items, sv)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].title == "Избранные" && groups[j].title != "Избранные"
	})

	var objs []fyne.CanvasObject
	var rows []*Pressable
	var acts []func()
	active, _ := h.st.Active()
	for _, g := range groups {
		objs = append(objs, inset(8, 4, caps(strings.ToUpper(g.title))))
		for _, sv := range g.items {
			row, update := p.row(sv, sv.ID == active.ID)
			p.updates = append(p.updates, update)
			id := sv.ID
			rows = append(rows, row)
			acts = append(acts, func() {
				h.st.SetActive(id)
				h.layer.Close()
			})
			objs = append(objs, row)
		}
	}
	if len(match) == 0 {
		objs = append(objs, inset(8, 12, line("Ничего не найдено", true)))
	}
	p.body.Objects = objs
	p.body.Refresh()
	p.list.Set(rows, acts)
	if q != "" && p.list.Len() > 0 {
		p.list.Select(0)
	}
	h.layer.Relayout()
}

// row — строка сервера: ★ name ENV статус / ip ✎. Статус у выбранного —
// состояние подключения, у остальных — только ping: без подключения больше
// ничего не известно.
func (p *serverPanel) row(sv agent.Server, active bool) (*Pressable, func()) {
	h := p.h
	status := small("")
	update := func() {
		if active {
			hl := h.st.Health(sv.ID)
			status.Text = statusText(hl)
			status.ColorName = statusColorName(hl.Status)
		} else {
			status.Text, status.ColorName = reachText(h.st, sv.ID)
		}
		status.Refresh()
	}
	update()

	star := NewPressable(icon(starIcon(sv.Favorite), 16), func() { h.st.SetFavorite(sv.ID, !sv.Favorite) })
	star.Pad = fyne.NewSquareSize(2)
	edit := NewPressable(icon(theme.DocumentCreateIcon(), 16), func() {
		h.layer.Close()
		h.editServer(sv)
	})
	edit.Pad = fyne.NewSquareSize(2)

	name := text(sv.Name, theme.TextSize(), theme.ColorNameForeground, active)
	top := container.NewHBox(name, envBadge(sv.Env), spacer(), status)
	subText := sv.Login()
	if active {
		subText += " · подключён"
	}
	sub := line(subText, true)
	info := container.NewVBox(top, sub)

	row := NewPressable(container.NewBorder(nil, nil, star, edit, info), nil)
	if active {
		row.SetSelected(true)
	}
	return row, update
}
