package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Trade-Cloud/vanta/internal/agent"
)

// Список серверов — свой файл рядом с state.json:
// <os.UserConfigDir>/vanta/servers.json
// (macOS: ~/Library/Application Support/vanta/servers.json).
//
// Отдельно от prefs, потому что state.json правит ещё и debugger, а здесь
// лежит то, что терять нельзя. Секретов в файле нет: пароли и passphrase —
// в системной связке ключей (secret.go).
const serversFile = "servers.json"

type serversDoc struct {
	Version int            `json:"version"`
	Servers []agent.Server `json:"servers"`
}

func serversPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("конфиг-папка ОС: %w", err)
	}
	return filepath.Join(dir, "vanta", serversFile), nil
}

// loadServers читает список. Нет файла — пустой список без ошибки.
func loadServers() ([]agent.Server, error) {
	p, err := serversPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", p, err)
	}
	var doc serversDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("разбор %s: %w", p, err)
	}
	return doc.Servers, nil
}

// saveServers пишет список атомарно: временный файл и rename поверх.
// Права 0600: в файле адреса и логины, читать его остальным незачем.
func saveServers(list []agent.Server) error {
	p, err := serversPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("создание папки: %w", err)
	}
	if list == nil {
		list = []agent.Server{}
	}
	data, err := json.MarshalIndent(serversDoc{Version: 1, Servers: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("запись %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("замена %s: %w", p, err)
	}
	return nil
}
