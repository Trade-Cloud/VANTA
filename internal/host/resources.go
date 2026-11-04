package host

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Resources — снимок нагрузки сервера.
type Resources struct {
	Hostname string
	Kernel   string
	OS       string
	Uptime   time.Duration
	CPUs     int
	CPUModel string
	Load     [3]float64
	Running  int // готовых к выполнению потоков (4-е поле /proc/loadavg)
	Threads  int // всего потоков

	CPUPercent float64 // за последний интервал между снимками; <0 — ещё не посчитали
	CPUSteal   float64
	CPUIOWait  float64
	CPUUser    float64
	CPUSystem  float64
	Cores      []float64 // загрузка каждого ядра, %; nil — ещё не посчитали

	MemTotal, MemAvailable, MemUsed uint64 // байты
	SwapTotal, SwapUsed             uint64
	Buffers, Cached                 uint64
	Shmem, Slab, Dirty              uint64

	Procs int

	Disks  []Disk    // смонтированные ФС (без tmpfs/overlay и т.п.)
	DiskIO []IORate  // блочные устройства, скорость чтения/записи
	Net    []NetRate // сетевые интерфейсы без lo и veth*
	Temps  []Temp    // датчики температуры; на VPS обычно пусто

	FilesOpen, FilesMax uint64 // /proc/sys/fs/file-nr
	TCP, TCPTimeWait    int    // сокеты TCP в работе / TIME_WAIT
	UDP                 int
	Users               int // сеансов в системе (who)

	at    time.Time
	cpu   cpuTimes
	cores []cpuTimes
	disk  map[string][2]uint64 // имя → байт прочитано/записано всего
	net   map[string][2]uint64 // имя → байт принято/отправлено всего
}

// Disk — смонтированная файловая система.
type Disk struct {
	Device, FS, Mount  string
	Size, Used, Avail  uint64
	Inodes, InodesUsed uint64
}

// IORate — блочное устройство: скорость (байт/с) и всего с загрузки.
type IORate struct {
	Name                  string
	ReadPS, WritePS       float64
	ReadTotal, WriteTotal uint64
}

// NetRate — интерфейс: скорость (байт/с) и всего с загрузки.
type NetRate struct {
	Name             string
	RxPS, TxPS       float64
	RxTotal, TxTotal uint64
}

// Temp — датчик температуры, °C.
type Temp struct {
	Name string
	C    float64
}

type cpuTimes struct {
	total, idle, iowait, steal, user, system uint64
}

// Части вывода разделены строкой "---". Порядок важен для readResources.
const resourcesCmd = `cat /proc/loadavg; echo ---; cat /proc/uptime; echo ---; grep '^cpu' /proc/stat; echo ---; cat /proc/meminfo; echo ---; ` +
	`hostname; echo ---; uname -sr; echo ---; nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo; echo ---; ` +
	`(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME"); echo ---; ls -d /proc/[0-9]* 2>/dev/null | wc -l; echo ---; ` +
	`df -P -B1 -T -x tmpfs -x devtmpfs -x squashfs -x overlay -x efivarfs 2>/dev/null; echo ---; ` +
	`df -P -i -x tmpfs -x devtmpfs -x squashfs -x overlay -x efivarfs 2>/dev/null; echo ---; ` +
	`ls /sys/block 2>/dev/null; echo ---; cat /proc/diskstats 2>/dev/null; echo ---; cat /proc/net/dev; echo ---; ` +
	`grep -m1 -E '^(model name|Hardware|Processor)' /proc/cpuinfo 2>/dev/null; echo ---; ` +
	`for z in /sys/class/thermal/thermal_zone*; do [ -r "$z/temp" ] && echo "$(cat $z/type 2>/dev/null) $(cat $z/temp)"; done 2>/dev/null; echo ---; ` +
	`cat /proc/sys/fs/file-nr 2>/dev/null; echo ---; cat /proc/net/sockstat 2>/dev/null; echo ---; who 2>/dev/null | wc -l`

const resourcesParts = 18

// GetResources снимает нагрузку. prev — прошлый снимок (для % CPU и
// скоростей); nil — первый снимок, всё посчитается за короткую паузу внутри.
func (h *Host) GetResources(ctx context.Context, prev *Resources) (*Resources, error) {
	if prev == nil {
		first, err := h.readResources(ctx)
		if err != nil {
			return nil, err
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		prev = first
	}
	r, err := h.readResources(ctx)
	if err != nil {
		return nil, err
	}
	if pct, ok := cpuDelta(prev.cpu, r.cpu); ok {
		r.CPUPercent = pct[0]
		r.CPUIOWait, r.CPUSteal, r.CPUUser, r.CPUSystem = pct[1], pct[2], pct[3], pct[4]
	}
	if len(prev.cores) == len(r.cores) {
		r.Cores = make([]float64, len(r.cores))
		for i := range r.cores {
			if pct, ok := cpuDelta(prev.cores[i], r.cores[i]); ok {
				r.Cores[i] = pct[0]
			}
		}
	}
	dt := r.at.Sub(prev.at).Seconds()
	rate := func(now, was uint64) float64 {
		if dt <= 0 || now < was {
			return 0
		}
		return float64(now-was) / dt
	}
	for i := range r.DiskIO {
		d := &r.DiskIO[i]
		if p, ok := prev.disk[d.Name]; ok {
			d.ReadPS, d.WritePS = rate(d.ReadTotal, p[0]), rate(d.WriteTotal, p[1])
		}
	}
	for i := range r.Net {
		n := &r.Net[i]
		if p, ok := prev.net[n.Name]; ok {
			n.RxPS, n.TxPS = rate(n.RxTotal, p[0]), rate(n.TxTotal, p[1])
		}
	}
	return r, nil
}

// cpuDelta — % загрузки между двумя снимками: всего, iowait, steal, user, system.
func cpuDelta(a, b cpuTimes) ([5]float64, bool) {
	var out [5]float64
	if b.total <= a.total {
		return out, false
	}
	dt := float64(b.total - a.total)
	sub := func(x, y uint64) float64 {
		if y < x {
			return 0
		}
		return float64(y - x)
	}
	out[0] = 100 * (1 - (sub(a.idle, b.idle)+sub(a.iowait, b.iowait))/dt)
	out[1] = 100 * sub(a.iowait, b.iowait) / dt
	out[2] = 100 * sub(a.steal, b.steal) / dt
	out[3] = 100 * sub(a.user, b.user) / dt
	out[4] = 100 * sub(a.system, b.system) / dt
	for i := range out {
		out[i] = max(0, min(100, out[i]))
	}
	return out, true
}

func parseCPU(f []string) cpuTimes {
	var t cpuTimes
	var v []uint64
	for _, x := range f[1:] {
		n, _ := strconv.ParseUint(x, 10, 64)
		v = append(v, n)
	}
	// user nice system idle iowait irq softirq steal guest guest_nice
	for i, n := range v {
		if i < 8 { // guest уже входит в user
			t.total += n
		}
	}
	at := func(i int) uint64 {
		if i < len(v) {
			return v[i]
		}
		return 0
	}
	t.user, t.system, t.idle, t.iowait, t.steal = at(0)+at(1), at(2)+at(5)+at(6), at(3), at(4), at(7)
	return t
}

func (h *Host) readResources(ctx context.Context) (*Resources, error) {
	out, err := h.Run(ctx, resourcesCmd)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out, "---\n")
	if len(parts) < resourcesParts {
		return nil, fmt.Errorf("неожиданный вывод /proc")
	}
	r := &Resources{CPUPercent: -1, at: time.Now()}
	if f := strings.Fields(parts[0]); len(f) >= 3 {
		for i := 0; i < 3; i++ {
			r.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
		if len(f) >= 4 {
			a, b, _ := strings.Cut(f[3], "/")
			r.Running, _ = strconv.Atoi(a)
			r.Threads, _ = strconv.Atoi(b)
		}
	}
	if f := strings.Fields(parts[1]); len(f) >= 1 {
		sec, _ := strconv.ParseFloat(f[0], 64)
		r.Uptime = time.Duration(sec) * time.Second
	}
	for _, l := range lines(parts[2]) {
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		if f[0] == "cpu" {
			r.cpu = parseCPU(f)
		} else if strings.HasPrefix(f[0], "cpu") {
			r.cores = append(r.cores, parseCPU(f))
		}
	}
	mem := map[string]uint64{}
	for _, l := range lines(parts[3]) {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(f[0], 10, 64)
		mem[k] = n * 1024
	}
	r.MemTotal = mem["MemTotal"]
	r.MemAvailable = mem["MemAvailable"]
	if r.MemAvailable == 0 {
		r.MemAvailable = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	if r.MemTotal > r.MemAvailable {
		r.MemUsed = r.MemTotal - r.MemAvailable
	}
	r.Buffers, r.Cached = mem["Buffers"], mem["Cached"]
	r.Shmem, r.Slab, r.Dirty = mem["Shmem"], mem["Slab"], mem["Dirty"]
	r.SwapTotal = mem["SwapTotal"]
	if r.SwapTotal > mem["SwapFree"] {
		r.SwapUsed = r.SwapTotal - mem["SwapFree"]
	}
	r.Hostname = strings.TrimSpace(parts[4])
	r.Kernel = strings.TrimSpace(parts[5])
	r.CPUs, _ = strconv.Atoi(strings.TrimSpace(parts[6]))
	r.OS = strings.TrimSpace(parts[7])
	r.Procs, _ = strconv.Atoi(strings.TrimSpace(parts[8]))
	r.Disks = parseDF(parts[9], parts[10])
	r.DiskIO, r.disk = parseDiskstats(parts[11], parts[12])
	r.Net, r.net = parseNetDev(parts[13])
	if _, v, ok := strings.Cut(strings.TrimSpace(parts[14]), ":"); ok {
		r.CPUModel = strings.Join(strings.Fields(v), " ")
	}
	for _, l := range lines(parts[15]) {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseFloat(f[len(f)-1], 64)
		if err != nil || n <= 0 {
			continue
		}
		r.Temps = append(r.Temps, Temp{Name: strings.Join(f[:len(f)-1], " "), C: n / 1000})
	}
	if f := strings.Fields(parts[16]); len(f) >= 3 {
		r.FilesOpen, _ = strconv.ParseUint(f[0], 10, 64)
		r.FilesMax, _ = strconv.ParseUint(f[2], 10, 64)
	}
	for _, l := range lines(parts[17]) {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		val := func(key string) int {
			for i := 1; i+1 < len(f); i++ {
				if f[i] == key {
					n, _ := strconv.Atoi(f[i+1])
					return n
				}
			}
			return 0
		}
		switch f[0] {
		case "TCP:":
			r.TCP, r.TCPTimeWait = val("inuse"), val("tw")
		case "UDP:":
			r.UDP = val("inuse")
		}
	}
	if len(parts) > resourcesParts {
		r.Users, _ = strconv.Atoi(strings.TrimSpace(parts[18]))
	}
	return r, nil
}

// parseDF — `df -P -B1 -T` и `df -P -i`, без дублей одного устройства
// (bind-монтирования) и без псевдо-ФС.
func parseDF(sizes, inodes string) []Disk {
	ino := map[string][2]uint64{}
	for i, l := range lines(inodes) {
		f := strings.Fields(l)
		if i == 0 || len(f) < 6 {
			continue
		}
		total, _ := strconv.ParseUint(f[1], 10, 64)
		used, _ := strconv.ParseUint(f[2], 10, 64)
		ino[strings.Join(f[5:], " ")] = [2]uint64{total, used}
	}
	var out []Disk
	seen := map[string]bool{}
	for i, l := range lines(sizes) {
		f := strings.Fields(l)
		if i == 0 || len(f) < 7 {
			continue
		}
		d := Disk{Device: f[0], FS: f[1], Mount: strings.Join(f[6:], " ")}
		d.Size, _ = strconv.ParseUint(f[2], 10, 64)
		d.Used, _ = strconv.ParseUint(f[3], 10, 64)
		d.Avail, _ = strconv.ParseUint(f[4], 10, 64)
		if d.Size == 0 || seen[d.Device] {
			continue
		}
		seen[d.Device] = true
		n := ino[d.Mount]
		d.Inodes, d.InodesUsed = n[0], n[1]
		out = append(out, d)
	}
	return out
}

// parseDiskstats — только целые устройства из /sys/block, без loop/ram/zram.
func parseDiskstats(block, stats string) ([]IORate, map[string][2]uint64) {
	dev := map[string]bool{}
	for _, n := range strings.Fields(block) {
		if !strings.HasPrefix(n, "loop") && !strings.HasPrefix(n, "ram") && !strings.HasPrefix(n, "zram") {
			dev[n] = true
		}
	}
	var out []IORate
	tot := map[string][2]uint64{}
	for _, l := range lines(stats) {
		f := strings.Fields(l)
		if len(f) < 10 || !dev[f[2]] {
			continue
		}
		rd, _ := strconv.ParseUint(f[5], 10, 64)
		wr, _ := strconv.ParseUint(f[9], 10, 64)
		io := IORate{Name: f[2], ReadTotal: rd * 512, WriteTotal: wr * 512}
		tot[io.Name] = [2]uint64{io.ReadTotal, io.WriteTotal}
		out = append(out, io)
	}
	return out, tot
}

// parseNetDev — /proc/net/dev без lo и veth* (их плодит Docker).
func parseNetDev(s string) ([]NetRate, map[string][2]uint64) {
	var out []NetRate
	tot := map[string][2]uint64{}
	for _, l := range lines(s) {
		name, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if name == "lo" || strings.HasPrefix(name, "veth") || len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		n := NetRate{Name: name, RxTotal: rx, TxTotal: tx}
		tot[name] = [2]uint64{rx, tx}
		out = append(out, n)
	}
	return out, tot
}

// Bytes — размер для человека: 1.5 GiB.
func Bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Duration — аптайм для человека: 3 д 4 ч 12 мин.
func Duration(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d д %d ч %d мин", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%d ч %d мин", hours, mins)
	}
	return fmt.Sprintf("%d мин", mins)
}
