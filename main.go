package main

import (
	"log/slog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/apptheme"
	"github.com/Trade-Cloud/vanta/internal/core"
	"github.com/Trade-Cloud/vanta/internal/debugbridge"
	"github.com/Trade-Cloud/vanta/internal/header"
	"github.com/Trade-Cloud/vanta/internal/prefs"
	"github.com/Trade-Cloud/vanta/internal/screens"
	"github.com/Trade-Cloud/vanta/internal/stage"
)

const appID = "org.trade-cloud.vanta"

func main() {
	// Debug-мост для debugger/. Без VANTA_DEBUG вернёт nil и ничего не делает.
	dbg := debugbridge.FromEnv()
	defer dbg.Close()

	// Состояние читаем один раз при старте. Сброс первого запуска делается
	// в debugger/ при выключенной VANTA и подхватывается здесь.
	st, err := prefs.Load()
	if err != nil {
		slog.Error("не удалось прочитать состояние, считаем первым запуском", "err", err)
	}

	a := app.NewWithID(appID)
	apptheme.Apply(a, st.Theme)

	// Агента пока нет: данные панели — из Mock за интерфейсом agent.Backend.
	backend := agent.NewMock()
	defer backend.Close()
	store := core.NewStore(backend, fyne.Do, st)
	store.Start()
	defer store.Stop()

	m := stage.NewManager(a)
	m.OnWindow = dbg.Attach
	header.WindowHook = dbg.Attach // отдельные окна header (сервер) — тоже в отладчик

	m.Register(stage.Start, stage.Spec{
		Title: "Vanta",
		Size:  fyne.NewSize(610, 440),
		Fixed: true,
		Build: func(fyne.Window) fyne.CanvasObject {
			return screens.Start(func() {
				if err := prefs.Update(func(s *prefs.State) { s.OnboardingDone = true }); err != nil {
					slog.Error("не удалось сохранить первый запуск", "err", err)
				}
				m.Go(stage.Main)
			})
		},
	})
	m.Register(stage.Main, stage.Spec{
		Title:   "Vanta",
		Size:    fyne.NewSize(1000, 700),
		MinSize: fyne.NewSize(830, 460),
		Build:   func(w fyne.Window) fyne.CanvasObject { return screens.Main(w, store) },
	})

	if st.OnboardingDone {
		m.Go(stage.Main)
	} else {
		m.Go(stage.Start)
	}
	a.Run()
}
