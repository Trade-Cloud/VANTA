// Package remote — настоящий agent.Backend: список серверов в локальном
// конфиге (config.go), секреты в связке ключей (secret.go), подключение по
// SSH с проверкой known_hosts (hostkey.go, ssh.go), ping по TCP (ping.go).
//
// При старте ни к чему не подключается: только читает список и пингует.
package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/Trade-Cloud/vanta/internal/agent"
)

// Backend — список серверов и SSH-подключения к ним.
type Backend struct {
	prompt agent.Prompter

	mu      sync.Mutex
	servers []agent.Server
	conns   map[string]*conn
	subs    map[int]func(agent.Event)
	nextSub int
	closed  bool

	pingNow chan struct{}
	stop    chan struct{}
	once    sync.Once
}

// conn — живое подключение к серверу.
type conn struct {
	client *ssh.Client
	done   chan struct{} // закрыт — Disconnect, keepalive выходит молча
}

var _ agent.Backend = (*Backend)(nil)

// New создаёт Backend. p задаёт вопросы человеку при подключении
// (ключ нового хоста, пароль); nil — на вопросы отвечается «нет».
func New(p agent.Prompter) *Backend {
	b := &Backend{
		prompt:  p,
		conns:   map[string]*conn{},
		subs:    map[int]func(agent.Event){},
		pingNow: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	go b.pingLoop()
	return b
}

// Close закрывает все подключения и останавливает ping.
func (b *Backend) Close() {
	b.once.Do(func() {
		close(b.stop)
		b.mu.Lock()
		b.closed = true
		conns := b.conns
		b.conns = map[string]*conn{}
		b.mu.Unlock()
		for _, c := range conns {
			c.close()
		}
	})
}

func (c *conn) close() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	c.client.Close()
}

// ---------- список серверов ----------

// Servers перечитывает servers.json: ручные правки файла подхватываются
// кнопкой «обновить» без перезапуска.
func (b *Backend) Servers(ctx context.Context) ([]agent.Server, error) {
	list, err := loadServers()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.servers = list
	b.mu.Unlock()
	b.kickPing()
	out := make([]agent.Server, len(list))
	copy(out, list)
	return out, nil
}

func (b *Backend) find(id string) (agent.Server, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.servers {
		if s.ID == id {
			return s, true
		}
	}
	return agent.Server{}, false
}

func (b *Backend) SaveServer(ctx context.Context, s agent.Server) (agent.Server, error) {
	secret := s.Secret
	s.Secret = ""
	s.Name = strings.TrimSpace(s.Name)
	s.IP = strings.TrimSpace(s.IP)
	s.User = strings.TrimSpace(s.User)
	if s.Name == "" || s.IP == "" {
		return agent.Server{}, errors.New("нужны название и адрес")
	}
	if s.Port < 0 || s.Port > 65535 {
		return agent.Server{}, errors.New("порт вне диапазона 1–65535")
	}
	if s.AuthKind() == agent.AuthPassword {
		s.KeyPath = ""
	} else if s.KeyPath == "" {
		return agent.Server{}, errors.New("не выбран SSH-ключ")
	}

	// Список берём из файла, а не из памяти: его могли поправить руками.
	list, err := loadServers()
	if err != nil {
		return agent.Server{}, err
	}
	idx := -1
	for i := range list {
		if list[i].ID == s.ID && s.ID != "" {
			idx = i
		}
	}
	if s.ID != "" && idx < 0 {
		return agent.Server{}, errors.New("сервер уже удалён")
	}
	if s.ID == "" {
		s.ID = newID()
	}

	// Секрет. Сменился способ входа или ключ — старый секрет к новому не
	// подходит, удаляем.
	if idx >= 0 {
		old := list[idx]
		if old.AuthKind() != s.AuthKind() || old.KeyPath != s.KeyPath {
			if err := deleteSecret(s.ID); err != nil {
				slog.Warn("не удалось удалить старый секрет", "server", s.ID, "err", err)
			}
		}
	}
	switch {
	case !s.Remember:
		if err := deleteSecret(s.ID); err != nil {
			slog.Warn("не удалось удалить секрет", "server", s.ID, "err", err)
		}
	case secret != "":
		if err := setSecret(s.ID, secret); err != nil {
			return agent.Server{}, err
		}
	}

	if idx >= 0 {
		list[idx] = s
	} else {
		list = append(list, s)
	}
	if err := saveServers(list); err != nil {
		return agent.Server{}, err
	}
	b.mu.Lock()
	b.servers = list
	b.mu.Unlock()
	b.kickPing()
	b.emit(agent.Event{ServersChanged: true})
	return s, nil
}

func (b *Backend) DeleteServer(ctx context.Context, id string) error {
	list, err := loadServers()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, s := range list {
		if s.ID != id {
			out = append(out, s)
		}
	}
	if err := saveServers(out); err != nil {
		return err
	}
	b.Disconnect(id)
	if err := deleteSecret(id); err != nil {
		slog.Warn("не удалось удалить секрет", "server", id, "err", err)
	}
	b.mu.Lock()
	b.servers = out
	b.mu.Unlock()
	b.emit(agent.Event{ServersChanged: true})
	return nil
}

func newID() string {
	var buf [6]byte
	rand.Read(buf[:])
	return "srv-" + hex.EncodeToString(buf[:])
}

// Notifications — истории уведомлений пока нет: их будет присылать агент.
func (b *Backend) Notifications(ctx context.Context) ([]agent.Notification, error) {
	return nil, nil
}

// ---------- подключение ----------

func (b *Backend) Connect(ctx context.Context, id string) error {
	sv, ok := b.find(id)
	if !ok {
		return errors.New("сервер не найден")
	}
	b.Disconnect(id)

	client, err := b.dial(ctx, sv)
	if err != nil {
		slog.Info("ssh: не подключились", "server", sv.Name, "addr", sv.Addr(), "err", err)
		return err
	}
	c := &conn{client: client, done: make(chan struct{})}
	b.mu.Lock()
	// Пока шло рукопожатие, подключение могли отменить или закрыть Backend.
	if b.closed || ctx.Err() != nil {
		b.mu.Unlock()
		client.Close()
		return agent.ErrCancelled
	}
	b.conns[id] = c
	b.mu.Unlock()
	slog.Info("ssh: подключились", "server", sv.Name, "login", sv.Login(),
		"version", string(client.ServerVersion()))

	go b.keepalive(sv, c)
	return nil
}

func (b *Backend) Disconnect(id string) {
	b.mu.Lock()
	c := b.conns[id]
	delete(b.conns, id)
	b.mu.Unlock()
	if c != nil {
		c.close()
		b.kickPing() // снова следим за сервером ping-ом
	}
}

// Client — SSH-клиент подключённого сервера для следующих шагов (команды,
// файлы, systemctl, docker). nil — не подключены.
func (b *Backend) Client(id string) *ssh.Client {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.conns[id]; c != nil {
		return c.client
	}
	return nil
}

// lost — подключение оборвалось само. Если его уже закрыл Disconnect,
// молчим: это не обрыв.
func (b *Backend) lost(id string, c *conn, err error) {
	b.mu.Lock()
	mine := b.conns[id] == c
	if mine {
		delete(b.conns, id)
	}
	b.mu.Unlock()
	c.close()
	if !mine {
		return
	}
	if err == nil {
		err = errors.New("сервер закрыл соединение")
	}
	slog.Warn("ssh: соединение потеряно", "server", id, "err", err)
	b.emit(agent.Event{Lost: &agent.Lost{ServerID: id, Err: fmt.Errorf("соединение потеряно: %w", err)}})
}

// ---------- события ----------

func (b *Backend) Subscribe(fn func(agent.Event)) func() {
	b.mu.Lock()
	b.nextSub++
	id := b.nextSub
	b.subs[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

func (b *Backend) emit(e agent.Event) {
	b.mu.Lock()
	subs := make([]func(agent.Event), 0, len(b.subs))
	for _, fn := range b.subs {
		subs = append(subs, fn)
	}
	b.mu.Unlock()
	for _, fn := range subs {
		fn(e)
	}
}
