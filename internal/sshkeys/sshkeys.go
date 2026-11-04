// Package sshkeys — локальные SSH-ключи пользователя в ~/.ssh на этом
// компьютере: список, создание, удаление.
//
// Ключ — пара файлов <name> (приватный) и <name>.pub (публичный). В списке
// показываются все *.pub; приватная половина может отсутствовать.
package sshkeys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Key — один ключ из ~/.ssh.
type Key struct {
	Name        string // имя файла без .pub
	PrivPath    string
	PubPath     string
	Type        string // ED25519, RSA, ECDSA, …
	Bits        int    // для RSA/ECDSA; 0 — неизвестно/не важно
	Comment     string
	Fingerprint string // SHA256:… — как у ssh-keygen -l
	PublicLine  string // содержимое .pub одной строкой
	HasPrivate  bool
	Encrypted   bool // приватный ключ защищён паролем
	Modified    time.Time
	Err         string // .pub не разобрался
}

// Dir — ~/.ssh.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("домашняя папка: %w", err)
	}
	return filepath.Join(home, ".ssh"), nil
}

// List — все ключи в ~/.ssh, по имени. Нет папки — пустой список.
func List() ([]Key, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", dir, err)
	}
	var keys []Key
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		keys = append(keys, load(dir, strings.TrimSuffix(e.Name(), ".pub")))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	return keys, nil
}

func load(dir, name string) Key {
	k := Key{Name: name, PrivPath: filepath.Join(dir, name), PubPath: filepath.Join(dir, name+".pub")}
	if st, err := os.Stat(k.PubPath); err == nil {
		k.Modified = st.ModTime()
	}
	data, err := os.ReadFile(k.PubPath)
	if err != nil {
		k.Err = err.Error()
		return k
	}
	k.PublicLine = strings.TrimSpace(string(data))
	pub, comment, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		k.Err = "не похоже на публичный ключ"
		return k
	}
	k.Comment = comment
	k.Fingerprint = ssh.FingerprintSHA256(pub)
	k.Type, k.Bits = describe(pub)

	if priv, err := os.ReadFile(k.PrivPath); err == nil {
		k.HasPrivate = true
		k.Encrypted = encrypted(priv)
	}
	return k
}

func describe(pub ssh.PublicKey) (string, int) {
	switch pub.Type() {
	case ssh.KeyAlgoED25519:
		return "ED25519", 256
	case ssh.KeyAlgoRSA:
		if c, ok := pub.(ssh.CryptoPublicKey); ok {
			if r, ok := c.CryptoPublicKey().(*rsa.PublicKey); ok {
				return "RSA", r.N.BitLen()
			}
		}
		return "RSA", 0
	case ssh.KeyAlgoECDSA256:
		return "ECDSA", 256
	case ssh.KeyAlgoECDSA384:
		return "ECDSA", 384
	case ssh.KeyAlgoECDSA521:
		return "ECDSA", 521
	case ssh.KeyAlgoSKED25519:
		return "ED25519-SK", 256
	case ssh.KeyAlgoSKECDSA256:
		return "ECDSA-SK", 256
	}
	return strings.ToUpper(pub.Type()), 0
}

// encrypted — приватный ключ под паролем. Новый формат OpenSSH хранит имя
// шифра в начале тела («none» — без пароля); старый PEM — заголовок ENCRYPTED.
func encrypted(priv []byte) bool {
	block, _ := pem.Decode(priv)
	if block == nil {
		return false
	}
	if block.Type != "OPENSSH PRIVATE KEY" {
		return strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") || strings.Contains(block.Type, "ENCRYPTED")
	}
	const magic = "openssh-key-v1\x00"
	b := block.Bytes
	if !bytes.HasPrefix(b, []byte(magic)) || len(b) < len(magic)+4 {
		return false
	}
	b = b[len(magic):]
	n := binary.BigEndian.Uint32(b)
	if int(n) > len(b)-4 {
		return false
	}
	return string(b[4:4+n]) != "none"
}

// ---------- создание ----------

type Algo string

const (
	ED25519 Algo = "ed25519"
	RSA4096 Algo = "rsa4096"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// CheckName — пусто, если имя файла годится и не занято.
func CheckName(name string) string {
	switch {
	case name == "":
		return "Укажите имя файла"
	case !nameRe.MatchString(name) || strings.HasPrefix(name, "."):
		return "Только латиница, цифры, точка, дефис и подчёркивание"
	case strings.HasSuffix(name, ".pub"):
		return "Без .pub — он добавится сам"
	}
	dir, err := Dir()
	if err != nil {
		return err.Error()
	}
	for _, p := range []string{name, name + ".pub"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			return "Файл ~/.ssh/" + p + " уже есть"
		}
	}
	return ""
}

// SuggestName — свободное имя: id_ed25519, id_ed25519_2, …
func SuggestName(a Algo) string {
	base := "id_ed25519"
	if a == RSA4096 {
		base = "id_rsa"
	}
	if CheckName(base) == "" {
		return base
	}
	for i := 2; i < 100; i++ {
		n := fmt.Sprintf("%s_%d", base, i)
		if CheckName(n) == "" {
			return n
		}
	}
	return base + "_new"
}

// DefaultComment — user@host, как у ssh-keygen.
func DefaultComment() string {
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	if user == "" || host == "" {
		return "vanta"
	}
	return user + "@" + strings.TrimSuffix(host, ".local")
}

// Generate создаёт пару ключей в ~/.ssh. Пустой passphrase — без пароля.
// Блокируется (RSA 4096 — до нескольких секунд): звать не из UI-горутины.
func Generate(name, comment, passphrase string, a Algo) (Key, error) {
	if msg := CheckName(name); msg != "" {
		return Key{}, errors.New(msg)
	}
	dir, err := Dir()
	if err != nil {
		return Key{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Key{}, fmt.Errorf("создание %s: %w", dir, err)
	}

	var priv any
	var pub ssh.PublicKey
	switch a {
	case RSA4096:
		k, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			return Key{}, err
		}
		priv = k
		pub, err = ssh.NewPublicKey(&k.PublicKey)
		if err != nil {
			return Key{}, err
		}
	default:
		pk, sk, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return Key{}, err
		}
		priv = sk
		pub, err = ssh.NewPublicKey(pk)
		if err != nil {
			return Key{}, err
		}
	}

	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, comment)
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, comment, []byte(passphrase))
	}
	if err != nil {
		return Key{}, fmt.Errorf("приватный ключ: %w", err)
	}

	privPath := filepath.Join(dir, name)
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		pubLine += " " + comment
	}
	// O_EXCL — не затереть файл, появившийся между проверкой и записью.
	if err := writeExcl(privPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return Key{}, err
	}
	if err := writeExcl(privPath+".pub", []byte(pubLine+"\n"), 0o644); err != nil {
		os.Remove(privPath)
		return Key{}, err
	}
	return load(dir, name), nil
}

func writeExcl(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("запись %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("запись %s: %w", path, err)
	}
	return f.Close()
}

// Delete удаляет обе половины ключа. Безвозвратно.
func Delete(k Key) error {
	var errs []error
	for _, p := range []string{k.PrivPath, k.PubPath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Rename переименовывает обе половины ключа. Сначала .pub, потом приватный:
// при ошибке на втором шаге .pub возвращается на место.
func Rename(k Key, newName string) (Key, error) {
	if newName == k.Name {
		return k, nil
	}
	if msg := CheckName(newName); msg != "" {
		return Key{}, errors.New(msg)
	}
	dir := filepath.Dir(k.PubPath)
	newPub := filepath.Join(dir, newName+".pub")
	newPriv := filepath.Join(dir, newName)
	if err := os.Rename(k.PubPath, newPub); err != nil {
		return Key{}, fmt.Errorf("переименование: %w", err)
	}
	if k.HasPrivate {
		if err := os.Rename(k.PrivPath, newPriv); err != nil {
			os.Rename(newPub, k.PubPath)
			return Key{}, fmt.Errorf("переименование: %w", err)
		}
	}
	return load(dir, newName), nil
}

// ErrWrongPassphrase — текущий пароль ключа не подошёл.
var ErrWrongPassphrase = errors.New("неверный текущий пароль")

// ChangePassphrase ставит, меняет или снимает (next == "") пароль приватного
// ключа. Файл перезаписывается атомарно: временный файл → rename.
func ChangePassphrase(k Key, current, next string) error {
	if !k.HasPrivate {
		return errors.New("у ключа нет приватной части")
	}
	data, err := os.ReadFile(k.PrivPath)
	if err != nil {
		return fmt.Errorf("чтение ключа: %w", err)
	}
	var priv any
	if encrypted(data) {
		priv, err = ssh.ParseRawPrivateKeyWithPassphrase(data, []byte(current))
		if errors.Is(err, x509.IncorrectPasswordError) {
			return ErrWrongPassphrase
		}
	} else {
		priv, err = ssh.ParseRawPrivateKey(data)
	}
	if err != nil {
		return fmt.Errorf("разбор ключа: %w", err)
	}
	var block *pem.Block
	if next == "" {
		block, err = ssh.MarshalPrivateKey(priv, k.Comment)
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, k.Comment, []byte(next))
	}
	if err != nil {
		return fmt.Errorf("приватный ключ: %w", err)
	}
	tmp := k.PrivPath + ".vanta-tmp"
	os.Remove(tmp)
	if err := writeExcl(tmp, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, k.PrivPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("запись ключа: %w", err)
	}
	return nil
}

// ShortFingerprint — для узких мест: SHA256:abcd…wxyz.
func ShortFingerprint(fp string) string {
	body := strings.TrimPrefix(fp, "SHA256:")
	if len(body) <= 16 {
		return fp
	}
	return "SHA256:" + body[:8] + "…" + body[len(body)-6:]
}
