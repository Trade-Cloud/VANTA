// Package prefs — состояние VANTA, которое переживает перезапуск.
//
// Хранится в своём JSON-файле, а не в fyne.Preferences: файл читает и правит
// не только VANTA, но и debugger/ (сброс первого запуска при выключенной
// программе), а путь и формат хранилища Fyne — его внутреннее дело.
//
// Файл: <os.UserConfigDir>/vanta/state.json
// (macOS: ~/Library/Application Support/vanta/state.json).
//
// VANTA читает файл один раз при старте, поэтому правка «на лету» на уже
// запущенную программу не действует.
package prefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	dirName  = "vanta"
	fileName = "state.json"
)

// State — всё, что сохраняется. Новые поля добавлять с omitempty,
// отсутствие поля = значение по умолчанию.
type State struct {
	OnboardingDone bool `json:"onboarding_done,omitempty"`

	// Панель «Основа».
	Theme         string   `json:"theme,omitempty"`          // "" (как в ОС), "light", "dark"
	SearchHistory []string `json:"search_history,omitempty"` // последние запросы, новые первыми
}

// Path — путь к файлу состояния.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("prefs: конфиг-папка ОС: %w", err)
	}
	return filepath.Join(dir, dirName, fileName), nil
}

// Load читает состояние. Нет файла — пустое состояние без ошибки.
func Load() (State, error) {
	var s State
	p, err := Path()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("prefs: чтение %s: %w", p, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("prefs: разбор %s: %w", p, err)
	}
	return s, nil
}

// Save пишет состояние атомарно: во временный файл и rename поверх.
func Save(s State) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("prefs: создание папки: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("prefs: запись %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("prefs: замена %s: %w", p, err)
	}
	return nil
}

// Update — Load, правка, Save.
func Update(fn func(*State)) error {
	s, err := Load()
	if err != nil {
		return err
	}
	fn(&s)
	return Save(s)
}

// ResetOnboarding — при следующем запуске VANTA снова откроет окно «Начало».
func ResetOnboarding() error {
	return Update(func(s *State) { s.OnboardingDone = false })
}
