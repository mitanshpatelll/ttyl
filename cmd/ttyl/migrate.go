package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitanshpatelll/ttyl/internal/secret"
)

// secretNames are the Keychain items a data directory can have.
var secretNames = []string{"store-key", "telegram-session", "instagram-cookies"}

// migrateFromDMZ moves data from before the rename (dmz → ttyl) once: the data folder and
// the files in it, the media cache, and the Keychain items, so linked accounts and the
// encrypted messages keep working.
func migrateFromDMZ() (bool, error) {
	if os.Getenv("TTYL_HOME") != "" {
		return false, nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return false, err
	}
	moved, err := migrateDir(filepath.Join(config, "dmz"), filepath.Join(config, "ttyl"))
	if moved {
		if cache, err := os.UserCacheDir(); err == nil {
			os.Rename(filepath.Join(cache, "dmz"), filepath.Join(cache, "ttyl")) // only cached attachments
		}
	}
	return moved, err
}

// migrateDir copies the Keychain items first and deletes the old ones last, so a failure in
// between leaves everything usable under one of the two names.
func migrateDir(oldDir, newDir string) (bool, error) {
	if _, err := os.Stat(newDir); err == nil {
		return false, nil // already moved
	}
	if _, err := os.Stat(oldDir); err != nil {
		return false, nil // nothing from dmz
	}
	if err := secret.New(newDir).CopyFrom("dmz", oldDir, secretNames...); err != nil {
		return false, fmt.Errorf("copy Keychain items: %w", err)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return false, err
	}
	for _, f := range [][2]string{{"dmz.db", "ttyl.db"}, {"dmz.db-wal", "ttyl.db-wal"}, {"dmz.db-shm", "ttyl.db-shm"}, {"dmz.log", "ttyl.log"}} {
		if err := os.Rename(filepath.Join(newDir, f[0]), filepath.Join(newDir, f[1])); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, err
		}
	}
	if err := secret.DeleteFrom("dmz", oldDir, secretNames...); err != nil {
		return true, fmt.Errorf("remove old Keychain items: %w", err)
	}
	return true, nil
}
