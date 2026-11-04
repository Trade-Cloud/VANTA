package screens

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/host"
)

// ---------- Docker ----------

type dockerPage struct {
	*base
	obj   fyne.CanvasObject
	d     *host.Docker
	cg    *grid
	ig    *grid
	vg    *grid
	ng    *grid
	all   *widget.Check
	stats *widget.Check
	csel  selButtons
	isel  selButtons
	vsel  selButtons
	nsel  selButtons
}

func newDockerPage(env *toolEnv) toolPage {
	p := &dockerPage{base: newBase(env)}
	p.cg = newGrid([]string{"Имя", "Образ", "Состояние", "Статус", "CPU", "Память", "Порты", "ID"},
		[]float32{180, 220, 90, 160, 70, 150, 260, 110})
	p.cg.OnPick = func(k string) { p.csel.set(k != "") }
	p.ig = newGrid([]string{"Репозиторий", "Тег", "ID", "Размер", "Создан"}, []float32{300, 140, 130, 100, 140})
	p.ig.OnPick = func(k string) { p.isel.set(k != "") }
	p.vg = newGrid([]string{"Том", "Драйвер"}, []float32{460, 100})
	p.vg.OnPick = func(k string) { p.vsel.set(k != "") }
	p.ng = newGrid([]string{"Сеть", "Драйвер", "Scope", "ID"}, []float32{220, 100, 80, 130})
	p.ng.OnPick = func(k string) { p.nsel.set(k != "") }
	p.all = widget.NewCheck("Остановленные", func(bool) { p.render() })
	p.all.SetChecked(true)
	p.stats = widget.NewCheck("CPU/память", func(bool) { p.reload() })

	cact := container.NewHBox()
	for _, a := range host.ContainerActions {
		a := a
		b := p.csel.add(btn(a.Label, nil, func() { p.containerAction(a.Verb, a.Label, a.Danger) }))
		if a.Verb == "rm -f" {
			b.Importance = widget.DangerImportance
		}
		cact.Add(b)
	}
	logs := p.csel.add(btn("Логи", theme.ListIcon(), p.logs))
	inspect := p.csel.add(btn("Inspect", theme.InfoIcon(), func() { p.inspect(p.cg.selected()) }))
	execB := p.csel.add(btn("Exec…", theme.ComputerIcon(), p.exec))
	cact.Add(logs)
	cact.Add(inspect)
	cact.Add(execB)

	pull := btn("Pull…", theme.DownloadIcon(), func() {
		p.ask("docker pull", "Скачать", []askField{{Label: "Образ", Hint: "nginx:latest"}}, false, func(ctx context.Context, v []string) error {
			ref := v[0]
			fyne.Do(func() {
				p.stream("pull:"+p.env.sv.ID, "docker pull "+ref, true, func(ctx context.Context, out func(string)) error {
					return p.env.h.PullImage(ctx, p.root(), ref, out)
				}, p.reload)
			})
			return nil
		}, nil)
	})
	rmi := p.isel.add(btn("Удалить образ", theme.DeleteIcon(), func() {
		id := p.ig.selected()
		p.act(true, "Удалить образ "+id, "Удалить образ "+id+"? Образ, который используют контейнеры, не удалится.", "Удалить",
			func(ctx context.Context) error { return p.env.h.RemoveImage(ctx, p.root(), id) }, p.reload)
	}))
	iinspect := p.isel.add(btn("Inspect", theme.InfoIcon(), func() { p.inspect(p.ig.selected()) }))
	vrm := p.vsel.add(btn("Удалить том", theme.DeleteIcon(), func() {
		v := p.vg.selected()
		p.act(true, "Удалить том "+v, "Удалить том "+v+" вместе с данными? Это необратимо.", "Удалить",
			func(ctx context.Context) error { return p.env.h.RemoveVolume(ctx, p.root(), v) }, p.reload)
	}))
	vinspect := p.vsel.add(btn("Inspect", theme.InfoIcon(), func() { p.inspect(p.vg.selected()) }))
	nrm := p.nsel.add(btn("Удалить сеть", theme.DeleteIcon(), func() {
		n := p.ng.selected()
		p.act(true, "Удалить сеть "+n, "Удалить docker-сеть "+n+"?", "Удалить",
			func(ctx context.Context) error { return p.env.h.RemoveNetwork(ctx, p.root(), n) }, p.reload)
	}))
	prune := btn("Prune…", theme.DeleteIcon(), func() {
		p.ask("docker system prune", "Очистить", []askField{{Label: "Вместе с неиспользуемыми томами (данные пропадут)", Check: true}}, true,
			func(ctx context.Context, v []string) error {
				out, err := p.env.h.Prune(ctx, p.root(), v[0] == "1")
				if err == nil {
					fyne.Do(func() { showText("prune:"+p.env.sv.ID, "docker system prune", out) })
				}
				return err
			}, p.reload)
	})

	tabs := container.NewAppTabs(
		container.NewTabItem("Контейнеры", container.NewBorder(container.NewVBox(
			container.NewBorder(nil, nil, container.NewHBox(p.all, p.stats), nil, filterEntry(p.cg)),
			container.NewHScroll(cact)), nil, nil, nil, p.cg.t)),
		container.NewTabItem("Образы", container.NewBorder(
			container.NewBorder(nil, nil, nil, container.NewHBox(pull, iinspect, rmi), filterEntry(p.ig)), nil, nil, nil, p.ig.t)),
		container.NewTabItem("Тома", container.NewBorder(
			container.NewBorder(nil, nil, nil, container.NewHBox(vinspect, vrm), filterEntry(p.vg)), nil, nil, nil, p.vg.t)),
		container.NewTabItem("Сети", container.NewBorder(
			container.NewBorder(nil, nil, nil, nrm, filterEntry(p.ng)), nil, nil, nil, p.ng.t)),
	)
	p.obj = container.NewBorder(p.head("Docker", prune, btn("Обновить", theme.ViewRefreshIcon(), p.reload)), nil, nil, nil, tabs)
	p.reload()
	return p
}

func (p *dockerPage) object() fyne.CanvasObject { return p.obj }

func (p *dockerPage) root() bool { return p.d != nil && p.d.Root }

func (p *dockerPage) reload() {
	stats := p.stats.Checked
	var d *host.Docker
	p.load(func(ctx context.Context) (err error) {
		d, err = p.env.h.GetDocker(ctx, stats)
		return err
	}, func() {
		p.d = d
		p.render()
	})
}

func (p *dockerPage) render() {
	if p.d == nil {
		return
	}
	var rows [][]string
	var keys []string
	running := 0
	for _, c := range p.d.Containers {
		if c.State == "running" {
			running++
		} else if !p.all.Checked {
			continue
		}
		rows = append(rows, []string{c.Name, c.Image, c.State, c.Status, c.CPU, c.Mem, c.Ports, c.ID})
		keys = append(keys, c.ID)
	}
	p.cg.set(rows, keys)
	rows, keys = nil, nil
	for _, i := range p.d.Images {
		rows = append(rows, []string{i.Repository, i.Tag, i.ID, i.Size, i.Created})
		keys = append(keys, i.ID)
	}
	p.ig.set(rows, keys)
	rows, keys = nil, nil
	for _, v := range p.d.Volumes {
		rows = append(rows, []string{v.Name, v.Driver})
		keys = append(keys, v.Name)
	}
	p.vg.set(rows, keys)
	rows, keys = nil, nil
	for _, n := range p.d.Networks {
		rows = append(rows, []string{n.Name, n.Driver, n.Scope, n.ID})
		keys = append(keys, n.Name)
	}
	p.ng.set(rows, keys)
	via := ""
	if p.d.Root {
		via = " · через sudo"
	}
	p.setInfo(fmt.Sprintf("Docker %s%s · контейнеров: %d (запущено %d) · образов: %d · томов: %d",
		p.d.Version, via, len(p.d.Containers), running, len(p.d.Images), len(p.d.Volumes)))
}

func (p *dockerPage) containerName(id string) string {
	if p.d != nil {
		for _, c := range p.d.Containers {
			if c.ID == id {
				return c.Name
			}
		}
	}
	return id
}

func (p *dockerPage) containerAction(verb, label string, danger bool) {
	id := p.cg.selected()
	if id == "" {
		return
	}
	name := p.containerName(id)
	q := fmt.Sprintf("%s контейнер %s (%s)?", label, name, id)
	if verb == "rm -f" {
		q += "\n\nКонтейнер будет остановлен и удалён. Данные вне томов пропадут."
	}
	p.act(danger, label+": "+name, q, label,
		func(ctx context.Context) error { return p.env.h.ContainerAction(ctx, p.root(), verb, id) }, p.reload)
}

func (p *dockerPage) logs() {
	id := p.cg.selected()
	name := p.containerName(id)
	p.stream("dlogs:"+p.env.sv.ID+id, "Логи "+name, false, func(ctx context.Context, out func(string)) error {
		return p.env.h.FollowContainerLogs(ctx, p.root(), id, out)
	}, nil)
}

func (p *dockerPage) inspect(ref string) {
	if ref == "" {
		return
	}
	var out string
	p.load(func(ctx context.Context) (err error) {
		out, err = p.env.h.Inspect(ctx, p.root(), ref)
		return err
	}, func() { showText("inspect:"+p.env.sv.ID+ref, "docker inspect "+ref, out) })
}

func (p *dockerPage) exec() {
	id := p.cg.selected()
	name := p.containerName(id)
	p.ask("Exec в "+name, "Выполнить", []askField{{Label: "Команда (sh -c)", Hint: "ls -la /app"}}, false,
		func(ctx context.Context, v []string) error {
			out, err := p.env.h.ExecInContainer(ctx, p.root(), id, v[0])
			if err != nil {
				return err
			}
			fyne.Do(func() { showText("exec:"+p.env.sv.ID+id, name+" $ "+v[0], out) })
			return nil
		}, nil)
}

// ---------- Databases ----------

type databasesPage struct {
	*base
	obj     fyne.CanvasObject
	engines []host.DBEngine
	pick    *widget.Select
	dbg     *grid
	dbs     []host.DBInfo
	query   *widget.Entry
	out     *widget.TextGrid
	outText string
	sel     selButtons
}

func newDatabasesPage(env *toolEnv) toolPage {
	p := &databasesPage{base: newBase(env)}
	p.pick = widget.NewSelect(nil, func(string) { p.listDBs() })
	p.pick.PlaceHolder = "СУБД не найдены"
	p.dbg = newGrid([]string{"База", "Размер", "Примечание"}, []float32{220, 100, 220})
	p.dbg.OnPick = func(k string) { p.sel.set(k != "") }
	p.query = widget.NewMultiLineEntry()
	p.query.TextStyle = fyne.TextStyle{Monospace: true}
	p.query.SetMinRowsVisible(4)
	p.query.PlaceHolder = "SQL / команда redis / выражение mongosh"
	p.out = widget.NewTextGrid()

	run := btn("Выполнить", theme.MediaPlayIcon(), p.run)
	run.Importance = widget.HighImportance
	dump := p.sel.add(btn("Дамп базы", theme.DocumentSaveIcon(), p.dump))
	copyOut := btn("Копировать вывод", theme.ContentCopyIcon(), func() { fyne.CurrentApp().Clipboard().SetContent(p.outText) })

	left := container.NewBorder(container.NewHBox(widget.NewLabel("Базы"), dump), nil, nil, nil, p.dbg.t)
	right := container.NewBorder(container.NewVBox(p.query, container.NewHBox(run, copyOut)), nil, nil, nil, container.NewScroll(p.out))
	split := container.NewHSplit(left, right)
	split.Offset = 0.35
	top := container.NewVBox(p.head("Databases", btn("Найти СУБД", theme.SearchIcon(), p.reload)),
		container.NewBorder(nil, nil, widget.NewLabel("СУБД"), nil, p.pick))
	p.obj = container.NewBorder(top, nil, nil, nil, split)
	p.reload()
	return p
}

func (p *databasesPage) object() fyne.CanvasObject { return p.obj }

func (p *databasesPage) reload() {
	var list []host.DBEngine
	p.setInfo("Ищем PostgreSQL, MySQL/MariaDB, Redis, MongoDB на сервере и в контейнерах…")
	p.load(func(ctx context.Context) (err error) {
		list, err = p.env.h.DetectDatabases(ctx)
		return err
	}, func() {
		p.engines = list
		var labels []string
		for _, e := range list {
			labels = append(labels, e.Label)
		}
		p.pick.Options = labels
		if len(labels) == 0 {
			p.setInfo("СУБД не найдены (ищем запущенные postgres, mysqld/mariadbd, redis-server, mongod и контейнеры с такими образами).")
			p.pick.ClearSelected()
			return
		}
		p.setInfo("")
		p.pick.SetSelectedIndex(0)
	})
}

func (p *databasesPage) engine() (host.DBEngine, bool) {
	i := p.pick.SelectedIndex()
	if i < 0 || i >= len(p.engines) {
		return host.DBEngine{}, false
	}
	return p.engines[i], true
}

func (p *databasesPage) listDBs() {
	e, ok := p.engine()
	if !ok {
		return
	}
	var list []host.DBInfo
	p.load(func(ctx context.Context) (err error) {
		list, err = p.env.h.ListDatabases(ctx, e)
		return err
	}, func() {
		p.dbs = list
		var rows [][]string
		var keys []string
		for _, d := range list {
			size := ""
			if d.Size > 0 {
				size = host.Bytes(d.Size)
			}
			rows = append(rows, []string{d.Name, size, d.Note})
			keys = append(keys, d.Name)
		}
		p.dbg.set(rows, keys)
	})
}

func (p *databasesPage) run() {
	e, ok := p.engine()
	if !ok {
		return
	}
	db, q := p.dbg.selected(), p.query.Text
	exec := func(ctx context.Context) error {
		out, err := p.env.h.Query(ctx, e, db, q)
		if err != nil {
			return err
		}
		fyne.Do(func() {
			p.outText = stripANSI(out)
			if strings.TrimSpace(p.outText) == "" {
				p.outText = "(пустой вывод)"
			}
			p.out.SetText(p.outText)
		})
		return nil
	}
	if host.IsDangerQuery(q) {
		target := db
		if target == "" {
			target = "(база по умолчанию)"
		}
		p.act(true, "Опасный запрос", "Запрос удаляет данные или меняет схему. Выполнить в "+target+"?\n\n"+q, "Выполнить", exec, p.listDBs)
		return
	}
	p.load(exec, nil)
}

func (p *databasesPage) dump() {
	e, ok := p.engine()
	db := p.dbg.selected()
	if !ok || db == "" {
		return
	}
	var file string
	p.setInfo("Снимаем дамп " + db + "…")
	p.load(func(ctx context.Context) (err error) {
		file, err = p.env.h.Dump(ctx, e, db)
		return err
	}, func() {
		p.setInfo("Дамп готов: " + file + " (скачать — во вкладке Files)")
		p.env.st.Notify(agent.SeveritySuccess, "Дамп "+db+" готов", file)
	})
}

// ---------- Firewall ----------

type firewallPage struct {
	*base
	obj     fyne.CanvasObject
	fw      *host.Firewall
	g       *grid
	summary *widget.Label
	toggle  *widget.Button
	add     *widget.Button
	sel     selButtons
}

func newFirewallPage(env *toolEnv) toolPage {
	p := &firewallPage{base: newBase(env)}
	p.g = newGrid([]string{"№", "Куда", "Действие", "Откуда"}, []float32{50, 300, 120, 260})
	p.g.OnPick = func(k string) { p.sel.set(k != "") }
	p.summary = widget.NewLabel("")
	p.summary.TextStyle = fyne.TextStyle{Monospace: true}
	p.summary.Wrapping = fyne.TextWrapWord
	p.toggle = btn("Включить", theme.ConfirmIcon(), p.switchFW)
	p.add = btn("Добавить правило", theme.ContentAddIcon(), p.addRule)
	p.add.Importance = widget.HighImportance
	del := p.sel.add(btn("Удалить правило", theme.DeleteIcon(), p.deleteRule))
	del.Importance = widget.DangerImportance

	rules := container.NewBorder(container.NewBorder(nil, nil, nil, container.NewHBox(p.add, del), filterEntry(p.g)), nil, nil, nil, p.g.t)
	tabs := container.NewAppTabs(
		container.NewTabItem("Правила", rules),
		container.NewTabItem("Статус", container.NewVScroll(p.summary)),
	)
	p.obj = container.NewBorder(p.head("Firewall", p.toggle, btn("Обновить", theme.ViewRefreshIcon(), p.reload)), nil, nil, nil, tabs)
	p.reload()
	return p
}

func (p *firewallPage) object() fyne.CanvasObject { return p.obj }

func (p *firewallPage) manageable() bool {
	return p.fw != nil && (p.fw.Kind == host.FWUfw || p.fw.Kind == host.FWFirewalld)
}

func (p *firewallPage) reload() {
	var fw *host.Firewall
	p.load(func(ctx context.Context) (err error) {
		fw, err = p.env.h.GetFirewall(ctx)
		return err
	}, func() {
		p.fw = fw
		var rows [][]string
		var keys []string
		for _, r := range fw.Rules {
			num := ""
			if r.Num > 0 {
				num = fmt.Sprint(r.Num)
			}
			rows = append(rows, []string{num, r.To, r.Action, r.From})
			keys = append(keys, r.Raw)
		}
		p.g.set(rows, keys)
		text := fw.Summary
		if fw.Raw != "" {
			text += "\n\n" + fw.Raw
		}
		p.summary.SetText(text)
		state := "выключен"
		if fw.Active {
			state = "включён"
		}
		kind := fw.Kind
		if kind == "" {
			kind = "не найден"
		}
		p.setInfo(fmt.Sprintf("Фаервол: %s · %s · правил: %d", kind, state, len(fw.Rules)))
		if p.manageable() {
			p.add.Enable()
			p.toggle.Enable()
		} else {
			p.add.Disable()
			p.toggle.Disable()
		}
		if fw.Active {
			p.toggle.SetText("Выключить")
		} else {
			p.toggle.SetText("Включить")
		}
	})
}

func (p *firewallPage) switchFW() {
	if !p.manageable() {
		return
	}
	on := !p.fw.Active
	kind, port := p.fw.Kind, p.env.sv.SSHPort()
	if on {
		p.act(true, "Включить "+kind, fmt.Sprintf("Включить фаервол %s на %s? Перед включением VANTA разрешит порт SSH %d/tcp, "+
			"остальные входящие подключения будут закрыты по политике по умолчанию.", kind, p.env.sv.Name, port), "Включить",
			func(ctx context.Context) error { return p.env.h.SetFirewall(ctx, kind, true, port) }, p.reload)
		return
	}
	p.act(true, "Выключить "+kind, "Выключить фаервол на "+p.env.sv.Name+"? Все порты станут доступны извне.", "Выключить",
		func(ctx context.Context) error { return p.env.h.SetFirewall(ctx, kind, false, port) }, p.reload)
}

func (p *firewallPage) addRule() {
	if !p.manageable() {
		return
	}
	kind := p.fw.Kind
	p.ask("Новое правило", "Добавить", []askField{
		{Label: "Действие", Options: []string{"allow", "deny", "reject", "limit"}},
		{Label: "Порт, диапазон (8000:8100) или сервис (OpenSSH, http)", Hint: "443"},
		{Label: "Протокол", Options: []string{"tcp", "udp", "любой"}},
		{Label: "Откуда (IP или подсеть; пусто — отовсюду)", Hint: "203.0.113.0/24"},
	}, true, func(ctx context.Context, v []string) error {
		proto := v[2]
		if proto == "любой" {
			proto = ""
		}
		return p.env.h.AddRule(ctx, kind, host.NewRule{Action: v[0], Port: v[1], Proto: proto, From: v[3]})
	}, p.reload)
}

func (p *firewallPage) deleteRule() {
	if !p.manageable() {
		return
	}
	key := p.g.selected()
	var rule host.FWRule
	for _, r := range p.fw.Rules {
		if r.Raw == key {
			rule = r
		}
	}
	if rule.Raw == "" {
		return
	}
	q := "Удалить правило?\n\n" + rule.Raw
	if strings.Contains(rule.To, fmt.Sprint(p.env.sv.SSHPort())) || strings.Contains(strings.ToLower(rule.To), "ssh") {
		q += "\n\nВнимание: похоже, это правило для SSH. Без него можно потерять доступ к серверу."
	}
	kind := p.fw.Kind
	p.act(true, "Удалить правило", q, "Удалить",
		func(ctx context.Context) error { return p.env.h.DeleteRule(ctx, kind, rule) }, p.reload)
}
