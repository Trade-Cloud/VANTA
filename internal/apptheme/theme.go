// Package apptheme — тема VANTA: стандартная тема Fyne с вариантом
// (светлая/тёмная) по выбору пользователя или как в ОС.
package apptheme

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

const (
	System = ""
	Light  = "light"
	Dark   = "dark"
)

// vantaTheme — DefaultTheme с двумя правками:
//   - forced != nil — вариант задан пользователем, вариант ОС игнорируется;
//   - ColorNameDisabled светлее стандартного. Fyne красит им приглушённый
//     текст (Label с LowImportance — подписи и описания в header), а
//     стандартный цвет в тёмной теме почти сливается с фоном;
//   - все радиусы скругления = 0: строгий стиль, острые углы у полей,
//     кнопок, выделения, скроллбаров и плавающих панелей.
type vantaTheme struct {
	fyne.Theme
	forced *fyne.ThemeVariant
}

func (t vantaTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if t.forced != nil {
		v = *t.forced
	}
	if n == theme.ColorNameDisabled {
		if v == theme.VariantDark {
			return color.NRGBA{0x9a, 0x9c, 0xa3, 0xff}
		}
		return color.NRGBA{0x7a, 0x7c, 0x83, 0xff}
	}
	return t.Theme.Color(n, v)
}

// Size обнуляет радиусы: поля, кнопки, выделение в списках, скроллбары.
// Наши виджеты берут радиус через theme.InputRadiusSize(), так что
// острые углы везде меняются отсюда.
func (t vantaTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameInputRadius,
		theme.SizeNameSelectionRadius,
		theme.SizeNameScrollBarRadius,
		theme.SizeNameWindowButtonRadius:
		return 0
	}
	return t.Theme.Size(n)
}

// Apply ставит тему приложению: System, Light или Dark.
func Apply(a fyne.App, mode string) {
	t := vantaTheme{Theme: theme.DefaultTheme()}
	switch mode {
	case Light:
		v := theme.VariantLight
		t.forced = &v
	case Dark:
		v := theme.VariantDark
		t.forced = &v
	}
	a.Settings().SetTheme(t)
}

// Label — подпись режима для меню.
func Label(mode string) string {
	switch mode {
	case Light:
		return "Светлая"
	case Dark:
		return "Тёмная"
	}
	return "Как в системе"
}
