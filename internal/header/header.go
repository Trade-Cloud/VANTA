// Package header — глобальный header панели «Основа» (спецификация v1.1).
//
// Два уровня:
//
//	Level 01 — Global:     Logo / Server Selector + Status · Search · Notifications · Settings
//	Level 02 — Navigation: Overview · Monitor · System · Developer · Automation · Security · More
//
// Header не знает о страницах: всё состояние берёт из core.Store и туда же
// пишет (Navigate, SetActive). Плавающие панели — в общем Layer поверх
// окна. Под навигацией — полоса тревоги, если агент активного сервера не
// отвечает: последнее известное состояние не выдаётся за актуальное.
//
// Отложено (в Fyne-десктопе неприменимо или рано):
//   - адаптив под телефоны/планшеты — окно десктопное, минимум 830 px;
//     dropdown открывается и по клику, как требуется для устройств без hover;
//   - WebSocket — события идут через agent.Backend.Subscribe (сейчас SSH keepalive и ping);
//   - скринридеры — у Fyne нет API доступности; клавиатура поддержана;
//   - пользовательская переназначаемость клавиш — таблица Bindings готова,
//     хранения и UI нет.
package header

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/core"
)

const (
	keyServers  = "servers"
	keySearch   = "search"
	keyPalette  = "palette"
	keyNotes    = "notifications"
	keySettings = "settings"
	keyNavPref  = "nav:"

	iconSize    = 20
	searchWidth = 280
)

type Header struct {
	w     fyne.Window
	st    *core.Store
	layer *Layer
	offs  []func()

	// Level 01
	serverBtn  *FocusPressable
	search     *keyEntry
	searchBody *fyne.Container
	searchList *navList
	bellBtn    *FocusPressable
	gearBtn    *FocusPressable

	// Level 02
	nav        *navBar
	agentAlert *alertStrip
	listAlert  *alertStrip

	obj fyne.CanvasObject
}

// New собирает header. layer — слой плавающих панелей, который вызывающий
// кладёт поверх всего окна.
func New(w fyne.Window, st *core.Store, layer *Layer) *Header {
	h := &Header{w: w, st: st, layer: layer}

	level1 := container.NewBorder(nil, nil,
		container.NewHBox(h.logo(), h.buildServerTrigger()),
		// Поиск фиксированной ширины справа: длина имени сервера слева
		// ничего не сдвигает.
		container.NewHBox(container.NewCenter(fixedWidth(searchWidth, h.buildSearch())), h.buildBell(), h.buildGear()),
		nil,
	)

	h.nav = newNavBar(h)
	paletteHint := small(hint(bindPalette) + "  Command Palette")
	level2 := container.NewBorder(nil, nil, nil, container.NewCenter(paletteHint), h.nav.obj)

	bg := newVRect(theme.ColorNameHeaderBackground)
	body := container.NewVBox(
		inset(theme.Padding()*2, theme.Padding(), level1),
		hsep(),
		inset(theme.Padding()*2, 0, level2),
		h.buildAlerts(),
		hsep(),
	)
	h.obj = container.NewStack(bg, body)

	h.installKeys()
	return h
}

func (h *Header) Object() fyne.CanvasObject { return h.obj }

// Dispose отписывает header от Store (перед пересборкой окна).
func (h *Header) Dispose() {
	for _, off := range h.offs {
		off()
	}
	h.offs = nil
	h.layer.Close()
}

func (h *Header) on(t core.Topic, fn func()) {
	h.offs = append(h.offs, h.st.On(t, fn))
}

// closeAll — Escape: закрыть панель, а если её нет — снять фокус.
func (h *Header) closeAll() {
	if h.layer.Key() != "" {
		h.layer.Close()
		return
	}
	h.w.Canvas().Unfocus()
}

// toggle открывает панель key под anchor или закрывает её, если уже открыта.
func (h *Header) toggle(key string, anchor fyne.CanvasObject, build func() (fyne.CanvasObject, Opts)) {
	if h.layer.IsOpen(key) {
		h.layer.Close()
		return
	}
	content, o := build()
	h.layer.Open(key, anchor, content, o)
}

func (h *Header) logo() fyne.CanvasObject {
	t := text("VANTA", 17, theme.ColorNameForeground, true)
	slash := text("/", 17, theme.ColorNamePlaceHolder, false)
	logo := NewPressable(container.NewCenter(t), func() { h.layer.Close(); h.st.Navigate(core.RouteOverview) })
	return container.NewHBox(logo, container.NewCenter(slash))
}

// iconButton — квадратная кнопка с иконкой для правой части Level 01.
func (h *Header) iconButton(content fyne.CanvasObject, onTap func()) *FocusPressable {
	b := NewFocusPressable(container.NewCenter(content), onTap)
	b.Pad = fyne.NewSquareSize(theme.Padding() * 1.5)
	b.OnKey = h.escKey
	return b
}

// ---------- полоса «агент не отвечает» ----------

// alertStrip — полоса предупреждения. Создаётся один раз, refreshAlert
// только меняет текст и видимость: она обновляется каждую секунду.
type alertStrip struct {
	obj  fyne.CanvasObject
	text *widget.Label
}

func (h *Header) newAlertStrip(sev fyne.ThemeColorName, retryFn func()) *alertStrip {
	bg := newVRect(sev)
	bg.Alpha = 0x30
	res := theme.NewColoredResource(theme.WarningIcon(), sev)
	retry := widget.NewButtonWithIcon("Повторить", theme.ViewRefreshIcon(), retryFn)
	retry.Importance = widget.LowImportance
	a := &alertStrip{text: line("", false)}
	row := container.NewBorder(nil, nil, icon(res, 16), retry, a.text)
	a.obj = container.NewStack(bg, inset(theme.Padding()*2, 0, row))
	a.obj.Hide()
	return a
}

func (a *alertStrip) set(msg string) {
	if msg == "" {
		a.obj.Hide()
		return
	}
	a.text.SetText(msg)
	a.obj.Show()
}

func (h *Header) buildAlerts() fyne.CanvasObject {
	// Повтор для сервера — переподключиться к нему же.
	h.agentAlert = h.newAlertStrip(theme.ColorNameError, func() {
		if sv, ok := h.st.Active(); ok {
			h.st.SetActive(sv.ID)
		}
	})
	h.listAlert = h.newAlertStrip(theme.ColorNameWarning, h.st.ReloadServers)
	h.on(core.TopicActive, h.refreshAlert)
	h.on(core.TopicHealth, h.refreshAlert)
	h.on(core.TopicServers, h.refreshAlert)
	h.refreshAlert()
	return container.NewVBox(h.agentAlert.obj, h.listAlert.obj)
}

func (h *Header) refreshAlert() {
	msg := ""
	if sv, ok := h.st.Active(); ok {
		hl := h.st.Health(sv.ID)
		switch hl.Status {
		case core.StatusFailed:
			msg = "Не удалось подключиться к " + sv.Name + ": " + h.st.ConnError().Error()
		case core.StatusNoResponse:
			if hl.LastSeen.IsZero() {
				msg = "Агент " + sv.Name + " не отвечает. Данных о сервере нет."
			} else {
				msg = "Агент " + sv.Name + " не отвечает " + ago(hl.LastSeen) +
					". Последние данные от " + hl.LastSeen.Format("15:04:05") + " могут быть неактуальны."
			}
		case core.StatusOffline:
			msg = sv.Name + " offline: агент сообщил, что сервер недоступен."
		}
	}
	h.agentAlert.set(msg)

	msg = ""
	if _, loaded, _, err := h.st.ServersState(); err != nil {
		msg = "Не удалось обновить список серверов: " + err.Error()
		if loaded {
			msg += ". Показан последний загруженный список."
		}
	}
	h.listAlert.set(msg)
}

// spacer — растяжка для HBox.
func spacer() fyne.CanvasObject { return layout.NewSpacer() }
