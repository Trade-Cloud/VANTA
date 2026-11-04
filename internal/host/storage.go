package host

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Mount — файловая система из df.
type Mount struct {
	Source string
	FSType string
	Size   uint64
	Used   uint64
	Avail  uint64
	Target string
	Inodes string // % занятых inode
}

func (m Mount) Percent() float64 {
	if m.Size == 0 {
		return 0
	}
	return 100 * float64(m.Used) / float64(m.Used+m.Avail)
}

// BlockDev — строка lsblk.
type BlockDev struct {
	Name, Type, FSType, Mount, Model string
	Size                             uint64
}

// Storage — диски и разделы.
type Storage struct {
	Mounts []Mount
	Blocks []BlockDev
}

// GetStorage — df и lsblk. Псевдо-ФС (tmpfs, overlay докера) скрыты.
func (h *Host) GetStorage(ctx context.Context) (*Storage, error) {
	out, err := h.Run(ctx, "df -PT -B1 -x tmpfs -x devtmpfs -x squashfs -x overlay -x efivarfs 2>/dev/null | tail -n +2; echo ---; "+
		"df -Pi -x tmpfs -x devtmpfs -x squashfs -x overlay -x efivarfs 2>/dev/null | tail -n +2; echo ---; "+
		"lsblk -bPno NAME,TYPE,SIZE,FSTYPE,MOUNTPOINT,MODEL 2>/dev/null")
	if err != nil && out == "" {
		return nil, err
	}
	parts := strings.Split(out, "---\n")
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	st := &Storage{}
	inodes := map[string]string{}
	for _, l := range lines(parts[1]) {
		f := strings.Fields(l)
		if len(f) >= 6 {
			inodes[f[5]] = f[4]
		}
	}
	for _, l := range lines(parts[0]) {
		f := strings.Fields(l)
		if len(f) < 7 {
			continue
		}
		m := Mount{Source: f[0], FSType: f[1], Target: strings.Join(f[6:], " ")}
		m.Size, _ = strconv.ParseUint(f[2], 10, 64)
		m.Used, _ = strconv.ParseUint(f[3], 10, 64)
		m.Avail, _ = strconv.ParseUint(f[4], 10, 64)
		m.Inodes = inodes[m.Target]
		st.Mounts = append(st.Mounts, m)
	}
	for _, l := range lines(parts[2]) {
		kv := parseKV(l)
		b := BlockDev{Name: kv["NAME"], Type: kv["TYPE"], FSType: kv["FSTYPE"], Mount: kv["MOUNTPOINT"], Model: strings.TrimSpace(kv["MODEL"])}
		b.Size, _ = strconv.ParseUint(kv["SIZE"], 10, 64)
		if b.Type == "loop" {
			continue
		}
		st.Blocks = append(st.Blocks, b)
	}
	return st, nil
}

// parseKV — строка вида KEY="v" KEY2="v 2" (lsblk -P).
func parseKV(l string) map[string]string {
	out := map[string]string{}
	for len(l) > 0 {
		l = strings.TrimLeft(l, " ")
		eq := strings.Index(l, `="`)
		if eq < 0 {
			break
		}
		key := l[:eq]
		rest := l[eq+2:]
		end := strings.Index(rest, `"`)
		if end < 0 {
			break
		}
		out[key] = rest[:end]
		l = rest[end+1:]
	}
	return out
}

// DirUsage — размер каталога и его вложенных (du -x глубиной 1), крупные первыми.
type DirUsage struct {
	Path string
	Size uint64
}

func (h *Host) DirUsage(ctx context.Context, path string) ([]DirUsage, error) {
	out, err := h.Sudo(ctx, "du -x -B1 --max-depth=1 "+Q(path)+" 2>/dev/null")
	if err != nil && out == "" {
		return nil, err
	}
	var list []DirUsage
	for _, l := range lines(out) {
		size, p, ok := strings.Cut(l, "\t")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(size, 10, 64)
		list = append(list, DirUsage{Path: p, Size: n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Size > list[j].Size })
	return list, nil
}
