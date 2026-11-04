package remote

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"

	"github.com/Trade-Cloud/vanta/internal/agent"
)

const (
	dialTimeout      = 10 * time.Second
	handshakeTimeout = 20 * time.Second // без учёта времени на вопросы человеку
	beatEvery        = 5 * time.Second  // keepalive подключённого сервера
	beatTimeout      = 10 * time.Second // keepalive без ответа — обрыв
	slowBeat         = 1500 * time.Millisecond
	secretTries      = 3
)

// dial — TCP, рукопожатие SSH с проверкой known_hosts и вход.
func (b *Backend) dial(ctx context.Context, sv agent.Server) (*ssh.Client, error) {
	kh, khPath, err := openKnownHosts()
	if err != nil {
		return nil, err
	}
	addr := sv.Addr()

	d := net.Dialer{Timeout: dialTimeout}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, agent.ErrCancelled
		}
		return nil, netError(sv, err)
	}
	// Отмена ctx (переключили сервер, закрыли окно вопроса) рвёт TCP —
	// рукопожатие сразу выходит с ошибкой.
	stopWatch := context.AfterFunc(ctx, func() { nc.Close() })

	resume := func() { nc.SetDeadline(time.Now().Add(handshakeTimeout)) }
	pause := func() { nc.SetDeadline(time.Time{}) }
	resume()

	// Ошибка, которую мы сами вернули из колбэков (ключ хоста, отмена
	// вопроса), — точнее, чем то, во что её завернёт x/crypto/ssh.
	var fail error
	sec := &secretSource{b: b, ctx: ctx, sv: sv, pause: pause, resume: resume, fail: &fail}
	auth, cleanup, err := authMethods(sv, sec)
	if err != nil {
		stopWatch()
		nc.Close()
		return nil, err
	}
	defer cleanup()

	cfg := &ssh.ClientConfig{
		User:              sv.SSHUser(),
		Auth:              auth,
		HostKeyCallback:   hostKeyCheck(ctx, kh, khPath, sv, b.prompt, pause, resume, &fail),
		HostKeyAlgorithms: knownAlgos(kh, addr),
		ClientVersion:     "SSH-2.0-VANTA",
	}
	cc, chans, reqs, err := ssh.NewClientConn(nc, addr, cfg)
	if err != nil {
		stopWatch()
		nc.Close()
		switch {
		case ctx.Err() != nil:
			return nil, agent.ErrCancelled
		case fail != nil:
			return nil, fail
		}
		return nil, sshError(sv, err)
	}
	if !stopWatch() {
		// ctx отменили ровно в момент успеха — соединение уже закрыто.
		cc.Close()
		return nil, agent.ErrCancelled
	}
	nc.SetDeadline(time.Time{})
	sec.commit()
	return ssh.NewClient(cc, chans, reqs), nil
}

// authMethods — способы входа для сервера. cleanup закрывает то, что
// нужно только на время рукопожатия (соединение с ssh-agent).
func authMethods(sv agent.Server, sec *secretSource) ([]ssh.AuthMethod, func(), error) {
	nop := func() {}
	if sv.AuthKind() == agent.AuthPassword {
		pw := ssh.RetryableAuthMethod(ssh.PasswordCallback(sec.get), secretTries)
		// Многие sshd принимают пароль только через keyboard-interactive (PAM).
		ki := ssh.RetryableAuthMethod(ssh.KeyboardInteractive(func(_, _ string, qs []string, echos []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range qs {
				if echos[i] {
					continue // не пароль — без ответа
				}
				v, err := sec.get()
				if err != nil {
					return nil, err
				}
				ans[i] = v
			}
			return ans, nil
		}), secretTries)
		return []ssh.AuthMethod{pw, ki}, nop, nil
	}

	if sv.KeyPath == "" {
		return nil, nop, errors.New("не выбран SSH-ключ")
	}
	data, err := os.ReadFile(sv.KeyPath)
	if err != nil {
		return nil, nop, fmt.Errorf("ключ %s: %w", sv.KeyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nop, nil
	}
	var pme *ssh.PassphraseMissingError
	if !errors.As(err, &pme) {
		return nil, nop, fmt.Errorf("ключ %s: %w", sv.KeyPath, err)
	}

	// Ключ под паролем. Если он уже загружен в ssh-agent (на macOS — обычно
	// через Keychain), подписываем им и ничего не спрашиваем.
	if s, closeAgent := agentSigner(pme.PublicKey); s != nil {
		return []ssh.AuthMethod{ssh.PublicKeys(s)}, closeAgent, nil
	}
	return []ssh.AuthMethod{ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		for {
			pass, err := sec.get()
			if err != nil {
				return nil, err
			}
			s, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(pass))
			if errors.Is(err, x509.IncorrectPasswordError) && sec.tries < secretTries {
				continue // спросим ещё раз, с пометкой «неверный»
			}
			if err != nil {
				return nil, fmt.Errorf("ключ %s: %w", sv.KeyPath, err)
			}
			return []ssh.Signer{s}, nil
		}
	})}, nop, nil
}

// agentSigner — подпись через ssh-agent, если в нём есть ключ pub.
func agentSigner(pub ssh.PublicKey) (ssh.Signer, func()) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if pub == nil || sock == "" {
		return nil, nil
	}
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return nil, nil
	}
	signers, err := sshagent.NewClient(c).Signers()
	if err == nil {
		want := pub.Marshal()
		for _, s := range signers {
			if bytes.Equal(s.PublicKey().Marshal(), want) {
				return s, func() { c.Close() }
			}
		}
	}
	c.Close()
	return nil, nil
}

// secretSource — пароль/passphrase на время одного подключения: сначала из
// связки ключей, потом (или если тот не подошёл) — вопросом человеку.
type secretSource struct {
	b             *Backend
	ctx           context.Context
	sv            agent.Server
	pause, resume func()
	fail          *error

	tries    int
	val      string
	prompted bool // val ввёл человек — после удачного входа сохранить
}

func (s *secretSource) get() (string, error) {
	retry := s.tries > 0
	s.tries++
	if !retry && s.sv.Remember {
		v, ok, err := getSecret(s.sv.ID)
		if err != nil {
			slog.Warn("связка ключей недоступна", "server", s.sv.ID, "err", err)
		}
		if ok {
			s.val = v
			return v, nil
		}
	}
	if s.b.prompt == nil {
		return "", agent.ErrCancelled
	}
	s.pause()
	v, ok := s.b.prompt.Secret(s.ctx, s.sv, retry)
	s.resume()
	if !ok || s.ctx.Err() != nil {
		*s.fail = agent.ErrCancelled
		return "", agent.ErrCancelled
	}
	s.val, s.prompted = v, true
	return v, nil
}

// commit — вход удался: введённый секрет сохраняем, если так настроен сервер.
func (s *secretSource) commit() {
	if s.prompted && s.sv.Remember {
		if err := setSecret(s.sv.ID, s.val); err != nil {
			slog.Warn("не удалось сохранить секрет", "server", s.sv.ID, "err", err)
		}
	}
	s.val = ""
}

// keepalive — пока подключены, раз в beatEvery шлём keepalive@openssh.com.
// Ответ — heartbeat (и ping: подключённый сервер по TCP не пингуем), нет
// ответа или соединение закрылось — обрыв.
func (b *Backend) keepalive(sv agent.Server, c *conn) {
	waitErr := make(chan error, 1)
	go func() { waitErr <- c.client.Wait() }()

	beat := func() bool {
		start := time.Now()
		res := make(chan error, 1)
		go func() {
			_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		select {
		case err := <-res:
			if err != nil {
				b.lost(sv.ID, c, err)
				return false
			}
		case <-time.After(beatTimeout):
			b.lost(sv.ID, c, errors.New("сервер не отвечает на keepalive"))
			return false
		case <-c.done:
			return false
		}
		now := time.Now()
		rtt := now.Sub(start)
		st := agent.StateOnline
		if rtt > slowBeat {
			st = agent.StateDegraded
		}
		b.emit(agent.Event{Heartbeat: &agent.Heartbeat{ServerID: sv.ID, State: st, At: now}})
		b.emit(agent.Event{Reach: &agent.Reach{ServerID: sv.ID, Online: true, RTT: rtt, At: now}})
		return true
	}

	if !beat() {
		return
	}
	t := time.NewTicker(beatEvery)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case err := <-waitErr:
			b.lost(sv.ID, c, err)
			return
		case <-t.C:
			if !beat() {
				return
			}
		}
	}
}

// ---------- человеческие ошибки ----------

func netError(sv agent.Server, err error) error {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		return fmt.Errorf("хост %s не найден", sv.IP)
	case strings.Contains(err.Error(), "connection refused"):
		return fmt.Errorf("%s: порт %d закрыт (connection refused)", sv.IP, sv.SSHPort())
	case errors.As(err, &ne) && ne.Timeout():
		return fmt.Errorf("%s: нет ответа за %s", sv.Addr(), dialTimeout)
	}
	return fmt.Errorf("%s: %w", sv.Addr(), err)
}

func sshError(sv agent.Server, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"):
		how := "по ключу " + sv.KeyPath
		if sv.AuthKind() == agent.AuthPassword {
			how = "по паролю"
		}
		return fmt.Errorf("сервер не пустил %s %s", sv.SSHUser(), how)
	case errors.Is(err, os.ErrDeadlineExceeded) || strings.Contains(msg, "i/o timeout"):
		return fmt.Errorf("%s: SSH не ответил за %s", sv.Addr(), handshakeTimeout)
	case strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset"):
		return fmt.Errorf("%s: сервер закрыл соединение во время рукопожатия", sv.Addr())
	}
	return fmt.Errorf("%s: %w", sv.Addr(), err)
}
