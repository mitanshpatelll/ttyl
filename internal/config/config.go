// Package config loads dmz settings and resolves where its data lives.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Focus    Focus    `toml:"focus"`
	Telegram Telegram `toml:"telegram"`
}

// Telegram needs your own API app: https://my.telegram.org → API development tools.
type Telegram struct {
	APIID   int    `toml:"api_id"`
	APIHash string `toml:"api_hash"`
}

func (t Telegram) Configured() bool { return t.APIID != 0 && t.APIHash != "" }

type Focus struct {
	ReadReceipts bool     `toml:"read_receipts"`
	Windows      []string `toml:"windows"`
	VIP          []string `toml:"vip"`
	DailyMinutes int      `toml:"daily_minutes"`
}

const defaultConfig = `# dmz config

[focus]
# Send "seen" (blue ticks) when you open a chat in dmz.
read_receipts = false

# Delivery windows (24h, local time). Messages arriving between windows are held
# and shown at the next window. Empty = live delivery.
# Example: windows = ["12:30", "19:00"]
windows = []

# Phone numbers with country code that bypass delivery windows.
# Example: vip = ["+15551234567"]
vip = []

# Daily time budget in minutes (time with dmz focused). 0 = no budget.
# When it runs out dmz locks; typing "extend" gives 5 more minutes.
daily_minutes = 0

[telegram]
# Telegram needs your own API app (free): https://my.telegram.org → API development tools.
# Then run: dmz login telegram
api_id = 0
api_hash = ""
`

// Dir returns the data directory ($DMZ_HOME, or ~/Library/Application Support/dmz on macOS),
// creating it with owner-only permissions. It holds the WhatsApp session, so keep it private.
func Dir() (string, error) {
	dir := os.Getenv("DMZ_HOME")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "dmz")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

// MediaDir is where opened attachments are cached: $DMZ_HOME/media, or ~/Library/Caches/dmz/media.
func MediaDir() (string, error) {
	if home := os.Getenv("DMZ_HOME"); home != "" {
		return filepath.Join(home, "media"), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "dmz", "media"), nil
}

// Load reads config.toml from dir, writing the default file on first run.
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(defaultConfig), 0o600); err != nil {
			return Config{}, err
		}
	}
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
