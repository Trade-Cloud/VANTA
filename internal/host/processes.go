package host

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Process — строка ps.
type Process struct {
	PID     int
	PPID    int
	User    string
	CPU     float64 // %
	Mem     float64 // %
	RSS     uint64  // байты
	State   string
	Elapsed string
	Name    string
	Command string
}

// Processes — все процессы, по убыванию CPU.
func (h *Host) Processes(ctx context.Context) ([]Process, error) {
	out, err := h.Run(ctx, "ps -eo pid=,ppid=,user:32=,pcpu=,pmem=,rss=,stat=,etime=,comm:32=,args= --sort=-pcpu 2>/dev/null || "+
		"ps -eo pid=,ppid=,user=,pcpu=,pmem=,rss=,stat=,etime=,comm=,args=")
	if err != nil {
		return nil, err
	}
	var list []Process
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) < 9 {
			continue
		}
		p := Process{User: f[2], State: f[6], Elapsed: f[7], Name: f[8]}
		p.PID, _ = strconv.Atoi(f[0])
		p.PPID, _ = strconv.Atoi(f[1])
		p.CPU, _ = strconv.ParseFloat(f[3], 64)
		p.Mem, _ = strconv.ParseFloat(f[4], 64)
		rss, _ := strconv.ParseUint(f[5], 10, 64)
		p.RSS = rss * 1024
		if len(f) > 9 {
			p.Command = strings.Join(f[9:], " ")
		} else {
			p.Command = p.Name
		}
		if p.PID > 0 {
			list = append(list, p)
		}
	}
	return list, nil
}

// Signals — что можно отправить процессу из UI.
var Signals = []string{"TERM", "KILL", "HUP", "INT", "STOP", "CONT"}

// Kill отправляет сигнал. Чужие процессы — через sudo.
func (h *Host) Kill(ctx context.Context, pid int, sig string, root bool) error {
	cmd := fmt.Sprintf("kill -s %s %d", sig, pid)
	var err error
	if root {
		_, err = h.Sudo(ctx, cmd)
	} else {
		_, err = h.Run(ctx, cmd)
	}
	return err
}

// Renice меняет приоритет процесса (nice -20…19).
func (h *Host) Renice(ctx context.Context, pid, nice int) error {
	_, err := h.Sudo(ctx, fmt.Sprintf("renice -n %d -p %d", nice, pid))
	return err
}
