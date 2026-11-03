package agent

import (
	"context"
	"errors"
)

// ErrNoResponse — агент сервера не ответил.
var ErrNoResponse = errors.New("агент не отвечает")

// Backend — источник данных для header и остального UI.
//
// Методы могут блокироваться (сеть), поэтому их зовут не из UI-горутины.
// Колбэк Subscribe тоже вызывается из чужой горутины.
type Backend interface {
	// Servers — список подключённых серверов.
	Servers(ctx context.Context) ([]Server, error)
	// SaveServer добавляет (ID == "") или обновляет сервер.
	SaveServer(ctx context.Context, s Server) (Server, error)
	// Notifications — история уведомлений на момент подключения.
	Notifications(ctx context.Context) ([]Notification, error)
	// Connect подключается к серверу (SSH). Пока не подключились, о сервере
	// известен только ping, heartbeat агента не приходит.
	Connect(ctx context.Context, serverID string) error
	// Disconnect закрывает подключение.
	Disconnect(serverID string)
	// Subscribe — поток событий (heartbeat, уведомления). Возвращает отписку.
	Subscribe(fn func(Event)) (cancel func())
}
