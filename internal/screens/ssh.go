package screens

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/core"
	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/sshkeys"
)

// RouteSSH — вкладка Security › SSH: локальные SSH-ключи этого компьютера
// (~/.ssh). От выбранного сервера не зависит.
const RouteSSH core.Route = "security/ssh"

// Колонки таблицы: ключ (гибкая) · тип · защита · изменён · действия.
var sshCols = []float32{-1, 96, 124, 92, 108}

const sshGap = sp4

type sshPage struct {
	st  *core.Store
	w   fyne.Window
	obj fyne.CanvasObject

	summary *header.Text
	filter  *widget.Entry
	table   *fyne.Container

	keys     []sshkeys.Key
	err      error
	loading  bool
	closed   bool
	expanded map[string]bool
}

func newSSHPage(st *core.Store, w fyne.Window) *sshPage {
	p := &sshPage{st: st, w: w, expanded: map[string]bool{}}
	p.summary = muted("")
	p.filter = widget.NewEntry()
	p.filter.PlaceHolder = "Поиск"
	p.filter.ActionItem = pad(sp2, 0, sp2, 0, square(16, widget.NewIcon(theme.SearchIcon())))
	p.filter.OnChanged = func(string) { p.render() }
	p.table = vstack(0)
	p.obj = pad(sp2, sp2, sp2, sp4, vstack(sp5, p.head(), p.table))
	p.reload()
	return p
}

func (p *sshPage) dispose() { p.closed = true }

// reload перечитывает ~/.ssh в фоне.
func (p *sshPage) reload() {
	p.loading = true
	p.render()
	go func() {
		keys, err := sshkeys.List()
		fyne.Do(func() {
			if p.closed {
				return
			}
			p.keys, p.err, p.loading = keys, err, false
			p.render()
		})
	}()
}

// ---------- шапка ----------

func (p *sshPage) head() fyne.CanvasObject {
	dir, _ := sshkeys.Dir()
	openDir := iconButton(theme.FolderOpenIcon(), func() { p.openDir(dir) })
	refresh := iconButton(theme.ViewRefreshIcon(), p.reload)
	create := widget.NewButtonWithIcon("Новый ключ", theme.ContentAddIcon(), p.openCreate)
	create.Importance = widget.HighImportance

	tools := hstack(sp2, fixedW(260, p.filter), openDir, refresh, create)
	left := vstack(sp1, title("SSH-ключи", theme.TextSize()+7), p.summary)
	return container.NewBorder(nil, nil, nil, tools, left)
}

func (p *sshPage) updateSummary() {
	dir, _ := sshkeys.Dir()
	s := dir
	if len(p.keys) > 0 {
		s += "  ·  " + plural(len(p.keys), "ключ", "ключа", "ключей")
		open := 0
		for _, k := range p.keys {
			if k.HasPrivate && !k.Encrypted && k.Err == "" {
				open++
			}
		}
		if open > 0 {
			s += "  ·  без пароля: " + fmt.Sprint(open)
		}
	}
	p.summary.Text = s
	p.summary.Refresh()
}

// ---------- таблица ----------

func (p *sshPage) visible() []sshkeys.Key {
	q := strings.ToLower(strings.TrimSpace(p.filter.Text))
	if q == "" {
		return p.keys
	}
	var out []sshkeys.Key
	for _, k := range p.keys {
		hay := strings.ToLower(k.Name + " " + k.Comment + " " + k.Type + " " + k.Fingerprint)
		if strings.Contains(hay, q) {
			out = append(out, k)
		}
	}
	return out
}

func (p *sshPage) render() {
	p.updateSummary()
	var rows []fyne.CanvasObject
	switch {
	case p.loading && p.keys == nil:
		rows = []fyne.CanvasObject{p.state(theme.ViewRefreshIcon(), theme.ColorNamePrimary,
			"Читаем ~/.ssh…", "", nil)}
	case p.err != nil:
		rows = []fyne.CanvasObject{p.state(theme.ErrorIcon(), theme.ColorNameError,
			"Не удалось прочитать ~/.ssh", p.err.Error(),
			widget.NewButtonWithIcon("Повторить", theme.ViewRefreshIcon(), p.reload))}
	case len(p.keys) == 0:
		b := widget.NewButtonWithIcon("Новый ключ", theme.ContentAddIcon(), p.openCreate)
		b.Importance = widget.HighImportance
		rows = []fyne.CanvasObject{p.state(icKey, theme.ColorNamePrimary, "Ключей пока нет",
			"Создайте ключ, а его публичную часть добавьте на серверы.", b)}
	default:
		rows = append(rows, p.columnsHead(), hline())
		vis := p.visible()
		if len(vis) == 0 {
			rows = append(rows, pad(sp4, sp6, sp4, sp6, container.NewCenter(
				muted("Ничего не найдено по «"+strings.TrimSpace(p.filter.Text)+"»"))))
		}
		for i, k := range vis {
			if i > 0 {
				rows = append(rows, hline())
			}
			rows = append(rows, p.row(k))
			if p.expanded[k.Name] {
				rows = append(rows, p.details(k))
			}
		}
	}
	p.table.Objects = []fyne.CanvasObject{card(vstack(0, rows...))}
	p.table.Refresh()
}

func (p *sshPage) columnsHead() fyne.CanvasObject {
	right := capsLabel("")
	return pad(sp4, sp3-2, sp4, sp3-2, cols(sshCols, sshGap,
		capsLabel("КЛЮЧ"), capsLabel("ТИП"), capsLabel("ЗАЩИТА"), capsLabel("ИЗМЕНЁН"), right))
}

func algoLabel(k sshkeys.Key) string {
	if k.Type == "" {
		return "—"
	}
	if k.Bits > 0 && !strings.HasPrefix(k.Type, "ED25519") {
		return fmt.Sprintf("%s %d", k.Type, k.Bits)
	}
	return k.Type
}

// protection — плашка «Пароль / Без пароля / Только .pub / Ошибка».
func protection(k sshkeys.Key) fyne.CanvasObject {
	switch {
	case k.Err != "":
		return chip(theme.ErrorIcon(), "Ошибка", theme.ColorNameError)
	case !k.HasPrivate:
		return chip(theme.WarningIcon(), "Только .pub", theme.ColorNamePlaceHolder)
	case k.Encrypted:
		return chip(icLock, "Пароль", theme.ColorNameSuccess)
	default:
		return chip(icLockOpen, "Без пароля", theme.ColorNameWarning)
	}
}

func (p *sshPage) row(k sshkeys.Key) fyne.CanvasObject {
	name := truncated(txt(k.Name, theme.TextSize(), theme.ColorNameForeground, true))
	sub := k.Comment
	if k.Err != "" {
		sub = k.Err
	} else if sub == "" {
		sub = "без комментария"
	}
	keyCell := hstack(sp3, iconTile(icKey, theme.ColorNamePrimary, 32),
		vstack(2, name, truncated(muted(sub))))
	keyCell.Layout = &fillLast{gap: sp3}

	typeCell := container.NewHBox(chipPlain(algoLabel(k)))
	date := muted("—")
	if !k.Modified.IsZero() {
		date = muted(k.Modified.Format("02.01.2006"))
	}

	copyBtn := iconButton(theme.ContentCopyIcon(), func() { p.copyPub(k) })
	if k.PublicLine == "" || k.Err != "" {
		copyBtn.Disable()
	}
	var more *widget.Button
	more = iconButton(theme.MoreHorizontalIcon(), func() { p.menu(k, more) })
	chev := theme.MenuDropDownIcon()
	if p.expanded[k.Name] {
		chev = theme.MenuDropUpIcon()
	}
	actions := hstack(0, layout.NewSpacer(), copyBtn, more, square(20, widget.NewIcon(chev)))

	content := cols(sshCols, sshGap, keyCell, typeCell, protection(k), date, actions)
	row := header.NewPressable(content, func() {
		p.expanded[k.Name] = !p.expanded[k.Name]
		p.render()
	})
	row.Pad = fyne.NewSize(sp4, sp3)
	return row
}

// chipPlain — нейтральная плашка без иконки (тип ключа).
func chipPlain(s string) fyne.CanvasObject {
	frame := header.NewRect("", theme.ColorNameSeparator, 1, radiusChip)
	t := txt(s, theme.CaptionTextSize(), theme.ColorNameForeground, false)
	t.Mono = true
	return container.NewStack(frame, pad(sp2, 3, sp2, 3, t))
}

// details — раскрытая строка: отпечаток, файлы, публичный ключ, действия.
func (p *sshPage) details(k sshkeys.Key) fyne.CanvasObject {
	kv := func(label string, value fyne.CanvasObject) fyne.CanvasObject {
		return cols([]float32{124, -1}, sp3, capsLabel(label), value)
	}
	files := filepath.Base(k.PubPath)
	if k.HasPrivate {
		files = filepath.Base(k.PrivPath) + "  ·  " + files
	}
	info := vstack(sp3,
		kv("ОТПЕЧАТОК", truncated(mono(orDash(k.Fingerprint), theme.CaptionTextSize()+1))),
		kv("ФАЙЛЫ", truncated(mono(files, theme.CaptionTextSize()+1))),
	)
	if !k.Modified.IsZero() {
		info.Add(kv("ИЗМЕНЁН", muted(k.Modified.Format("02.01.2006 в 15:04"))))
	}

	var pub fyne.CanvasObject = muted("—")
	if k.PublicLine != "" {
		l := widget.NewLabel(k.PublicLine)
		l.TextStyle = fyne.TextStyle{Monospace: true}
		l.SizeName = theme.SizeNameCaptionText
		l.Wrapping = fyne.TextWrapBreak
		frame := header.NewRect(theme.ColorNameBackground, theme.ColorNameSeparator, 1, radiusChip)
		pub = container.NewStack(frame, pad(sp1, 0, sp1, 0, l))
	}

	copyPub := widget.NewButtonWithIcon("Копировать .pub", theme.ContentCopyIcon(), func() { p.copyPub(k) })
	copyPub.Importance = widget.HighImportance
	copyFp := widget.NewButtonWithIcon("Отпечаток", icFinger, func() { p.copyFingerprint(k) })
	if k.PublicLine == "" || k.Err != "" {
		copyPub.Disable()
		copyFp.Disable()
	}
	rename := widget.NewButtonWithIcon("Переименовать", theme.DocumentCreateIcon(), func() { p.openRename(k) })
	passText := "Сменить пароль"
	if !k.Encrypted {
		passText = "Задать пароль"
	}
	pass := widget.NewButtonWithIcon(passText, icShield, func() { p.openPassphrase(k) })
	if !k.HasPrivate || k.Err != "" {
		pass.Disable()
	}
	del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { p.openDelete(k) })
	del.Importance = widget.DangerImportance

	actions := hstack(sp2, copyPub, copyFp, layout.NewSpacer(), rename, pass, del)

	body := vstack(sp4, info, field("ПУБЛИЧНЫЙ КЛЮЧ", pub, nil), actions)
	bg := header.NewRect(theme.ColorNameHover, "", 0, 0)
	// Слева — под текстом имени, а не под иконкой: 16 + 32 + 12.
	return vstack(0, hline(), container.NewStack(bg, pad(sp4+32+sp3, sp4, sp4, sp4+2, body)))
}

func (p *sshPage) state(res fyne.Resource, c fyne.ThemeColorName, head, text string, action fyne.CanvasObject) fyne.CanvasObject {
	box := vstack(sp3, container.NewCenter(iconTile(res, c, 48)),
		container.NewCenter(title(head, theme.TextSize()+1)))
	if text != "" {
		box.Add(container.NewCenter(muted(text)))
	}
	if action != nil {
		box.Add(pad(0, sp2, 0, 0, container.NewCenter(action)))
	}
	return pad(sp6, sp6+sp4, sp6, sp6+sp4, box)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ---------- действия ----------

func (p *sshPage) menu(k sshkeys.Key, anchor fyne.CanvasObject) {
	item := func(label string, res fyne.Resource, fn func(), off bool) *fyne.MenuItem {
		m := fyne.NewMenuItem(label, fn)
		m.Icon = res
		m.Disabled = off
		return m
	}
	noPub := k.PublicLine == "" || k.Err != ""
	passText := "Сменить пароль…"
	if !k.Encrypted {
		passText = "Задать пароль…"
	}
	dir := filepath.Dir(k.PubPath)
	m := fyne.NewMenu("",
		item("Копировать публичный ключ", theme.ContentCopyIcon(), func() { p.copyPub(k) }, noPub),
		item("Копировать отпечаток", icFinger, func() { p.copyFingerprint(k) }, noPub),
		item("Открыть папку ~/.ssh", theme.FolderOpenIcon(), func() { p.openDir(dir) }, false),
		fyne.NewMenuItemSeparator(),
		item("Переименовать…", theme.DocumentCreateIcon(), func() { p.openRename(k) }, false),
		item(passText, icShield, func() { p.openPassphrase(k) }, !k.HasPrivate || k.Err != ""),
		fyne.NewMenuItemSeparator(),
		item("Удалить…", theme.DeleteIcon(), func() { p.openDelete(k) }, false),
	)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	pos = pos.Add(fyne.NewPos(0, anchor.Size().Height))
	widget.ShowPopUpMenuAtPosition(m, p.w.Canvas(), pos)
}

func (p *sshPage) copyPub(k sshkeys.Key) {
	p.w.Clipboard().SetContent(k.PublicLine)
	p.st.Notify(agent.SeverityInfo, "Публичный ключ скопирован", k.Name+".pub: вставьте его в authorized_keys на сервере")
}

func (p *sshPage) copyFingerprint(k sshkeys.Key) {
	p.w.Clipboard().SetContent(k.Fingerprint)
	p.st.Notify(agent.SeverityInfo, "Отпечаток скопирован", k.Name+": "+k.Fingerprint)
}

func (p *sshPage) openDir(dir string) {
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}
	if err := fyne.CurrentApp().OpenURL(u); err != nil {
		dialog.ShowError(err, p.w)
	}
}

func (p *sshPage) done(title, body string) {
	p.st.Notify(agent.SeveritySuccess, title, body)
	if !p.closed {
		p.reload()
	}
}

// ---------- окно «Новый ключ» ----------

func (p *sshPage) openCreate() {
	algo := sshkeys.ED25519
	name := widget.NewEntry()
	name.SetText(sshkeys.SuggestName(algo))
	nameTouched := false
	comment := widget.NewEntry()
	comment.SetText(sshkeys.DefaultComment())
	pass := widget.NewPasswordEntry()
	pass.PlaceHolder = "Необязательно"
	pass2 := widget.NewPasswordEntry()
	pass2.PlaceHolder = "Повторите"

	nameHint, passHint := hint(), hint()
	var f *formWin
	validate := func() {
		ok := true
		if msg := sshkeys.CheckName(strings.TrimSpace(name.Text)); msg != "" {
			setHint(nameHint, msg, theme.ColorNameError)
			ok = false
		} else {
			setHint(nameHint, "Файлы ~/.ssh/"+strings.TrimSpace(name.Text)+" и .pub", theme.ColorNamePlaceHolder)
		}
		ok = passHintFor(passHint, pass.Text, pass2.Text, false) && ok
		if f != nil {
			f.SetValid(ok)
		}
	}

	var tiles []*algoTile
	pick := func(a sshkeys.Algo) {
		algo = a
		for _, t := range tiles {
			t.setOn(t.algo == a)
		}
		if !nameTouched {
			name.SetText(sshkeys.SuggestName(a))
			nameTouched = false
		}
		validate()
	}
	tiles = []*algoTile{
		newAlgoTile(sshkeys.ED25519, "ED25519", "Рекомендуется. Быстрый и короткий", pick),
		newAlgoTile(sshkeys.RSA4096, "RSA 4096", "Для старых систем без ED25519", pick),
	}

	name.OnChanged = func(string) { nameTouched = true; validate() }
	pass.OnChanged = func(string) { validate() }
	pass2.OnChanged = func(string) { validate() }

	dir, _ := sshkeys.Dir()
	body := vstack(sp5,
		field("ТИП КЛЮЧА", container.NewGridWithColumns(2, tiles[0], tiles[1]), nil),
		field("ИМЯ ФАЙЛА", name, nameHint),
		field("КОММЕНТАРИЙ", comment, muted("Видно в authorized_keys на серверах, обычно user@host")),
		field("ПАРОЛЬ КЛЮЧА", container.NewGridWithColumns(2, pass, pass2), passHint),
	)
	f = showForm(formSpec{
		Key: "ssh-create", Title: "Новый SSH-ключ", Subtitle: "Пара ключей появится в " + dir,
		Icon: icKey, Body: body, Confirm: "Создать ключ", Width: 540, Focus: name,
		OnConfirm: func(f *formWin) {
			n, c, pw, a := strings.TrimSpace(name.Text), strings.TrimSpace(comment.Text), pass.Text, algo
			f.Busy("Создание…")
			go func() {
				k, err := sshkeys.Generate(n, c, pw, a)
				fyne.Do(func() {
					if err != nil {
						f.Fail(err)
						return
					}
					f.Close()
					p.done("Ключ создан", "~/.ssh/"+k.Name+" · "+k.Fingerprint)
				})
			}()
		},
	})
	if f == nil {
		return
	}
	f.Submit(name, comment, pass, pass2)
	pick(sshkeys.ED25519)
	nameTouched = false
}

// passHintFor — подсказка к паре «пароль / повтор». required — пустой нельзя.
func passHintFor(t *header.Text, a, b string, required bool) bool {
	switch {
	case a != b:
		setHint(t, "Пароли не совпадают", theme.ColorNameError)
		return false
	case a == "" && required:
		setHint(t, "Введите новый пароль", theme.ColorNamePlaceHolder)
		return false
	case a == "":
		setHint(t, "Без пароля ключ сработает у любого, кто получит файл", theme.ColorNameWarning)
	default:
		setHint(t, "Пароль будут спрашивать при каждом использовании ключа", theme.ColorNameSuccess)
	}
	return true
}

// algoTile — плитка выбора типа ключа с рамкой.
type algoTile struct {
	*fyne.Container
	algo  sshkeys.Algo
	frame *header.Rect
	radio *widget.Icon
}

func newAlgoTile(a sshkeys.Algo, name, desc string, pick func(sshkeys.Algo)) *algoTile {
	t := &algoTile{algo: a}
	t.frame = header.NewRect("", theme.ColorNameSeparator, 1, radiusCard)
	t.radio = widget.NewIcon(theme.RadioButtonIcon())
	body := hstack(sp3, square(18, t.radio),
		vstack(2, txt(name, theme.TextSize(), theme.ColorNameForeground, true), muted(desc)))
	press := header.NewPressable(body, func() { pick(a) })
	press.Pad = fyne.NewSize(sp3+2, sp3)
	t.Container = container.NewStack(t.frame, press)
	return t
}

func (t *algoTile) setOn(on bool) {
	if on {
		t.frame.StrokeName, t.frame.StrokeWidth = theme.ColorNamePrimary, 2
		t.radio.SetResource(tinted(theme.RadioButtonCheckedIcon(), theme.ColorNamePrimary))
	} else {
		t.frame.StrokeName, t.frame.StrokeWidth = theme.ColorNameSeparator, 1
		t.radio.SetResource(theme.RadioButtonIcon())
	}
	t.frame.Refresh()
}

// ---------- окно «Переименовать» ----------

func (p *sshPage) openRename(k sshkeys.Key) {
	name := widget.NewEntry()
	name.SetText(k.Name)
	h := hint()
	var f *formWin
	validate := func() {
		n := strings.TrimSpace(name.Text)
		ok := false
		switch msg := sshkeys.CheckName(n); {
		case n == k.Name:
			setHint(h, "Введите новое имя", theme.ColorNamePlaceHolder)
		case msg != "":
			setHint(h, msg, theme.ColorNameError)
		default:
			setHint(h, "Станет ~/.ssh/"+n+" и "+n+".pub", theme.ColorNamePlaceHolder)
			ok = true
		}
		if f != nil {
			f.SetValid(ok)
		}
	}
	name.OnChanged = func(string) { validate() }
	body := vstack(sp4,
		field("НОВОЕ ИМЯ ФАЙЛА", name, h),
		muted("Если ключ указан в ~/.ssh/config (IdentityFile), путь там нужно поправить."),
	)
	f = showForm(formSpec{
		Key: "ssh-rename-" + k.Name, Title: "Переименовать ключ", Subtitle: "~/.ssh/" + k.Name,
		Icon: theme.DocumentCreateIcon(), Body: body, Confirm: "Переименовать", Focus: name,
		OnConfirm: func(f *formWin) {
			nk, err := sshkeys.Rename(k, strings.TrimSpace(name.Text))
			if err != nil {
				f.Fail(err)
				return
			}
			if p.expanded[k.Name] {
				delete(p.expanded, k.Name)
				p.expanded[nk.Name] = true
			}
			f.Close()
			p.done("Ключ переименован", k.Name+" → "+nk.Name)
		},
	})
	if f == nil {
		return
	}
	f.Submit(name)
	validate()
}

// ---------- окно «Пароль ключа» ----------

func (p *sshPage) openPassphrase(k sshkeys.Key) {
	cur := widget.NewPasswordEntry()
	next := widget.NewPasswordEntry()
	next.PlaceHolder = "Новый пароль"
	next2 := widget.NewPasswordEntry()
	next2.PlaceHolder = "Повторите"
	h := hint()
	var f *formWin
	validate := func() {
		ok := true
		if k.Encrypted {
			switch {
			case next.Text != next2.Text:
				setHint(h, "Пароли не совпадают", theme.ColorNameError)
				ok = false
			case next.Text == "":
				setHint(h, "Пустой новый пароль снимет защиту с ключа", theme.ColorNameWarning)
			default:
				setHint(h, "Старый пароль перестанет подходить", theme.ColorNamePlaceHolder)
			}
			ok = ok && cur.Text != ""
		} else {
			ok = passHintFor(h, next.Text, next2.Text, true)
		}
		if f != nil {
			f.SetValid(ok)
		}
	}
	cur.OnChanged = func(string) { validate() }
	next.OnChanged = func(string) { validate() }
	next2.OnChanged = func(string) { validate() }

	body := vstack(sp5)
	var focus fyne.Focusable = next
	if k.Encrypted {
		body.Add(field("ТЕКУЩИЙ ПАРОЛЬ", cur, nil))
		focus = cur
	}
	body.Add(field("НОВЫЙ ПАРОЛЬ", container.NewGridWithColumns(2, next, next2), h))

	t, confirm := "Сменить пароль", "Сохранить"
	if !k.Encrypted {
		t, confirm = "Задать пароль", "Задать пароль"
	}
	f = showForm(formSpec{
		Key: "ssh-pass-" + k.Name, Title: t, Subtitle: "~/.ssh/" + k.Name,
		Icon: icShield, IconColor: theme.ColorNameSuccess, Body: body, Confirm: confirm, Focus: focus,
		OnConfirm: func(f *formWin) {
			c, n := cur.Text, next.Text
			f.Busy("Сохранение…")
			go func() {
				err := sshkeys.ChangePassphrase(k, c, n)
				fyne.Do(func() {
					if err != nil {
						if errors.Is(err, sshkeys.ErrWrongPassphrase) {
							err = errors.New("Текущий пароль не подошёл")
						}
						f.Fail(err)
						return
					}
					f.Close()
					msg := "Пароль ключа изменён"
					if n == "" {
						msg = "Пароль с ключа снят"
					}
					p.done(msg, "~/.ssh/"+k.Name)
				})
			}()
		},
	})
	if f == nil {
		return
	}
	f.Submit(cur, next, next2)
	validate()
}

// ---------- окно «Удалить» ----------

func (p *sshPage) openDelete(k sshkeys.Key) {
	files := vstack(sp1)
	if k.HasPrivate {
		files.Add(mono("~/.ssh/"+filepath.Base(k.PrivPath), theme.CaptionTextSize()+1))
	}
	files.Add(mono("~/.ssh/"+filepath.Base(k.PubPath), theme.CaptionTextSize()+1))
	frame := header.NewRect(theme.ColorNameInputBackground, theme.ColorNameSeparator, 1, radiusChip)

	warn := widget.NewLabel("Это нельзя отменить. Серверы, где стоит этот ключ, перестанут пускать по нему.")
	warn.Wrapping = fyne.TextWrapWord
	body := vstack(sp4,
		field("БУДУТ УДАЛЕНЫ", container.NewStack(frame, padXY(sp3, sp3-2, files)), nil),
		pad(-sp2, 0, -sp2, 0, warn),
	)
	showForm(formSpec{
		Key: "ssh-delete-" + k.Name, Title: "Удалить ключ «" + k.Name + "»", Subtitle: algoLabel(k) + "  ·  " + sshkeys.ShortFingerprint(k.Fingerprint),
		Icon: theme.DeleteIcon(), IconColor: theme.ColorNameError, Body: body, Confirm: "Удалить", Danger: true,
		OnConfirm: func(f *formWin) {
			if err := sshkeys.Delete(k); err != nil {
				f.Fail(err)
				return
			}
			delete(p.expanded, k.Name)
			f.Close()
			p.done("Ключ удалён", "~/.ssh/"+k.Name)
		},
	})
}

// ---------- мелочи ----------

// fillLast — ряд, где последний элемент забирает всю оставшуюся ширину
// (текст рядом с иконкой обрезается, а не распирает колонку).
type fillLast struct{ gap float32 }

func (l *fillLast) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var x float32
	for i, o := range objs {
		m := o.MinSize()
		w := m.Width
		if i == len(objs)-1 {
			w = fyne.Max(0, size.Width-x)
		}
		o.Move(fyne.NewPos(x, (size.Height-m.Height)/2))
		o.Resize(fyne.NewSize(w, m.Height))
		x += w + l.gap
	}
}

func (l *fillLast) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for i, o := range objs {
		om := o.MinSize()
		m.Width += om.Width
		if i > 0 {
			m.Width += l.gap
		}
		m.Height = fyne.Max(m.Height, om.Height)
	}
	return m
}

// fixedW — ровно w в ширину.
func fixedW(w float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&fixedWLayout{w}, o)
}

type fixedWLayout struct{ w float32 }

func (l *fixedWLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l *fixedWLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		h = fyne.Max(h, o.MinSize().Height)
	}
	return fyne.NewSize(l.w, h)
}

// plural — «1 ключ», «3 ключа», «5 ключей».
func plural(n int, one, few, many string) string {
	w := many
	switch {
	case n%10 == 1 && n%100 != 11:
		w = one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		w = few
	}
	return fmt.Sprintf("%d %s", n, w)
}
