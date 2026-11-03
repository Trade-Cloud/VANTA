// Package debugproto описывает протокол между VANTA и debug-приложением (debugger/).
//
// Транспорт: TCP на localhost, одно JSON-сообщение (Message) на строку.
// VANTA — сервер, debugger — клиент. Сервер шлёт hello, log, window, window.closed, stats, tree;
// клиент шлёт только command.
package debugproto

import "time"

// DefaultAddr — адрес по умолчанию. Слушаем только loopback.
const DefaultAddr = "127.0.0.1:47800"

// Version — версия протокола, меняется при несовместимых правках.
const Version = 1

// EnvVar включает debug-мост в основном приложении: "1" — адрес по умолчанию,
// "host:port" — свой адрес, пусто или "0" — выключено.
const EnvVar = "VANTA_DEBUG"

type Type string

const (
	TypeHello   Type = "hello"
	TypeLog     Type = "log"
	TypeWindow  Type = "window"
	TypeClosed  Type = "window.closed" // окно закрыто; в Window заполнен только ID
	TypeStats   Type = "stats"
	TypeTree    Type = "tree"
	TypeCommand Type = "command"
)

// Команды debugger → VANTA.
const (
	CmdOverlayBounds = "overlay.bounds" // Bool: рамки вокруг всех объектов
	CmdOverlayFPS    = "overlay.fps"    // Bool: бейдж FPS в углу окна
	CmdTreeGet       = "tree.get"       // прислать дерево UI
	CmdWindowGet     = "window.get"     // прислать данные окон прямо сейчас
)

// Message — конверт. Заполнено только поле, соответствующее Type.
type Message struct {
	Type    Type        `json:"type"`
	Time    time.Time   `json:"time"`
	Hello   *Hello      `json:"hello,omitempty"`
	Log     *LogEntry   `json:"log,omitempty"`
	Window  *WindowInfo `json:"window,omitempty"`
	Stats   *Stats      `json:"stats,omitempty"`
	Tree    *Tree       `json:"tree,omitempty"`
	Command *Command    `json:"command,omitempty"`
}

type Hello struct {
	App       string    `json:"app"`
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	GOOS      string    `json:"goos"`
	GOARCH    string    `json:"goarch"`
	GoVersion string    `json:"go_version"`
	Started   time.Time `json:"started"`
}

type LogEntry struct {
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
	Source  string            `json:"source,omitempty"`
}

type WindowInfo struct {
	ID         int     `json:"id"`
	Title      string  `json:"title"`
	Width      float32 `json:"width"`
	Height     float32 `json:"height"`
	Scale      float32 `json:"scale"`
	FullScreen bool    `json:"full_screen"`
	Padded     bool    `json:"padded"`
	MinWidth   float32 `json:"min_width"`
	MinHeight  float32 `json:"min_height"`
	Focused    string  `json:"focused,omitempty"`
	Theme      string  `json:"theme"`
	Objects    int     `json:"objects"`
	OverlayOn  bool    `json:"overlay_on"`
	OverlayFPS bool    `json:"overlay_fps"`
}

// Stats — метрики рендер-цикла и рантайма Go за последний интервал.
// FPS считается по тикам цикла отрисовки Fyne (один цикл на все окна).
type Stats struct {
	FPS         float64 `json:"fps"`
	FrameAvgMs  float64 `json:"frame_avg_ms"`
	FrameMaxMs  float64 `json:"frame_max_ms"`
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	HeapSysMB   float64 `json:"heap_sys_mb"`
	NumGC       uint32  `json:"num_gc"`
	UptimeSec   float64 `json:"uptime_sec"`
}

type Tree struct {
	WindowID int   `json:"window_id"`
	Root     *Node `json:"root"`
}

type Node struct {
	Type     string  `json:"type"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	W        float32 `json:"w"`
	H        float32 `json:"h"`
	Visible  bool    `json:"visible"`
	Children []*Node `json:"children,omitempty"`
}

type Command struct {
	Name     string `json:"name"`
	WindowID int    `json:"window_id,omitempty"` // 0 — все окна
	Bool     bool   `json:"bool,omitempty"`
}
