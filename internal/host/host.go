// Package host — работа с подключённым сервером поверх SSH: выполнение
// команд, sudo, SFTP и разбор вывода системных утилит.
//
// Пакет ничего не знает об UI. Каждая вкладка панели (Resources, Services,
// Docker…) — набор функций здесь (данные и действия) и отдельная страница в
// internal/screens. Все методы блокируются (сеть), их зовут не из UI-горутины.
package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// maxSessions — сколько команд держим одновременно. У sshd по умолчанию
// MaxSessions 10; оставляем место терминалу и потокам (journal -f, логи).
const maxSessions = 4

// ErrNoSudo — команда требует root, а sudo недоступен или пароль не введён.
var ErrNoSudo = errors.New("нужны права root: войдите как root или настройте sudo")

// SudoAsker спрашивает у человека пароль sudo. ok=false — отказался.
// Зовётся не из UI-горутины и блокируется до ответа.
type SudoAsker func(ctx context.Context, user string, retry bool) (password string, ok bool)

// Host — подключённый сервер.
type Host struct {
	c    *ssh.Client
	user string
	ask  SudoAsker
	sem  chan struct{}

	mu       sync.Mutex
	probed   bool
	root     bool
	nopass   bool   // sudo -n работает
	sudoPass string // введённый пароль sudo (только в памяти, на время подключения)
	sftp     *sftp.Client
	pkg      string // менеджер пакетов, "" — ещё не определяли
	closed   bool
}

// New оборачивает подключение. user — под кем вошли (для окна пароля sudo).
func New(c *ssh.Client, user string, ask SudoAsker) *Host {
	return &Host{c: c, user: user, ask: ask, sem: make(chan struct{}, maxSessions)}
}

// Close закрывает SFTP. Само SSH-подключение принадлежит Backend.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.sftp != nil {
		h.sftp.Close()
		h.sftp = nil
	}
	h.sudoPass = ""
}

// User — под кем вошли.
func (h *Host) User() string { return h.user }

// Client — само SSH-подключение (терминал, потоки).
func (h *Host) Client() *ssh.Client { return h.c }

// ExitError — команда отработала, но вернула ненулевой код.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("команда завершилась с кодом %d", e.Code)
	}
	if len(msg) > 600 {
		msg = msg[:600] + "…"
	}
	return msg
}

// Q — строка в одинарных кавычках для sh.
func Q(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// script — команда для sh -c: локаль C (стабильный вывод для разбора).
func script(cmd string) string {
	return "export LC_ALL=C LANG=C; " + cmd
}

// Run выполняет команду от пользователя подключения и возвращает stdout.
// Ненулевой код — *ExitError со stderr.
func (h *Host) Run(ctx context.Context, cmd string) (string, error) {
	out, err := h.exec(ctx, "exec sh -c "+Q(script(cmd)), nil)
	return string(out), err
}

// RunInput — Run с данными на stdin.
func (h *Host) RunInput(ctx context.Context, cmd string, stdin []byte) (string, error) {
	out, err := h.exec(ctx, "exec sh -c "+Q(script(cmd)), stdin)
	return string(out), err
}

// Sudo выполняет команду от root: напрямую (если вошли root), через sudo -n
// или через sudo -S с паролем, который спросит SudoAsker.
func (h *Host) Sudo(ctx context.Context, cmd string) (string, error) {
	return h.SudoInput(ctx, cmd, nil)
}

// SudoInput — Sudo с данными на stdin.
func (h *Host) SudoInput(ctx context.Context, cmd string, stdin []byte) (string, error) {
	wrap, pre, err := h.sudoWrap(ctx)
	if err != nil {
		return "", err
	}
	in := stdin
	if pre != "" {
		in = append([]byte(pre+"\n"), stdin...)
	}
	out, err := h.exec(ctx, wrap+Q(script(cmd)), in)
	var ee *ExitError
	if pre != "" && errors.As(err, &ee) && isSudoAuthFail(ee.Stderr) {
		h.mu.Lock()
		h.sudoPass = ""
		h.mu.Unlock()
		return "", errors.New("sudo: неверный пароль")
	}
	return string(out), err
}

// IsRoot — вошли под root (или sudo без пароля): привилегированные действия
// пройдут без вопросов.
func (h *Host) IsRoot(ctx context.Context) bool {
	h.probe(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.root || h.nopass
}

func isSudoAuthFail(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "incorrect password") || strings.Contains(s, "sorry, try again") ||
		strings.Contains(s, "no password was provided") || strings.Contains(s, "password attempts")
}

func (h *Host) probe(ctx context.Context) {
	h.mu.Lock()
	done := h.probed
	h.mu.Unlock()
	if done {
		return
	}
	out, err := h.exec(ctx, "exec sh -c "+Q("id -u; sudo -n true >/dev/null 2>&1 && echo nopass"), nil)
	if err != nil && ctx.Err() != nil {
		return
	}
	lines := strings.Fields(string(out))
	h.mu.Lock()
	h.probed = true
	h.root = len(lines) > 0 && lines[0] == "0"
	h.nopass = strings.Contains(string(out), "nopass")
	h.mu.Unlock()
}

// sudoWrap — префикс для sh -c и строка пароля для stdin ("" — не нужен).
func (h *Host) sudoWrap(ctx context.Context) (wrap, pass string, err error) {
	h.probe(ctx)
	h.mu.Lock()
	root, nopass, pw := h.root, h.nopass, h.sudoPass
	h.mu.Unlock()
	switch {
	case root:
		return "exec sh -c ", "", nil
	case nopass:
		return "exec sudo -n -- sh -c ", "", nil
	case pw != "":
		return "exec sudo -S -p '' -- sh -c ", pw, nil
	}
	if h.ask == nil {
		return "", "", ErrNoSudo
	}
	for retry := false; ; retry = true {
		pw, ok := h.ask(ctx, h.user, retry)
		if !ok {
			return "", "", ErrNoSudo
		}
		// Проверяем пароль, прежде чем запускать саму команду.
		_, err := h.exec(ctx, "exec sudo -S -p '' -k -v", []byte(pw+"\n"))
		if err == nil {
			h.mu.Lock()
			h.sudoPass = pw
			h.mu.Unlock()
			return "exec sudo -S -p '' -- sh -c ", pw, nil
		}
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		var ee *ExitError
		if !errors.As(err, &ee) {
			return "", "", err
		}
		if !isSudoAuthFail(ee.Stderr) {
			return "", "", fmt.Errorf("sudo: %w", err)
		}
	}
}

// exec — одна SSH-сессия на команду. Отмена ctx закрывает сессию.
func (h *Host) exec(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
	select {
	case h.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.sem }()

	s, err := h.c.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh: %w", err)
	}
	defer s.Close()
	var out, errb bytes.Buffer
	s.Stdout = &out
	s.Stderr = &errb
	if stdin != nil {
		s.Stdin = bytes.NewReader(stdin)
	}
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	err = s.Run(cmd)
	if ctx.Err() != nil {
		return out.Bytes(), ctx.Err()
	}
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			return out.Bytes(), &ExitError{Code: ee.ExitStatus(), Stderr: errb.String()}
		}
		var em *ssh.ExitMissingError
		if errors.As(err, &em) {
			return out.Bytes(), &ExitError{Code: -1, Stderr: errb.String()}
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

// Stream запускает долгую команду и отдаёт вывод (stdout+stderr) кусками в
// fn, пока команда не завершится или не отменится ctx. root — через sudo.
func (h *Host) Stream(ctx context.Context, cmd string, root bool, fn func(chunk string)) error {
	wrap, pass := "exec sh -c ", ""
	if root {
		var err error
		wrap, pass, err = h.sudoWrap(ctx)
		if err != nil {
			return err
		}
	}
	s, err := h.c.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: %w", err)
	}
	defer s.Close()
	pr, pw := io.Pipe()
	s.Stdout = pw
	s.Stderr = pw
	if pass != "" {
		s.Stdin = strings.NewReader(pass + "\n")
	}
	stop := context.AfterFunc(ctx, func() { s.Close(); pw.Close() })
	defer stop()
	if err := s.Start(wrap + Q(script(cmd))); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				fn(string(buf[:n]))
			}
			if err != nil {
				return
			}
		}
	}()
	err = s.Wait()
	pw.Close()
	<-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitStatus()}
	}
	return err
}

// SFTP — клиент SFTP поверх того же подключения (создаётся один раз).
func (h *Host) SFTP() (*sftp.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("сервер отключён")
	}
	if h.sftp != nil {
		return h.sftp, nil
	}
	c, err := sftp.NewClient(h.c)
	if err != nil {
		return nil, fmt.Errorf("sftp: %w", err)
	}
	h.sftp = c
	return c, nil
}

// Has — есть ли команда на сервере.
func (h *Host) Has(ctx context.Context, name string) bool {
	_, err := h.Run(ctx, "command -v "+Q(name)+" >/dev/null 2>&1")
	return err == nil
}

// lines — непустые строки вывода.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
