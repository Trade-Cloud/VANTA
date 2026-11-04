package agent

import (
	"context"
	"errors"
)

// ErrNoResponse — агент сервера не ответил.
var ErrNoResponse = errors.New("агент не отвечает")

// ErrCancelled — человек сам отменил подключение (закрыл окно с вопросом).
// Это не ошибка сервера: UI просто возвращается к «сервер не выбран».
var ErrCancelled = errors.New("подключение отменено")

// Backend — источник данных для header и остального UI.
//
// Методы могут блокироваться (сеть), поэтому их зовут не из UI-горутины.
// Колбэк Subscribe тоже вызывается из чужой горутины.
type Backend interface {
	// Servers — список серверов.
	Servers(ctx context.Context) ([]Server, error)
	// SaveServer добавляет (ID == "") или обновляет сервер.
	SaveServer(ctx context.Context, s Server) (Server, error)
	// DeleteServer удаляет сервер вместе с его сохранённым секретом.
	DeleteServer(ctx context.Context, id string) error
	// Notifications — история уведомлений на момент подключения.
	Notifications(ctx context.Context) ([]Notification, error)
	// Connect подключается к серверу (SSH). Пока не подключились, о сервере
	// известен только ping, heartbeat не приходит. Может ждать ответа
	// человека (ключ хоста, пароль), поэтому отменяется через ctx.
	Connect(ctx context.Context, serverID string) error
	// Disconnect закрывает подключение.
	Disconnect(serverID string)
	// Subscribe — поток событий (heartbeat, уведомления). Возвращает отписку.
	Subscribe(fn func(Event)) (cancel func())
}
