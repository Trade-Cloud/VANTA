package host

import (
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
)

// Shell — интерактивная сессия с PTY (вкладка Terminal).
type Shell struct {
	s   *ssh.Session
	In  io.WriteCloser
	Out io.Reader // stdout и stderr вместе (PTY)
}

// OpenShell открывает login shell на сервере с терминалом cols×rows.
func (h *Host) OpenShell(cols, rows int) (*Shell, error) {
	s, err := h.c.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh: %w", err)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	if err := s.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		s.Close()
		return nil, fmt.Errorf("pty: %w", err)
	}
	in, err := s.StdinPipe()
	if err != nil {
		s.Close()
		return nil, err
	}
	out, err := s.StdoutPipe()
	if err != nil {
		s.Close()
		return nil, err
	}
	s.Stderr = nil // с PTY stderr идёт в тот же поток
	if err := s.Shell(); err != nil {
		s.Close()
		return nil, err
	}
	return &Shell{s: s, In: in, Out: out}, nil
}

// Resize — новый размер окна терминала.
func (sh *Shell) Resize(cols, rows int) {
	if cols > 0 && rows > 0 {
		sh.s.WindowChange(rows, cols)
	}
}

// Wait ждёт выхода из оболочки.
func (sh *Shell) Wait() error { return sh.s.Wait() }

// Close закрывает сессию.
func (sh *Shell) Close() { sh.s.Close() }
