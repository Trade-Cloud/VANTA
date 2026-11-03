// Package debugbridge — минимальный хук в основном приложении для debugger/.
//
// По умолчанию выключен: FromEnv возвращает nil, а все методы *Bridge
// безопасны на nil, так что в обычном запуске код моста ничего не делает.
// Включается переменной окружения VANTA_DEBUG (см. debugproto.EnvVar).
package debugbridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"

	"github.com/Trade-Cloud/vanta/internal/debugproto"
)

const (
	logBacklog    = 500 // сколько последних логов отдать при подключении
	clientBuffer  = 2048
	statsInterval = time.Second
)

type Bridge struct {
	ln      net.Listener
	started time.Time
	fps     fpsCounter

	mu      sync.Mutex
	clients map[*client]struct{}
	logs    []debugproto.Message

	// windows и fpsAnim трогаются только из UI-горутины (fyne.Do / до ShowAndRun).
	windows []*watched
	fpsAnim *fyne.Animation
	nextID  int

	ready         atomic.Bool // true после первого Attach: есть fyne.App, можно звать fyne.Do
	overlayOn     atomic.Bool // хотя бы в одном окне включён оверлей
	overlayLoopOn atomic.Bool // крутится горутина перерисовки оверлея
}

type client struct {
	conn net.Conn
	out  chan debugproto.Message
}

// FromEnv поднимает мост, если задан VANTA_DEBUG. Иначе возвращает nil.
func FromEnv() *Bridge {
	v := strings.TrimSpace(os.Getenv(debugproto.EnvVar))
	if v == "" || v == "0" {
		return nil
	}
	addr := debugproto.DefaultAddr
	if strings.Contains(v, ":") {
		addr = v
	}
	b, err := Start(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "debugbridge: не удалось запустить на %s: %v\n", addr, err)
		return nil
	}
	return b
}

// Start слушает addr и перехватывает slog/log, дублируя логи в debugger.
func Start(addr string) (*Bridge, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	b := &Bridge{
		ln:      ln,
		started: time.Now(),
		clients: map[*client]struct{}{},
	}
	// После SetDefault и log.Printf (в т.ч. fyne.LogError) идут через этот handler.
	stderr := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(&logHandler{b: b, next: stderr}))

	go b.acceptLoop()
	go b.statsLoop()
	slog.Info("debug-мост запущен", "addr", ln.Addr().String())
	return b, nil
}

// Close останавливает мост и отключает клиентов.
func (b *Bridge) Close() {
	if b == nil {
		return
	}
	_ = b.ln.Close()
	b.mu.Lock()
	for c := range b.clients {
		b.dropLocked(c)
	}
	b.mu.Unlock()
}

func (b *Bridge) hello() debugproto.Message {
	return debugproto.Message{
		Type: debugproto.TypeHello,
		Time: time.Now(),
		Hello: &debugproto.Hello{
			App:       "vanta",
			Version:   debugproto.Version,
			PID:       os.Getpid(),
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			GoVersion: runtime.Version(),
			Started:   b.started,
		},
	}
}

func (b *Bridge) acceptLoop() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}
		c := &client{conn: conn, out: make(chan debugproto.Message, clientBuffer)}
		b.mu.Lock()
		b.clients[c] = struct{}{}
		b.sendLocked(c, b.hello())
		for _, m := range b.logs {
			b.sendLocked(c, m)
		}
		b.mu.Unlock()

		go b.writeLoop(c)
		go b.readLoop(c)
		b.pushWindows()
	}
}

func (b *Bridge) writeLoop(c *client) {
	enc := json.NewEncoder(c.conn)
	for m := range c.out {
		_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := enc.Encode(m); err != nil {
			b.drop(c)
			return
		}
	}
}

func (b *Bridge) readLoop(c *client) {
	sc := bufio.NewScanner(c.conn)
	for sc.Scan() {
		var m debugproto.Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m.Command == nil {
			continue
		}
		b.handleCommand(c, *m.Command)
	}
	b.drop(c)
}

func (b *Bridge) drop(c *client) {
	b.mu.Lock()
	b.dropLocked(c)
	b.mu.Unlock()
}

func (b *Bridge) dropLocked(c *client) {
	if _, ok := b.clients[c]; !ok {
		return
	}
	delete(b.clients, c)
	close(c.out)
	_ = c.conn.Close()
}

// sendLocked не блокирует: медленный клиент теряет сообщения, а не тормозит UI.
func (b *Bridge) sendLocked(c *client, m debugproto.Message) {
	select {
	case c.out <- m:
	default:
	}
}

func (b *Bridge) broadcast(m debugproto.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m.Type == debugproto.TypeLog {
		b.logs = append(b.logs, m)
		if len(b.logs) > logBacklog {
			b.logs = b.logs[len(b.logs)-logBacklog:]
		}
	}
	for c := range b.clients {
		b.sendLocked(c, m)
	}
}

func (b *Bridge) sendTo(c *client, m debugproto.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.clients[c]; ok {
		b.sendLocked(c, m)
	}
}

func (b *Bridge) hasClients() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients) > 0
}

func (b *Bridge) handleCommand(c *client, cmd debugproto.Command) {
	switch cmd.Name {
	case debugproto.CmdOverlayBounds, debugproto.CmdOverlayFPS:
		b.do(func() {
			for _, w := range b.windows {
				if cmd.WindowID != 0 && cmd.WindowID != w.id {
					continue
				}
				if cmd.Name == debugproto.CmdOverlayBounds {
					w.bounds = cmd.Bool
				} else {
					w.showFPS = cmd.Bool
				}
				w.redraw(b.fps.current())
			}
			b.ensureOverlayTicker()
		})
		b.pushWindows()
	case debugproto.CmdTreeGet:
		b.do(func() {
			for _, w := range b.windows {
				if cmd.WindowID != 0 && cmd.WindowID != w.id {
					continue
				}
				b.sendTo(c, debugproto.Message{
					Type: debugproto.TypeTree,
					Time: time.Now(),
					Tree: &debugproto.Tree{WindowID: w.id, Root: buildNode(w.root)},
				})
			}
		})
	case debugproto.CmdWindowGet:
		b.pushWindows()
	}
}

// do выполняет fn в UI-горутине Fyne. До первого Attach приложения ещё нет — пропускаем.
func (b *Bridge) do(fn func()) {
	if !b.ready.Load() {
		return
	}
	fyne.Do(fn)
}

// pushWindows собирает данные окон в UI-горутине и рассылает их.
func (b *Bridge) pushWindows() {
	b.do(func() {
		for _, w := range b.windows {
			info := w.info()
			b.broadcast(debugproto.Message{Type: debugproto.TypeWindow, Time: time.Now(), Window: &info})
		}
	})
}

func (b *Bridge) statsLoop() {
	t := time.NewTicker(statsInterval)
	defer t.Stop()
	var ms runtime.MemStats
	for range t.C {
		snap := b.fps.snapshot()
		if !b.hasClients() {
			continue
		}
		runtime.ReadMemStats(&ms)
		b.broadcast(debugproto.Message{
			Type: debugproto.TypeStats,
			Time: time.Now(),
			Stats: &debugproto.Stats{
				FPS:         snap.fps,
				FrameAvgMs:  snap.avgMs,
				FrameMaxMs:  snap.maxMs,
				Goroutines:  runtime.NumGoroutine(),
				HeapAllocMB: float64(ms.HeapAlloc) / (1 << 20),
				HeapSysMB:   float64(ms.HeapSys) / (1 << 20),
				NumGC:       ms.NumGC,
				UptimeSec:   time.Since(b.started).Seconds(),
			},
		})
		b.pushWindows()
	}
}
