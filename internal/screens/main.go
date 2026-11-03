package screens

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Trade-Cloud/vanta/internal/apptheme"
	"github.com/Trade-Cloud/vanta/internal/core"
	"github.com/Trade-Cloud/vanta/internal/header"
)

// Main — окно «Основа»: панель. Сверху sticky header (не прокручивается
// вместе со страницей), под ним — страница текущего раздела, поверх всего —
// слой плавающих панелей header.
//
// Смена темы пересобирает содержимое: часть цветов задана при создании
// объектов. Состояние живёт в core.Store, поэтому пересборка ничего не теряет.
func Main(w fyne.Window, st *core.Store) fyne.CanvasObject {
	root := container.NewStack()
	var dispose func()
	build := func() {
		if dispose != nil {
			dispose()
		}
		layer := header.NewLayer()
		hd := header.New(w, st, layer)
		pg := newSectionPage(st)
		body := container.NewBorder(hd.Object(), nil, nil, nil, container.NewVScroll(pg.obj))
		root.Objects = []fyne.CanvasObject{body, layer.Object()}
		root.Refresh()
		dispose = func() {
			hd.Dispose()
			pg.dispose()
		}
	}
	build()
	// Ещё раз, когда цикл приложения уже запущен: окно строится до a.Run(),
	// и цвета, взятые из темы в этот момент, могли не совпасть с итоговыми.
	fyne.Do(build)

	app := fyne.CurrentApp()
	st.On(core.TopicTheme, func() { apptheme.Apply(app, st.Theme()) })
	app.Settings().AddListener(func(fyne.Settings) { fyne.Do(build) })
	return root
}

// sectionPage — заглушка контента раздела: заголовок, контекст сервера, «в разработке».
type sectionPage struct {
	st     *core.Store
	obj    *fyne.Container
	status *widget.Label
	note   *widget.Label
	offs   []func()
}

func newSectionPage(st *core.Store) *sectionPage {
	p := &sectionPage{st: st, obj: container.NewVBox()}
	p.offs = []func(){
		st.On(core.TopicRoute, p.build),
		st.On(core.TopicActive, p.build),
		st.On(core.TopicServers, p.build),
		st.On(core.TopicHealth, p.tick),
	}
	p.build()
	return p
}

func (p *sectionPage) dispose() {
	for _, off := range p.offs {
		off()
	}
}

func (p *sectionPage) build() {
	title, trail := core.PageOf(p.st.Route())
	// Только виджеты Fyne: они берут цвета из темы при отрисовке.
	h := widget.NewLabel(title)
	h.SizeName = theme.SizeNameHeadingText
	h.TextStyle = fyne.TextStyle{Bold: true}
	crumbs := widget.NewLabel(trail)
	crumbs.SizeName = theme.SizeNameCaptionText

	p.status = widget.NewLabel("")
	p.status.SizeName = theme.SizeNameCaptionText
	p.note = widget.NewLabel("")
	p.note.Wrapping = fyne.TextWrapWord

	var server fyne.CanvasObject
	if sv, ok := p.st.Active(); ok {
		name := widget.NewLabel(sv.Name)
		name.TextStyle = fyne.TextStyle{Bold: true}
		server = widget.NewCard("", "", container.NewVBox(
			container.NewHBox(name, p.status),
			widget.NewLabel(sv.IP+" · "+sv.Env.Label()),
		))
	} else {
		server = widget.NewCard("", "", widget.NewLabel("Сервер не выбран. Выберите сервер в header — VANTA подключится к нему по SSH."))
		p.note.Hide()
	}
	stub := widget.NewLabel("Раздел «" + title + "» в разработке. Здесь появится его содержимое; header от страницы не зависит.")
	stub.Wrapping = fyne.TextWrapWord

	p.obj.Objects = []fyne.CanvasObject{
		container.NewVBox(h, crumbs),
		server,
		p.note,
		stub,
	}
	p.tick()
	p.obj.Refresh()
}

// tick — статус активного сервера; раз в секунду.
func (p *sectionPage) tick() {
	sv, ok := p.st.Active()
	if !ok || p.status == nil {
		return
	}
	hl := p.st.Health(sv.ID)
	p.status.SetText(hl.Status.Label())
	p.note.Hide()
	if hl.Status == core.StatusNoResponse {
		p.note.SetText("Агент не отвечает: данные на странице могут быть устаревшими.")
		p.note.Show()
	}
}
