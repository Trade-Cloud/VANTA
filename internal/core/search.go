package core

import (
	"sort"
	"strings"
)

// ResultKind — что нашёл глобальный поиск.
type ResultKind int

const (
	KindSection  ResultKind = iota // раздел приложения
	KindObject                     // объект: сервер
	KindFunction                   // функция: настройки
)

func (k ResultKind) Label() string {
	switch k {
	case KindSection:
		return "Раздел"
	case KindObject:
		return "Сервер"
	case KindFunction:
		return "Функция"
	}
	return ""
}

// Result — одна строка выдачи. Заполнено ровно одно из Route/ServerID.
type Result struct {
	Kind     ResultKind
	Title    string
	Hint     string
	Route    Route
	ServerID string
	score    int
}

// Search ищет по разделам, серверам и функциям.
// Пустой запрос — всё подряд (для Command Palette); header при пустом
// запросе показывает историю и сюда не ходит.
func (s *Store) Search(q string, limit int) []Result {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []Result
	add := func(r Result, fields ...string) {
		best := 0
		for _, f := range fields {
			if sc := match(strings.ToLower(f), q); sc > best {
				best = sc
			}
		}
		if q == "" {
			best = 1
		}
		if best > 0 {
			r.score = best
			out = append(out, r)
		}
	}

	add(Result{Kind: KindSection, Title: "Overview", Hint: "Обзор сервера", Route: RouteOverview}, "overview", "обзор")
	for _, sec := range Sections {
		for _, pg := range sec.Pages {
			add(Result{Kind: KindSection, Title: pg.Title, Hint: sec.Title + " · " + pg.Description, Route: pg.Route},
				pg.Title, sec.Title, pg.Description)
		}
	}
	for _, sv := range s.servers {
		add(Result{Kind: KindObject, Title: sv.Name, Hint: sv.IP + " · " + sv.Env.Label(), ServerID: sv.ID},
			sv.Name, sv.IP, string(sv.Env), sv.Group)
	}
	for _, pg := range SettingsPages {
		add(Result{Kind: KindFunction, Title: pg.Title, Hint: pg.Description, Route: pg.Route}, pg.Title, pg.Description)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// match — 0 нет совпадения; больше — лучше: начало строки > начало слова > подстрока.
func match(field, q string) int {
	switch {
	case q == "":
		return 0
	case strings.HasPrefix(field, q):
		return 3
	case strings.Contains(field, " "+q) || strings.Contains(field, "-"+q) || strings.Contains(field, "/"+q):
		return 2
	case strings.Contains(field, q):
		return 1
	}
	return 0
}
