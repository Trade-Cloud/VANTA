package screens

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// Start — окно «Начало» (первый запуск): одна кнопка «Let's go».
func Start(onStart func()) fyne.CanvasObject {
	btn := widget.NewButton("Let's go", onStart)
	btn.Importance = widget.HighImportance
	return page(btn)
}
