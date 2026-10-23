package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"golang.org/x/term"

	"github.com/mitanshpatelll/dmz/internal/config"
	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/platform"
	"github.com/mitanshpatelll/dmz/internal/platform/telegram"
	"github.com/mitanshpatelll/dmz/internal/platform/whatsapp"
	"github.com/mitanshpatelll/dmz/internal/secret"
	"github.com/mitanshpatelll/dmz/internal/store"
)

// env is what every command shares: data dir, config, logger, keychain vault and the message store.
type env struct {
	dir   string
	cfg   config.Config
	log   zerolog.Logger
	vault secret.Vault
	store *store.Store

	logFile *os.File

	// unavailable explains why a platform has no adapter (not configured, not built yet).
	unavailable map[core.PlatformID]string
}

func setup() (*env, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	// The TUI owns stdout, so every log line goes to a file.
	logFile, err := os.OpenFile(filepath.Join(dir, "dmz.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	level := zerolog.InfoLevel
	if os.Getenv("DMZ_DEBUG") != "" {
		level = zerolog.DebugLevel
	}
	e := &env{dir: dir, cfg: cfg, vault: secret.New(dir), logFile: logFile,
		log: zerolog.New(logFile).Level(level).With().Timestamp().Logger()}

	key, where, err := e.vault.StoreKey()
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("%w (is the login keychain locked?)", err)
	}
	e.log.Debug().Str("key", where).Msg("message store encrypted at rest")
	if e.store, err = store.Open(filepath.Join(dir, "dmz.db"), key); err != nil {
		logFile.Close()
		return nil, err
	}
	return e, nil
}

func (e *env) Close() {
	e.store.Close()
	e.logFile.Close()
}

// accounts opens the adapter of every platform that is linked, configured, or about to be linked.
func (e *env) accounts(ctx context.Context, login core.PlatformID) ([]platform.Account, error) {
	wa, err := whatsapp.New(ctx, filepath.Join(e.dir, "whatsapp.db"), e.log)
	if err != nil {
		return nil, err
	}
	var vip []string
	for _, phone := range e.cfg.Focus.VIP {
		vip = append(vip, whatsapp.PhoneToChatID(phone))
	}
	if err := e.store.SetVIP(core.WhatsApp, vip); err != nil {
		return nil, err
	}
	accounts := []platform.Account{wa}
	e.unavailable = map[core.PlatformID]string{core.Instagram: "not supported yet"}

	if tc := e.cfg.Telegram; tc.Configured() {
		tgClient, err := telegram.New(ctx, telegram.Config{
			AppID: tc.APIID, AppHash: tc.APIHash, Dir: e.dir, Vault: e.vault, Version: version, Log: e.log,
		})
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, tgClient)
	} else {
		e.unavailable[core.Telegram] = "add api_id and api_hash from https://my.telegram.org to [telegram] in " +
			filepath.Join(e.dir, "config.toml")
	}
	return accounts, nil
}

// terminal reads prompts from stdin; hidden prompts don't echo.
type terminal struct {
	in  *bufio.Reader
	out io.Writer
}

func newTerminal() terminal { return terminal{in: bufio.NewReader(os.Stdin), out: os.Stdout} }

func (t terminal) Write(p []byte) (int, error) { return t.out.Write(p) }

func (t terminal) Prompt(label string, hidden bool) (string, error) {
	fmt.Fprint(t.out, label)
	if fd := int(os.Stdin.Fd()); hidden && term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(t.out)
		return strings.TrimSpace(string(b)), err
	}
	line, err := t.in.ReadString('\n')
	if err == io.EOF && line != "" {
		err = nil
	}
	return strings.TrimSpace(line), err
}
