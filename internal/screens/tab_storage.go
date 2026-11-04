package screens

import (
	"context"
	"fmt"
	"path"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/host"
)

// ---------- Storage ----------

// В стиле Resources: без заголовка, «куда подключены» и «Обновить».
// Фоновое обновление df/lsblk — Settings › Настройки приложения
// (core.Store.AutoRefresh("storage")). Сверху плашки (ёмкость, занято,
// свободно, устройства), ниже файловые системы с полосами, дерево дисков
// и «что занимает место» (du): клик по строке — зайти глубже.

const storageEvery = 15

var (
	mountCols = []float32{-1, -1, 150, 52, 96, 60}
	blockCols = []float32{-1, 72, 96, 80, -1}
	duCols    = []float32{-1, -1, 104, 52}
)

type mountRow struct {
	press                   *header.Pressable
	name, sub               *header.Text
	bar                     *meter
	used, pct, free, inodes *header.Text
	key                     string
}

type blockRow struct {
	name, model, size, fs, mount *header.Text
	kind                         *vchip
}

type duRow struct {
	press     *header.Pressable
	name, sub *header.Text
	bar       *meter
	size, pct *header.Text
	key       string
}

type storagePage struct {
	*base
	obj     fyne.CanvasObject
	st      *host.Storage
	loading bool

	tSize, tUsed, tFree, tDev *statTile

	mounts *rowSet[*mountRow]
	mCount *header.Text
	blocks *rowSet[*blockRow]
	bCount *header.Text

	du      *rowSet[*duRow]
	duPath  *widget.Entry
	duTotal *header.Text
	duList  []host.DirUsage
	duDir   string
	duBusy  bool
}

func newStoragePage(env *toolEnv) toolPage {
	p := &storagePage{base: newBase(env)}

	p.tSize = newStatTile(icDisk, theme.ColorNamePrimary, "ЁМКОСТЬ")
	p.tUsed = newStatTile(icPie, theme.ColorNameWarning, "ЗАНЯТО")
	p.tFree = newStatTile(theme.StorageIcon(), theme.ColorNameSuccess, "СВОБОДНО")
	p.tDev = newStatTile(theme.ComputerIcon(), theme.ColorNamePrimary, "УСТРОЙСТВА")

	// Файловые системы: клик — посчитать, что занимает место.
	p.mCount = muted("")
	p.mounts = newRowSet(func() (*mountRow, fyne.CanvasObject) {
		r := &mountRow{
			name:   truncated(txt("", theme.TextSize(), theme.ColorNameForeground, true)),
			sub:    truncated(muted("")),
			bar:    newMeter(),
			used:   right(monoCell()),
			pct:    right(monoCell()),
			free:   right(monoCell()),
			inodes: right(monoCell()),
		}
		r.press = rowPress(cols(mountCols, sp3, vstack(2, r.name, r.sub), r.bar, r.used, r.pct, r.free, r.inodes),
			func() { p.open(r.key) })
		return r, r.press
	})
	mounts := section("Файловые системы", p.mCount, nil,
		tableHead(mountCols, capsLabel("ТОЧКА МОНТИРОВАНИЯ"), capsLabel("ЗАПОЛНЕНИЕ"),
			right(capsLabel("ЗАНЯТО / ВСЕГО")), right(capsLabel("%")), right(capsLabel("СВОБОДНО")), right(capsLabel("INODE"))),
		p.mounts.box)

	// Диски и разделы (lsblk).
	p.bCount = muted("")
	p.blocks = newRowSet(func() (*blockRow, fyne.CanvasObject) {
		r := &blockRow{
			name:  truncated(txt("", theme.TextSize(), theme.ColorNameForeground, true)),
			model: truncated(muted("")),
			kind:  newVChip(),
			size:  right(monoCell()),
			fs:    monoCell(),
			mount: monoCell(),
		}
		return r, rowPlain(cols(blockCols, sp3, vstack(2, r.name, r.model), r.kind.obj, r.size, r.fs, r.mount))
	})
	blocks := section("Диски и разделы", p.bCount, nil,
		tableHead(blockCols, capsLabel("УСТРОЙСТВО"), capsLabel("ТИП"), right(capsLabel("РАЗМЕР")),
			capsLabel("ФС"), capsLabel("СМОНТИРОВАНО")),
		p.blocks.box)

	// Что занимает место.
	p.duPath = widget.NewEntry()
	p.duPath.SetText("/")
	p.duPath.OnSubmitted = func(string) { p.scan() }
	p.duTotal = muted("")
	scan := btn("Посчитать", theme.SearchIcon(), p.scan)
	scan.Importance = widget.HighImportance
	up := iconButton(theme.MoveUpIcon(), p.up)
	p.du = newRowSet(func() (*duRow, fyne.CanvasObject) {
		r := &duRow{
			name: truncated(txt("", theme.TextSize(), theme.ColorNameForeground, true)),
			sub:  truncated(muted("")),
			bar:  newMeter(),
			size: right(monoCell()),
			pct:  right(monoCell()),
		}
		r.press = rowPress(cols(duCols, sp3, vstack(2, r.name, r.sub), r.bar, r.size, r.pct),
			func() { p.open(r.key) })
		return r, r.press
	})
	du := section("Что занимает место", p.duTotal, hstack(sp2, fixedW(280, p.duPath), up, scan),
		tableHead(duCols, capsLabel("КАТАЛОГ"), capsLabel("ДОЛЯ"), right(capsLabel("РАЗМЕР")), right(capsLabel("%"))),
		p.du.box)

	p.obj = vstack(sp4,
		p.bar(),
		tiles(p.tSize, p.tUsed, p.tFree, p.tDev),
		mounts,
		blocks,
		du,
	)
	p.render()
	p.renderDU()
	p.reload()
	p.every(storageEvery, func() bool { return p.env.st.AutoRefresh("storage") && !p.loading }, p.reload)
	return p
}

func (p *storagePage) object() fyne.CanvasObject { return p.obj }

func (p *storagePage) reload() {
	p.loading = true
	var st *host.Storage
	p.work(func(ctx context.Context) (err error) {
		st, err = p.env.h.GetStorage(ctx)
		return err
	}, func(err error) {
		p.loading = false
		p.setErr(err)
		if err == nil {
			p.st = st
			p.render()
		}
	})
}

func (p *storagePage) render() {
	st := p.st
	if st == nil {
		p.mounts.resize(0, "Читаем файловые системы…")
		p.blocks.resize(0, "Читаем диски…")
		return
	}

	// Плашки. Одно устройство, смонтированное в нескольких местах
	// (bind-монтирования), считаем один раз.
	var size, used, free uint64
	seen := map[string]bool{}
	for _, m := range st.Mounts {
		if seen[m.Source] {
			continue
		}
		seen[m.Source] = true
		size, used, free = size+m.Size, used+m.Used, free+m.Avail
	}
	all := pct(used, used+free)
	p.tSize.set(host.Bytes(size), plural(len(seen), "файловая система", "файловые системы", "файловых систем"))
	p.tUsed.set(host.Bytes(used), fmt.Sprintf("%.0f%% от доступного", all))
	setTC(p.tUsed.val, host.Bytes(used), usageColorText(all))
	p.tFree.set(host.Bytes(free), fmt.Sprintf("%.0f%% свободно", 100-all))
	var disks int
	var disksSize uint64
	for _, b := range st.Blocks {
		if b.Type == "disk" {
			disks++
			disksSize += b.Size
		}
	}
	p.tDev.set(fmt.Sprint(disks), plural(disks, "диск", "диска", "дисков")+" на "+host.Bytes(disksSize))

	// Файловые системы.
	setT(p.mCount, plural(len(st.Mounts), "раздел", "раздела", "разделов"))
	rows := p.mounts.resize(len(st.Mounts), "Нет данных (df недоступен)")
	for i, m := range st.Mounts {
		r := rows[i]
		r.key = m.Target
		v := m.Percent()
		setT(r.name, m.Target)
		setT(r.sub, m.Source+"  ·  "+m.FSType)
		r.bar.set(v, usageColor(v))
		setT(r.used, host.Bytes(m.Used)+" / "+host.Bytes(m.Size))
		setTC(r.pct, fmt.Sprintf("%.0f%%", v), usageColorText(v))
		setT(r.free, host.Bytes(m.Avail))
		setT(r.inodes, orDash(m.Inodes))
		r.press.SetSelected(m.Target == p.duDir)
	}

	// Диски: разделы с отступом под своим диском.
	setT(p.bCount, plural(len(st.Blocks), "устройство", "устройства", "устройств"))
	brows := p.blocks.resize(len(st.Blocks), "Нет данных (lsblk недоступен)")
	for i, b := range st.Blocks {
		r := brows[i]
		name := b.Name
		if b.Type != "disk" {
			name = "└  " + name
		}
		setT(r.name, name)
		model := b.Model
		if model == "" && b.Type == "disk" {
			model = "модель неизвестна"
		}
		setT(r.model, model)
		c := theme.ColorNamePlaceHolder
		switch b.Type {
		case "disk":
			c = theme.ColorNamePrimary
		case "part":
			c = theme.ColorNameSuccess
		case "lvm", "crypt", "raid1", "raid0", "raid5", "raid10":
			c = theme.ColorNameWarning
		}
		r.kind.set(b.Type, c)
		setT(r.size, host.Bytes(b.Size))
		setT(r.fs, orDash(b.FSType))
		setT(r.mount, orDash(b.Mount))
	}
}

// usageColorText — цвет числа заполненности: обычный текст, пока не тревожно.
func usageColorText(v float64) fyne.ThemeColorName {
	if c := usageColor(v); c != theme.ColorNamePrimary {
		return c
	}
	return theme.ColorNameForeground
}

// ---------- du ----------

func (p *storagePage) open(dir string) {
	if dir == "" || p.duBusy {
		return
	}
	p.duPath.SetText(dir)
	p.scan()
}

func (p *storagePage) up() {
	d := strings.TrimRight(strings.TrimSpace(p.duPath.Text), "/")
	if i := strings.LastIndex(d, "/"); i > 0 {
		d = d[:i]
	} else {
		d = "/"
	}
	p.open(d)
}

func (p *storagePage) scan() {
	dir := strings.TrimSpace(p.duPath.Text)
	if dir == "" {
		dir = "/"
	}
	if p.duBusy {
		return
	}
	p.duBusy = true
	p.duDir = dir
	setT(p.duTotal, "считаем "+dir+"…")
	p.render()
	var list []host.DirUsage
	p.work(func(ctx context.Context) (err error) {
		list, err = p.env.h.DirUsage(ctx, dir)
		return err
	}, func(err error) {
		p.duBusy = false
		p.setErr(err)
		if err != nil {
			setT(p.duTotal, "")
			return
		}
		p.duList = list
		p.renderDU()
	})
}

func (p *storagePage) renderDU() {
	if p.duDir == "" {
		setT(p.duTotal, "")
		p.du.resize(0, "Выберите файловую систему выше или введите путь и нажмите «Посчитать»")
		return
	}
	// Первая строка du — сам каталог (итог), остальные — вложенные.
	self := strings.TrimRight(p.duDir, "/")
	var total uint64
	var kids []host.DirUsage
	for _, d := range p.duList {
		if strings.TrimRight(d.Path, "/") == self {
			total = d.Size
			continue
		}
		kids = append(kids, d)
	}
	setT(p.duTotal, p.duDir+"  ·  всего "+host.Bytes(total))
	rows := p.du.resize(len(kids), "Вложенных каталогов нет (или нет прав их прочитать)")
	for i, d := range kids {
		r := rows[i]
		r.key = d.Path
		setT(r.name, path.Base(d.Path))
		setT(r.sub, d.Path)
		v := pct(d.Size, total)
		r.bar.set(v, theme.ColorNamePrimary)
		setT(r.size, host.Bytes(d.Size))
		setT(r.pct, fmt.Sprintf("%.0f%%", v))
	}
}
