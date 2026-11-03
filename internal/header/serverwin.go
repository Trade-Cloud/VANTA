package header

import (
	"net"
	"regexp"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
)

// WindowHook вызывается для каждого окна, которое открывает header (до Show).
// main подключает сюда debug-мост.
var WindowHook func(fyne.Window)

// serverWins — открытые окна серверов по ID ("" — новый): повторное
// открытие поднимает уже открытое окно, а не плодит копии.
var serverWins = map[string]fyne.Window{}

var serverWinSize = fyne.NewSize(520, 600)

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$`)

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
	group          *widget.Select
	nameErr, ipErr *vText
	envRows        map[agent.Env]*Pressable
	env            agent.Env
	fav            *widget.Check
	banner         *fyne.Container
	save           *widget.Button
	touched        map[string]bool
}

func newServerForm(h *Header, sv agent.Server, close func()) *serverForm {
	if sv.Env == "" {
		sv.Env = agent.EnvProduction
	}
	return &serverForm{h: h, sv: sv, close: close, env: sv.Env, touched: map[string]bool{}}
}

func (f *serverForm) build(isNew bool, title string) fyne.CanvasObject {
	// ---- шапка ----
	sub := "Подключение VPS к VANTA"
	if !isNew {
		sub = f.sv.IP + " · " + f.sv.Env.Label()
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

	conn := container.NewVBox(
		section("ПОДКЛЮЧЕНИЕ"),
		container.NewGridWithColumns(2,
			field("Название", f.name, f.nameErr),
			field("IP-адрес или хост", f.ip, f.ipErr),
		),
	)

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

	body := inset(24, 16, container.NewVBox(conn, spacerH(8), envBox, spacerH(8), org, f.banner))

	// ---- подвал ----
	cancel := widget.NewButton("Отмена", f.close)
	saveLabel := "Добавить сервер"
	if !isNew {
		saveLabel = "Сохранить"
	}
	f.save = widget.NewButtonWithIcon(saveLabel, theme.ConfirmIcon(), f.submit)
	f.save.Importance = widget.HighImportance
	footer := container.NewStack(
		newVRect(theme.ColorNameHeaderBackground),
		inset(24, 12, container.NewHBox(spacer(), cancel, f.save)),
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

	show := func(t *vText, msg string, touched bool) {
		if !touched {
			msg = ""
		}
		t.Text = msg
		t.Refresh()
	}
	show(f.nameErr, nameErr, f.touched["name"])
	show(f.ipErr, ipErr, f.touched["ip"])
	return nameErr == "" && ipErr == ""
}

func (f *serverForm) submit() {
	f.touched["name"], f.touched["ip"] = true, true
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
