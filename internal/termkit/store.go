package termkit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry — запись книги команд. Больше одной команды — это группа: она
// запускается целиком, последовательно.
type Entry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	Commands    []string `json:"commands"`
	// OnlyIfOK — в группе каждая следующая команда только после успеха предыдущей.
	OnlyIfOK bool `json:"only_if_ok,omitempty"`
}

// IsGroup — несколько команд.
func (e Entry) IsGroup() bool { return len(e.Commands) > 1 }

// Params — параметры {{имя}} в командах записи.
func (e Entry) Params() []string { return Params(strings.Join(e.Commands, "\n")) }

// Snapshot — сохранённый вывод (терминала, команды очереди) или чекпоинт.
type Snapshot struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Server  string    `json:"server"`
	At      time.Time `json:"at"`
	Text    string    `json:"text"`
	Command string    `json:"command,omitempty"`
	Exit    *int      `json:"exit,omitempty"`

	// Чекпоинт: переменные на этот момент и то, что осталось выполнить.
	Checkpoint bool              `json:"checkpoint,omitempty"`
	Vars       map[string]string `json:"vars,omitempty"`
	Remaining  []Step            `json:"remaining,omitempty"`
}

// data — то, что лежит в terminal.json.
type data struct {
	Book []Entry           `json:"book"`
	Vars map[string]string `json:"vars"`
}

// Store — книга команд, переменные и снимки на этом компьютере.
// Файлы: <UserConfigDir>/vanta/terminal.json и snapshots/*.json.
type Store struct {
	mu   sync.Mutex
	dir  string
	d    data
	snap []Snapshot
}

var (
	defaultOnce  sync.Once
	defaultStore *Store
)

// Default — общий Store приложения (загружается один раз).
func Default() *Store {
	defaultOnce.Do(func() {
		base, err := os.UserConfigDir()
		if err != nil {
			base = os.TempDir()
		}
		defaultStore = Load(filepath.Join(base, "vanta"))
	})
	return defaultStore
}

// Load читает данные из dir. Ошибки чтения — пустые данные.
func Load(dir string) *Store {
	s := &Store{dir: dir, d: data{Vars: map[string]string{}}}
	if b, err := os.ReadFile(filepath.Join(dir, "terminal.json")); err == nil {
		json.Unmarshal(b, &s.d)
	}
	if s.d.Vars == nil {
		s.d.Vars = map[string]string{}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "snapshots", "*.json"))
	for _, f := range files {
		var sn Snapshot
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &sn) == nil && sn.ID != "" {
			s.snap = append(s.snap, sn)
		}
	}
	s.sortSnaps()
	return s
}

func (s *Store) sortSnaps() {
	sort.Slice(s.snap, func(i, j int) bool { return s.snap[i].At.After(s.snap[j].At) })
}

func (s *Store) save() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s.d, "", "  ")
	tmp := filepath.Join(s.dir, "terminal.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "terminal.json"))
}

func NewID() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ---------- книга ----------

func (s *Store) Book() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Entry(nil), s.d.Book...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Categories — категории книги по алфавиту.
func (s *Store) Categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range s.Book() {
		if e.Category != "" && !seen[e.Category] {
			seen[e.Category] = true
			out = append(out, e.Category)
		}
	}
	sort.Strings(out)
	return out
}

// SaveEntry добавляет (ID == "") или обновляет запись.
func (s *Store) SaveEntry(e Entry) (Entry, error) {
	e.Name = strings.TrimSpace(e.Name)
	e.Category = strings.TrimSpace(e.Category)
	var cmds []string
	for _, c := range e.Commands {
		if c = strings.TrimSpace(c); c != "" {
			cmds = append(cmds, c)
		}
	}
	e.Commands = cmds
	if e.Name == "" || len(e.Commands) == 0 {
		return e, errors.New("нужны название и хотя бы одна команда")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = NewID()
		s.d.Book = append(s.d.Book, e)
	} else {
		found := false
		for i := range s.d.Book {
			if s.d.Book[i].ID == e.ID {
				s.d.Book[i], found = e, true
			}
		}
		if !found {
			s.d.Book = append(s.d.Book, e)
		}
	}
	return e, s.save()
}

func (s *Store) DeleteEntry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.d.Book[:0]
	for _, e := range s.d.Book {
		if e.ID != id {
			out = append(out, e)
		}
	}
	s.d.Book = out
	return s.save()
}

// ---------- переменные ----------

var varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ValidVar — допустимое имя переменной.
func ValidVar(n string) bool { return varNameRe.MatchString(n) }

func (s *Store) Vars() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.d.Vars))
	for k, v := range s.d.Vars {
		out[k] = v
	}
	return out
}

func (s *Store) SetVar(name, value string) error {
	if !ValidVar(name) {
		return errors.New("имя переменной: латиница, цифры и _, не с цифры")
	}
	if Builtin(name) {
		return fmt.Errorf("%s — встроенная переменная", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Vars[name] = value
	return s.save()
}

func (s *Store) DeleteVar(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.d.Vars, name)
	return s.save()
}

// ---------- снимки ----------

func (s *Store) Snapshots() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Snapshot(nil), s.snap...)
}

func (s *Store) AddSnapshot(sn Snapshot) (Snapshot, error) {
	if sn.ID == "" {
		sn.ID = NewID()
	}
	if sn.At.IsZero() {
		sn.At = time.Now()
	}
	dir := filepath.Join(s.dir, "snapshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return sn, err
	}
	b, _ := json.MarshalIndent(sn, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, sn.ID+".json"), b, 0o600); err != nil {
		return sn, err
	}
	s.mu.Lock()
	s.snap = append(s.snap, sn)
	s.sortSnaps()
	s.mu.Unlock()
	return sn, nil
}

func (s *Store) DeleteSnapshot(id string) error {
	if err := os.Remove(filepath.Join(s.dir, "snapshots", id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.snap[:0]
	for _, sn := range s.snap {
		if sn.ID != id {
			out = append(out, sn)
		}
	}
	s.snap = out
	return nil
}
