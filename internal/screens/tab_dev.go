package screens

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/host"
)

// ---------- Files ----------

type filesPage struct {
	*base
	obj    fyne.CanvasObject
	g      *grid
	dir    string
	pathE  *widget.Entry
	list   []host.FileInfo
	owners map[uint32]string
	hidden *widget.Check
	sel    selButtons
}

func newFilesPage(env *toolEnv) toolPage {
	p := &filesPage{base: newBase(env)}
	p.g = newGrid([]string{"Имя", "Размер", "Права", "Владелец", "Изменён"}, []float32{340, 100, 110, 110, 150})
	p.g.OnPick = func(k string) { p.sel.set(k != "") }
	p.pathE = widget.NewEntry()
	p.pathE.OnSubmitted = func(s string) { p.open(s) }
	p.hidden = widget.NewCheck("Скрытые", func(bool) { p.render() })

	up := btn("", theme.MoveUpIcon(), func() { p.open(path.Dir(p.dir)) })
	home := btn("", theme.HomeIcon(), func() {
		var d string
		p.load(func(ctx context.Context) error { d = p.env.h.Home(ctx); return nil }, func() { p.open(d) })
	})
	refresh := btn("", theme.ViewRefreshIcon(), func() { p.open(p.dir) })
	openB := p.sel.add(btn("Открыть", theme.FolderOpenIcon(), p.openSel))
	ren := p.sel.add(btn("Переименовать", theme.DocumentCreateIcon(), p.rename))
	chmod := p.sel.add(btn("Права", theme.SettingsIcon(), p.chmod))
	chown := p.sel.add(btn("Владелец", theme.AccountIcon(), p.chown))
	down := p.sel.add(btn("Скачать", theme.DownloadIcon(), p.download))
	del := p.sel.add(btn("Удалить", theme.DeleteIcon(), p.remove))
	del.Importance = widget.DangerImportance
	mkdir := btn("Папка", theme.FolderNewIcon(), func() {
		p.ask("Новая папка", "Создать", []askField{{Label: "Имя"}}, false, func(ctx context.Context, v []string) error {
			return p.env.h.Mkdir(ctx, path.Join(p.dir, v[0]))
		}, func() { p.open(p.dir) })
	})
	mkfile := btn("Файл", theme.FileIcon(), func() {
		p.ask("Новый файл", "Создать", []askField{{Label: "Имя"}}, false, func(ctx context.Context, v []string) error {
			return p.env.h.CreateFile(ctx, path.Join(p.dir, v[0]))
		}, func() { p.open(p.dir) })
	})
	upload := btn("Загрузить", theme.UploadIcon(), p.upload)

	top := container.NewVBox(
		p.head("Files", mkdir, mkfile, upload),
		container.NewBorder(nil, nil, container.NewHBox(up, home, refresh), p.hidden, p.pathE),
		container.NewBorder(nil, nil, nil, container.NewHBox(openB, ren, chmod, chown, down, del), filterEntry(p.g)),
	)
	p.obj = container.NewBorder(top, nil, nil, nil, p.g.t)
	p.load(func(ctx context.Context) error {
		p.owners = p.env.h.Owners(ctx)
		p.dir = p.env.h.Home(ctx)
		return nil
	}, func() { p.open(p.dir) })
	return p
}

func (p *filesPage) object() fyne.CanvasObject { return p.obj }

func (p *filesPage) open(dir string) {
	dir = path.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		dir = "/"
	}
	var list []host.FileInfo
	p.load(func(ctx context.Context) (err error) {
		list, err = p.env.h.ListDir(ctx, dir)
		return err
	}, func() {
		p.dir, p.list = dir, list
		p.pathE.SetText(dir)
		p.g.sel, p.g.selRow = "", -1
		p.g.t.UnselectAll()
		p.sel.set(false)
		p.render()
	})
}

func (p *filesPage) render() {
	var rows [][]string
	var keys []string
	for _, f := range p.list {
		if strings.HasPrefix(f.Name, ".") && !p.hidden.Checked {
			continue
		}
		name := f.Name
		size := host.Bytes(uint64(f.Size))
		if f.IsDir {
			name += "/"
			size = ""
		}
		if f.Link != "" {
			name += " → " + f.Link
		}
		owner := p.owners[f.UID]
		if owner == "" {
			owner = strconv.Itoa(int(f.UID))
		}
		rows = append(rows, []string{name, size, f.Mode.String(), owner, f.ModTime.Format("2006-01-02 15:04")})
		keys = append(keys, f.Path)
	}
	p.g.set(rows, keys)
	p.setInfo(fmt.Sprintf("%s · элементов: %d", p.dir, len(p.list)))
}

func (p *filesPage) current() (host.FileInfo, bool) {
	for _, f := range p.list {
		if f.Path == p.g.selected() {
			return f, true
		}
	}
	return host.FileInfo{}, false
}

func (p *filesPage) openSel() {
	f, ok := p.current()
	if !ok {
		return
	}
	if f.IsDir {
		p.open(f.Path)
		return
	}
	p.edit(f)
}

// edit — файл в отдельном окне-редакторе.
func (p *filesPage) edit(f host.FileInfo) {
	var data []byte
	p.load(func(ctx context.Context) (err error) {
		data, err = p.env.h.ReadFile(ctx, f.Path)
		return err
	}, func() {
		w := fyne.CurrentApp().NewWindow(f.Path + " · " + p.env.sv.Name)
		e := widget.NewMultiLineEntry()
		e.TextStyle = fyne.TextStyle{Monospace: true}
		e.Wrapping = fyne.TextWrapOff
		e.SetText(string(data))
		state := widget.NewLabel("")
		dirty := false
		e.OnChanged = func(string) {
			if !dirty {
				dirty = true
				state.SetText("Есть несохранённые изменения")
			}
		}
		var save *widget.Button
		save = btn("Сохранить", theme.DocumentSaveIcon(), func() {
			text := e.Text
			save.Disable()
			state.SetText("Сохраняем…")
			go func() {
				err := p.env.h.WriteFile(context.Background(), f.Path, []byte(text))
				fyne.Do(func() {
					save.Enable()
					if err != nil {
						state.SetText("Ошибка: " + err.Error())
						return
					}
					dirty = false
					state.SetText("Сохранено")
				})
			}()
		})
		save.Importance = widget.HighImportance
		closeB := btn("Закрыть", theme.CancelIcon(), func() { w.Close() })
		w.SetContent(container.NewBorder(nil, container.NewBorder(nil, nil, state, container.NewHBox(closeB, save)), nil, nil, e))
		w.Canvas().AddShortcut(&desktopSave, func(fyne.Shortcut) { save.OnTapped() })
		w.Resize(fyne.NewSize(900, 640))
		if header.WindowHook != nil {
			header.WindowHook(w)
		}
		w.Show()
	})
}

func (p *filesPage) rename() {
	f, ok := p.current()
	if !ok {
		return
	}
	p.ask("Переименовать", "Переименовать", []askField{{Label: "Новое имя или путь", Value: f.Name}}, false,
		func(ctx context.Context, v []string) error {
			to := v[0]
			if !strings.HasPrefix(to, "/") {
				to = path.Join(p.dir, to)
			}
			return p.env.h.Rename(ctx, f.Path, to)
		}, func() { p.open(p.dir) })
}

func (p *filesPage) chmod() {
	f, ok := p.current()
	if !ok {
		return
	}
	p.ask("Права "+f.Name, "Применить", []askField{{Label: "Права (восьмерично)", Value: fmt.Sprintf("%o", f.Mode.Perm())}}, false,
		func(ctx context.Context, v []string) error {
			m, err := strconv.ParseUint(v[0], 8, 32)
			if err != nil || m > 0o7777 {
				return fmt.Errorf("права — восьмеричное число, например 644 или 755")
			}
			return p.env.h.Chmod(ctx, f.Path, os.FileMode(m))
		}, func() { p.open(p.dir) })
}

func (p *filesPage) chown() {
	f, ok := p.current()
	if !ok {
		return
	}
	p.ask("Владелец "+f.Name, "Применить", []askField{
		{Label: "Владелец (user или user:group)", Value: p.owners[f.UID]},
		{Label: "Рекурсивно (для папки)", Check: true},
	}, false, func(ctx context.Context, v []string) error {
		return p.env.h.Chown(ctx, f.Path, v[0], v[1] == "1")
	}, func() { p.open(p.dir) })
}

func (p *filesPage) remove() {
	f, ok := p.current()
	if !ok {
		return
	}
	what := "файл"
	if f.IsDir {
		what = "папку со всем содержимым"
	}
	p.ask("Удалить "+f.Name, "Удалить", []askField{
		{Label: "Через sudo (если не хватает прав)", Check: true},
	}, true, func(ctx context.Context, v []string) error {
		return p.env.h.Remove(ctx, f.Path, v[0] == "1")
	}, func() { p.open(p.dir) })
	p.setInfo(fmt.Sprintf("Удалить %s %s? Подтвердите в окне.", what, f.Path))
}

// pickerWindow — файловый диалог Fyne в своём отдельном окне (не поверх панели).
type picker struct {
	w      fyne.Window
	closed bool
}

func pickerWindow(title string) *picker {
	p := &picker{w: fyne.CurrentApp().NewWindow(title + " · VANTA")}
	p.w.Resize(fyne.NewSize(820, 560))
	p.w.SetOnClosed(func() { p.closed = true })
	if header.WindowHook != nil {
		header.WindowHook(p.w)
	}
	p.w.Show()
	return p
}

func (p *picker) Close() {
	if !p.closed {
		p.closed = true
		p.w.Close()
	}
}

func (p *filesPage) download() {
	f, ok := p.current()
	if !ok || f.IsDir {
		p.setInfo("Скачать можно только файл.")
		return
	}
	pw := pickerWindow("Сохранить " + f.Name)
	w := pw
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		w.Close()
		if err != nil || wc == nil {
			return
		}
		p.transfer("Скачать "+f.Name, func(ctx context.Context, out func(string)) error {
			defer wc.Close()
			out(fmt.Sprintf("%s → %s\n", f.Path, wc.URI().Path()))
			return p.env.h.Download(ctx, f.Path, wc, progressOut(out, f.Size))
		})
	}, pw.w)
	d.SetFileName(f.Name)
	d.Resize(fyne.NewSize(800, 540))
	d.SetOnClosed(func() { w.Close() })
	d.Show()
}

func (p *filesPage) upload() {
	dir := p.dir
	w := pickerWindow("Загрузить в " + dir)
	d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		w.Close()
		if err != nil || rc == nil {
			return
		}
		target := path.Join(dir, rc.URI().Name())
		var size int64 = -1
		if st, err := os.Stat(rc.URI().Path()); err == nil {
			size = st.Size()
		}
		p.transfer("Загрузить "+rc.URI().Name(), func(ctx context.Context, out func(string)) error {
			defer rc.Close()
			out(fmt.Sprintf("%s → %s\n", rc.URI().Path(), target))
			return p.env.h.Upload(ctx, rc, target, progressOut(out, size))
		})
	}, w.w)
	d.Resize(fyne.NewSize(800, 540))
	d.SetOnClosed(func() { w.Close() })
	d.Show()
}

func (p *filesPage) transfer(title string, fn func(ctx context.Context, out func(string)) error) {
	p.stream("xfer:"+p.env.sv.ID+title, title, false, fn, func() { p.open(p.dir) })
}

// progressOut — строка прогресса не чаще, чем раз на 5%.
func progressOut(out func(string), total int64) func(int64) {
	last := int64(-1)
	return func(n int64) {
		if total > 0 {
			pct := n * 100 / total
			if pct/5 != last/5 {
				last = pct
				out(fmt.Sprintf("%d%% (%s)\n", pct, host.Bytes(uint64(n))))
			}
		}
	}
}

// desktopSave — Cmd/Ctrl+S в редакторе файла.
var desktopSave = desktop.CustomShortcut{KeyName: fyne.KeyS, Modifier: fyne.KeyModifierShortcutDefault}
