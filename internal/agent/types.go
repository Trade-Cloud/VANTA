// Package agent — то, что VANTA знает об агентах на серверах: типы данных и
// интерфейс Backend, через который их получает.
//
// Настоящего агента и control plane пока нет, поэтому есть только Mock.
// Реальный источник (WebSocket/HTTP к агенту) должен реализовать тот же
// Backend, и UI менять не придётся.
package agent

import "time"

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

// Server — описание подключённого VPS.
type Server struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Env      Env    `json:"env"`
	Group    string `json:"group,omitempty"`
	Favorite bool   `json:"favorite,omitempty"`
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

// Event — то, что Backend присылает сам (сейчас Mock по таймеру,
// потом — события из WebSocket агента).
type Event struct {
	Heartbeat      *Heartbeat // только от подключённого сервера
	Reach          *Reach     // ping — от всех серверов
	Notification   *Notification
	ServersChanged bool // список серверов поменялся — перечитать Servers
}
