package remote

import (
	"net"
	"sync"
	"time"

	"github.com/Trade-Cloud/vanta/internal/agent"
)

// Ping — до подключения о сервере известно только «отвечает ли порт SSH».
// Это TCP-connect к host:port без рукопожатия: ICMP на macOS и в облаках
// часто режется, а порт SSH — ровно то, что нужно для подключения.
//
// Каждая проверка — короткое соединение, которое sshd пишет в журнал
// («Connection closed … [preauth]»), поэтому интервал не частый, а
// подключённые серверы не пингуются вовсе: их здоровье — keepalive.
const (
	pingEvery   = 30 * time.Second
	pingTimeout = 3 * time.Second
)

func (b *Backend) kickPing() {
	select {
	case b.pingNow <- struct{}{}:
	default:
	}
}

func (b *Backend) pingLoop() {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
		case <-b.pingNow:
		}
		b.pingAll()
	}
}

func (b *Backend) pingAll() {
	b.mu.Lock()
	var targets []agent.Server
	for _, s := range b.servers {
		if b.conns[s.ID] == nil {
			targets = append(targets, s)
		}
	}
	b.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range targets {
		wg.Add(1)
		go func(s agent.Server) {
			defer wg.Done()
			r := agent.Reach{ServerID: s.ID}
			start := time.Now()
			c, err := net.DialTimeout("tcp", s.Addr(), pingTimeout)
			r.At = time.Now()
			if err == nil {
				r.Online, r.RTT = true, r.At.Sub(start)
				c.Close()
			}
			b.emit(agent.Event{Reach: &r})
		}(s)
	}
	wg.Wait()
}
