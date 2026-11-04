package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

// FileInfo — элемент каталога на сервере.
type FileInfo struct {
	Name    string
	Path    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
	Link    string // цель симлинка, "" — не симлинк
	UID     uint32
	GID     uint32
}

// MaxEditSize — файлы больше этого в редакторе не открываем.
const MaxEditSize = 2 << 20

// Home — домашний каталог пользователя подключения.
func (h *Host) Home(ctx context.Context) string {
	c, err := h.SFTP()
	if err == nil {
		if wd, err := c.Getwd(); err == nil && wd != "" {
			return wd
		}
	}
	return "/"
}

// ListDir — содержимое каталога: каталоги первыми, по имени.
func (h *Host) ListDir(ctx context.Context, dir string) ([]FileInfo, error) {
	c, err := h.SFTP()
	if err != nil {
		return nil, err
	}
	entries, err := c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	list := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		fi := FileInfo{Name: e.Name(), Path: path.Join(dir, e.Name()), Size: e.Size(), Mode: e.Mode(), ModTime: e.ModTime(), IsDir: e.IsDir()}
		if st, ok := e.Sys().(*sftp.FileStat); ok {
			fi.UID, fi.GID = st.UID, st.GID
		}
		if e.Mode()&os.ModeSymlink != 0 {
			fi.Link, _ = c.ReadLink(fi.Path)
			if st, err := c.Stat(fi.Path); err == nil && st.IsDir() {
				fi.IsDir = true
			}
		}
		list = append(list, fi)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}

// ReadFile — содержимое файла (до MaxEditSize).
func (h *Host) ReadFile(ctx context.Context, p string) ([]byte, error) {
	c, err := h.SFTP()
	if err != nil {
		return nil, err
	}
	st, err := c.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > MaxEditSize {
		return nil, fmt.Errorf("файл слишком большой для редактора (%s); скачайте его", Bytes(uint64(st.Size())))
	}
	f, err := c.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// WriteFile перезаписывает файл, сохраняя права. Нет прав у пользователя —
// пишет через sudo (tee).
func (h *Host) WriteFile(ctx context.Context, p string, data []byte) error {
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	f, err := c.OpenFile(p, os.O_WRONLY|os.O_TRUNC|os.O_CREATE)
	if err == nil {
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return werr
		}
		return cerr
	}
	if !errors.Is(err, os.ErrPermission) && !strings.Contains(strings.ToLower(err.Error()), "permission") {
		return err
	}
	_, serr := h.SudoInput(ctx, "cat > "+Q(p), data)
	return serr
}

// Mkdir — новый каталог.
func (h *Host) Mkdir(ctx context.Context, p string) error {
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	return c.Mkdir(p)
}

// CreateFile — пустой файл (ошибка, если уже есть).
func (h *Host) CreateFile(ctx context.Context, p string) error {
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	if _, err := c.Lstat(p); err == nil {
		return errors.New("такой файл уже есть")
	}
	f, err := c.Create(p)
	if err != nil {
		return err
	}
	return f.Close()
}

// Rename — переименование/перемещение.
func (h *Host) Rename(ctx context.Context, from, to string) error {
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	return c.Rename(from, to)
}

// Remove — удалить файл или каталог целиком.
func (h *Host) Remove(ctx context.Context, p string, root bool) error {
	clean := path.Clean(p)
	if clean == "/" || clean == "." || clean == "" {
		return errors.New("этот путь удалять нельзя")
	}
	if root {
		_, err := h.Sudo(ctx, "rm -rf -- "+Q(clean))
		return err
	}
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	return c.RemoveAll(clean)
}

// Chmod — права в восьмеричном виде (644, 0755).
func (h *Host) Chmod(ctx context.Context, p string, mode os.FileMode) error {
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	return c.Chmod(p, mode)
}

// Chown — владелец user[:group] (через sudo).
func (h *Host) Chown(ctx context.Context, p, owner string, recursive bool) error {
	flag := ""
	if recursive {
		flag = "-R "
	}
	_, err := h.Sudo(ctx, "chown "+flag+"-- "+Q(owner)+" "+Q(p))
	return err
}

// Download копирует файл сервера в локальный w. progress — байт передано.
func (h *Host) Download(ctx context.Context, remote string, w io.Writer, progress func(int64)) error {
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	f, err := c.Open(remote)
	if err != nil {
		return err
	}
	defer f.Close()
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	_, err = io.Copy(w, &progressReader{r: f, fn: progress})
	return err
}

// Upload пишет локальный r в файл сервера.
func (h *Host) Upload(ctx context.Context, r io.Reader, remote string, progress func(int64)) error {
	c, err := h.SFTP()
	if err != nil {
		return err
	}
	f, err := c.OpenFile(remote, os.O_WRONLY|os.O_TRUNC|os.O_CREATE)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	if _, err := io.Copy(f, &progressReader{r: r, fn: progress}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

type progressReader struct {
	r  io.Reader
	n  int64
	fn func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.fn != nil && n > 0 {
		p.fn(p.n)
	}
	return n, err
}

// Owners — uid → имя пользователя (для колонки «владелец»).
func (h *Host) Owners(ctx context.Context) map[uint32]string {
	out, _ := h.Run(ctx, "getent passwd 2>/dev/null || cat /etc/passwd")
	m := map[uint32]string{}
	for _, l := range lines(out) {
		f := strings.Split(l, ":")
		if len(f) >= 3 {
			var id uint32
			fmt.Sscan(f[2], &id)
			m[id] = f[0]
		}
	}
	return m
}
