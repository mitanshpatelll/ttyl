// Command dmz is a distraction-free terminal client for your DMs.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/dmz/internal/app"
	"github.com/mitanshpatelll/dmz/internal/config"
	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/platform/whatsapp"
	"github.com/mitanshpatelll/dmz/internal/rules"
	"github.com/mitanshpatelll/dmz/internal/secret"
	"github.com/mitanshpatelll/dmz/internal/store"
	"github.com/mitanshpatelll/dmz/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dmz:", err)
		os.Exit(1)
	}
}

func run() error {
	// Session keys and message text: every file dmz or SQLite creates (incl. -wal/-shm) is owner-only.
	syscall.Umask(0o077)

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	r, err := rules.New(cfg.Focus.Windows)
	if err != nil {
		return err
	}

	// The TUI owns stdout, so every log line goes to a file.
	logFile, err := os.OpenFile(filepath.Join(dir, "dmz.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	level := zerolog.InfoLevel
	if os.Getenv("DMZ_DEBUG") != "" {
		level = zerolog.DebugLevel
	}
	log := zerolog.New(logFile).Level(level).With().Timestamp().Logger()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	key, keyWhere, err := secret.New(dir).StoreKey()
	if err != nil {
		return fmt.Errorf("%w (is the login keychain locked?)", err)
	}
	log.Info().Str("key", keyWhere).Msg("message store encrypted at rest")
	st, err := store.Open(filepath.Join(dir, "dmz.db"), key)
	if err != nil {
		return err
	}
	defer st.Close()

	wa, err := whatsapp.New(ctx, filepath.Join(dir, "whatsapp.db"), log)
	if err != nil {
		return err
	}
	defer wa.Disconnect()
	if err := os.Chmod(filepath.Join(dir, "whatsapp.db"), 0o600); err != nil {
		return err
	}

	var vip []string
	for _, phone := range cfg.Focus.VIP {
		vip = append(vip, whatsapp.PhoneToChatID(phone))
	}
	if err := st.SetVIP(core.WhatsApp, vip); err != nil {
		return err
	}

	mediaDir, err := config.MediaDir()
	if err != nil {
		return err
	}
	a := app.New(app.Options{Store: st, Rules: r, ReadReceipts: cfg.Focus.ReadReceipts, MediaDir: mediaDir, Log: log}, wa)
	a.CleanMedia()
	// Start consuming events before login: history sync begins right after pairing.
	go a.Run(ctx)

	if wa.LoggedIn() {
		if err := wa.Connect(ctx); err != nil {
			return err
		}
	} else if err := wa.Login(ctx, os.Stdout); err != nil {
		return err
	}

	p := tea.NewProgram(tui.New(a), tea.WithAltScreen(), tea.WithContext(ctx))
	go func() {
		for range a.Changed() {
			p.Send(tui.RefreshMsg{})
		}
	}()
	_, err = p.Run()
	interrupted := ctx.Err() != nil
	cancel() // stop event handling before the deferred disconnect and store close
	if err != nil && !interrupted {
		return err
	}
	return nil
}
