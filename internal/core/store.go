// Package core — общее состояние панели «Основа»: серверы и их здоровье,
// активный сервер, уведомления, текущий раздел, тема, история поиска.
//
// Это единый источник правды для header и страниц. UI ничего не хранит сам:
// читает из Store и подписывается на темы (On). Поэтому переключение сервера
// или раздела меняет контекст без пересоздания окна.
//
// Все методы Store вызываются из UI-горутины. Ответы Backend приходят из
// своих горутин и переносятся в UI через post (в приложении это fyne.Do).
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/host"
	"github.com/Trade-Cloud/vanta/internal/prefs"
)

// StaleAfter — сколько ждать heartbeat, прежде чем считать, что агент не отвечает.
// После этого header показывает «нет ответа», а не последнее известное состояние.
const StaleAfter = 10 * time.Second

const (
	loadTimeout = 8 * time.Second
	// connectTimeout — общий предел Connect. Большой, потому что внутри
	// бывают вопросы человеку (ключ хоста, пароль); сетевые таймауты
	// Backend держит сам.
	connectTimeout = 5 * time.Minute
	historyMax     = 8
)

// Topic — что поменялось. Подписчик сам перечитывает нужное из Store.
type Topic int

const (
	TopicServers       Topic = iota // список серверов, загрузка/ошибка
	TopicActive                     // выбран другой сервер / сменилось подключение
	TopicHealth                     // ping и heartbeat (тикает раз в секунду)
	TopicNotifications              // уведомления
	TopicRoute                      // текущий раздел
	TopicTheme                      // тема оформления
	TopicHistory                    // история поиска
	TopicSettings                   // настройки приложения
)

// Status — то, что показывает индикатор сервера.
type Status int

const (
	StatusNone       Status = iota // сервер не выбран / не подключён
	StatusConnecting               // идёт SSH-подключение или ждём первый heartbeat
	StatusFailed                   // подключиться не удалось
	StatusOnline
	StatusDegraded
	StatusOffline
	StatusNoResponse // heartbeat давно не приходил — состояние неизвестно
)

func (s Status) Label() string {
	switch s {
	case StatusNone:
		return "Не подключён"
	case StatusConnecting:
		return "Подключение…"
	case StatusFailed:
		return "Ошибка подключения"
	case StatusOnline:
		return "Online"
	case StatusDegraded:
		return "Degraded"
	case StatusOffline:
		return "Offline"
	case StatusNoResponse:
		return "Нет ответа"
	}
	return "?"
}

// Health — статус сервера и когда агент последний раз выходил на связь.
type Health struct {
	Status   Status
	LastSeen time.Time // ноль — ни разу
}

type Store struct {
	be   agent.Backend
	post func(func())

	servers        []agent.Server
	serversLoaded  bool // хоть раз загрузили (кэш есть)
	serversLoading bool
	serversErr     error

	beats       map[string]agent.Heartbeat
	reach       map[string]agent.Reach
	connectedAt time.Time // когда подключились к active — отсчёт для «нет ответа»
	active      string    // "" — сервер не выбран
	connecting  bool
	connErr     error
	connGen     int // ответ старого Connect игнорируем
	connCancel  context.CancelFunc

	notes       []agent.Notification
	notesLoaded bool
	localID     int

	history []string
	theme   string
	route   Route
	autoRes bool // автообновление вкладки Resources
	// Вкладки с выключенным автообновлением (AutoRefresh).
	autoOff map[string]bool

	subs   map[Topic]map[int]func()
	nextID int

	unsub func()
	stop  chan struct{}

	// Работа с подключённым сервером (вкладки Resources, Services…).
	host    *host.Host
	hostGen int
	// SudoAsker спрашивает пароль sudo (окно). Задаётся до Start.
	SudoAsker func(ctx context.Context, sv agent.Server, retry bool) (string, bool)
}

// clientSource — Backend, у которого можно взять SSH-клиент подключения
// (remote.Backend; у Mock его нет).
type clientSource interface {
	Client(id string) *ssh.Client
}

// Host — подключённый сервер для команд; nil — не подключены (или Backend
// без SSH, например Mock). Объект живёт, пока живо подключение: после
// переподключения это будет уже другой *host.Host.
func (s *Store) Host() *host.Host {
	if !s.Connected() {
		s.dropHost()
		return nil
	}
	if s.host != nil && s.hostGen == s.connGen {
		return s.host
	}
	s.dropHost()
	cs, ok := s.be.(clientSource)
	if !ok {
		return nil
	}
	c := cs.Client(s.active)
	if c == nil {
		return nil
	}
	sv, _ := s.Active()
	var ask host.SudoAsker
	if s.SudoAsker != nil {
		f := s.SudoAsker
		ask = func(ctx context.Context, _ string, retry bool) (string, bool) { return f(ctx, sv, retry) }
	}
	s.host = host.New(c, sv.SSHUser(), ask)
	s.hostGen = s.connGen
	return s.host
}

func (s *Store) dropHost() {
	if s.host != nil {
		go s.host.Close()
		s.host = nil
	}
}

// NewStore создаёт состояние. post должен выполнить функцию в UI-горутине.
func NewStore(be agent.Backend, post func(func()), st prefs.State) *Store {
	s := &Store{
		be:      be,
		post:    post,
		beats:   map[string]agent.Heartbeat{},
		reach:   map[string]agent.Reach{},
		theme:   st.Theme,
		history: st.SearchHistory,
		autoRes: !st.ResourcesAutoOff,
		autoOff: offSet(st.AutoOff),
		route:   RouteOverview,
		subs:    map[Topic]map[int]func(){},
	}
	return s
}

// Start подписывается на события Backend и загружает данные.
func (s *Store) Start() {
	s.stop = make(chan struct{})
	s.unsub = s.be.Subscribe(func(e agent.Event) { s.post(func() { s.handle(e) }) })
	s.ReloadServers()
	s.loadNotifications()

	// Раз в секунду пересчитываем свежесть heartbeat: статус «нет ответа»
	// должен появиться сам, даже если событий нет совсем.
	go func(stop chan struct{}) {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.post(func() { s.emit(TopicHealth) })
			}
		}
	}(s.stop)
}

// Stop отписывается от Backend и останавливает таймеры.
func (s *Store) Stop() {
	if s.unsub != nil {
		s.unsub()
		s.unsub = nil
	}
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
}

// On подписывает fn на тему. Возвращает отписку.
func (s *Store) On(t Topic, fn func()) (off func()) {
	s.nextID++
	id := s.nextID
	if s.subs[t] == nil {
		s.subs[t] = map[int]func(){}
	}
	s.subs[t][id] = fn
	return func() { delete(s.subs[t], id) }
}

func (s *Store) emit(t Topic) {
	ids := make([]int, 0, len(s.subs[t]))
	for id := range s.subs[t] {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if fn, ok := s.subs[t][id]; ok {
			fn()
		}
	}
}

func (s *Store) handle(e agent.Event) {
	if hb := e.Heartbeat; hb != nil {
		s.beats[hb.ServerID] = *hb
		s.emit(TopicHealth)
	}
	if r := e.Reach; r != nil {
		s.reach[r.ServerID] = *r
		s.emit(TopicHealth)
	}
	if n := e.Notification; n != nil {
		s.addNote(*n)
	}
	if l := e.Lost; l != nil && l.ServerID == s.active && !s.connecting && s.connErr == nil {
		s.connErr = l.Err
		delete(s.beats, l.ServerID)
		if sv, ok := s.Server(l.ServerID); ok {
			s.Notify(agent.SeverityError, "Соединение с "+sv.Name+" потеряно", l.Err.Error())
		}
		s.emit(TopicActive)
	}
	if e.ServersChanged {
		s.ReloadServers()
	}
}

// cancelConnect прерывает идущий Connect (и закрывает окна вопросов).
func (s *Store) cancelConnect() {
	if s.connCancel != nil {
		s.connCancel()
		s.connCancel = nil
	}
}

// ---------- серверы ----------

// ServersState — список (последний удачный, т.е. кэш), идёт ли загрузка и
// ошибка последней попытки.
func (s *Store) ServersState() (list []agent.Server, loaded, loading bool, err error) {
	return s.servers, s.serversLoaded, s.serversLoading, s.serversErr
}

func (s *Store) Server(id string) (agent.Server, bool) {
	for _, sv := range s.servers {
		if sv.ID == id {
			return sv, true
		}
	}
	return agent.Server{}, false
}

// ReloadServers перечитывает список. При ошибке старый список остаётся.
func (s *Store) ReloadServers() {
	if s.serversLoading {
		return
	}
	s.serversLoading = true
	s.emit(TopicServers)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		list, err := s.be.Servers(ctx)
		s.post(func() {
			s.serversLoading = false
			s.serversErr = err
			if err == nil {
				s.servers = list
				s.serversLoaded = true
				s.fixActive()
			} else {
				slog.Error("не удалось загрузить серверы", "err", err)
			}
			s.emit(TopicServers)
		})
	}()
}

// fixActive — выбранный сервер пропал из списка: отключаемся.
func (s *Store) fixActive() {
	if s.active != "" {
		if _, ok := s.Server(s.active); !ok {
			s.Disconnect()
			return
		}
	}
	s.emit(TopicActive)
}

// Active — выбранный сервер; ok=false — не выбран. При старте сервер не
// выбран никогда: пользователь сам выбирает, куда подключаться.
func (s *Store) Active() (agent.Server, bool) {
	if s.active == "" {
		return agent.Server{}, false
	}
	return s.Server(s.active)
}

// Connected — подключены к активному серверу.
func (s *Store) Connected() bool {
	return s.active != "" && !s.connecting && s.connErr == nil
}

// ConnError — почему не удалось подключиться к активному серверу.
func (s *Store) ConnError() error { return s.connErr }

// SetActive выбирает сервер и подключается к нему. Окно не пересоздаётся:
// подписчики TopicActive сами обновляют свой контекст.
func (s *Store) SetActive(id string) {
	if _, ok := s.Server(id); !ok {
		return
	}
	if id == s.active && (s.connecting || s.connErr == nil) {
		return // уже подключены или подключаемся
	}
	s.cancelConnect()
	if s.active != "" && s.active != id {
		s.be.Disconnect(s.active)
	}
	s.active, s.connecting, s.connErr = id, true, nil
	delete(s.beats, id)
	s.connGen++
	gen := s.connGen
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	s.connCancel = cancel
	s.emit(TopicActive)
	go func() {
		defer cancel()
		err := s.be.Connect(ctx, id)
		s.post(func() {
			if gen != s.connGen {
				return
			}
			s.connCancel = nil
			if errors.Is(err, agent.ErrCancelled) {
				// Человек сам отказался (закрыл окно пароля/ключа хоста).
				s.be.Disconnect(id)
				s.active, s.connecting, s.connErr = "", false, nil
				s.connGen++
				s.emit(TopicActive)
				return
			}
			s.connecting, s.connErr = false, err
			s.connectedAt = time.Now()
			if err != nil {
				sv, _ := s.Server(id)
				s.Notify(agent.SeverityError, "Не удалось подключиться к "+sv.Name, err.Error())
			}
			s.emit(TopicActive)
		})
	}()
}

// Disconnect отключается от активного сервера, выбор сбрасывается.
func (s *Store) Disconnect() {
	if s.active == "" {
		return
	}
	s.cancelConnect()
	s.be.Disconnect(s.active)
	delete(s.beats, s.active)
	s.active, s.connecting, s.connErr = "", false, nil
	s.connGen++
	s.emit(TopicActive)
}

// Reach — последний ping сервера; ok=false — ещё не пинговали.
func (s *Store) Reach(id string) (agent.Reach, bool) {
	r, ok := s.reach[id]
	return r, ok
}

// Health — состояние активного сервера: подключение, затем свежесть heartbeat.
// Для неподключённого сервера — StatusNone (о нём известен только ping).
func (s *Store) Health(id string) Health {
	if id == "" || id != s.active {
		return Health{Status: StatusNone}
	}
	if s.connecting {
		return Health{Status: StatusConnecting}
	}
	if s.connErr != nil {
		return Health{Status: StatusFailed}
	}
	hb, ok := s.beats[id]
	if !ok {
		if time.Since(s.connectedAt) < StaleAfter {
			return Health{Status: StatusConnecting}
		}
		return Health{Status: StatusNoResponse}
	}
	h := Health{LastSeen: hb.At}
	if time.Since(hb.At) > StaleAfter {
		h.Status = StatusNoResponse
		return h
	}
	switch hb.State {
	case agent.StateOnline:
		h.Status = StatusOnline
	case agent.StateDegraded:
		h.Status = StatusDegraded
	case agent.StateOffline:
		h.Status = StatusOffline
	default:
		h.Status = StatusConnecting
	}
	return h
}

// SaveServer добавляет или меняет сервер; done вызывается в UI-горутине.
func (s *Store) SaveServer(sv agent.Server, done func(agent.Server, error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		saved, err := s.be.SaveServer(ctx, sv)
		s.post(func() {
			if err == nil {
				s.upsertLocal(saved)
			}
			if done != nil {
				done(saved, err)
			}
		})
	}()
}

// DeleteServer удаляет сервер (и его сохранённый секрет); done — в UI-горутине.
func (s *Store) DeleteServer(id string, done func(error)) {
	if id == s.active {
		s.Disconnect()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		err := s.be.DeleteServer(ctx, id)
		s.post(func() {
			if err == nil {
				for i := range s.servers {
					if s.servers[i].ID == id {
						s.servers = append(s.servers[:i:i], s.servers[i+1:]...)
						break
					}
				}
				delete(s.reach, id)
				s.emit(TopicServers)
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

// SetFavorite — сразу в UI, потом в Backend; при ошибке откатываем.
func (s *Store) SetFavorite(id string, fav bool) {
	sv, ok := s.Server(id)
	if !ok {
		return
	}
	old := sv
	sv.Favorite = fav
	s.upsertLocal(sv)
	s.SaveServer(sv, func(_ agent.Server, err error) {
		if err != nil {
			s.upsertLocal(old)
			s.Notify(agent.SeverityError, "Не удалось изменить избранное", err.Error())
		}
	})
}

func (s *Store) upsertLocal(sv agent.Server) {
	for i := range s.servers {
		if s.servers[i].ID == sv.ID {
			s.servers[i] = sv
			s.emit(TopicServers)
			s.emit(TopicActive)
			return
		}
	}
	s.servers = append(s.servers, sv)
	s.emit(TopicServers)
}

// ---------- уведомления ----------

func (s *Store) loadNotifications() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		list, err := s.be.Notifications(ctx)
		s.post(func() {
			s.notesLoaded = true
			if err != nil {
				s.Notify(agent.SeverityError, "Не удалось загрузить уведомления", err.Error())
				return
			}
			// Живые события могли прийти раньше истории — не теряем их.
			s.notes = append(s.notes, list...)
			s.sortNotes()
			s.emit(TopicNotifications)
		})
	}()
}

func (s *Store) sortNotes() {
	sort.SliceStable(s.notes, func(i, j int) bool { return s.notes[i].At.After(s.notes[j].At) })
}

func (s *Store) addNote(n agent.Notification) {
	s.notes = append([]agent.Notification{n}, s.notes...)
	s.emit(TopicNotifications)
}

// Notify добавляет уведомление от самого приложения.
func (s *Store) Notify(sev agent.Severity, title, body string) {
	s.localID++
	s.addNote(agent.Notification{
		ID: fmt.Sprintf("local-%d", s.localID), ServerID: s.active,
		Severity: sev, Title: title, Body: body, At: time.Now(),
	})
}

// Notifications — все уведомления, новые первыми; loaded=false — история ещё грузится.
func (s *Store) Notifications() (list []agent.Notification, loaded bool) {
	return s.notes, s.notesLoaded
}

func (s *Store) Unread() int {
	n := 0
	for _, x := range s.notes {
		if !x.Read {
			n++
		}
	}
	return n
}

func (s *Store) MarkRead(id string) {
	for i := range s.notes {
		if s.notes[i].ID == id && !s.notes[i].Read {
			s.notes[i].Read = true
			s.emit(TopicNotifications)
		}
	}
}

func (s *Store) MarkAllRead() {
	for i := range s.notes {
		s.notes[i].Read = true
	}
	s.emit(TopicNotifications)
}

func (s *Store) ClearNotifications() {
	s.notes = nil
	s.emit(TopicNotifications)
}

// ---------- навигация, тема, история поиска ----------

func (s *Store) Route() Route { return s.route }

func (s *Store) Navigate(r Route) {
	if r == "" || r == s.route {
		return
	}
	s.route = r
	s.emit(TopicRoute)
}

// Theme — "" (как в ОС), "light" или "dark".
func (s *Store) Theme() string { return s.theme }

func (s *Store) SetTheme(t string) {
	if t == s.theme {
		return
	}
	s.theme = t
	if err := prefs.Update(func(st *prefs.State) { st.Theme = t }); err != nil {
		slog.Error("не удалось сохранить тему", "err", err)
	}
	s.emit(TopicTheme)
}

// ResourcesAuto — обновлять ли вкладку Resources самой (раз в 2 с).
func (s *Store) ResourcesAuto() bool { return s.autoRes }

func (s *Store) SetResourcesAuto(on bool) {
	if on == s.autoRes {
		return
	}
	s.autoRes = on
	if err := prefs.Update(func(st *prefs.State) { st.ResourcesAutoOff = !on }); err != nil {
		slog.Error("не удалось сохранить настройку автообновления", "err", err)
	}
	s.emit(TopicSettings)
}

func (s *Store) History() []string { return s.history }

func (s *Store) AddHistory(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return
	}
	h := []string{q}
	for _, x := range s.history {
		if !strings.EqualFold(x, q) && len(h) < historyMax {
			h = append(h, x)
		}
	}
	s.history = h
	s.saveHistory()
}

func (s *Store) ClearHistory() {
	s.history = nil
	s.saveHistory()
}

func (s *Store) saveHistory() {
	h := s.history
	if err := prefs.Update(func(st *prefs.State) { st.SearchHistory = h }); err != nil {
		slog.Error("не удалось сохранить историю поиска", "err", err)
	}
	s.emit(TopicHistory)
}
