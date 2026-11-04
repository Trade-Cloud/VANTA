// Package assets — встроенные в бинарник ресурсы приложения.
package assets

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed app-icon/mac-1024.png
var appIconPNG []byte

// AppIcon — иконка VANTA (док macOS, окна, панель задач).
var AppIcon = fyne.NewStaticResource("vanta-icon.png", appIconPNG)
