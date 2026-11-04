// Package agent — то, что VANTA знает о серверах: типы данных и интерфейс
// Backend, через который их получает.
//
// Настоящая реализация — internal/remote (список серверов в конфиге,
// подключение по SSH). Mock остался для разработки UI без серверов.
package agent

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Env — окружение сервера.
type Env string

const (
	EnvProduction  Env = "production"
	EnvStaging     Env = "staging"
	EnvDevelopment Env = "development"
)

func (e Env) Label() string {
	switch e {
	case EnvProduction:
		return "Production"
	case EnvStaging:
		return "Staging"
	case EnvDevelopment:
		return "Development"
	}
	return string(e)
}

// Auth — чем входим на сервер по SSH.
type Auth string

const (
	AuthKey      Auth = "key"      // локальный ключ из ~/.ssh
	AuthPassword Auth = "password" // пароль пользователя
)

// DefaultPort и DefaultUser — что подставляется, если поле пустое.
const (
	DefaultPort = 22
	DefaultUser = "root"
)

// Server — описание VPS.
type Server struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IP       string `json:"ip"` // IP-адрес или имя хоста
	Env      Env    `json:"env"`
	Group    string `json:"group,omitempty"`
	Favorite bool   `json:"favorite,omitempty"`

	// SSH.
	Port    int    `json:"port,omitempty"` // 0 — 22
	User    string `json:"user,omitempty"` // "" — root
	Auth    Auth   `json:"auth,omitempty"` // "" — ключ
	KeyPath string `json:"key_path,omitempty"`
	// Remember — пароль (или passphrase ключа) лежит в системной связке
	// ключей. Иначе его спрашивают при каждом подключении.
	Remember bool `json:"remember,omitempty"`

	// Secret — новый пароль/passphrase из формы, только на пути
	// UI → Backend.SaveServer. Не сериализуется и не хранится в памяти
	// Backend: уходит в связку ключей (если Remember) и забывается.
	// Пусто — сохранённый секрет не трогать.
	Secret string `json:"-"`
}

// SSHPort — порт с учётом значения по умолчанию.
func (s Server) SSHPort() int {
	if s.Port <= 0 {
		return DefaultPort
	}
	return s.Port
}

// SSHUser — пользователь с учётом значения по умолчанию.
func (s Server) SSHUser() string {
	if s.User == "" {
		return DefaultUser
	}
	return s.User
}

// AuthKind — способ входа с учётом значения по умолчанию.
func (s Server) AuthKind() Auth {
	if s.Auth == "" {
		return AuthKey
	}
	return s.Auth
}

// Addr — host:port для net.Dial (IPv6 — в скобках).
func (s Server) Addr() string {
	return netJoin(s.IP, s.SSHPort())
}

// Login — как сервер показывать человеку: user@host, порт — если не 22.
func (s Server) Login() string {
	l := s.SSHUser() + "@" + s.IP
	if s.SSHPort() != DefaultPort {
		l = s.SSHUser() + "@" + netJoin(s.IP, s.SSHPort())
	}
	return l
}

func netJoin(host string, port int) string {
	for i := 0; i < len(host); i++ {
		if host[i] == ':' {
			return fmt.Sprintf("[%s]:%d", host, port)
		}
	}
	return host + ":" + strconv.Itoa(port)
}

// ConnState — что агент сам сообщил о себе в последнем heartbeat.
type ConnState int

const (
	StateUnknown  ConnState = iota
	StateOnline             // всё хорошо
	StateDegraded           // агент жив, но есть проблемы (нагрузка, частичный отказ)
	StateOffline            // агент сообщил, что сервер выключен/недоступен
)

// Heartbeat — последний сигнал от агента. Свежесть считает core.Store:
// старый heartbeat — это «агент не отвечает», а не последнее известное состояние.
type Heartbeat struct {
	ServerID string
	State    ConnState
	At       time.Time
}

type Severity int

const (
	SeverityInfo Severity = iota
	SeveritySuccess
	SeverityWarning
	SeverityError
)

// Notification — событие для центра уведомлений.
type Notification struct {
	ID       string
	ServerID string // пусто — событие приложения, не сервера
	Severity Severity
	Title    string
	Body     string
	At       time.Time
	Read     bool
}

// Reach — доступен ли сервер по сети (ping). Это всё, что известно о
// сервере, к которому VANTA не подключена.
type Reach struct {
	ServerID string
	Online   bool
	RTT      time.Duration
	At       time.Time
}

// Lost — подключение к серверу оборвалось само (сеть, сервер ушёл в
// перезагрузку, sshd закрыл сессию).
type Lost struct {
	ServerID string
	Err      error
}

// Event — то, что Backend присылает сам: ping, keepalive подключения,
// обрыв, уведомления.
type Event struct {
	Heartbeat      *Heartbeat // только от подключённого сервера
	Reach          *Reach     // ping — от всех серверов
	Lost           *Lost      // подключение оборвалось
	Notification   *Notification
	ServersChanged bool // список серверов поменялся — перечитать Servers
}

// HostKey — ключ сервера, который SSH видит впервые (нет в known_hosts).
type HostKey struct {
	Host        string // как записан в known_hosts: host или [host]:port
	Type        string // ssh-ed25519, ecdsa-sha2-nistp256, ssh-rsa…
	Fingerprint string // SHA256:…
}

// Prompter — вопросы человеку посреди подключения. Методы блокируются, пока
// человек не ответит или не отменится ctx; зовутся НЕ из UI-горутины.
type Prompter interface {
	// TrustHostKey — доверять ли ключу хоста, которого нет в known_hosts.
	TrustHostKey(ctx context.Context, sv Server, key HostKey) bool
	// Secret — пароль (AuthPassword) или passphrase ключа (AuthKey), когда
	// он не сохранён. ok=false — человек отказался.
	Secret(ctx context.Context, sv Server, retry bool) (secret string, ok bool)
}
