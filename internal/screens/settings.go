package screens

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/core"
)

// RouteAppSettings — Settings › Настройки приложения.
const RouteAppSettings core.Route = "settings/app"

// appSettings — общие параметры VANTA. Значения живут в core.Store и
// сохраняются в state.json сразу при изменении.
func appSettings(st *core.Store) fyne.CanvasObject {
	h := widget.NewLabel("Настройки приложения")
	h.SizeName = theme.SizeNameHeadingText
	h.TextStyle = fyne.TextStyle{Bold: true}
	crumbs := widget.NewLabel("Settings › Настройки приложения")
	crumbs.SizeName = theme.SizeNameCaptionText

	auto := widget.NewCheck("Автообновление вкладки Resources", st.SetResourcesAuto)
	auto.SetChecked(st.ResourcesAuto())
	hint := widget.NewLabel("Данные о CPU, памяти и системе обновляются сами раз в 2 секунды.")
	hint.SizeName = theme.SizeNameCaptionText
	hint.Wrapping = fyne.TextWrapWord

	// Остальные вкладки мониторинга: Processes и Network — раз в 3 с,
	// Storage (df, lsblk) — раз в 15 с.
	autoTab := func(tab, label string) *widget.Check {
		c := widget.NewCheck(label, func(on bool) { st.SetAutoRefresh(tab, on) })
		c.SetChecked(st.AutoRefresh(tab))
		return c
	}
	tabHint := widget.NewLabel("Processes и Network обновляются раз в 3 секунды, Storage — раз в 15 секунд. Обновление идёт в фоне, без полосы загрузки.")
	tabHint.SizeName = theme.SizeNameCaptionText
	tabHint.Wrapping = fyne.TextWrapWord

	return container.NewVBox(
		container.NewVBox(h, crumbs),
		widget.NewCard("Серверы", "", container.NewVBox(auto, hint,
			autoTab("processes", "Автообновление вкладки Processes"),
			autoTab("network", "Автообновление вкладки Network"),
			autoTab("storage", "Автообновление вкладки Storage"),
			tabHint)),
	)
}
