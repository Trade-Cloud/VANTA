package screens

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/header"
)

// formWin — отдельное окно-форма в стиле VANTA (не modal): шапка с иконкой,
// тело, подвал «Отмена / действие». Одно окно на key: повторный вызов
// поднимает уже открытое.
type formWin struct {
	w       fyne.Window
	confirm *widget.Button
	errText *widget.Label
	label   string
	onClose func()
}

type formSpec struct {
	Key       string
	Title     string
	Subtitle  string
	Icon      fyne.Resource
	IconColor fyne.ThemeColorName
	Body      fyne.CanvasObject
	Confirm   string
	Danger    bool
	Width     float32
	Focus     fyne.Focusable
	// OnConfirm вызывается по кнопке/Enter, когда кнопка активна.
	OnConfirm func(f *formWin)
}

var openForms = map[string]*formWin{}

func showForm(s formSpec) *formWin {
	if f := openForms[s.Key]; f != nil {
		f.w.RequestFocus()
		return nil
	}
	w := fyne.CurrentApp().NewWindow(s.Title + " · VANTA")
	f := &formWin{w: w, label: s.Confirm}
	openForms[s.Key] = f
	f.onClose = func() { delete(openForms, s.Key) }
	w.SetCloseIntercept(f.Close)

	f.errText = widget.NewLabel("")
	f.errText.Importance = widget.DangerImportance
	f.errText.Wrapping = fyne.TextWrapWord
	f.errText.Hide()

	f.confirm = widget.NewButton(s.Confirm, func() {
		if s.OnConfirm != nil {
			s.OnConfirm(f)
		}
	})
	f.confirm.Importance = widget.HighImportance
	if s.Danger {
		f.confirm.Importance = widget.DangerImportance
	}
	cancel := widget.NewButton("Отмена", f.Close)

	color := s.IconColor
	if color == "" {
		color = theme.ColorNamePrimary
	}
	head := container.NewStack(header.ThemedRect(theme.ColorNameHeaderBackground),
		pad(sp6, sp5, sp6, sp5, hstack(sp3+2, iconTile(s.Icon, color, 40),
			vstack(sp1, title(s.Title, theme.TextSize()+3), muted(s.Subtitle)))))

	body := pad(sp6, sp5, sp6, sp5, vstack(sp4, s.Body, f.errText))
	foot := container.NewStack(header.ThemedRect(theme.ColorNameHeaderBackground),
		pad(sp6, sp3, sp6, sp3, hstack(sp2, layout.NewSpacer(), cancel, f.confirm)))

	w.SetContent(container.NewBorder(
		vstack(0, head, hline()),
		vstack(0, hline(), foot),
		nil, nil, container.NewVScroll(body)))

	width := s.Width
	if width == 0 {
		width = 520
	}
	h := head.MinSize().Height + body.MinSize().Height + foot.MinSize().Height + 2
	w.Resize(fyne.NewSize(width, fyne.Min(h, 720)))
	w.SetFixedSize(true)
	w.CenterOnScreen()
	w.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeyEscape:
			f.Close()
		case fyne.KeyReturn, fyne.KeyEnter:
			if !f.confirm.Disabled() {
				f.confirm.OnTapped()
			}
		}
	})
	if header.WindowHook != nil {
		header.WindowHook(w)
	}
	w.Show()
	if s.Focus != nil {
		w.Canvas().Focus(s.Focus)
	}
	return f
}

func (f *formWin) Close() {
	if f.onClose != nil {
		f.onClose()
		f.onClose = nil
	}
	f.w.Close()
}

// Submit — Enter в поле ввода жмёт кнопку действия.
func (f *formWin) Submit(entries ...*widget.Entry) {
	for _, e := range entries {
		e.OnSubmitted = func(string) {
			if !f.confirm.Disabled() {
				f.confirm.OnTapped()
			}
		}
	}
}

// SetValid включает/выключает кнопку действия.
func (f *formWin) SetValid(ok bool) {
	if ok {
		f.confirm.Enable()
	} else {
		f.confirm.Disable()
	}
}

// Busy — идёт работа: кнопка неактивна, подпись busyText.
func (f *formWin) Busy(busyText string) {
	f.errText.Hide()
	f.confirm.SetText(busyText)
	f.confirm.Disable()
}

// Fail — показать ошибку и вернуть кнопку.
func (f *formWin) Fail(err error) {
	f.confirm.SetText(f.label)
	f.confirm.Enable()
	f.errText.SetText(err.Error())
	f.errText.Show()
}
