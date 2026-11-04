package agent

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Mock — фальшивый Backend, пока нет настоящего агента.
//
// Что он умеет показать в UI:
//   - задержку загрузки (skeleton);
//   - ping всех серверов (backup-01 не пингуется) и SSH-подключение к одному
//     (к backup-01 подключиться нельзя);
//   - сервер, чей агент «замолкает» через silentAfter и перестаёт слать
//     heartbeat (staging-01) — header должен показать «нет ответа»;
//   - сервер в offline и сервер в degraded;
//   - случайные уведомления.
type Mock struct {
	mu      sync.Mutex
	servers []Server
	states  map[string]ConnState
	silent  map[string]time.Time // с этого момента агент молчит
	conn    map[string]bool      // подключённые серверы
	subs    map[int]func(Event)
	nextSub int
	nextID  int
	started time.Time
	stop    chan struct{}
	once    sync.Once
}

const (
	mockLatency  = 700 * time.Millisecond
	mockBeat     = 3 * time.Second
	mockNoteGap  = 25 * time.Second
	mockSilentAt = 40 * time.Second
	mockPing     = 5 * time.Second
	mockConnect  = 900 * time.Millisecond
)

func NewMock() *Mock {
	now := time.Now()
	m := &Mock{
		servers: []Server{
			{ID: "srv-prod-01", Name: "production-01", IP: "185.104.112.21", Env: EnvProduction, Group: "Core", Favorite: true},
			{ID: "srv-prod-02", Name: "production-02", IP: "185.104.112.22", Env: EnvProduction, Group: "Core"},
			{ID: "srv-edge-fra", Name: "edge-fra-01", IP: "91.211.40.7", Env: EnvProduction, Group: "Edge"},
			{ID: "srv-stage-01", Name: "staging-01", IP: "10.0.4.12", Env: EnvStaging, Group: "Core", Favorite: true},
			{ID: "srv-dev-01", Name: "dev-01", IP: "192.168.1.40", Env: EnvDevelopment, Group: "Lab"},
			{ID: "srv-backup", Name: "backup-01", IP: "10.0.9.3", Env: EnvProduction, Group: "Storage"},
		},
		states: map[string]ConnState{
			"srv-prod-01":  StateOnline,
			"srv-prod-02":  StateOnline,
			"srv-edge-fra": StateDegraded,
			"srv-stage-01": StateOnline,
			"srv-dev-01":   StateOnline,
			"srv-backup":   StateOffline,
		},
		silent:  map[string]time.Time{"srv-stage-01": now.Add(mockSilentAt)},
		subs:    map[int]func(Event){},
		conn:    map[string]bool{},
		started: now,
		stop:    make(chan struct{}),
	}
	go m.loop()
	return m
}

// Close останавливает фоновые таймеры.
func (m *Mock) Close() { m.once.Do(func() { close(m.stop) }) }

func (m *Mock) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Mock) Servers(ctx context.Context) ([]Server, error) {
	if err := m.sleep(ctx, mockLatency); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Server, len(m.servers))
	copy(out, m.servers)
	return out, nil
}

func (m *Mock) SaveServer(ctx context.Context, s Server) (Server, error) {
	if err := m.sleep(ctx, 300*time.Millisecond); err != nil {
		return Server{}, err
	}
	s.Secret = ""
	m.mu.Lock()
	if s.ID == "" {
		m.nextID++
		s.ID = fmt.Sprintf("srv-new-%d", m.nextID)
		m.servers = append(m.servers, s)
		m.states[s.ID] = StateOnline
	} else {
		for i := range m.servers {
			if m.servers[i].ID == s.ID {
				m.servers[i] = s
			}
		}
	}
	m.mu.Unlock()
	m.emit(Event{ServersChanged: true})
	return s, nil
}

func (m *Mock) DeleteServer(ctx context.Context, id string) error {
	if err := m.sleep(ctx, 300*time.Millisecond); err != nil {
		return err
	}
	m.mu.Lock()
	for i := range m.servers {
		if m.servers[i].ID == id {
			m.servers = append(m.servers[:i], m.servers[i+1:]...)
			break
		}
	}
	delete(m.conn, id)
	m.mu.Unlock()
	m.emit(Event{ServersChanged: true})
	return nil
}

func (m *Mock) Notifications(ctx context.Context) ([]Notification, error) {
	if err := m.sleep(ctx, mockLatency/2); err != nil {
		return nil, err
	}
	now := time.Now()
	return []Notification{
		{ID: "n-1", ServerID: "srv-edge-fra", Severity: SeverityWarning, Title: "Высокая нагрузка CPU", Body: "edge-fra-01: load average 7.8 за 15 минут", At: now.Add(-6 * time.Minute)},
		{ID: "n-2", ServerID: "srv-backup", Severity: SeverityError, Title: "Сервер недоступен", Body: "backup-01 не отвечает на ping с 03:12", At: now.Add(-48 * time.Minute)},
		{ID: "n-3", ServerID: "srv-prod-01", Severity: SeveritySuccess, Title: "Бэкап завершён", Body: "postgres → s3://vanta-backups, 1.4 GB", At: now.Add(-3 * time.Hour), Read: true},
		{ID: "n-4", Severity: SeverityInfo, Title: "Добро пожаловать в VANTA", Body: "Данные пока моковые: агент ещё не подключён", At: now.Add(-5 * time.Hour), Read: true},
	}, nil
}

func (m *Mock) Connect(ctx context.Context, id string) error {
	if err := m.sleep(ctx, mockConnect); err != nil {
		return err
	}
	m.mu.Lock()
	var ip string
	for _, sv := range m.servers {
		if sv.ID == id {
			ip = sv.IP
		}
	}
	st := m.states[id]
	if st != StateOffline {
		m.conn[id] = true
	}
	m.mu.Unlock()
	if st == StateOffline {
		return fmt.Errorf("ssh: connect to host %s port 22: connection timed out", ip)
	}
	go m.beatAll() // первый heartbeat сразу, не ждать тика
	return nil
}

func (m *Mock) Disconnect(id string) {
	m.mu.Lock()
	delete(m.conn, id)
	m.mu.Unlock()
}

func (m *Mock) Subscribe(fn func(Event)) func() {
	m.mu.Lock()
	m.nextSub++
	id := m.nextSub
	m.subs[id] = fn
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.subs, id)
		m.mu.Unlock()
	}
}

func (m *Mock) emit(e Event) {
	m.mu.Lock()
	subs := make([]func(Event), 0, len(m.subs))
	for _, fn := range m.subs {
		subs = append(subs, fn)
	}
	m.mu.Unlock()
	for _, fn := range subs {
		fn(e)
	}
}

func (m *Mock) isSilentLocked(id string, now time.Time) bool {
	at, ok := m.silent[id]
	return ok && now.After(at)
}

func (m *Mock) loop() {
	beat := time.NewTicker(mockBeat)
	defer beat.Stop()
	note := time.NewTicker(mockNoteGap)
	defer note.Stop()
	ping := time.NewTicker(mockPing)
	defer ping.Stop()
	m.pingAll()
	for {
		select {
		case <-m.stop:
			return
		case <-beat.C:
			m.beatAll()
		case <-note.C:
			m.randomNote()
		case <-ping.C:
			m.pingAll()
		}
	}
}

func (m *Mock) beatAll() {
	now := time.Now()
	m.mu.Lock()
	var beats []Heartbeat
	for _, s := range m.servers {
		if !m.conn[s.ID] || m.isSilentLocked(s.ID, now) {
			continue // не подключены или агент молчит: heartbeat не приходит
		}
		beats = append(beats, Heartbeat{ServerID: s.ID, State: m.states[s.ID], At: now})
	}
	m.mu.Unlock()
	for i := range beats {
		m.emit(Event{Heartbeat: &beats[i]})
	}
}

func (m *Mock) pingAll() {
	now := time.Now()
	m.mu.Lock()
	var rs []Reach
	for _, s := range m.servers {
		r := Reach{ServerID: s.ID, At: now, Online: m.states[s.ID] != StateOffline}
		if r.Online {
			r.RTT = time.Duration(8+rand.IntN(70)) * time.Millisecond
		}
		rs = append(rs, r)
	}
	m.mu.Unlock()
	for i := range rs {
		m.emit(Event{Reach: &rs[i]})
	}
}

func (m *Mock) randomNote() {
	m.mu.Lock()
	s := m.servers[rand.IntN(len(m.servers))]
	m.nextID++
	id := fmt.Sprintf("n-live-%d", m.nextID)
	m.mu.Unlock()

	samples := []Notification{
		{Severity: SeverityInfo, Title: "Деплой завершён", Body: s.Name + ": docker compose up -d, 4 контейнера"},
		{Severity: SeverityWarning, Title: "Диск заполнен на 85%", Body: s.Name + ": /var/lib/docker"},
		{Severity: SeveritySuccess, Title: "Сертификат обновлён", Body: s.Name + ": Let's Encrypt, ещё 90 дней"},
		{Severity: SeverityError, Title: "Сервис упал", Body: s.Name + ": nginx.service failed (exit 1)"},
	}
	n := samples[rand.IntN(len(samples))]
	n.ID, n.ServerID, n.At = id, s.ID, time.Now()
	m.emit(Event{Notification: &n})
}
