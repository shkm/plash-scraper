package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "plash-scraper"

// Store keeps refresh tokens in the OS keychain, keyed by account ID. Keychains
// can't list their entries, so accounts are also kept in a plain JSON file.
type Store struct {
	path string
}

func DefaultStore() (Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Store{}, err
	}
	return Store{path: filepath.Join(dir, "plash-scraper", "accounts.json")}, nil
}

func (s Store) Accounts() ([]Account, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var accounts []Account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return accounts, nil
}

// Add saves the account, replacing the token and online ID if it already exists.
func (s Store) Add(a Account, npsso string) error {
	if err := keyring.Set(keyringService, a.ID, npsso); err != nil {
		return fmt.Errorf("save token to keychain: %w", err)
	}
	accounts, err := s.Accounts()
	if err != nil {
		return err
	}
	accounts = slices.DeleteFunc(accounts, func(b Account) bool { return b.ID == a.ID })
	return s.save(append(accounts, a))
}

func (s Store) Find(onlineID string) (Account, error) {
	accounts, err := s.Accounts()
	if err != nil {
		return Account{}, err
	}
	i := slices.IndexFunc(accounts, func(a Account) bool { return strings.EqualFold(a.OnlineID, onlineID) })
	if i < 0 {
		return Account{}, fmt.Errorf("no account named %q", onlineID)
	}
	return accounts[i], nil
}

func (s Store) Remove(onlineID string) error {
	a, err := s.Find(onlineID)
	if err != nil {
		return err
	}
	if err := keyring.Delete(keyringService, a.ID); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete token from keychain: %w", err)
	}
	accounts, err := s.Accounts()
	if err != nil {
		return err
	}
	return s.save(slices.DeleteFunc(accounts, func(b Account) bool { return b.ID == a.ID }))
}

func (s Store) Token(a Account) (string, error) {
	token, err := keyring.Get(keyringService, a.ID)
	if err != nil {
		return "", fmt.Errorf("read token from keychain: %w", err)
	}
	return token, nil
}

func (s Store) save(accounts []Account) error {
	slices.SortFunc(accounts, func(a, b Account) int {
		return strings.Compare(strings.ToLower(a.OnlineID), strings.ToLower(b.OnlineID))
	})
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
