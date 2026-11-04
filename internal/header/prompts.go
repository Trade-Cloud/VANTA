package header

import (
	"context"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
)

// Prompter — вопросы при SSH-подключении, каждый в своём окне (не modal):
// подтверждение ключа нового сервера и пароль, если он не сохранён.
//
// Методы зовёт Backend из своей горутины и ждёт ответа; окна создаются в
// UI-горутине через fyne.Do. Отмена подключения (ctx) закрывает окно.
type Prompter struct{}

var _ agent.Prompter = Prompter{}

// ask открывает окно и ждёт ответа. build получает answer — вызвать один
// раз с результатом; закрытие окна крестиком или Esc — ответ «нет».
func ask[T any](ctx context.Context, title string, size fyne.Size,
	build func(answer func(T, bool)) (fyne.CanvasObject, fyne.Focusable)) (T, bool) {
	type result struct {
		v  T
		ok bool
	}
	res := make(chan result, 1)
	var w fyne.Window
	closed := false // только в UI-горутине
	fyne.Do(func() {
		if ctx.Err() != nil {
			res <- result{}
			return
		}
		w = fyne.CurrentApp().NewWindow(title + " · VANTA")
		answered := false
		answer := func(v T, ok bool) {
			if answered {
				return
			}
			answered = true
			res <- result{v, ok}
			closed = true
			w.Close()
		}
		var zero T
		w.SetCloseIntercept(func() { answer(zero, false) })
		content, focus := build(answer)
		w.SetContent(content)
		w.Resize(size)
		w.SetFixedSize(true)
		w.CenterOnScreen()
		w.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
			if e.Name == fyne.KeyEscape {
				answer(zero, false)
			}
		})
		if WindowHook != nil {
			WindowHook(w)
		}
		w.Show()
		w.RequestFocus()
		if focus != nil {
			w.Canvas().Focus(focus)
		}
	})
	select {
	case r := <-res:
		return r.v, r.ok
	case <-ctx.Done():
		fyne.Do(func() {
			if w != nil && !closed {
				closed = true
				w.Close()
			}
		})
		var zero T
		return zero, false
	}
}

func (Prompter) TrustHostKey(ctx context.Context, sv agent.Server, key agent.HostKey) bool {
	ok, _ := ask(ctx, "Ключ сервера", fyne.NewSize(520, 420), func(answer func(bool, bool)) (fyne.CanvasObject, fyne.Focusable) {
		fp := widget.NewLabelWithStyle(key.Fingerprint, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true, Bold: true})
		fp.Selectable = true
		fp.Wrapping = fyne.TextWrapBreak
		cmd := widget.NewLabelWithStyle(hostKeyCmd(key.Type), fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
		cmd.Selectable = true
		cmd.Wrapping = fyne.TextWrapBreak

		intro := widget.NewLabel("VANTA подключается к этому серверу впервые, его ключа нет в ~/.ssh/known_hosts. " +
			"Сверьте отпечаток с тем, что показывает сам сервер, прежде чем доверять.")
		intro.Wrapping = fyne.TextWrapWord

		body := container.NewVBox(
			intro,
			section("ОТПЕЧАТОК · "+strings.ToUpper(key.Type)),
			fp,
			section("НА СЕРВЕРЕ"),
			cmd,
		)
		cancel := widget.NewButton("Отмена", func() { answer(false, false) })
		trust := widget.NewButtonWithIcon("Доверять и подключиться", theme.ConfirmIcon(), func() { answer(true, true) })
		trust.Importance = widget.HighImportance
		return promptLayout(theme.WarningIcon(), "Подтвердите ключ сервера", sv.Name+" · "+key.Host,
			body, cancel, trust), nil
	})
	return ok
}

// hostKeyCmd — чем посмотреть отпечаток на самом сервере.
func hostKeyCmd(keyType string) string {
	file := "ed25519"
	switch {
	case strings.HasPrefix(keyType, "ecdsa"):
		file = "ecdsa"
	case strings.Contains(keyType, "rsa"):
		file = "rsa"
	}
	return "ssh-keygen -lf /etc/ssh/ssh_host_" + file + "_key.pub"
}

func (Prompter) Secret(ctx context.Context, sv agent.Server, retry bool) (string, bool) {
	isKey := sv.AuthKind() == agent.AuthKey
	title, head, label := "Пароль", "Пароль для "+sv.Login(), "Пароль"
	wrong := "Неверный пароль, попробуйте ещё раз"
	if isKey {
		title, head, label = "Пароль ключа", "Пароль ключа "+shortPath(sv.KeyPath), "Passphrase"
		wrong = "Пароль ключа не подошёл, попробуйте ещё раз"
	}
	return ask(ctx, title, fyne.NewSize(460, 300), func(answer func(string, bool)) (fyne.CanvasObject, fyne.Focusable) {
		e := widget.NewPasswordEntry()
		submit := func() {
			if e.Text != "" {
				answer(e.Text, true)
			}
		}
		e.OnSubmitted = func(string) { submit() }

		body := container.NewVBox()
		if retry {
			body.Add(banner(theme.ColorNameError, wrong))
			body.Add(spacerH(8))
		}
		body.Add(field(label, e, nil))
		if sv.Remember {
			body.Add(small("После входа сохранится в связке ключей"))
		}

		cancel := widget.NewButton("Отмена", func() { answer("", false) })
		ok := widget.NewButtonWithIcon("Войти", theme.LoginIcon(), submit)
		ok.Importance = widget.HighImportance
		return promptLayout(theme.AccountIcon(), head, sv.Name, body, cancel, ok), e
	})
}

// promptLayout — как окно сервера: шапка, тело, подвал с кнопками.
func promptLayout(ic fyne.Resource, title, sub string, body fyne.CanvasObject, buttons ...fyne.CanvasObject) fyne.CanvasObject {
	head := container.NewStack(
		newVRect(theme.ColorNameHeaderBackground),
		inset(24, 16, container.NewBorder(nil, nil,
			icon(ic, 28), nil,
			container.NewVBox(
				text(title, 18, theme.ColorNameForeground, true),
				small(sub),
			),
		)),
	)
	foot := container.NewHBox(spacer())
	for _, b := range buttons {
		foot.Add(b)
	}
	footer := container.NewStack(
		newVRect(theme.ColorNameHeaderBackground),
		inset(24, 12, foot),
	)
	return container.NewBorder(
		container.NewVBox(head, hsep()),
		container.NewVBox(hsep(), footer),
		nil, nil,
		container.NewVScroll(inset(24, 16, body)),
	)
}

// shortPath — ~/… вместо домашней папки.
func shortPath(p string) string {
	if home := homeDir(); home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// SudoPassword — пароль sudo для команд, которым нужен root. Живёт только в
// памяти до отключения от сервера, на диск и в связку ключей не попадает.
func (Prompter) SudoPassword(ctx context.Context, sv agent.Server, retry bool) (string, bool) {
	return ask(ctx, "Пароль sudo", fyne.NewSize(460, 300), func(answer func(string, bool)) (fyne.CanvasObject, fyne.Focusable) {
		e := widget.NewPasswordEntry()
		submit := func() {
			if e.Text != "" {
				answer(e.Text, true)
			}
		}
		e.OnSubmitted = func(string) { submit() }
		body := container.NewVBox()
		if retry {
			body.Add(banner(theme.ColorNameError, "Неверный пароль, попробуйте ещё раз"))
			body.Add(spacerH(8))
		}
		body.Add(field("Пароль "+sv.SSHUser(), e, nil))
		body.Add(small("Действию нужны права root. Пароль хранится только до отключения от сервера."))
		cancel := widget.NewButton("Отмена", func() { answer("", false) })
		ok := widget.NewButtonWithIcon("Продолжить", theme.ConfirmIcon(), submit)
		ok.Importance = widget.HighImportance
		return promptLayout(theme.WarningIcon(), "sudo на "+sv.Name, sv.Login(), body, cancel, ok), e
	})
}
