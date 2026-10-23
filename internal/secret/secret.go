// Package secret keeps small secrets (the message store key, platform sessions) in the OS keychain.
package secret

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const service = "ttyl"

// Vault scopes secrets to one data directory, so a test or second profile never shares keys.
type Vault struct{ dir string }

func New(dataDir string) Vault { return Vault{dir: dataDir} }

func (v Vault) account(name string) string { return fmt.Sprintf("%s (%s)", name, v.dir) }

// Get returns the secret stored under name; ok is false if there is none.
func (v Vault) Get(name string) (value string, ok bool, err error) {
	value, err = keyring.Get(service, v.account(name))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", false, nil
	}
	return value, err == nil, err
}

func (v Vault) Set(name, value string) error { return keyring.Set(service, v.account(name), value) }

func (v Vault) Delete(name string) error {
	err := keyring.Delete(service, v.account(name))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// CopyFrom copies secrets kept under an older service name and data directory into this
// vault, keeping any value already here. The old items stay until DeleteFrom.
func (v Vault) CopyFrom(oldService, oldDir string, names ...string) error {
	for _, name := range names {
		value, err := keyring.Get(oldService, fmt.Sprintf("%s (%s)", name, oldDir))
		if errors.Is(err, keyring.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, ok, err := v.Get(name); err != nil {
			return err
		} else if !ok {
			if err := v.Set(name, value); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}

// DeleteFrom removes secrets kept under an older service name and data directory.
func DeleteFrom(oldService, oldDir string, names ...string) error {
	for _, name := range names {
		err := keyring.Delete(oldService, fmt.Sprintf("%s (%s)", name, oldDir))
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// StoreKey returns the 32-byte message store key, creating it on first use, and where it lives.
// Only on platforms without a keychain does it fall back to a 0600 key file next to the data,
// which protects copies of ttyl.db made without the key file, and nothing more.
func (v Vault) StoreKey() (key []byte, where string, err error) {
	encoded, ok, err := v.Get("store-key")
	switch {
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return v.fileKey()
	case err != nil:
		return nil, "", fmt.Errorf("read store key from keychain: %w", err)
	case !ok:
		key = make([]byte, 32)
		rand.Read(key)
		if err := v.Set("store-key", base64.StdEncoding.EncodeToString(key)); err != nil {
			return nil, "", fmt.Errorf("save store key to keychain: %w", err)
		}
		return key, "keychain", nil
	}
	key, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, "", errors.New("store key in keychain is corrupt")
	}
	return key, "keychain", nil
}

func (v Vault) fileKey() ([]byte, string, error) {
	path := filepath.Join(v.dir, "store.key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key)
		err = os.WriteFile(path, key, 0o600)
	}
	if err != nil {
		return nil, "", err
	}
	if len(key) != 32 {
		return nil, "", fmt.Errorf("%s is corrupt", path)
	}
	return key, path, nil
}
