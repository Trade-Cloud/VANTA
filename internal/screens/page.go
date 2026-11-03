// Package screens — содержимое окон этапов. Экраны ничего не знают о настройках и
// переходах: всё, что они делают, отдают наружу колбэками.
package screens

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// page кладёт content по центру на белый фон во всё окно.
func page(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.White)
	return container.NewStack(bg, container.NewCenter(content))
}
