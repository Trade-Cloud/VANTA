// Package termkit — всё, что вкладка Terminal делает поверх PTY-сессий, без
// UI: маркеры завершения команд, выполнение команд с кодом выхода, очередь,
// переменные, книга команд, снимки и чекпоинты.
package termkit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Trade-Cloud/vanta/internal/host"
)

// Маркер, который оболочка печатает перед каждым приглашением:
// ESC ] 777 ; vanta ; <код выхода> BEL. Терминал его не видит — Session
// вырезает маркер из потока.
const (
	markPrefix = "\x1b]777;vanta;"
	markEnd    = '\a'
)

// hookLine ставит маркер в bash (PROMPT_COMMAND) и zsh (precmd). Пробел в
// начале — чтобы строка не попала в историю (HISTCONTROL=ignorespace).
const hookLine = ` __vanta_mark(){ printf '\033]777;vanta;%s\007' "$?"; }; ` +
	`if [ -n "$ZSH_VERSION" ]; then precmd_functions=(__vanta_mark $precmd_functions); ` +
	`elif [ -n "$BASH_VERSION" ]; then PROMPT_COMMAND="__vanta_mark${PROMPT_COMMAND:+;$PROMPT_COMMAND}"; fi; clear` + "\n"

// ErrNoHook — оболочка не bash/zsh: VANTA не видит, когда закончилась команда.
var ErrNoHook = errors.New("оболочка сессии не bash и не zsh: VANTA не видит конец команды")

// ErrBusy — в сессии сейчас идёт другая команда.
var ErrBusy = errors.New("в сессии сейчас идёт команда: дождитесь её конца или прервите (Ctrl+C)")

// ErrClosed — сессия закрыта.
var ErrClosed = errors.New("сессия закрыта")

// Result — команда завершилась.
type Result struct {
	Exit     int
	Output   string // вывод без эха самой команды (с ANSI — чистит UI)
	Started  time.Time
	Finished time.Time
}

// Session — PTY-сессия на сервере. Out отдаётся виджету терминала, In —
// ввод с клавиатуры (через Write).
type Session struct {
	sh  *host.Shell
	Out io.Reader // поток для терминала, без маркеров

	pw *io.PipeWriter

	mu        sync.Mutex
	hooked    bool
	idle      bool // на приглашении: команда не идёт
	closed    bool
	capturing bool
	capture   bytes.Buffer
	waiter    chan Result
	started   time.Time
	execMu    sync.Mutex // одна Exec за раз
	reminders []func(Result)
	onDone    func(Result) // любая завершённая команда (в т.ч. набранная руками)
	lastStart time.Time
	done      chan struct{}
}

// Open — новая сессия с маркерами.
func Open(h *host.Host, cols, rows int) (*Session, error) {
	sh, err := h.OpenShell(cols, rows)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	s := &Session{sh: sh, Out: pr, pw: pw, done: make(chan struct{}), lastStart: time.Now()}
	go s.pump()
	if _, err := io.WriteString(sh.In, hookLine); err != nil {
		sh.Close()
		return nil, err
	}
	return s, nil
}

// Write — ввод с клавиатуры (io.WriteCloser для виджета терминала).
func (s *Session) Write(b []byte) (int, error) {
	if bytes.ContainsAny(b, "\r\n") {
		s.mu.Lock()
		if !s.capturing {
			s.lastStart = time.Now()
			s.idle = false
		}
		s.mu.Unlock()
	}
	return s.sh.In.Write(b)
}

// Close закрывает сессию (io.WriteCloser).
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	w := s.waiter
	s.waiter = nil
	s.mu.Unlock()
	if w != nil {
		close(w)
	}
	s.sh.Close()
	return nil
}

// Done закрыт, когда оболочка завершилась.
func (s *Session) Done() <-chan struct{} { return s.done }

// Hooked — маркеры работают (bash/zsh).
func (s *Session) Hooked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hooked
}

// Resize — новый размер окна.
func (s *Session) Resize(cols, rows int) { s.sh.Resize(cols, rows) }

// OnDone — колбэк на каждую завершённую команду (из чужой горутины).
func (s *Session) OnDone(fn func(Result)) {
	s.mu.Lock()
	s.onDone = fn
	s.mu.Unlock()
}

// Remind — fn вызовется один раз, когда закончится текущая (или следующая)
// команда. Работает и для набранных руками.
func (s *Session) Remind(fn func(Result)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hooked {
		return ErrNoHook
	}
	s.reminders = append(s.reminders, fn)
	return nil
}

// Busy — идёт Exec.
func (s *Session) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capturing
}

// Idle — сессия на приглашении (команда не идёт). Без маркеров — false.
func (s *Session) Idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idle
}

// Send набирает команду в сессии без ожидания.
func (s *Session) Send(cmd string) error {
	s.mu.Lock()
	s.lastStart = time.Now()
	s.idle = false
	s.mu.Unlock()
	_, err := io.WriteString(s.sh.In, cmd+"\n")
	return err
}

// Exec набирает команду в сессии (её видно в терминале) и ждёт её конца.
func (s *Session) Exec(ctx context.Context, cmd string) (Result, error) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Result{}, ErrClosed
	}
	if !s.hooked {
		s.mu.Unlock()
		return Result{}, ErrNoHook
	}
	if !s.idle {
		s.mu.Unlock()
		return Result{}, ErrBusy
	}
	s.idle = false
	w := make(chan Result, 1)
	s.waiter = w
	s.capturing = true
	s.capture.Reset()
	s.started = time.Now()
	s.mu.Unlock()

	if _, err := io.WriteString(s.sh.In, strings.TrimRight(cmd, "\n")+"\n"); err != nil {
		s.stopCapture()
		return Result{}, err
	}
	select {
	case r, ok := <-w:
		if !ok {
			return Result{}, ErrClosed
		}
		return r, nil
	case <-ctx.Done():
		// Прерываем команду в сессии (Ctrl+C) и перестаём ждать.
		s.sh.In.Write([]byte{3})
		s.stopCapture()
		return Result{}, ctx.Err()
	}
}

// Interrupt — Ctrl+C в сессию.
func (s *Session) Interrupt() { s.sh.In.Write([]byte{3}) }

func (s *Session) stopCapture() {
	s.mu.Lock()
	s.capturing = false
	s.waiter = nil
	s.mu.Unlock()
}

// pump читает вывод оболочки, вырезает маркеры и отдаёт остальное терминалу.
func (s *Session) pump() {
	defer close(s.done)
	defer s.pw.Close()
	buf := make([]byte, 32*1024)
	var pend []byte
	for {
		n, err := s.sh.Out.Read(buf)
		if n > 0 {
			pend = append(pend, buf[:n]...)
			pend = s.scan(pend)
		}
		if err != nil {
			if len(pend) > 0 {
				s.emit(pend)
			}
			s.Close()
			return
		}
	}
}

// scan отдаёт всё до маркера, обрабатывает маркеры; хвост, похожий на
// начало маркера, оставляет до следующего чтения.
func (s *Session) scan(b []byte) []byte {
	for {
		i := bytes.Index(b, []byte(markPrefix))
		if i < 0 {
			keep := partialPrefix(b)
			s.emit(b[:len(b)-keep])
			return append([]byte(nil), b[len(b)-keep:]...)
		}
		end := bytes.IndexByte(b[i+len(markPrefix):], markEnd)
		if end < 0 {
			s.emit(b[:i])
			return append([]byte(nil), b[i:]...)
		}
		s.emit(b[:i])
		code, _ := strconv.Atoi(string(b[i+len(markPrefix) : i+len(markPrefix)+end]))
		s.mark(code)
		b = b[i+len(markPrefix)+end+1:]
	}
}

// partialPrefix — сколько последних байт b совпадают с началом маркера.
func partialPrefix(b []byte) int {
	max := len(markPrefix) - 1
	if max > len(b) {
		max = len(b)
	}
	for k := max; k > 0; k-- {
		if bytes.HasSuffix(b, []byte(markPrefix[:k])) {
			return k
		}
	}
	return 0
}

func (s *Session) emit(b []byte) {
	if len(b) == 0 {
		return
	}
	s.mu.Lock()
	if s.capturing {
		s.capture.Write(b)
	}
	s.mu.Unlock()
	s.pw.Write(b)
}

func (s *Session) mark(code int) {
	s.mu.Lock()
	first := !s.hooked
	s.hooked = true
	s.idle = true
	now := time.Now()
	var w chan Result
	var r Result
	if s.capturing {
		w = s.waiter
		r = Result{Exit: code, Output: dropEcho(s.capture.String()), Started: s.started, Finished: now}
		s.capturing, s.waiter = false, nil
	} else {
		r = Result{Exit: code, Started: s.lastStart, Finished: now}
	}
	var rem []func(Result)
	if !first {
		rem, s.reminders = s.reminders, nil
	}
	done := s.onDone
	s.mu.Unlock()
	if first {
		return // приглашение после установки маркера — это не команда
	}
	if w != nil {
		w <- r
	}
	for _, fn := range rem {
		fn(r)
	}
	if done != nil {
		done(r)
	}
}

// dropEcho убирает первую строку (эхо набранной команды).
func dropEcho(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return ""
}
