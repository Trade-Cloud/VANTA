//go:build !darwin

package dockicon

// Set на других ОС не нужен: Fyne сам ставит иконку окна через app.SetIcon.
func Set([]byte) {}
