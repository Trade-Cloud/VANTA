package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Trade-Cloud/vanta/internal/agent"
	"github.com/Trade-Cloud/vanta/internal/sshkeys"
)

// Ключи хостов проверяются по обычному ~/.ssh/known_hosts — тому же, что у
// ssh в терминале. Сервер, которого там нет, VANTA добавляет только после
// подтверждения отпечатка человеком. Сменившийся ключ — всегда отказ: его
// нужно убрать из known_hosts руками, VANTA сама этого не делает.

// HostKeyChangedError — ключ сервера не совпал с known_hosts.
type HostKeyChangedError struct {
	Host string
	Got  string // отпечаток, который прислал сервер
	File string
	Line int
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("ключ сервера %s изменился (сейчас %s). Возможна подмена сервера. "+
		"Если ключ сменили намеренно, удалите строку %d из %s и подключитесь снова",
		e.Host, e.Got, e.Line, e.File)
}

// khMu — одна запись в known_hosts за раз.
var khMu sync.Mutex

func knownHostsPath() (string, error) {
	dir, err := sshkeys.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "known_hosts"), nil
}

// openKnownHosts — колбэк проверки по known_hosts. Нет файла — создаём пустой.
func openKnownHosts() (ssh.HostKeyCallback, string, error) {
	p, err := knownHostsPath()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, "", fmt.Errorf("known_hosts: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("known_hosts: %w", err)
	}
	f.Close()
	kh, err := knownhosts.New(p)
	if err != nil {
		return nil, "", fmt.Errorf("known_hosts: %w", err)
	}
	return kh, p, nil
}

// probeKey — заведомо чужой ключ: проверка им показывает, какие ключи для
// хоста уже записаны в known_hosts (KeyError.Want).
type probeKey struct{}

func (probeKey) Type() string                        { return "vanta-probe" }
func (probeKey) Marshal() []byte                     { return []byte("vanta-probe") }
func (probeKey) Verify([]byte, *ssh.Signature) error { return errors.New("probe") }

var probeRemote = &net.TCPAddr{IP: net.IPv4zero}

// knownAlgos — алгоритмы ключей хоста, которые уже есть в known_hosts.
// Если их не задать, сервер может предложить ключ другого типа, и
// проверка ложно скажет «ключ изменился».
func knownAlgos(kh ssh.HostKeyCallback, addr string) []string {
	var ke *knownhosts.KeyError
	if !errors.As(kh(addr, probeRemote, probeKey{}), &ke) {
		return nil
	}
	var algos []string
	seen := map[string]bool{}
	add := func(a string) {
		if !seen[a] {
			seen[a] = true
			algos = append(algos, a)
		}
	}
	for _, w := range ke.Want {
		switch t := w.Key.Type(); t {
		case ssh.KeyAlgoRSA:
			add(ssh.KeyAlgoRSASHA512)
			add(ssh.KeyAlgoRSASHA256)
			add(ssh.KeyAlgoRSA)
		default:
			add(t)
		}
	}
	return algos
}

// hostKeyCheck — проверка ключа хоста с вопросом человеку для нового
// сервера. pause/resume снимают и возвращают таймаут соединения на время
// вопроса: человек может думать дольше таймаута рукопожатия.
func hostKeyCheck(ctx context.Context, kh ssh.HostKeyCallback, khPath string, sv agent.Server,
	p agent.Prompter, pause, resume func(), fail *error) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := kh(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		var re *knownhosts.RevokedError
		switch {
		case errors.As(err, &re):
			err = fmt.Errorf("ключ сервера отозван в %s:%d", re.Revoked.Filename, re.Revoked.Line)
		case errors.As(err, &ke) && len(ke.Want) > 0:
			err = &HostKeyChangedError{
				Host: knownhosts.Normalize(hostname), Got: ssh.FingerprintSHA256(key),
				File: ke.Want[0].Filename, Line: ke.Want[0].Line,
			}
		case errors.As(err, &ke):
			hk := agent.HostKey{
				Host:        knownhosts.Normalize(hostname),
				Type:        key.Type(),
				Fingerprint: ssh.FingerprintSHA256(key),
			}
			pause()
			ok := p != nil && p.TrustHostKey(ctx, sv, hk)
			resume()
			if ok && ctx.Err() == nil {
				err = appendKnownHost(khPath, hostname, key)
			} else {
				err = agent.ErrCancelled // не доверился — как отмена
			}
		}
		if err != nil {
			*fail = err
		}
		return err
	}
}

func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	khMu.Lock()
	defer khMu.Unlock()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	// Файл мог кончаться без перевода строки — не склеиваем записи.
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		line = "\n" + line
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("known_hosts: %w", err)
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return fmt.Errorf("known_hosts: %w", err)
	}
	return f.Close()
}
