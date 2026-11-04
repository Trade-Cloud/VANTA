package host

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Package — установленный пакет.
type Package struct {
	Name    string
	Version string
	Arch    string
	Size    uint64 // байты, 0 — неизвестно
	Summary string
	Upgrade string // доступная версия, "" — обновлений нет
}

// PkgManager — поддерживаемые менеджеры пакетов.
const (
	PkgApt    = "apt"
	PkgDnf    = "dnf"
	PkgYum    = "yum"
	PkgApk    = "apk"
	PkgPacman = "pacman"
)

// PackageManager — какой менеджер пакетов на сервере ("" — не найден).
func (h *Host) PackageManager(ctx context.Context) string {
	h.mu.Lock()
	pm := h.pkg
	h.mu.Unlock()
	if pm != "" {
		return pm
	}
	out, _ := h.Run(ctx, "for m in apt-get dnf yum apk pacman; do command -v $m >/dev/null 2>&1 && { echo $m; break; }; done")
	pm = strings.TrimSpace(out)
	if pm == "apt-get" {
		pm = PkgApt
	}
	h.mu.Lock()
	h.pkg = pm
	h.mu.Unlock()
	return pm
}

var pkgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_:@-]*$`)

// ValidPackage — имя пакета без пробелов и метасимволов.
func ValidPackage(s string) bool { return pkgNameRe.MatchString(s) }

// Packages — установленные пакеты вместе с доступными обновлениями
// (по локальному индексу, без обновления индекса).
func (h *Host) Packages(ctx context.Context) ([]Package, error) {
	pm := h.PackageManager(ctx)
	var cmd string
	switch pm {
	case PkgApt:
		cmd = `dpkg-query -W -f='${db:Status-Abbrev}\t${Package}\t${Version}\t${Architecture}\t${Installed-Size}\t${binary:Summary}\n'`
	case PkgDnf, PkgYum:
		cmd = `rpm -qa --qf '%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\t%{SIZE}\t%{SUMMARY}\n'`
	case PkgApk:
		cmd = `apk info -v 2>/dev/null`
	case PkgPacman:
		cmd = `pacman -Q`
	default:
		return nil, errors.New("менеджер пакетов не найден (apt, dnf, yum, apk, pacman)")
	}
	out, err := h.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	var list []Package
	for _, l := range lines(out) {
		var p Package
		switch pm {
		case PkgApt:
			f := strings.SplitN(l, "\t", 6)
			if len(f) < 6 || !strings.HasPrefix(f[0], "ii") {
				continue
			}
			p = Package{Name: f[1], Version: f[2], Arch: f[3], Summary: f[5]}
			p.Size = parseU(f[4]) * 1024
		case PkgDnf, PkgYum:
			f := strings.SplitN(l, "\t", 5)
			if len(f) < 5 {
				continue
			}
			p = Package{Name: f[0], Version: f[1], Arch: f[2], Summary: f[4]}
			p.Size = parseU(f[3])
		case PkgApk:
			// name-1.2.3-r0
			i := strings.LastIndex(l, "-")
			if i > 0 {
				if j := strings.LastIndex(l[:i], "-"); j > 0 {
					p = Package{Name: l[:j], Version: l[j+1:]}
				}
			}
			if p.Name == "" {
				p.Name = l
			}
		case PkgPacman:
			f := strings.Fields(l)
			if len(f) >= 2 {
				p = Package{Name: f[0], Version: f[1]}
			}
		}
		if p.Name != "" {
			list = append(list, p)
		}
	}
	if up, err := h.Upgradable(ctx); err == nil {
		for i := range list {
			list[i].Upgrade = up[list[i].Name]
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// Upgradable — пакет → доступная версия по текущему индексу.
func (h *Host) Upgradable(ctx context.Context) (map[string]string, error) {
	pm := h.PackageManager(ctx)
	var cmd string
	switch pm {
	case PkgApt:
		cmd = "apt list --upgradable 2>/dev/null"
	case PkgDnf, PkgYum:
		cmd = pm + " -q check-update 2>/dev/null; true"
	case PkgApk:
		cmd = "apk version -l '<' 2>/dev/null"
	case PkgPacman:
		cmd = "pacman -Qu 2>/dev/null; true"
	default:
		return nil, errors.New("нет менеджера пакетов")
	}
	out, err := h.Run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	up := map[string]string{}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		switch pm {
		case PkgApt: // nginx/jammy-updates 1.2 amd64 [upgradable from: 1.1]
			if len(f) >= 2 && strings.Contains(f[0], "/") {
				up[strings.SplitN(f[0], "/", 2)[0]] = f[1]
			}
		case PkgDnf, PkgYum: // nginx.x86_64  1:1.2-1.el9  appstream
			if len(f) >= 3 && strings.Contains(f[0], ".") {
				up[f[0][:strings.LastIndex(f[0], ".")]] = f[1]
			}
		case PkgApk: // nginx-1.1-r0 < 1.2-r0
			if len(f) >= 3 {
				n := f[0]
				if i := strings.LastIndex(n, "-"); i > 0 {
					if j := strings.LastIndex(n[:i], "-"); j > 0 {
						n = n[:j]
					}
				}
				up[n] = f[2]
			}
		case PkgPacman: // nginx 1.1 -> 1.2
			if len(f) >= 4 {
				up[f[0]] = f[3]
			}
		}
	}
	return up, nil
}

// PkgOp — операция с пакетами.
type PkgOp string

const (
	OpRefresh    PkgOp = "refresh"     // обновить индекс
	OpUpgradeAll PkgOp = "upgrade-all" // обновить всё
	OpInstall    PkgOp = "install"
	OpRemove     PkgOp = "remove"
	OpUpgrade    PkgOp = "upgrade" // обновить выбранные
)

// PkgCommand — команда для операции (неинтерактивная).
func (h *Host) PkgCommand(ctx context.Context, op PkgOp, names []string) (string, error) {
	for _, n := range names {
		if !ValidPackage(n) {
			return "", errors.New("недопустимое имя пакета: " + n)
		}
	}
	args := strings.Join(names, " ")
	if op != OpRefresh && op != OpUpgradeAll && len(names) == 0 {
		return "", errors.New("не выбраны пакеты")
	}
	switch h.PackageManager(ctx) {
	case PkgApt:
		apt := "DEBIAN_FRONTEND=noninteractive apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "
		switch op {
		case OpRefresh:
			return "apt-get update", nil
		case OpUpgradeAll:
			return apt + "upgrade", nil
		case OpInstall:
			return apt + "install " + args, nil
		case OpRemove:
			return apt + "remove " + args, nil
		case OpUpgrade:
			return apt + "install --only-upgrade " + args, nil
		}
	case PkgDnf, PkgYum:
		pm := h.PackageManager(ctx)
		switch op {
		case OpRefresh:
			return pm + " -y makecache", nil
		case OpUpgradeAll:
			return pm + " -y upgrade", nil
		case OpInstall:
			return pm + " -y install " + args, nil
		case OpRemove:
			return pm + " -y remove " + args, nil
		case OpUpgrade:
			return pm + " -y upgrade " + args, nil
		}
	case PkgApk:
		switch op {
		case OpRefresh:
			return "apk update", nil
		case OpUpgradeAll:
			return "apk upgrade", nil
		case OpInstall:
			return "apk add " + args, nil
		case OpRemove:
			return "apk del " + args, nil
		case OpUpgrade:
			return "apk add -u " + args, nil
		}
	case PkgPacman:
		switch op {
		case OpRefresh:
			return "pacman -Sy --noconfirm", nil
		case OpUpgradeAll:
			return "pacman -Syu --noconfirm", nil
		case OpInstall, OpUpgrade:
			return "pacman -S --noconfirm " + args, nil
		case OpRemove:
			return "pacman -R --noconfirm " + args, nil
		}
	}
	return "", errors.New("операция не поддерживается")
}

// PkgRun выполняет операцию от root, вывод — в fn по мере появления.
func (h *Host) PkgRun(ctx context.Context, op PkgOp, names []string, fn func(string)) error {
	cmd, err := h.PkgCommand(ctx, op, names)
	if err != nil {
		return err
	}
	fn("$ " + cmd + "\n")
	return h.Stream(ctx, cmd+" 2>&1", true, fn)
}

// SearchPackages — поиск в репозиториях (имя и описание).
func (h *Host) SearchPackages(ctx context.Context, q string) ([]Package, error) {
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	var cmd string
	pm := h.PackageManager(ctx)
	switch pm {
	case PkgApt:
		cmd = "apt-cache search -- " + Q(q) + " | head -n 200"
	case PkgDnf, PkgYum:
		cmd = pm + " -q search " + Q(q) + " 2>/dev/null | grep -v '^=' | head -n 200"
	case PkgApk:
		cmd = "apk search -v -- " + Q(q) + " | head -n 200"
	case PkgPacman:
		cmd = "pacman -Ss -- " + Q(q) + " | paste - - | head -n 200"
	default:
		return nil, errors.New("нет менеджера пакетов")
	}
	out, err := h.Run(ctx, cmd)
	if err != nil && out == "" {
		return nil, err
	}
	var list []Package
	for _, l := range lines(out) {
		switch pm {
		case PkgApt: // name - summary
			n, s, _ := strings.Cut(l, " - ")
			list = append(list, Package{Name: strings.TrimSpace(n), Summary: s})
		case PkgDnf, PkgYum: // name.arch : summary
			n, s, _ := strings.Cut(l, " : ")
			n = strings.TrimSpace(n)
			if i := strings.LastIndex(n, "."); i > 0 {
				n = n[:i]
			}
			list = append(list, Package{Name: n, Summary: s})
		case PkgApk:
			n, s, _ := strings.Cut(l, " - ")
			list = append(list, Package{Name: n, Summary: s})
		case PkgPacman: // repo/name ver\t    summary
			f := strings.Fields(l)
			if len(f) >= 2 {
				n := f[0]
				if i := strings.Index(n, "/"); i >= 0 {
					n = n[i+1:]
				}
				list = append(list, Package{Name: n, Version: f[1], Summary: strings.Join(f[2:], " ")})
			}
		}
	}
	return list, nil
}

func parseU(s string) uint64 {
	var n uint64
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + uint64(c-'0')
	}
	return n
}
