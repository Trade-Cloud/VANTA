package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Container — docker ps -a.
type Container struct {
	ID      string
	Name    string
	Image   string
	State   string // running / exited / paused / restarting / created
	Status  string // Up 3 hours
	Ports   string
	Created string
	CPU     string // из docker stats, "" — не снимали
	Mem     string
}

// Image — docker images.
type Image struct {
	ID         string
	Repository string
	Tag        string
	Size       string
	Created    string
}

// Volume — docker volume ls.
type Volume struct {
	Name   string
	Driver string
}

// DockerNet — docker network ls.
type DockerNet struct {
	ID     string
	Name   string
	Driver string
	Scope  string
}

// Docker — всё сразу.
type Docker struct {
	Version    string
	Root       bool // docker требует sudo (пользователь не в группе docker)
	Containers []Container
	Images     []Image
	Volumes    []Volume
	Networks   []DockerNet
}

// docker — запуск docker напрямую или через sudo.
func (h *Host) docker(ctx context.Context, root bool, args string) (string, error) {
	if root {
		return h.Sudo(ctx, "docker "+args)
	}
	return h.Run(ctx, "docker "+args)
}

// GetDocker — контейнеры, образы, тома, сети. stats — ещё и CPU/память.
func (h *Host) GetDocker(ctx context.Context, stats bool) (*Docker, error) {
	if !h.Has(ctx, "docker") {
		return nil, errors.New("Docker не установлен на сервере")
	}
	d := &Docker{}
	ver, err := h.docker(ctx, false, "version --format '{{.Server.Version}}'")
	if err != nil {
		// Нет доступа к сокету — пробуем через sudo.
		if ver, err = h.docker(ctx, true, "version --format '{{.Server.Version}}'"); err != nil {
			return nil, fmt.Errorf("docker недоступен: %w", err)
		}
		d.Root = true
	}
	d.Version = strings.TrimSpace(ver)
	out, err := h.docker(ctx, d.Root, "ps -a --no-trunc --format '{{json .}}'; echo ---; docker images --format '{{json .}}'; echo ---; "+
		"docker volume ls --format '{{json .}}'; echo ---; docker network ls --format '{{json .}}'")
	if err != nil && out == "" {
		return nil, err
	}
	parts := strings.Split(out, "---\n")
	for len(parts) < 4 {
		parts = append(parts, "")
	}
	for _, l := range lines(parts[0]) {
		var m map[string]string
		if json.Unmarshal([]byte(l), &m) != nil {
			continue
		}
		id := m["ID"]
		if len(id) > 12 {
			id = id[:12]
		}
		d.Containers = append(d.Containers, Container{ID: id, Name: m["Names"], Image: m["Image"], State: m["State"],
			Status: m["Status"], Ports: m["Ports"], Created: m["CreatedAt"]})
	}
	for _, l := range lines(parts[1]) {
		var m map[string]string
		if json.Unmarshal([]byte(l), &m) == nil {
			d.Images = append(d.Images, Image{ID: m["ID"], Repository: m["Repository"], Tag: m["Tag"], Size: m["Size"], Created: m["CreatedSince"]})
		}
	}
	for _, l := range lines(parts[2]) {
		var m map[string]string
		if json.Unmarshal([]byte(l), &m) == nil {
			d.Volumes = append(d.Volumes, Volume{Name: m["Name"], Driver: m["Driver"]})
		}
	}
	for _, l := range lines(parts[3]) {
		var m map[string]string
		if json.Unmarshal([]byte(l), &m) == nil {
			d.Networks = append(d.Networks, DockerNet{ID: m["ID"], Name: m["Name"], Driver: m["Driver"], Scope: m["Scope"]})
		}
	}
	if stats {
		if s, err := h.docker(ctx, d.Root, "stats --no-stream --format '{{json .}}'"); err == nil {
			byID := map[string][2]string{}
			for _, l := range lines(s) {
				var m map[string]string
				if json.Unmarshal([]byte(l), &m) == nil {
					id := m["ID"]
					if len(id) > 12 {
						id = id[:12]
					}
					byID[id] = [2]string{m["CPUPerc"], m["MemUsage"]}
				}
			}
			for i := range d.Containers {
				if v, ok := byID[d.Containers[i].ID]; ok {
					d.Containers[i].CPU, d.Containers[i].Mem = v[0], v[1]
				}
			}
		}
	}
	return d, nil
}

var dockerRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]*$`)

// ContainerActions — действия с контейнером. Danger — с подтверждением.
var ContainerActions = []struct {
	Verb, Label string
	Danger      bool
}{
	{"start", "Запустить", false},
	{"stop", "Остановить", true},
	{"restart", "Перезапустить", true},
	{"pause", "Пауза", true},
	{"unpause", "Снять паузу", false},
	{"kill", "Убить (SIGKILL)", true},
	{"rm -f", "Удалить", true},
}

// ContainerAction — docker <verb> <id>.
func (h *Host) ContainerAction(ctx context.Context, root bool, verb, id string) error {
	if !dockerRefRe.MatchString(id) {
		return errors.New("недопустимый ID")
	}
	ok := false
	for _, a := range ContainerActions {
		ok = ok || a.Verb == verb
	}
	if !ok {
		return fmt.Errorf("неизвестное действие %q", verb)
	}
	_, err := h.docker(ctx, root, verb+" "+id)
	return err
}

// ContainerLogs — последние строки логов контейнера.
func (h *Host) ContainerLogs(ctx context.Context, root bool, id string, tail int) (string, error) {
	if !dockerRefRe.MatchString(id) {
		return "", errors.New("недопустимый ID")
	}
	return h.docker(ctx, root, fmt.Sprintf("logs --timestamps --tail %d %s 2>&1", tail, id))
}

// FollowContainerLogs — docker logs -f.
func (h *Host) FollowContainerLogs(ctx context.Context, root bool, id string, fn func(string)) error {
	if !dockerRefRe.MatchString(id) {
		return errors.New("недопустимый ID")
	}
	return h.Stream(ctx, fmt.Sprintf("docker logs -f --timestamps --tail 200 %s 2>&1", id), root, fn)
}

// Inspect — docker inspect (JSON).
func (h *Host) Inspect(ctx context.Context, root bool, ref string) (string, error) {
	if !dockerRefRe.MatchString(ref) {
		return "", errors.New("недопустимый ID")
	}
	return h.docker(ctx, root, "inspect "+ref)
}

// RemoveImage / RemoveVolume / RemoveNetwork.
func (h *Host) RemoveImage(ctx context.Context, root bool, id string) error {
	return h.dockerRm(ctx, root, "rmi", id)
}

func (h *Host) RemoveVolume(ctx context.Context, root bool, name string) error {
	return h.dockerRm(ctx, root, "volume rm", name)
}

func (h *Host) RemoveNetwork(ctx context.Context, root bool, name string) error {
	return h.dockerRm(ctx, root, "network rm", name)
}

func (h *Host) dockerRm(ctx context.Context, root bool, cmd, ref string) error {
	if !dockerRefRe.MatchString(ref) {
		return errors.New("недопустимое имя")
	}
	_, err := h.docker(ctx, root, cmd+" "+ref)
	return err
}

// PullImage — docker pull, вывод — в fn.
func (h *Host) PullImage(ctx context.Context, root bool, ref string, fn func(string)) error {
	if !dockerRefRe.MatchString(ref) {
		return errors.New("недопустимое имя образа")
	}
	return h.Stream(ctx, "docker pull "+ref+" 2>&1", root, fn)
}

// Prune — docker system prune -f (остановленные контейнеры, висячие образы, сети, кэш).
func (h *Host) Prune(ctx context.Context, root bool, volumes bool) (string, error) {
	args := "system prune -f"
	if volumes {
		args += " --volumes"
	}
	return h.docker(ctx, root, args+" 2>&1")
}

// ExecInContainer — команда внутри контейнера (sh -c).
func (h *Host) ExecInContainer(ctx context.Context, root bool, id, cmd string) (string, error) {
	if !dockerRefRe.MatchString(id) {
		return "", errors.New("недопустимый ID")
	}
	out, err := h.docker(ctx, root, "exec "+id+" sh -c "+Q(cmd)+" 2>&1")
	if err != nil && out != "" {
		return out, nil
	}
	return out, err
}
