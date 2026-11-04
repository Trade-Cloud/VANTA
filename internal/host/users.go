package host

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// User — учётная запись Linux на сервере.
type User struct {
	Name   string
	UID    int
	GID    int
	Gecos  string
	Home   string
	Shell  string
	Groups []string
	Locked bool // пароль заблокирован (passwd -S: L/LK)
	System bool // UID < 1000 (кроме root)
	Sudo   bool // в группе sudo/wheel/admin
}

// Group — группа.
type Group struct {
	Name    string
	GID     int
	Members []string
}

var nameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// ValidName — допустимое имя пользователя/группы.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Users — пользователи и группы. Статус блокировки читается через sudo,
// если он доступен без вопросов.
func (h *Host) Users(ctx context.Context) ([]User, []Group, error) {
	out, err := h.Run(ctx, "getent passwd || cat /etc/passwd; echo ---; getent group || cat /etc/group")
	if err != nil {
		return nil, nil, err
	}
	pw, gr, _ := strings.Cut(out, "---\n")
	var groups []Group
	member := map[string][]string{}
	gidName := map[int]string{}
	for _, l := range lines(gr) {
		f := strings.Split(l, ":")
		if len(f) < 4 {
			continue
		}
		g := Group{Name: f[0]}
		g.GID, _ = strconv.Atoi(f[2])
		if f[3] != "" {
			g.Members = strings.Split(f[3], ",")
		}
		for _, m := range g.Members {
			member[m] = append(member[m], g.Name)
		}
		gidName[g.GID] = g.Name
		groups = append(groups, g)
	}
	var users []User
	for _, l := range lines(pw) {
		f := strings.Split(l, ":")
		if len(f) < 7 {
			continue
		}
		u := User{Name: f[0], Gecos: f[4], Home: f[5], Shell: f[6]}
		u.UID, _ = strconv.Atoi(f[2])
		u.GID, _ = strconv.Atoi(f[3])
		u.System = u.UID != 0 && (u.UID < 1000 || u.UID == 65534)
		if g := gidName[u.GID]; g != "" {
			u.Groups = append(u.Groups, g)
		}
		for _, g := range member[u.Name] {
			if g != gidName[u.GID] {
				u.Groups = append(u.Groups, g)
			}
		}
		for _, g := range u.Groups {
			if g == "sudo" || g == "wheel" || g == "admin" {
				u.Sudo = true
			}
		}
		users = append(users, u)
	}
	if h.IsRoot(ctx) {
		if st, err := h.Sudo(ctx, "passwd -Sa 2>/dev/null || for u in $(cut -d: -f1 /etc/passwd); do passwd -S \"$u\" 2>/dev/null; done"); err == nil || st != "" {
			locked := map[string]bool{}
			for _, l := range lines(st) {
				f := strings.Fields(l)
				if len(f) >= 2 && (f[1] == "L" || f[1] == "LK") {
					locked[f[0]] = true
				}
			}
			for i := range users {
				users[i].Locked = locked[users[i].Name]
			}
		}
	}
	sort.SliceStable(users, func(i, j int) bool {
		if users[i].System != users[j].System {
			return !users[i].System
		}
		return users[i].UID < users[j].UID
	})
	sort.Slice(groups, func(i, j int) bool { return groups[i].GID < groups[j].GID })
	return users, groups, nil
}

// NewUser — что нужно для useradd.
type NewUser struct {
	Name     string
	Shell    string // "" — /bin/bash
	Password string // "" — без пароля (вход только по ключу)
	Sudo     bool
	Groups   []string
	PubKey   string // ключ в ~/.ssh/authorized_keys
}

// AddUser создаёт пользователя с домашним каталогом.
func (h *Host) AddUser(ctx context.Context, u NewUser) error {
	if !ValidName(u.Name) {
		return errors.New("имя: латиница в нижнем регистре, цифры, _ и -, до 32 символов")
	}
	shell := u.Shell
	if shell == "" {
		shell = "/bin/bash"
	}
	if _, err := h.Sudo(ctx, "useradd -m -s "+Q(shell)+" -- "+Q(u.Name)); err != nil {
		return err
	}
	if u.Password != "" {
		if err := h.SetPassword(ctx, u.Name, u.Password); err != nil {
			return err
		}
	}
	groups := append([]string{}, u.Groups...)
	if u.Sudo {
		groups = append(groups, "sudo-or-wheel")
	}
	for _, g := range groups {
		if err := h.AddToGroup(ctx, u.Name, g); err != nil {
			return err
		}
	}
	if strings.TrimSpace(u.PubKey) != "" {
		if err := h.AddAuthorizedKey(ctx, u.Name, u.PubKey); err != nil {
			return err
		}
	}
	return nil
}

// SetPassword — chpasswd (пароль идёт через stdin, не в аргументах).
func (h *Host) SetPassword(ctx context.Context, user, password string) error {
	if strings.ContainsAny(password, "\r\n") {
		return errors.New("пароль не может содержать перевод строки")
	}
	_, err := h.SudoInput(ctx, "chpasswd", []byte(user+":"+password+"\n"))
	return err
}

// AddToGroup — usermod -aG. Группа "sudo-or-wheel" — та из двух, что есть.
func (h *Host) AddToGroup(ctx context.Context, user, group string) error {
	if group == "sudo-or-wheel" {
		_, err := h.Sudo(ctx, `if getent group sudo >/dev/null; then g=sudo; else g=wheel; fi; usermod -aG "$g" -- `+Q(user))
		return err
	}
	_, err := h.Sudo(ctx, "usermod -aG "+Q(group)+" -- "+Q(user))
	return err
}

// RemoveFromGroup — gpasswd -d.
func (h *Host) RemoveFromGroup(ctx context.Context, user, group string) error {
	_, err := h.Sudo(ctx, "gpasswd -d "+Q(user)+" "+Q(group))
	return err
}

// LockUser / UnlockUser — usermod -L / -U.
func (h *Host) LockUser(ctx context.Context, user string, lock bool) error {
	flag := "-U"
	if lock {
		flag = "-L"
	}
	_, err := h.Sudo(ctx, "usermod "+flag+" -- "+Q(user))
	return err
}

// SetShell — chsh/usermod -s.
func (h *Host) SetShell(ctx context.Context, user, shell string) error {
	_, err := h.Sudo(ctx, "usermod -s "+Q(shell)+" -- "+Q(user))
	return err
}

// DeleteUser — userdel; withHome — вместе с домашним каталогом и почтой.
func (h *Host) DeleteUser(ctx context.Context, user string, withHome bool) error {
	if user == "root" {
		return errors.New("root удалять нельзя")
	}
	flag := ""
	if withHome {
		flag = "-r "
	}
	_, err := h.Sudo(ctx, "userdel "+flag+"-- "+Q(user))
	return err
}

// AddGroup / DeleteGroup.
func (h *Host) AddGroup(ctx context.Context, name string) error {
	if !ValidName(name) {
		return errors.New("недопустимое имя группы")
	}
	_, err := h.Sudo(ctx, "groupadd -- "+Q(name))
	return err
}

func (h *Host) DeleteGroup(ctx context.Context, name string) error {
	_, err := h.Sudo(ctx, "groupdel -- "+Q(name))
	return err
}

// Shells — оболочки из /etc/shells.
func (h *Host) Shells(ctx context.Context) []string {
	out, _ := h.Run(ctx, "grep -v '^#' /etc/shells 2>/dev/null")
	list := lines(out)
	list = append(list, "/usr/sbin/nologin")
	return list
}

// AuthorizedKeys — содержимое ~user/.ssh/authorized_keys.
func (h *Host) AuthorizedKeys(ctx context.Context, user string) ([]string, error) {
	out, err := h.Sudo(ctx, `d=$(getent passwd `+Q(user)+` | cut -d: -f6); cat "$d/.ssh/authorized_keys" 2>/dev/null; true`)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, l := range lines(out) {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			keys = append(keys, strings.TrimSpace(l))
		}
	}
	return keys, nil
}

// AddAuthorizedKey дописывает публичный ключ пользователю (с правами 700/600).
func (h *Host) AddAuthorizedKey(ctx context.Context, user, key string) error {
	key = strings.TrimSpace(key)
	if strings.Contains(key, "\n") || !strings.HasPrefix(key, "ssh-") && !strings.HasPrefix(key, "ecdsa-") && !strings.HasPrefix(key, "sk-") {
		return errors.New("это не похоже на публичный SSH-ключ (одна строка ssh-…)")
	}
	_, err := h.SudoInput(ctx, `u=`+Q(user)+`; d=$(getent passwd "$u" | cut -d: -f6); [ -n "$d" ] || exit 3; `+
		`mkdir -p "$d/.ssh" && cat >> "$d/.ssh/authorized_keys" && chmod 700 "$d/.ssh" && chmod 600 "$d/.ssh/authorized_keys" && `+
		`chown -R "$u": "$d/.ssh"`, []byte(key+"\n"))
	return err
}

// RemoveAuthorizedKey удаляет строку ключа из authorized_keys.
func (h *Host) RemoveAuthorizedKey(ctx context.Context, user, key string) error {
	_, err := h.SudoInput(ctx, `u=`+Q(user)+`; d=$(getent passwd "$u" | cut -d: -f6); f="$d/.ssh/authorized_keys"; [ -f "$f" ] || exit 0; `+
		`k=$(cat); grep -vxF -- "$k" "$f" > "$f.vanta" ; cat "$f.vanta" > "$f"; rm -f "$f.vanta"`, []byte(strings.TrimSpace(key)))
	return err
}
