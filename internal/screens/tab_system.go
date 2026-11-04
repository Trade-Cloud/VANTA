package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/host"
)

// ---------- Services ----------

type servicesPage struct {
	*base
	obj   fyne.CanvasObject
	g     *grid
	list  []host.Service
	show  *widget.Select
	sel   selButtons
	label *widget.Label
}

func newServicesPage(env *toolEnv) toolPage {
	p := &servicesPage{base: newBase(env)}
	p.g = newGrid([]string{"Сервис", "Состояние", "Подсостояние", "Автозапуск", "Описание"},
		[]float32{240, 90, 100, 100, 420})
	p.label = widget.NewLabel("Выберите сервис")
	p.g.OnPick = func(k string) {
		p.sel.set(k != "")
		if k == "" {
			p.label.SetText("Выберите сервис")
		} else {
			p.label.SetText("Выбран: " + k)
		}
	}
	p.show = widget.NewSelect([]string{"Все", "Запущенные", "Остановленные", "С ошибкой", "Автозапуск"}, func(string) { p.render() })
	p.show.SetSelected("Все")

	actions := container.NewHBox()
	for _, a := range host.ServiceActions {
		a := a
		actions.Add(p.sel.add(btn(a.Label, nil, func() { p.action(a.Verb, a.Label, a.Danger) })))
	}
	status := p.sel.add(btn("Статус", theme.InfoIcon(), p.status))
	unit := p.sel.add(btn("Юнит", theme.DocumentIcon(), p.unitFile))
	logs := p.sel.add(btn("Логи", theme.ListIcon(), p.logs))
	reload := btn("daemon-reload", theme.ViewRefreshIcon(), func() {
		p.act(false, "systemctl daemon-reload", "", "", func(ctx context.Context) error { return p.env.h.DaemonReload(ctx) }, p.reload)
	})

	top := container.NewVBox(
		p.head("Services", reload, btn("Обновить", theme.ViewRefreshIcon(), p.reload)),
		container.NewBorder(nil, nil, p.show, nil, filterEntry(p.g)),
		container.NewHScroll(actions),
		container.NewHBox(p.label, status, unit, logs),
	)
	p.obj = container.NewBorder(top, nil, nil, nil, p.g.t)
	p.reload()
	return p
}

func (p *servicesPage) object() fyne.CanvasObject { return p.obj }

func (p *servicesPage) reload() {
	var list []host.Service
	p.load(func(ctx context.Context) (err error) {
		if !p.env.h.HasSystemd(ctx) {
			return fmt.Errorf("на сервере нет systemd")
		}
		list, err = p.env.h.Services(ctx)
		return err
	}, func() {
		p.list = list
		p.render()
	})
}

func (p *servicesPage) render() {
	var rows [][]string
	var keys []string
	running, failed := 0, 0
	for _, s := range p.list {
		if s.Sub == "running" {
			running++
		}
		if s.Active == "failed" {
			failed++
		}
		switch p.show.Selected {
		case "Запущенные":
			if s.Active != "active" {
				continue
			}
		case "Остановленные":
			if s.Active == "active" {
				continue
			}
		case "С ошибкой":
			if s.Active != "failed" {
				continue
			}
		case "Автозапуск":
			if s.Enabled != "enabled" {
				continue
			}
		}
		rows = append(rows, []string{s.Name(), s.Active, s.Sub, s.Enabled, s.Description})
		keys = append(keys, s.Unit)
	}
	p.g.set(rows, keys)
	p.setInfo(fmt.Sprintf("Сервисов: %d · запущено: %d · с ошибкой: %d", len(p.list), running, failed))
}

func (p *servicesPage) action(verb, label string, danger bool) {
	unit := p.g.selected()
	if unit == "" {
		return
	}
	q := fmt.Sprintf("%s %s на %s?", label, unit, p.env.sv.Name)
	if strings.HasPrefix(unit, "ssh") && (verb == "stop" || verb == "disable" || verb == "mask") {
		q += "\n\nВнимание: это SSH-сервер. Можно потерять доступ к серверу."
	}
	p.act(danger, label+": "+unit, q, label,
		func(ctx context.Context) error { return p.env.h.ServiceAction(ctx, verb, unit) }, p.reload)
}

func (p *servicesPage) status() {
	unit := p.g.selected()
	var out string
	p.load(func(ctx context.Context) (err error) {
		out, err = p.env.h.ServiceStatus(ctx, unit)
		return err
	}, func() { showText("svc:"+p.env.sv.ID+unit, "Статус "+unit, out) })
}

func (p *servicesPage) unitFile() {
	unit := p.g.selected()
	var out string
	p.load(func(ctx context.Context) (err error) {
		out, err = p.env.h.UnitFile(ctx, unit)
		return err
	}, func() { showText("unit:"+p.env.sv.ID+unit, unit, out) })
}

func (p *servicesPage) logs() {
	unit := p.g.selected()
	var list []host.LogEntry
	p.load(func(ctx context.Context) (err error) {
		list, err = p.env.h.Journal(ctx, host.JournalQuery{Unit: unit, Priority: -1, Lines: 300})
		return err
	}, func() { showText("svclog:"+p.env.sv.ID+unit, "Логи "+unit, formatLog(list)) })
}

func formatLog(list []host.LogEntry) string {
	var b strings.Builder
	for _, e := range list {
		fmt.Fprintf(&b, "%s %-7s %s[%s]: %s\n", e.At.Format("2006-01-02 15:04:05"), e.Level(), e.Ident, e.PID, e.Message)
	}
	return b.String()
}

// ---------- Users ----------

type usersPage struct {
	*base
	obj    fyne.CanvasObject
	g      *grid
	gg     *grid
	users  []host.User
	groups []host.Group
	system *widget.Check
	sel    selButtons
	gsel   selButtons
}

func newUsersPage(env *toolEnv) toolPage {
	p := &usersPage{base: newBase(env)}
	p.g = newGrid([]string{"Имя", "UID", "Группы", "Домашний каталог", "Оболочка", "Sudo", "Заблокирован"},
		[]float32{130, 60, 260, 200, 150, 60, 110})
	p.g.OnPick = func(k string) { p.sel.set(k != "") }
	p.gg = newGrid([]string{"Группа", "GID", "Участники"}, []float32{180, 70, 420})
	p.gg.OnPick = func(k string) { p.gsel.set(k != "") }
	p.system = widget.NewCheck("Системные", func(bool) { p.render() })

	add := btn("Новый пользователь", theme.ContentAddIcon(), p.addUser)
	add.Importance = widget.HighImportance
	pass := p.sel.add(btn("Пароль", theme.AccountIcon(), p.setPassword))
	lock := p.sel.add(btn("Блок/разблок", theme.VisibilityOffIcon(), p.toggleLock))
	grp := p.sel.add(btn("В группу", theme.ContentAddIcon(), p.addToGroup))
	ungrp := p.sel.add(btn("Из группы", theme.ContentRemoveIcon(), p.removeFromGroup))
	shell := p.sel.add(btn("Оболочка", theme.ComputerIcon(), p.setShell))
	keys := p.sel.add(btn("SSH-ключи", theme.LoginIcon(), p.keys))
	addKey := p.sel.add(btn("Добавить ключ", theme.ContentAddIcon(), p.addKey))
	del := p.sel.add(btn("Удалить", theme.DeleteIcon(), p.deleteUser))
	del.Importance = widget.DangerImportance

	gadd := btn("Новая группа", theme.ContentAddIcon(), func() {
		p.ask("Новая группа", "Создать", []askField{{Label: "Имя группы"}}, false,
			func(ctx context.Context, v []string) error { return p.env.h.AddGroup(ctx, v[0]) }, p.reload)
	})
	gdel := p.gsel.add(btn("Удалить группу", theme.DeleteIcon(), func() {
		g := p.gg.selected()
		p.act(true, "Удалить группу "+g, "Удалить группу "+g+" на "+p.env.sv.Name+"?", "Удалить",
			func(ctx context.Context) error { return p.env.h.DeleteGroup(ctx, g) }, p.reload)
	}))

	usersTab := container.NewBorder(container.NewVBox(
		container.NewBorder(nil, nil, p.system, add, filterEntry(p.g)),
		container.NewHScroll(container.NewHBox(pass, lock, grp, ungrp, shell, keys, addKey, del)),
	), nil, nil, nil, p.g.t)
	groupsTab := container.NewBorder(container.NewBorder(nil, nil, nil, container.NewHBox(gadd, gdel), filterEntry(p.gg)), nil, nil, nil, p.gg.t)
	tabs := container.NewAppTabs(container.NewTabItem("Пользователи", usersTab), container.NewTabItem("Группы", groupsTab))
	p.obj = container.NewBorder(p.head("Users", btn("Обновить", theme.ViewRefreshIcon(), p.reload)), nil, nil, nil, tabs)
	p.reload()
	return p
}

func (p *usersPage) object() fyne.CanvasObject { return p.obj }

func (p *usersPage) reload() {
	var us []host.User
	var gs []host.Group
	p.load(func(ctx context.Context) (err error) {
		us, gs, err = p.env.h.Users(ctx)
		return err
	}, func() {
		p.users, p.groups = us, gs
		p.render()
	})
}

func yesNo(b bool) string {
	if b {
		return "да"
	}
	return ""
}

func (p *usersPage) render() {
	var rows [][]string
	var keys []string
	for _, u := range p.users {
		if u.System && !p.system.Checked {
			continue
		}
		rows = append(rows, []string{u.Name, strconv.Itoa(u.UID), strings.Join(u.Groups, ", "), u.Home, u.Shell, yesNo(u.Sudo), yesNo(u.Locked)})
		keys = append(keys, u.Name)
	}
	p.g.set(rows, keys)
	rows, keys = nil, nil
	for _, g := range p.groups {
		rows = append(rows, []string{g.Name, strconv.Itoa(g.GID), strings.Join(g.Members, ", ")})
		keys = append(keys, g.Name)
	}
	p.gg.set(rows, keys)
}

func (p *usersPage) user() (host.User, bool) {
	for _, u := range p.users {
		if u.Name == p.g.selected() {
			return u, true
		}
	}
	return host.User{}, false
}

func (p *usersPage) addUser() {
	p.ask("Новый пользователь", "Создать", []askField{
		{Label: "Имя", Hint: "deploy"},
		{Label: "Пароль (пусто — вход только по ключу)", Password: true},
		{Label: "Оболочка", Value: "/bin/bash"},
		{Label: "Дополнительные группы через запятую", Hint: "docker, www-data"},
		{Label: "Права sudo", Check: true},
		{Label: "Публичный SSH-ключ (необязательно)", Multi: true, Hint: "ssh-ed25519 AAAA… user@host"},
	}, false, func(ctx context.Context, v []string) error {
		var groups []string
		for _, g := range strings.Split(v[3], ",") {
			if g = strings.TrimSpace(g); g != "" {
				groups = append(groups, g)
			}
		}
		return p.env.h.AddUser(ctx, host.NewUser{Name: strings.TrimSpace(v[0]), Password: v[1], Shell: strings.TrimSpace(v[2]),
			Groups: groups, Sudo: v[4] == "1", PubKey: v[5]})
	}, p.reload)
}

func (p *usersPage) setPassword() {
	u, ok := p.user()
	if !ok {
		return
	}
	p.ask("Пароль "+u.Name, "Сменить", []askField{{Label: "Новый пароль", Password: true}, {Label: "Ещё раз", Password: true}}, false,
		func(ctx context.Context, v []string) error {
			if v[0] == "" || v[0] != v[1] {
				return fmt.Errorf("пароли пустые или не совпадают")
			}
			return p.env.h.SetPassword(ctx, u.Name, v[0])
		}, p.reload)
}

func (p *usersPage) toggleLock() {
	u, ok := p.user()
	if !ok {
		return
	}
	if u.Locked {
		p.act(false, "Разблокирован "+u.Name, "", "", func(ctx context.Context) error { return p.env.h.LockUser(ctx, u.Name, false) }, p.reload)
		return
	}
	p.act(true, "Заблокировать "+u.Name, "Заблокировать пароль пользователя "+u.Name+"? Вход по SSH-ключу при этом может остаться.", "Заблокировать",
		func(ctx context.Context) error { return p.env.h.LockUser(ctx, u.Name, true) }, p.reload)
}

func (p *usersPage) groupNames() []string {
	var out []string
	for _, g := range p.groups {
		out = append(out, g.Name)
	}
	return out
}

func (p *usersPage) addToGroup() {
	u, ok := p.user()
	if !ok {
		return
	}
	p.ask("Добавить "+u.Name+" в группу", "Добавить", []askField{{Label: "Группа", Options: p.groupNames()}}, false,
		func(ctx context.Context, v []string) error { return p.env.h.AddToGroup(ctx, u.Name, v[0]) }, p.reload)
}

func (p *usersPage) removeFromGroup() {
	u, ok := p.user()
	if !ok || len(u.Groups) == 0 {
		return
	}
	p.ask("Убрать "+u.Name+" из группы", "Убрать", []askField{{Label: "Группа", Options: u.Groups}}, true,
		func(ctx context.Context, v []string) error { return p.env.h.RemoveFromGroup(ctx, u.Name, v[0]) }, p.reload)
}

func (p *usersPage) setShell() {
	u, ok := p.user()
	if !ok {
		return
	}
	var shells []string
	p.load(func(ctx context.Context) error { shells = p.env.h.Shells(ctx); return nil }, func() {
		p.ask("Оболочка "+u.Name, "Сменить", []askField{{Label: "Оболочка", Options: shells, Value: u.Shell}}, false,
			func(ctx context.Context, v []string) error { return p.env.h.SetShell(ctx, u.Name, v[0]) }, p.reload)
	})
}

func (p *usersPage) keys() {
	u, ok := p.user()
	if !ok {
		return
	}
	var keys []string
	p.load(func(ctx context.Context) (err error) {
		keys, err = p.env.h.AuthorizedKeys(ctx, u.Name)
		return err
	}, func() {
		if len(keys) == 0 {
			showText("keys:"+p.env.sv.ID+u.Name, "authorized_keys "+u.Name, "Ключей нет.")
			return
		}
		p.keysWindow(u.Name, keys)
	})
}

// keysWindow — список ключей пользователя с удалением.
func (p *usersPage) keysWindow(user string, keys []string) {
	w := fyne.CurrentApp().NewWindow("SSH-ключи " + user + " · VANTA")
	box := container.NewVBox()
	for _, k := range keys {
		k := k
		short := k
		if len(short) > 90 {
			short = short[:40] + "…" + short[len(short)-40:]
		}
		l := widget.NewLabel(short)
		l.TextStyle = fyne.TextStyle{Monospace: true}
		del := btn("Удалить", theme.DeleteIcon(), func() {
			p.act(true, "Удалить ключ "+user, "Удалить ключ из authorized_keys пользователя "+user+"?\n\n"+short, "Удалить",
				func(ctx context.Context) error { return p.env.h.RemoveAuthorizedKey(ctx, user, k) }, func() { w.Close() })
		})
		box.Add(container.NewBorder(nil, nil, nil, del, l))
	}
	w.SetContent(container.NewVScroll(container.NewPadded(box)))
	w.Resize(fyne.NewSize(820, 360))
	w.Show()
}

func (p *usersPage) addKey() {
	u, ok := p.user()
	if !ok {
		return
	}
	p.ask("Ключ для "+u.Name, "Добавить", []askField{{Label: "Публичный ключ", Multi: true, Hint: "ssh-ed25519 AAAA… user@host"}}, false,
		func(ctx context.Context, v []string) error { return p.env.h.AddAuthorizedKey(ctx, u.Name, v[0]) }, nil)
}

func (p *usersPage) deleteUser() {
	u, ok := p.user()
	if !ok {
		return
	}
	p.ask("Удалить пользователя "+u.Name, "Удалить", []askField{{Label: "Удалить и домашний каталог " + u.Home, Check: true}}, true,
		func(ctx context.Context, v []string) error { return p.env.h.DeleteUser(ctx, u.Name, v[0] == "1") }, p.reload)
}

// ---------- Journal ----------

type journalPage struct {
	*base
	obj    fyne.CanvasObject
	out    *widget.TextGrid
	scr    *container.Scroll
	unit   *widget.Entry
	grep   *widget.Entry
	since  *widget.Select
	prio   *widget.Select
	lines  *widget.Select
	follow *widget.Check
	stopF  context.CancelFunc
	text   strings.Builder
}

var sinceOpts = []string{"Всё", "15 минут", "1 час", "6 часов", "Сегодня", "Вчера", "Эта загрузка"}

func newJournalPage(env *toolEnv) toolPage {
	p := &journalPage{base: newBase(env)}
	p.out = widget.NewTextGrid()
	p.scr = container.NewScroll(p.out)
	p.unit = widget.NewEntry()
	p.unit.PlaceHolder = "Юнит (nginx)"
	p.grep = widget.NewEntry()
	p.grep.PlaceHolder = "Поиск в сообщении"
	p.since = widget.NewSelect(sinceOpts, nil)
	p.since.SetSelected("1 час")
	p.prio = widget.NewSelect(append([]string{"Все уровни"}, host.PriorityNames...), nil)
	p.prio.SetSelected("Все уровни")
	p.lines = widget.NewSelect([]string{"100", "300", "1000", "5000"}, nil)
	p.lines.SetSelected("300")
	p.follow = widget.NewCheck("Следить (-f)", func(on bool) {
		if on {
			p.startFollow()
		} else {
			p.stopFollow()
		}
	})
	p.unit.OnSubmitted = func(string) { p.reload() }
	p.grep.OnSubmitted = func(string) { p.reload() }
	show := btn("Показать", theme.SearchIcon(), p.reload)
	show.Importance = widget.HighImportance
	usage := btn("Место", theme.StorageIcon(), func() {
		var out string
		p.load(func(ctx context.Context) (err error) { out, err = p.env.h.JournalDiskUsage(ctx); return err }, func() { p.setInfo(out) })
	})
	vacuum := btn("Очистить старое…", theme.DeleteIcon(), func() {
		p.ask("Очистить журнал", "Удалить", []askField{{Label: "Удалить записи старше", Options: []string{"1d", "3d", "7d", "14d", "30d"}, Value: "7d"}}, true,
			func(ctx context.Context, v []string) error {
				out, err := p.env.h.VacuumJournal(ctx, v[0])
				if err == nil {
					fyne.Do(func() { p.setInfo(strings.TrimSpace(out)) })
				}
				return err
			}, nil)
	})
	filters := container.NewGridWithColumns(5, p.unit, p.grep, p.since, p.prio, p.lines)
	top := container.NewVBox(p.head("Journal", usage, vacuum),
		container.NewBorder(nil, nil, nil, container.NewHBox(p.follow, show), filters))
	p.obj = container.NewBorder(top, nil, nil, nil, p.scr)
	p.reload()
	return p
}

func (p *journalPage) object() fyne.CanvasObject { return p.obj }

func (p *journalPage) dispose() {
	p.stopFollow()
	p.base.dispose()
}

func (p *journalPage) query() host.JournalQuery {
	q := host.JournalQuery{Unit: strings.TrimSpace(p.unit.Text), Grep: strings.TrimSpace(p.grep.Text), Priority: -1}
	q.Lines, _ = strconv.Atoi(p.lines.Selected)
	for i, n := range host.PriorityNames {
		if n == p.prio.Selected {
			q.Priority = i
		}
	}
	switch p.since.Selected {
	case "15 минут":
		q.Since = "15 min ago"
	case "1 час":
		q.Since = "1 hour ago"
	case "6 часов":
		q.Since = "6 hours ago"
	case "Сегодня":
		q.Since = "today"
	case "Вчера":
		q.Since = "yesterday"
	case "Эта загрузка":
		q.Boot = true
	}
	return q
}

func (p *journalPage) reload() {
	q := p.query()
	var list []host.LogEntry
	p.load(func(ctx context.Context) (err error) {
		list, err = p.env.h.Journal(ctx, q)
		return err
	}, func() {
		p.text.Reset()
		p.text.WriteString(formatLog(list))
		if len(list) == 0 {
			p.text.WriteString("Записей нет.\n")
		}
		p.out.SetText(p.text.String())
		p.scr.ScrollToBottom()
		p.setInfo(fmt.Sprintf("Записей: %d", len(list)))
	})
}

func (p *journalPage) startFollow() {
	p.stopFollow()
	q := p.query()
	q.Since, q.Boot = "", false
	ctx, cancel := context.WithCancel(p.env.ctx)
	p.stopF = cancel
	go func() {
		err := p.env.h.FollowJournal(ctx, q, func(e host.LogEntry) {
			line := formatLog([]host.LogEntry{e})
			fyne.Do(func() {
				if p.closed {
					return
				}
				p.text.WriteString(line)
				s := p.text.String()
				if len(s) > textMax {
					s = s[len(s)-textMax:]
					p.text.Reset()
					p.text.WriteString(s)
				}
				p.out.SetText(s)
				p.scr.ScrollToBottom()
			})
		})
		fyne.Do(func() {
			if p.closed || ctx.Err() != nil {
				return
			}
			p.setErr(err)
			p.follow.SetChecked(false)
		})
	}()
	p.setInfo("Следим за журналом с " + time.Now().Format("15:04:05") + "…")
}

func (p *journalPage) stopFollow() {
	if p.stopF != nil {
		p.stopF()
		p.stopF = nil
	}
}

// ---------- Packages ----------

type packagesPage struct {
	*base
	obj  fyne.CanvasObject
	g    *grid
	list []host.Package
	only *widget.Check
	sel  selButtons
	pm   string
}

func newPackagesPage(env *toolEnv) toolPage {
	p := &packagesPage{base: newBase(env)}
	p.g = newGrid([]string{"Пакет", "Версия", "Обновление", "Архитектура", "Размер", "Описание"},
		[]float32{220, 170, 170, 80, 90, 400})
	p.g.OnPick = func(k string) { p.sel.set(k != "") }
	p.only = widget.NewCheck("Только с обновлениями", func(bool) { p.render() })

	refresh := btn("Обновить индекс", theme.DownloadIcon(), func() { p.run(host.OpRefresh, nil, "Обновить индекс пакетов", false) })
	upAll := btn("Обновить всё…", theme.UploadIcon(), func() { p.run(host.OpUpgradeAll, nil, "Обновить все пакеты", true) })
	install := btn("Установить…", theme.ContentAddIcon(), p.install)
	install.Importance = widget.HighImportance
	upOne := p.sel.add(btn("Обновить пакет", theme.UploadIcon(), func() {
		p.run(host.OpUpgrade, []string{p.g.selected()}, "Обновить "+p.g.selected(), false)
	}))
	remove := p.sel.add(btn("Удалить", theme.DeleteIcon(), func() {
		p.run(host.OpRemove, []string{p.g.selected()}, "Удалить пакет "+p.g.selected(), true)
	}))
	remove.Importance = widget.DangerImportance

	top := container.NewVBox(
		p.head("Packages", refresh, upAll, install, btn("Перечитать", theme.ViewRefreshIcon(), p.reload)),
		container.NewBorder(nil, nil, p.only, container.NewHBox(upOne, remove), filterEntry(p.g)),
	)
	p.obj = container.NewBorder(top, nil, nil, nil, p.g.t)
	p.reload()
	return p
}

func (p *packagesPage) object() fyne.CanvasObject { return p.obj }

func (p *packagesPage) reload() {
	var list []host.Package
	var pm string
	p.load(func(ctx context.Context) (err error) {
		pm = p.env.h.PackageManager(ctx)
		list, err = p.env.h.Packages(ctx)
		return err
	}, func() {
		p.list, p.pm = list, pm
		p.render()
	})
}

func (p *packagesPage) render() {
	var rows [][]string
	var keys []string
	up := 0
	for _, pk := range p.list {
		if pk.Upgrade != "" {
			up++
		} else if p.only.Checked {
			continue
		}
		size := ""
		if pk.Size > 0 {
			size = host.Bytes(pk.Size)
		}
		rows = append(rows, []string{pk.Name, pk.Version, pk.Upgrade, pk.Arch, size, pk.Summary})
		keys = append(keys, pk.Name)
	}
	p.g.set(rows, keys)
	p.setInfo(fmt.Sprintf("%s · установлено: %d · есть обновления: %d (по последнему индексу)", p.pm, len(p.list), up))
}

// run — операция с выводом в отдельном окне. Доводится до конца, даже если
// окно закрыли.
func (p *packagesPage) run(op host.PkgOp, names []string, title string, danger bool) {
	start := func() {
		p.stream("pkg:"+p.env.sv.ID, title, true, func(ctx context.Context, out func(string)) error {
			return p.env.h.PkgRun(ctx, op, names, out)
		}, p.reload)
	}
	if !danger {
		start()
		return
	}
	q := title + " на " + p.env.sv.Name + "?"
	if op == host.OpRemove {
		q += "\n\nЗависящие пакеты тоже могут быть удалены — смотрите вывод."
	}
	p.confirm(title, q, "Выполнить", start)
}

func (p *packagesPage) install() {
	p.ask("Установить пакеты", "Найти / установить", []askField{
		{Label: "Пакеты через пробел", Hint: "htop nginx"},
		{Label: "Сначала только поиск в репозиториях", Check: true},
	}, false, func(ctx context.Context, v []string) error {
		if v[1] == "1" {
			res, err := p.env.h.SearchPackages(ctx, v[0])
			if err != nil {
				return err
			}
			var b strings.Builder
			for _, r := range res {
				fmt.Fprintf(&b, "%-40s %s\n", r.Name, r.Summary)
			}
			fyne.Do(func() { showText("pkgsearch:"+p.env.sv.ID, "Поиск: "+v[0], b.String()) })
			return nil
		}
		names := strings.Fields(v[0])
		for _, n := range names {
			if !host.ValidPackage(n) {
				return fmt.Errorf("недопустимое имя пакета: %s", n)
			}
		}
		fyne.Do(func() { p.run(host.OpInstall, names, "Установить "+strings.Join(names, " "), false) })
		return nil
	}, nil)
}
