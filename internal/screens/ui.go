package screens

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/header"
)

// Кирпичики страниц панели. Отступы — только из шкалы ниже, цвета — только
// именами темы (header.Text / header.Rect), чтобы всё жило при смене темы.

const (
	sp1 float32 = 4
	sp2 float32 = 8
	sp3 float32 = 12
	sp4 float32 = 16
	sp5 float32 = 20
	sp6 float32 = 24

	radiusCard float32 = 0
	radiusChip float32 = 0
)

// ---------- раскладки ----------

// padLayout — отступы слева/сверху/справа/снизу.
type padLayout struct{ l, t, r, b float32 }

func (p padLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(p.l, p.t))
		o.Resize(fyne.NewSize(size.Width-p.l-p.r, size.Height-p.t-p.b))
	}
}

func (p padLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, o := range objs {
		m = m.Max(o.MinSize())
	}
	return m.Add(fyne.NewSize(p.l+p.r, p.t+p.b))
}

func pad(l, t, r, b float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(padLayout{l, t, r, b}, o)
}

func padXY(x, y float32, o fyne.CanvasObject) fyne.CanvasObject { return pad(x, y, x, y, o) }

// stackLayout — ряд/колонка с точным зазором (VBox/HBox берут его из темы).
// В ряду дети выровнены по вертикали по центру, layout.Spacer растягивается.
type stackLayout struct {
	gap   float32
	horiz bool
}

func isSpacer(o fyne.CanvasObject) bool {
	_, ok := o.(layout.SpacerObject)
	return ok
}

func (s stackLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var used float32
	spacers, n := 0, 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		n++
		if isSpacer(o) {
			spacers++
			continue
		}
		if s.horiz {
			used += o.MinSize().Width
		} else {
			used += o.MinSize().Height
		}
	}
	if n > 1 {
		used += s.gap * float32(n-1)
	}
	var extra float32
	total := size.Height
	if s.horiz {
		total = size.Width
	}
	if spacers > 0 && total > used {
		extra = (total - used) / float32(spacers)
	}
	var at float32
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		if s.horiz {
			w := m.Width
			if isSpacer(o) {
				w = extra
			}
			o.Move(fyne.NewPos(at, (size.Height-m.Height)/2))
			o.Resize(fyne.NewSize(w, m.Height))
			at += w + s.gap
		} else {
			h := m.Height
			if isSpacer(o) {
				h = extra
			}
			o.Move(fyne.NewPos(0, at))
			o.Resize(fyne.NewSize(size.Width, h))
			at += h + s.gap
		}
	}
}

func (s stackLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		n++
		if isSpacer(o) {
			continue
		}
		om := o.MinSize()
		if s.horiz {
			m.Width += om.Width
			m.Height = fyne.Max(m.Height, om.Height)
		} else {
			m.Height += om.Height
			m.Width = fyne.Max(m.Width, om.Width)
		}
	}
	if n > 1 {
		if s.horiz {
			m.Width += s.gap * float32(n-1)
		} else {
			m.Height += s.gap * float32(n-1)
		}
	}
	return m
}

func vstack(gap float32, objs ...fyne.CanvasObject) *fyne.Container {
	return container.New(stackLayout{gap: gap}, objs...)
}

func hstack(gap float32, objs ...fyne.CanvasObject) *fyne.Container {
	return container.New(stackLayout{gap: gap, horiz: true}, objs...)
}

// colsLayout — колонки таблицы: ширина < 0 — гибкая (делят остаток поровну).
// Ячейка по вертикали по центру, по ширине — на всю колонку.
type colsLayout struct {
	widths []float32
	gap    float32
}

func (c colsLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var fixed float32
	flex := 0
	for _, w := range c.widths {
		if w < 0 {
			flex++
		} else {
			fixed += w
		}
	}
	fixed += c.gap * float32(len(c.widths)-1)
	var fw float32
	if flex > 0 {
		fw = fyne.Max(0, (size.Width-fixed)/float32(flex))
	}
	var x float32
	for i, o := range objs {
		if i >= len(c.widths) {
			break
		}
		w := c.widths[i]
		if w < 0 {
			w = fw
		}
		h := o.MinSize().Height
		o.Move(fyne.NewPos(x, (size.Height-h)/2))
		o.Resize(fyne.NewSize(w, h))
		x += w + c.gap
	}
}

func (c colsLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for i, o := range objs {
		if i >= len(c.widths) {
			break
		}
		om := o.MinSize()
		w := c.widths[i]
		if w < 0 {
			w = om.Width
		}
		m.Width += w
		m.Height = fyne.Max(m.Height, om.Height)
	}
	m.Width += c.gap * float32(len(c.widths)-1)
	return m
}

func cols(widths []float32, gap float32, cells ...fyne.CanvasObject) *fyne.Container {
	return container.New(colsLayout{widths: widths, gap: gap}, cells...)
}

// square — объект ровно size×size.
func square(size float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewCenter(container.NewGridWrap(fyne.NewSquareSize(size), o))
}

// ---------- текст ----------

func txt(s string, size float32, c fyne.ThemeColorName, bold bool) *header.Text {
	return header.NewText(s, size, c, bold)
}

// title — заголовок страницы/окна.
func title(s string, size float32) *header.Text {
	return txt(s, size, theme.ColorNameForeground, true)
}

// muted — вторичный текст.
func muted(s string) *header.Text {
	return txt(s, theme.CaptionTextSize(), theme.ColorNamePlaceHolder, false)
}

// capsLabel — подпись колонки/поля: мелко, жирно, приглушённо.
func capsLabel(s string) *header.Text {
	return txt(s, theme.CaptionTextSize()-1, theme.ColorNamePlaceHolder, true)
}

func mono(s string, size float32) *header.Text {
	t := txt(s, size, theme.ColorNameForeground, false)
	t.Mono = true
	return t
}

func truncated(t *header.Text) *header.Text {
	t.Truncate = true
	return t
}

// ---------- иконки ----------

func svg(name, path string) fyne.Resource {
	data := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000000" d="` + path + `"/></svg>`
	return theme.NewThemedResource(fyne.NewStaticResource(name, []byte(data)))
}

var (
	icKey      = svg("key.svg", "M12.65 10C11.83 7.67 9.61 6 7 6c-3.31 0-6 2.69-6 6s2.69 6 6 6c2.61 0 4.83-1.67 5.65-4H17v4h4v-4h2v-4H12.65zM7 14c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2z")
	icLock     = svg("lock.svg", "M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z")
	icLockOpen = svg("lock-open.svg", "M12 17c1.1 0 2-.9 2-2s-.9-2-2-2-2 .9-2 2 .9 2 2 2zm6-9h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6h1.9c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm0 12H6V10h12v10z")
	icFinger   = svg("fingerprint.svg", "M17.81 4.47c-.08 0-.16-.02-.23-.06C15.66 3.42 14 3 12.01 3c-1.98 0-3.86.47-5.57 1.41-.24.13-.54.04-.68-.2-.13-.24-.04-.55.2-.68C7.82 2.52 9.86 2 12.01 2c2.13 0 3.99.47 6.03 1.52.25.13.34.43.21.67-.09.18-.26.28-.44.28zM3.5 9.72c-.1 0-.2-.03-.29-.09-.23-.16-.28-.47-.12-.7.99-1.4 2.25-2.5 3.75-3.27C9.98 4.04 14 4.03 17.15 5.65c1.5.77 2.76 1.86 3.75 3.25.16.22.11.54-.12.7-.23.16-.54.11-.7-.12-.9-1.26-2.04-2.25-3.39-2.94-2.87-1.47-6.54-1.47-9.4.01-1.36.7-2.5 1.7-3.4 2.96-.08.14-.23.21-.39.21zm6.25 12.07c-.13 0-.26-.05-.35-.15-.87-.87-1.34-1.43-2.01-2.64-.69-1.23-1.05-2.73-1.05-4.34 0-2.97 2.54-5.39 5.66-5.39s5.66 2.42 5.66 5.39c0 .28-.22.5-.5.5s-.5-.22-.5-.5c0-2.42-2.09-4.39-4.66-4.39-2.57 0-4.66 1.97-4.66 4.39 0 1.44.32 2.77.93 3.85.64 1.15 1.08 1.64 1.85 2.42.19.2.19.51 0 .71-.11.1-.24.15-.37.15zm7.17-1.85c-1.19 0-2.24-.3-3.1-.89-1.49-1.01-2.38-2.65-2.38-4.39 0-.28.22-.5.5-.5s.5.22.5.5c0 1.41.72 2.74 1.94 3.56.71.48 1.54.71 2.54.71.24 0 .64-.03 1.04-.1.27-.05.53.13.58.41.05.27-.13.53-.41.58-.57.11-1.07.12-1.21.12zM14.91 22c-.04 0-.09-.01-.13-.02-1.59-.44-2.63-1.03-3.72-2.1-1.4-1.39-2.17-3.24-2.17-5.22 0-1.62 1.38-2.94 3.08-2.94 1.7 0 3.08 1.32 3.08 2.94 0 1.07.93 1.94 2.08 1.94s2.08-.87 2.08-1.94c0-3.77-3.25-6.83-7.25-6.83-2.84 0-5.44 1.58-6.61 4.03-.39.81-.59 1.76-.59 2.8 0 .78.07 2.01.67 3.61.1.26-.03.55-.29.64-.26.1-.55-.04-.64-.29-.49-1.31-.73-2.61-.73-3.96 0-1.2.23-2.29.68-3.24 1.33-2.79 4.28-4.6 7.51-4.6 4.55 0 8.25 3.51 8.25 7.83 0 1.62-1.38 2.94-3.08 2.94s-3.08-1.32-3.08-2.94c0-1.07-.93-1.94-2.08-1.94s-2.08.87-2.08 1.94c0 1.71.66 3.31 1.87 4.51.95.94 1.86 1.46 3.27 1.85.27.07.42.35.35.61-.05.23-.26.38-.47.38z")
	icShield   = svg("shield.svg", "M12 1L3 5v6c0 5.55 3.84 10.74 9 12 5.16-1.26 9-6.45 9-12V5l-9-4zm0 10.99h7c-.53 4.12-3.28 7.79-7 8.94V12H5V6.3l7-3.11v8.8z")
)

func tinted(res fyne.Resource, c fyne.ThemeColorName) fyne.Resource {
	return theme.NewColoredResource(res, c)
}

// iconTile — иконка цвета c на квадратной подложке того же цвета (прозрачной).
func iconTile(res fyne.Resource, c fyne.ThemeColorName, size float32) fyne.CanvasObject {
	bg := header.NewRect(c, "", 0, size/4)
	bg.Alpha = 34
	return square(size, container.NewStack(bg, container.NewCenter(
		container.NewGridWrap(fyne.NewSquareSize(size*0.55), widget.NewIcon(tinted(res, c))))))
}

// chip — плашка «иконка + текст» с рамкой цвета c.
func chip(res fyne.Resource, label string, c fyne.ThemeColorName) fyne.CanvasObject {
	frame := header.NewRect(c, c, 1, radiusChip)
	frame.Alpha = 22
	var row *fyne.Container
	t := txt(label, theme.CaptionTextSize(), c, true)
	if res != nil {
		row = hstack(sp1+1, square(13, widget.NewIcon(tinted(res, c))), t)
	} else {
		row = hstack(0, t)
	}
	return container.NewHBox(container.NewStack(frame, pad(sp2, 3, sp2+1, 3, row)))
}

// iconButton — кнопка-иконка без подписи.
func iconButton(res fyne.Resource, fn func()) *widget.Button {
	b := widget.NewButtonWithIcon("", res, fn)
	b.Importance = widget.LowImportance
	return b
}

// card — рамка с фоном вокруг содержимого.
func card(content fyne.CanvasObject) fyne.CanvasObject {
	return container.NewStack(header.NewRect(theme.ColorNameInputBackground, theme.ColorNameSeparator, 1, radiusCard), content)
}

// hline — горизонтальная линия 1px.
func hline() fyne.CanvasObject {
	return container.New(&lineLayout{}, header.NewRect(theme.ColorNameSeparator, "", 0, 0))
}

type lineLayout struct{}

func (lineLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(size.Width, 1))
	}
}

func (lineLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, 1) }

// field — подпись над полем ввода и подсказка под ним.
func field(label string, input fyne.CanvasObject, hint fyne.CanvasObject) fyne.CanvasObject {
	box := vstack(sp2-2, capsLabel(label), input)
	if hint != nil {
		box.Add(hint)
	}
	return box
}

// hint — строка-подсказка, цвет меняется через setHint.
func hint() *header.Text { return muted("") }

func setHint(t *header.Text, s string, c fyne.ThemeColorName) {
	t.Text, t.ColorName = s, c
	t.Refresh()
}
