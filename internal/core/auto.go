package core

import (
	"log/slog"
	"sort"

	"github.com/Trade-Cloud/vanta/internal/prefs"
)

// AutoRefresh — обновляется ли вкладка tab сама (processes, network,
// storage). По умолчанию — да; выключенные хранятся в prefs.AutoOff.
// Вкладка Resources — отдельно, ResourcesAuto.
func (s *Store) AutoRefresh(tab string) bool { return !s.autoOff[tab] }

func (s *Store) SetAutoRefresh(tab string, on bool) {
	if s.AutoRefresh(tab) == on {
		return
	}
	if s.autoOff == nil {
		s.autoOff = map[string]bool{}
	}
	if on {
		delete(s.autoOff, tab)
	} else {
		s.autoOff[tab] = true
	}
	off := make([]string, 0, len(s.autoOff))
	for k := range s.autoOff {
		off = append(off, k)
	}
	sort.Strings(off)
	if err := prefs.Update(func(st *prefs.State) { st.AutoOff = off }); err != nil {
		slog.Error("не удалось сохранить настройку автообновления", "tab", tab, "err", err)
	}
	s.emit(TopicSettings)
}

func offSet(list []string) map[string]bool {
	m := map[string]bool{}
	for _, k := range list {
		m[k] = true
	}
	return m
}
