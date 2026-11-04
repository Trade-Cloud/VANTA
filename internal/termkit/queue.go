package termkit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Step — команда очереди.
type Step struct {
	Command string `json:"command"`
	// Group — из какой группы книги пришла команда ("" — сама по себе).
	Group string `json:"group,omitempty"`
	// OnlyIfOK — запускать, только если предыдущая команда успешна.
	OnlyIfOK bool `json:"only_if_ok,omitempty"`
	// Checkpoint — после успеха сохранить вывод, переменные и остаток очереди.
	Checkpoint bool `json:"checkpoint,omitempty"`
	// SaveAs — сохранить вывод (обрезанный) в переменную VANTA.
	SaveAs string `json:"save_as,omitempty"`
}

// State — где команда очереди.
type State int

const (
	Pending State = iota
	Running
	Done
	Failed
	Skipped
	Stopped
)

func (s State) Label() string {
	switch s {
	case Running:
		return "выполняется"
	case Done:
		return "успешно"
	case Failed:
		return "ошибка"
	case Skipped:
		return "пропущена"
	case Stopped:
		return "остановлена"
	}
	return "в очереди"
}

// Item — команда очереди и её результат.
type Item struct {
	ID     string
	Step   Step
	State  State
	Result *Result
	Ran    string // команда после подстановки переменных
	Err    string
}

// Queue — очередь команд. Безопасна для разных горутин; OnChange
// вызывается из горутины выполнения.
type Queue struct {
	mu       sync.Mutex
	items    []*Item
	running  bool
	cancel   context.CancelFunc
	OnChange func()
}

func (q *Queue) changed() {
	if q.OnChange != nil {
		q.OnChange()
	}
}

// Items — копия очереди.
func (q *Queue) Items() []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Item, len(q.items))
	for i, it := range q.items {
		out[i] = *it
	}
	return out
}

func (q *Queue) Running() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running
}

// Add — в конец очереди.
func (q *Queue) Add(steps ...Step) {
	q.mu.Lock()
	for _, s := range steps {
		if strings.TrimSpace(s.Command) != "" {
			q.items = append(q.items, &Item{ID: NewID(), Step: s})
		}
	}
	q.mu.Unlock()
	q.changed()
}

func (q *Queue) index(id string) int {
	for i, it := range q.items {
		if it.ID == id {
			return i
		}
	}
	return -1
}

// Update меняет команду (пока очередь не идёт).
func (q *Queue) Update(id string, s Step) error {
	q.mu.Lock()
	defer q.changed()
	defer q.mu.Unlock()
	if q.running {
		return errors.New("очередь выполняется")
	}
	if i := q.index(id); i >= 0 {
		q.items[i].Step = s
		q.items[i].State, q.items[i].Result, q.items[i].Err = Pending, nil, ""
	}
	return nil
}

// Move сдвигает команду на delta позиций.
func (q *Queue) Move(id string, delta int) {
	q.mu.Lock()
	defer q.changed()
	defer q.mu.Unlock()
	if q.running {
		return
	}
	i := q.index(id)
	j := i + delta
	if i < 0 || j < 0 || j >= len(q.items) {
		return
	}
	q.items[i], q.items[j] = q.items[j], q.items[i]
}

func (q *Queue) Remove(id string) {
	q.mu.Lock()
	defer q.changed()
	defer q.mu.Unlock()
	if q.running {
		return
	}
	if i := q.index(id); i >= 0 {
		q.items = append(q.items[:i], q.items[i+1:]...)
	}
}

func (q *Queue) Clear() {
	q.mu.Lock()
	defer q.changed()
	defer q.mu.Unlock()
	if !q.running {
		q.items = nil
	}
}

// Steps — команды очереди (для сохранения группой).
func (q *Queue) Steps() []Step {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Step, len(q.items))
	for i, it := range q.items {
		out[i] = it.Step
	}
	return out
}

// Stop останавливает выполнение (текущей команде — Ctrl+C).
func (q *Queue) Stop() {
	q.mu.Lock()
	c := q.cancel
	q.mu.Unlock()
	if c != nil {
		c()
	}
}

// Summary — итог прогона.
type Summary struct {
	OK, Failed, Skipped int
	Stopped             bool
	Checkpoints         int
}

func (s Summary) String() string {
	t := fmt.Sprintf("успешно %d, с ошибкой %d, пропущено %d", s.OK, s.Failed, s.Skipped)
	if s.Checkpoints > 0 {
		t += fmt.Sprintf(", чекпоинтов %d", s.Checkpoints)
	}
	if s.Stopped {
		t += " · остановлено"
	}
	return t
}

// Run выполняет очередь в сессии сверху вниз. Уже выполненные (успешно)
// команды пропускает — так «Запустить» после ошибки продолжает с неё.
func (q *Queue) Run(ctx context.Context, sess *Session, st *Store, env Env) (Summary, error) {
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return Summary{}, errors.New("очередь уже выполняется")
	}
	if !sess.Hooked() {
		q.mu.Unlock()
		return Summary{}, ErrNoHook
	}
	ctx, cancel := context.WithCancel(ctx)
	q.running, q.cancel = true, cancel
	items := append([]*Item(nil), q.items...)
	for _, it := range items {
		if it.State != Done {
			it.State, it.Result, it.Err, it.Ran = Pending, nil, "", ""
		}
	}
	q.mu.Unlock()
	q.changed()
	defer func() {
		q.mu.Lock()
		q.running, q.cancel = false, nil
		q.mu.Unlock()
		cancel()
		q.changed()
	}()

	var sum Summary
	prevOK := true
	for i, it := range items {
		q.mu.Lock()
		state := it.State
		q.mu.Unlock()
		if state == Done {
			prevOK = true
			continue
		}
		if ctx.Err() != nil {
			sum.Stopped = true
			q.set(it, Stopped, nil, "", "")
			continue
		}
		if it.Step.OnlyIfOK && !prevOK {
			sum.Skipped++
			q.set(it, Skipped, nil, "", "предыдущая команда не выполнилась")
			continue
		}
		env.Vars = st.Vars()
		cmd, err := env.Expand(it.Step.Command)
		if err != nil {
			sum.Failed++
			prevOK = false
			q.set(it, Failed, nil, "", err.Error())
			continue
		}
		q.set(it, Running, nil, cmd, "")
		res, err := sess.Exec(ctx, cmd)
		if err != nil {
			if ctx.Err() != nil {
				sum.Stopped = true
				q.set(it, Stopped, nil, cmd, "остановлено")
				continue
			}
			q.set(it, Failed, nil, cmd, err.Error())
			return sum, err
		}
		out := strings.TrimSpace(cleanOutput(res.Output))
		env.LastExit, env.LastOutput = strconv.Itoa(res.Exit), out
		if res.Exit != 0 {
			sum.Failed++
			prevOK = false
			q.set(it, Failed, &res, cmd, fmt.Sprintf("код выхода %d", res.Exit))
			continue
		}
		sum.OK++
		prevOK = true
		q.set(it, Done, &res, cmd, "")
		if it.Step.SaveAs != "" {
			if err := st.SetVar(it.Step.SaveAs, out); err != nil {
				q.set(it, Done, &res, cmd, "не сохранили в переменную: "+err.Error())
			}
		}
		if it.Step.Checkpoint {
			var rest []Step
			for _, r := range items[i+1:] {
				rest = append(rest, r.Step)
			}
			code := res.Exit
			if _, err := st.AddSnapshot(Snapshot{Title: "Чекпоинт: " + short(cmd, 60), Server: env.Server,
				Text: out, Command: cmd, Exit: &code, Checkpoint: true, Vars: st.Vars(), Remaining: rest}); err == nil {
				sum.Checkpoints++
			}
		}
	}
	return sum, nil
}

func (q *Queue) set(it *Item, s State, r *Result, ran, errText string) {
	q.mu.Lock()
	it.State = s
	if r != nil {
		it.Result = r
	}
	if ran != "" {
		it.Ran = ran
	}
	it.Err = errText
	q.mu.Unlock()
	q.changed()
}

// Restore — очередь из чекпоинта: оставшиеся команды, переменные возвращаются в Store.
func (q *Queue) Restore(st *Store, sn Snapshot) error {
	if !sn.Checkpoint {
		return errors.New("это не чекпоинт")
	}
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return errors.New("очередь выполняется")
	}
	q.items = nil
	for _, s := range sn.Remaining {
		q.items = append(q.items, &Item{ID: NewID(), Step: s})
	}
	q.mu.Unlock()
	for k, v := range sn.Vars {
		if !Builtin(k) {
			st.SetVar(k, v)
		}
	}
	q.changed()
	return nil
}

func short(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}

// cleanOutput — вывод без ANSI-последовательностей и \r.
func cleanOutput(s string) string { return StripANSI(s) }

// StripANSI убирает escape-последовательности (CSI, OSC) и \r.
func StripANSI(s string) string {
	if !strings.ContainsAny(s, "\x1b\r") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\r':
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']':
			j := i + 2
			for j < len(s) && s[j] != '\a' && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j
		case c == 0x1b && i+1 < len(s):
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
