// Command dmz is a distraction-free terminal client for your DMs.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitanshpatelll/dmz/internal/app"
	"github.com/mitanshpatelll/dmz/internal/config"
	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/platform"
	"github.com/mitanshpatelll/dmz/internal/rules"
	"github.com/mitanshpatelll/dmz/internal/store"
	"github.com/mitanshpatelll/dmz/internal/tui"
)

// version is set at build time: -ldflags "-X main.version=v1.0.0".
var version = "dev"

const usage = `dmz: your DMs in the terminal, without the feed

Usage:
  dmz                              open your chats
  dmz login <platform>             link an account, then open your chats
  dmz logout <platform> [--purge]  unlink an account; --purge also deletes its stored messages
  dmz stats [days]                 time spent and messages per day (default 7)
  dmz version

Platforms: whatsapp (wa), telegram (tg), instagram (ig)
Data and config: ~/Library/Application Support/dmz (or $DMZ_HOME)
`

func main() {
	// Session keys and message text: every file dmz or SQLite creates (incl. -wal/-shm) is owner-only.
	syscall.Umask(0o077)

	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "":
		err = runChats("")
	case "login":
		err = runLogin(args)
	case "logout":
		err = runLogout(args)
	case "stats":
		err = runStats(args)
	case "version", "--version", "-v":
		fmt.Println("dmz", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "dmz: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "dmz:", err)
		os.Exit(1)
	}
}

func parsePlatform(args []string) (core.PlatformID, error) {
	if len(args) == 0 {
		return "", errors.New("which platform? whatsapp, telegram or instagram")
	}
	switch strings.ToLower(args[0]) {
	case "whatsapp", "wa":
		return core.WhatsApp, nil
	case "telegram", "tg":
		return core.Telegram, nil
	case "instagram", "ig":
		return core.Instagram, nil
	}
	return "", fmt.Errorf("unknown platform %q: use whatsapp, telegram or instagram", args[0])
}

// runChats opens the TUI with every linked account. If login is set, that account is linked first.
func runChats(login core.PlatformID) error {
	e, err := setup()
	if err != nil {
		return err
	}
	defer e.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	accounts, err := e.accounts(ctx, login)
	if err != nil {
		return err
	}
	if login == "" && !anyLinked(accounts) {
		fmt.Println("No accounts linked yet, starting with WhatsApp. (dmz login telegram / instagram for the others.)")
		login = core.WhatsApp
	}
	var linked []platform.Account
	for _, acct := range accounts {
		if acct.LoggedIn() || acct.ID() == login {
			linked = append(linked, acct)
		}
	}
	if login != "" && !slices.ContainsFunc(linked, func(a platform.Account) bool { return a.ID() == login }) {
		return fmt.Errorf("%s is not available: %s", login, e.unavailable[login])
	}
	platforms := make([]core.Platform, len(linked))
	for i, acct := range linked {
		platforms[i] = acct
	}

	r, err := rules.New(e.cfg.Focus.Windows)
	if err != nil {
		return err
	}
	mediaDir, err := config.MediaDir()
	if err != nil {
		return err
	}
	a := app.New(app.Options{
		Store:        e.store,
		Rules:        r,
		ReadReceipts: e.cfg.Focus.ReadReceipts,
		MediaDir:     mediaDir,
		DailyLimit:   time.Duration(e.cfg.Focus.DailyMinutes) * time.Minute,
		Log:          e.log,
	}, platforms...)
	a.CleanMedia()
	// Consume events before logging in: WhatsApp starts its history sync right after pairing.
	runDone := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(runDone)
	}()
	// Shutdown order matters: stop the platforms (no new events), stop the event loop,
	// then apply what is still buffered, all before the deferred store close.
	stop := sync.OnceFunc(func() {
		for _, acct := range linked {
			acct.Disconnect()
		}
		cancel()
		<-runDone
		a.Drain()
	})
	defer stop()

	for _, acct := range linked {
		if acct.ID() == login && !acct.LoggedIn() {
			if err := acct.Login(ctx, newTerminal()); err != nil {
				return fmt.Errorf("%s login: %w", login, err)
			}
			continue
		}
		if err := acct.Connect(ctx); err != nil {
			e.log.Error().Err(err).Str("platform", string(acct.ID())).Msg("connect")
		}
	}

	if err := a.RecordOpen(time.Now()); err != nil {
		e.log.Warn().Err(err).Msg("record open")
	}
	p := tea.NewProgram(tui.New(a), tea.WithAltScreen(), tea.WithReportFocus(), tea.WithContext(ctx))
	go func() {
		for range a.Changed() {
			p.Send(tui.RefreshMsg{})
		}
	}()
	final, err := p.Run()
	interrupted := ctx.Err() != nil
	if m, ok := final.(tui.Model); ok {
		if err := m.FlushUsage(); err != nil {
			e.log.Warn().Err(err).Msg("record usage")
		}
	}
	stop()
	if err != nil && !interrupted {
		return err
	}
	return nil
}

func anyLinked(accounts []platform.Account) bool {
	for _, a := range accounts {
		if a.LoggedIn() {
			return true
		}
	}
	return false
}

func runLogin(args []string) error {
	id, err := parsePlatform(args)
	if err != nil {
		return err
	}
	return runChats(id)
}

func runLogout(args []string) error {
	id, err := parsePlatform(args)
	if err != nil {
		return err
	}
	purge := len(args) > 1 && args[1] == "--purge"
	e, err := setup()
	if err != nil {
		return err
	}
	defer e.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	accounts, err := e.accounts(ctx, id)
	if err != nil {
		return err
	}
	for _, acct := range accounts {
		if acct.ID() != id {
			continue
		}
		if !acct.LoggedIn() {
			fmt.Println(id, "is not linked.")
		} else if err := acct.Logout(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "dmz:", err)
		} else {
			fmt.Println(id, "unlinked and local session deleted.")
		}
	}
	if purge {
		if err := e.store.DeletePlatform(id); err != nil {
			return err
		}
		fmt.Println("Stored", id, "messages deleted.")
	}
	return nil
}

func runStats(args []string) error {
	days := 7
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > 366 {
			return fmt.Errorf("days must be 1 to 366, got %q", args[0])
		}
		days = n
	}
	e, err := setup()
	if err != nil {
		return err
	}
	defer e.Close()
	usage, err := e.store.UsageDays(time.Now(), days)
	if err != nil {
		return err
	}

	fmt.Printf("dmz, last %d days\n\n", days)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "day\ttime\topens\tsent\treceived\textended\t")
	var total store.Usage
	for _, u := range usage {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t\n", u.Day.Format("Mon 02 Jan"), clock(u.Active), u.Opens, u.Sent, u.Received, u.Extensions)
		total.Active += u.Active
		total.Opens += u.Opens
		total.Sent += u.Sent
		total.Received += u.Received
		total.Extensions += u.Extensions
	}
	fmt.Fprintf(w, "total\t%s\t%d\t%d\t%d\t%d\t\n", clock(total.Active), total.Opens, total.Sent, total.Received, total.Extensions)
	w.Flush()
	fmt.Printf("\naverage %s a day", clock(total.Active/time.Duration(days)))
	if m := e.cfg.Focus.DailyMinutes; m > 0 {
		fmt.Printf(", budget %d min a day", m)
	}
	fmt.Println()
	return nil
}

// clock formats a duration as h:mm.
func clock(d time.Duration) string {
	return fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60)
}
