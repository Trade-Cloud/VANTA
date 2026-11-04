package header

import (
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/sshkeys"
)

// WindowHook вызывается для каждого окна, которое открывает header (до Show).
// main подключает сюда debug-мост.
var WindowHook func(fyne.Window)

// serverWins — открытые окна серверов по ID ("" — новый): повторное
// открытие поднимает уже открытое окно, а не плодит копии.
var serverWins = map[string]fyne.Window{}

var serverWinSize = fyne.NewSize(540, 680)

var (
	hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$`)
	userRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*\$?$`)
)

// editServer открывает отдельное окно добавления/редактирования сервера.
func (h *Header) editServer(sv agent.Server) {
	if w, ok := serverWins[sv.ID]; ok {
		w.RequestFocus()
		return
	}
	isNew := sv.ID == ""
	title := "Новый сервер"
	if !isNew {
		title = sv.Name
	}

	w := fyne.CurrentApp().NewWindow(title + " · VANTA")
	serverWins[sv.ID] = w
	closeWin := func() {
		delete(serverWins, sv.ID)
		w.Close()
	}
	// Своё OnClosed ставить нельзя (его занимает debug-мост) — перехватываем закрытие.
	w.SetCloseIntercept(closeWin)

	f := newServerForm(h, sv, closeWin)
	w.SetContent(f.build(isNew, title))
	w.Resize(serverWinSize)
	w.SetFixedSize(true)
	w.CenterOnScreen()
	w.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		if e.Name == fyne.KeyEscape {
			closeWin()
		}
	})
	if WindowHook != nil {
		WindowHook(w)
	}
	w.Show()
	w.Canvas().Focus(f.name)
}

type serverForm struct {
	h     *Header
	sv    agent.Server
	close func()

	name, ip       *widget.Entry
	user, port     *widget.Entry
	group          *widget.Select
	nameErr, ipErr *vText
	userErr        *vText
	portErr        *vText
	envRows        map[agent.Env]*Pressable
	env            agent.Env
	fav            *widget.Check
	banner         *fyne.Container
	save           *widget.Button
	del            *widget.Button
	delArmed       bool
	touched        map[string]bool

	// Вход.
	authRows map[agent.Auth]*Pressable
	auth     agent.Auth
	authBox  *fyne.Container
	keys     []sshkeys.Key
	keyByLbl map[string]sshkeys.Key
	keySel   *widget.Select
	keyNote  *vText
	keyHint  string // подсказка под выбором ключа, когда ошибки нет
	secret   *widget.Entry
	remember *widget.Check
}

func newServerForm(h *Header, sv agent.Server, close func()) *serverForm {
	if sv.Env == "" {
		sv.Env = agent.EnvProduction
	}
	if sv.ID == "" {
		sv.Remember = true // по умолчанию пароль кладём в связку ключей
	}
	return &serverForm{h: h, sv: sv, close: close, env: sv.Env, auth: sv.AuthKind(), touched: map[string]bool{}}
}

func (f *serverForm) build(isNew bool, title string) fyne.CanvasObject {
	// ---- шапка ----
	sub := "Подключение VPS к VANTA"
	if !isNew {
		sub = f.sv.Login() + " · " + f.sv.Env.Label()
	}
	head := container.NewStack(
		newVRect(theme.ColorNameHeaderBackground),
		inset(24, 18, container.NewBorder(nil, nil,
			icon(theme.ComputerIcon(), 32), nil,
			container.NewVBox(
				text(title, 20, theme.ColorNameForeground, true),
				small(sub),
			),
		)),
	)

	// ---- поля ----
	f.name = f.entry(f.sv.Name, "production-01", "name")
	f.ip = f.entry(f.sv.IP, "185.104.112.21 или host.example.com", "ip")
	f.nameErr = text("", theme.CaptionTextSize(), theme.ColorNameError, false)
	f.ipErr = text("", theme.CaptionTextSize(), theme.ColorNameError, false)

	f.user = f.entry(f.sv.User, agent.DefaultUser, "user")
	port := ""
	if f.sv.Port > 0 {
		port = strconv.Itoa(f.sv.Port)
	}
	f.port = f.entry(port, strconv.Itoa(agent.DefaultPort), "port")
	f.userErr = text("", theme.CaptionTextSize(), theme.ColorNameError, false)
	f.portErr = text("", theme.CaptionTextSize(), theme.ColorNameError, false)

	conn := container.NewVBox(
		section("ПОДКЛЮЧЕНИЕ"),
		container.NewGridWithColumns(2,
			field("Название", f.name, f.nameErr),
			field("IP-адрес или хост", f.ip, f.ipErr),
		),
		container.NewGridWithColumns(2,
			field("Пользователь", f.user, f.userErr),
			field("Порт SSH", f.port, f.portErr),
		),
	)

	authBox := f.buildAuth()

	// Окружение — сегменты вместо выпадающего списка.
	f.envRows = map[agent.Env]*Pressable{}
	seg := container.NewGridWithColumns(3)
	for _, e := range []agent.Env{agent.EnvProduction, agent.EnvStaging, agent.EnvDevelopment} {
		e := e
		desc := map[agent.Env]string{
			agent.EnvProduction:  "боевой",
			agent.EnvStaging:     "предрелизный",
			agent.EnvDevelopment: "для разработки",
		}[e]
		row := NewPressable(container.NewVBox(
			container.NewHBox(envBadge(e), text(e.Label(), theme.TextSize(), theme.ColorNameForeground, true)),
			small(desc),
		), func() { f.setEnv(e) })
		row.Pad = fyne.NewSize(12, 10)
		f.envRows[e] = row
		seg.Add(container.NewStack(segBorder(), row))
	}
	f.setEnv(f.env)
	envBox := container.NewVBox(section("ОКРУЖЕНИЕ"), seg)

	f.fav = widget.NewCheck("Добавить в избранное", nil)
	f.fav.SetChecked(f.sv.Favorite)
	// Группа — только выбор из списка существующих групп.
	f.group = widget.NewSelect(append([]string{noGroup}, f.groups()...), nil)
	f.group.SetSelected(noGroup)
	if f.sv.Group != "" {
		f.group.SetSelected(f.sv.Group)
	}
	org := container.NewVBox(section("ОРГАНИЗАЦИЯ"), field("Группа", f.group, nil), f.fav)

	f.banner = container.NewVBox()

	body := inset(24, 16, container.NewVBox(conn, spacerH(8), authBox, spacerH(8), envBox, spacerH(8), org, f.banner))

	// ---- подвал ----
	cancel := widget.NewButton("Отмена", f.close)
	saveLabel := "Добавить сервер"
	if !isNew {
		saveLabel = "Сохранить"
	}
	f.save = widget.NewButtonWithIcon(saveLabel, theme.ConfirmIcon(), f.submit)
	f.save.Importance = widget.HighImportance
	foot := container.NewHBox()
	if !isNew {
		f.del = widget.NewButtonWithIcon("Удалить", theme.DeleteIcon(), f.remove)
		foot.Add(f.del)
	}
	foot.Add(spacer())
	foot.Add(cancel)
	foot.Add(f.save)
	footer := container.NewStack(
		newVRect(theme.ColorNameHeaderBackground),
		inset(24, 12, foot),
	)

	f.validate()
	return container.NewBorder(
		container.NewVBox(head, hsep()),
		container.NewVBox(hsep(), footer),
		nil, nil,
		container.NewVScroll(body),
	)
}

func (f *serverForm) entry(val, placeholder, key string) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(val)
	e.PlaceHolder = placeholder
	e.OnChanged = func(string) {
		if key != "" {
			f.touched[key] = true
		}
		f.validate()
	}
	e.OnSubmitted = func(string) { f.submit() }
	return e
}

func (f *serverForm) setEnv(e agent.Env) {
	f.env = e
	for k, r := range f.envRows {
		r.SetSelected(k == e)
	}
}

const noGroup = "Без группы"

// groups — группы, которые уже есть у серверов, по алфавиту.
func (f *serverForm) groups() []string {
	list, _, _, _ := f.h.st.ServersState()
	seen := map[string]bool{}
	var groups []string
	for _, s := range list {
		if s.Group != "" && !seen[s.Group] {
			seen[s.Group] = true
			groups = append(groups, s.Group)
		}
	}
	sort.Strings(groups)
	return groups
}

// validate проверяет поля; ошибки показывает только у тронутых полей.
func (f *serverForm) validate() bool {
	name := strings.TrimSpace(f.name.Text)
	ip := strings.TrimSpace(f.ip.Text)

	nameErr := ""
	switch {
	case name == "":
		nameErr = "Укажите название"
	default:
		list, _, _, _ := f.h.st.ServersState()
		for _, s := range list {
			if s.ID != f.sv.ID && strings.EqualFold(s.Name, name) {
				nameErr = "Такое название уже есть"
			}
		}
	}
	ipErr := ""
	switch {
	case ip == "":
		ipErr = "Укажите IP-адрес или хост"
	case net.ParseIP(ip) == nil && !hostRe.MatchString(ip):
		ipErr = "Это не IP-адрес и не имя хоста"
	}
	userErr := ""
	if u := strings.TrimSpace(f.user.Text); u != "" && !userRe.MatchString(u) {
		userErr = "Только латиница, цифры, _ . -"
	}
	portErr := ""
	if p := strings.TrimSpace(f.port.Text); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			portErr = "Число от 1 до 65535"
		}
	}
	keyErr := ""
	if f.auth == agent.AuthKey && f.selectedKey().PrivPath == "" {
		keyErr = "Выберите ключ"
	}

	show := func(t *vText, msg string, touched bool) {
		if !touched {
			msg = ""
		}
		t.Text = msg
		t.Refresh()
	}
	show(f.nameErr, nameErr, f.touched["name"])
	show(f.ipErr, ipErr, f.touched["ip"])
	show(f.userErr, userErr, true)
	show(f.portErr, portErr, true)
	if f.keyNote != nil {
		if f.touched["key"] && keyErr != "" {
			f.keyNote.Text, f.keyNote.ColorName = keyErr, theme.ColorNameError
		} else {
			f.keyNote.Text, f.keyNote.ColorName = f.keyHint, theme.ColorNamePlaceHolder
		}
		f.keyNote.Refresh()
	}
	return nameErr == "" && ipErr == "" && userErr == "" && portErr == "" && keyErr == ""
}

func (f *serverForm) submit() {
	f.touched["name"], f.touched["ip"], f.touched["key"] = true, true, true
	if !f.validate() || f.save.Disabled() {
		return
	}
	sv := f.sv
	sv.Name = strings.TrimSpace(f.name.Text)
	sv.IP = strings.TrimSpace(f.ip.Text)
	sv.Group = f.group.Selected
	if sv.Group == noGroup {
		sv.Group = ""
	}
	sv.Env = f.env
	sv.Favorite = f.fav.Checked
	sv.User = strings.TrimSpace(f.user.Text)
	sv.Port, _ = strconv.Atoi(strings.TrimSpace(f.port.Text))
	sv.Auth = f.auth
	sv.KeyPath = ""
	if f.auth == agent.AuthKey {
		sv.KeyPath = f.selectedKey().PrivPath
	}
	sv.Secret = ""
	sv.Remember = false
	if f.needsSecret() {
		sv.Secret = f.secret.Text
		sv.Remember = f.remember.Checked
	}
	isNew := sv.ID == ""

	label := f.save.Text
	f.save.SetText("Сохранение…")
	f.save.Disable()
	f.banner.Objects = nil
	f.banner.Refresh()

	f.h.st.SaveServer(sv, func(saved agent.Server, err error) {
		if err != nil {
			f.save.SetText(label)
			f.save.Enable()
			f.banner.Objects = []fyne.CanvasObject{spacerH(8), banner(theme.ColorNameError, "Не удалось сохранить: "+err.Error())}
			f.banner.Refresh()
			return
		}
		if isNew {
			f.h.st.SetActive(saved.ID)
		}
		f.close()
	})
}

// remove — удаление в два нажатия: первое спрашивает «точно?» прямо на
// кнопке, второе удаляет. Отдельного окна подтверждения не нужно.
func (f *serverForm) remove() {
	if !f.delArmed {
		f.delArmed = true
		f.del.SetText("Точно удалить?")
		f.del.Importance = widget.DangerImportance
		f.del.Refresh()
		return
	}
	f.del.Disable()
	f.save.Disable()
	f.h.st.DeleteServer(f.sv.ID, func(err error) {
		if err != nil {
			f.del.Enable()
			f.save.Enable()
			f.banner.Objects = []fyne.CanvasObject{spacerH(8), banner(theme.ColorNameError, "Не удалось удалить: "+err.Error())}
			f.banner.Refresh()
			return
		}
		f.close()
	})
}

// ---------- вход: ключ или пароль ----------

func (f *serverForm) buildAuth() fyne.CanvasObject {
	// Ключи — только те, у которых есть приватная половина.
	all, err := sshkeys.List()
	f.keyByLbl = map[string]sshkeys.Key{}
	var labels []string
	for _, k := range all {
		if !k.HasPrivate {
			continue
		}
		lbl := k.Name
		if k.Type != "" {
			lbl += " · " + k.Type
		}
		f.keys = append(f.keys, k)
		f.keyByLbl[lbl] = k
		labels = append(labels, lbl)
	}
	// Сохранённый ключ, которого больше нет в ~/.ssh, всё равно показываем.
	if p := f.sv.KeyPath; p != "" && !f.hasKey(p) {
		lbl := shortPath(p)
		f.keyByLbl[lbl] = sshkeys.Key{Name: lbl, PrivPath: p, HasPrivate: true}
		labels = append(labels, lbl)
	}

	f.keyHint = "Ключи из ~/.ssh, создать новый — во вкладке SSH"
	switch {
	case err != nil:
		f.keyHint = "Не удалось прочитать ~/.ssh: " + err.Error()
	case len(labels) == 0:
		f.keyHint = "В ~/.ssh нет ключей — создайте во вкладке SSH или войдите по паролю"
	}
	f.keyNote = small(f.keyHint)

	f.keySel = widget.NewSelect(labels, func(string) {
		f.touched["key"] = true
		f.refreshAuth()
		f.validate()
	})
	f.keySel.PlaceHolder = "Выберите ключ"
	for lbl, k := range f.keyByLbl {
		if f.sv.KeyPath != "" && k.PrivPath == f.sv.KeyPath {
			f.keySel.Selected = lbl
		}
	}
	if f.keySel.Selected == "" {
		f.keySel.Selected = defaultKeyLabel(labels, f.keyByLbl)
	}

	f.secret = widget.NewPasswordEntry()
	f.secret.OnSubmitted = func(string) { f.submit() }
	f.remember = widget.NewCheck("Запомнить в связке ключей", nil)
	f.remember.SetChecked(f.sv.Remember)
	f.remember.OnChanged = func(bool) { f.refreshAuth() }

	f.authRows = map[agent.Auth]*Pressable{}
	seg := container.NewGridWithColumns(2)
	for _, a := range []struct {
		auth        agent.Auth
		title, desc string
	}{
		{agent.AuthKey, "SSH-ключ", "локальный ключ из ~/.ssh"},
		{agent.AuthPassword, "Пароль", "пароль пользователя"},
	} {
		a := a
		row := NewPressable(container.NewVBox(
			text(a.title, theme.TextSize(), theme.ColorNameForeground, true),
			small(a.desc),
		), func() { f.setAuth(a.auth) })
		row.Pad = fyne.NewSize(12, 10)
		f.authRows[a.auth] = row
		seg.Add(container.NewStack(segBorder(), row))
	}
	f.authBox = container.NewVBox()
	f.setAuth(f.auth)
	return container.NewVBox(section("ВХОД"), seg, f.authBox)
}

// defaultKeyLabel — id_ed25519, если есть, иначе первый ключ.
func defaultKeyLabel(labels []string, byLbl map[string]sshkeys.Key) string {
	for _, l := range labels {
		if byLbl[l].Name == "id_ed25519" {
			return l
		}
	}
	if len(labels) > 0 {
		return labels[0]
	}
	return ""
}

func (f *serverForm) hasKey(path string) bool {
	for _, k := range f.keys {
		if k.PrivPath == path {
			return true
		}
	}
	return false
}

func (f *serverForm) selectedKey() sshkeys.Key {
	if f.keySel == nil {
		return sshkeys.Key{}
	}
	return f.keyByLbl[f.keySel.Selected]
}

func (f *serverForm) setAuth(a agent.Auth) {
	f.auth = a
	for k, r := range f.authRows {
		r.SetSelected(k == a)
	}
	f.refreshAuth()
	f.validate()
}

// needsSecret — нужен ли пароль: для входа по паролю всегда, для ключа —
// если он под passphrase.
func (f *serverForm) needsSecret() bool {
	return f.auth == agent.AuthPassword || f.selectedKey().Encrypted
}

// refreshAuth пересобирает поля под выбранный способ входа.
func (f *serverForm) refreshAuth() {
	var objs []fyne.CanvasObject
	if f.auth == agent.AuthKey {
		objs = append(objs, field("Ключ", f.keySel, f.keyNote))
	}
	if f.needsSecret() {
		label := "Пароль"
		if f.auth == agent.AuthKey {
			label = "Пароль ключа"
		}
		// Секрет уже лежит в связке ключей, и способ входа не меняли.
		stored := f.sv.Remember && f.sv.AuthKind() == f.auth &&
			(f.auth == agent.AuthPassword || f.sv.KeyPath == f.selectedKey().PrivPath)
		switch {
		case !f.remember.Checked:
			f.secret.SetPlaceHolder("Спросим при каждом подключении")
		case stored:
			f.secret.SetPlaceHolder("Сохранён — оставьте пустым, чтобы не менять")
		case f.auth == agent.AuthKey:
			f.secret.SetPlaceHolder("Можно не вводить: спросим при подключении или возьмём из ssh-agent")
		default:
			f.secret.SetPlaceHolder("Можно не вводить: спросим при подключении")
		}
		if !f.remember.Checked {
			f.secret.SetText("")
			f.secret.Disable()
		} else {
			f.secret.Enable()
		}
		objs = append(objs, field(label, f.secret, nil), f.remember)
	}
	f.authBox.Objects = objs
	f.authBox.Refresh()
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// ---------- кирпичики окна ----------

func section(title string) fyne.CanvasObject {
	return inset(0, 6, caps(title))
}

// field — подпись над полем и строка ошибки/подсказки под ним.
func field(label string, input fyne.CanvasObject, note *vText) fyne.CanvasObject {
	box := container.NewVBox(text(label, theme.CaptionTextSize(), theme.ColorNameForeground, true), input)
	if note != nil {
		box.Add(note)
	}
	return box
}

func segBorder() fyne.CanvasObject {
	r := newVRect("")
	r.StrokeName, r.StrokeWidth, r.Radius = theme.ColorNameInputBorder, 1, theme.InputRadiusSize()
	return r
}

func banner(sev fyne.ThemeColorName, msg string) fyne.CanvasObject {
	bg := newVRect(sev)
	bg.Alpha, bg.Radius = 0x30, theme.InputRadiusSize()
	l := widget.NewLabel(msg)
	l.Wrapping = fyne.TextWrapWord
	return container.NewStack(bg, inset(8, 0, container.NewBorder(nil, nil,
		icon(theme.NewColoredResource(theme.ErrorIcon(), sev), 18), nil, l)))
}

func spacerH(h float32) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(1, h), newVRect(""))
}
