package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/mitanshpatelll/ttyl/internal/secret"
)

func TestMigrateFromDMZ(t *testing.T) {
	keyring.MockInit()
	base := t.TempDir()
	oldDir, newDir := filepath.Join(base, "dmz"), filepath.Join(base, "ttyl")
	os.MkdirAll(oldDir, 0o700)
	for _, f := range []string{"dmz.db", "dmz.db-wal", "dmz.log", "whatsapp.db", "config.toml"} {
		os.WriteFile(filepath.Join(oldDir, f), []byte(f), 0o600)
	}
	keyring.Set("dmz", "store-key ("+oldDir+")", "the-key")
	keyring.Set("dmz", "telegram-session ("+oldDir+")", "session")

	moved, err := migrateDir(oldDir, newDir)
	if err != nil || !moved {
		t.Fatalf("moved %v, err %v", moved, err)
	}
	for _, f := range []string{"ttyl.db", "ttyl.db-wal", "ttyl.log", "whatsapp.db", "config.toml"} {
		if _, err := os.Stat(filepath.Join(newDir, f)); err != nil {
			t.Errorf("%s missing after move", f)
		}
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("old folder still there")
	}
	v := secret.New(newDir)
	if key, ok, _ := v.Get("store-key"); !ok || key != "the-key" {
		t.Errorf("store key = %q, %v", key, ok)
	}
	if s, ok, _ := v.Get("telegram-session"); !ok || s != "session" {
		t.Errorf("telegram session = %q, %v", s, ok)
	}
	if _, err := keyring.Get("dmz", "store-key ("+oldDir+")"); err != keyring.ErrNotFound {
		t.Error("old Keychain item not removed")
	}

	if moved, err := migrateDir(oldDir, newDir); moved || err != nil {
		t.Errorf("second run: moved %v, err %v (must be a no-op)", moved, err)
	}
}
