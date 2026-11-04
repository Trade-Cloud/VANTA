package remote

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Секреты (пароль сервера или passphrase ключа) — только в системной связке
// ключей: Keychain на macOS, Credential Manager на Windows, Secret Service
// на Linux. Одна запись на сервер, аккаунт — ID сервера.
const keyringService = "org.trade-cloud.vanta"

func getSecret(serverID string) (string, bool, error) {
	v, err := keyring.Get(keyringService, serverID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("связка ключей: %w", err)
	}
	return v, true, nil
}

func setSecret(serverID, v string) error {
	if err := keyring.Set(keyringService, serverID, v); err != nil {
		return fmt.Errorf("связка ключей: %w", err)
	}
	return nil
}

func deleteSecret(serverID string) error {
	err := keyring.Delete(keyringService, serverID)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("связка ключей: %w", err)
}
