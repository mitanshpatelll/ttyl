// Package app wires platforms to the store and applies focus rules for the UI.
package app

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/rules"
	"github.com/mitanshpatelll/dmz/internal/store"
)

// HistoryPage is how many messages a chat loads at a time.
const HistoryPage = 300

// ExtensionStep is how much one "extend" adds to today's time budget.
const ExtensionStep = 5 * time.Minute

type Options struct {
	Store        *store.Store
	Rules        rules.Rules
	ReadReceipts bool
	MediaDir     string        // where opened attachments are saved
	DailyLimit   time.Duration // time budget per day; 0 = no budget
	Log          zerolog.Logger
	// Reveal hands a saved attachment to the OS; nil uses the default app (or Finder for risky types).
	Reveal func(path string) error
}

type App struct {
	store        *store.Store
	rules        rules.Rules
	readReceipts bool
	mediaDir     string
	dailyLimit   time.Duration
	reveal       func(path string) error
	platforms    map[core.PlatformID]core.Platform
	log          zerolog.Logger

	mu     sync.Mutex
	status map[core.PlatformID]core.StatusEvent
	sync   map[core.PlatformID]int

	changed chan struct{}
}

// Inbox is everything the UI needs for one refresh.
type Inbox struct {
	Chats    []core.ChatSummary
	Held     int       // incoming messages waiting for the next delivery window
	Next     time.Time // next delivery window; zero when delivery is live
	Status   map[core.PlatformID]core.StatusEvent
	Sync     map[core.PlatformID]int
	Receipts bool
	Used     time.Duration // dmz time today
	Limit    time.Duration // today's budget including extensions; 0 = no budget
}

// Locked reports whether today's time budget is used up.
func (in Inbox) Locked() bool { return in.Limit > 0 && in.Used >= in.Limit }

func New(o Options, platforms ...core.Platform) *App {
	a := &App{
		store:        o.Store,
		rules:        o.Rules,
		readReceipts: o.ReadReceipts,
		mediaDir:     o.MediaDir,
		dailyLimit:   o.DailyLimit,
		reveal:       o.Reveal,
		platforms:    map[core.PlatformID]core.Platform{},
		log:          o.Log,
		status:       map[core.PlatformID]core.StatusEvent{},
		sync:         map[core.PlatformID]int{},
		changed:      make(chan struct{}, 1),
	}
	if a.reveal == nil {
		a.reveal = reveal
	}
	for _, p := range platforms {
		a.platforms[p.ID()] = p
		a.status[p.ID()] = core.StatusEvent{Platform: p.ID(), State: core.StateConnecting}
	}
	return a
}

// Changed fires (coalesced) whenever platform events changed what the UI should show.
func (a *App) Changed() <-chan struct{} { return a.changed }

// Drain applies events still buffered in the platforms' channels. Call it after the platforms
// are disconnected and Run has returned, so nothing that was received is lost on quit.
func (a *App) Drain() {
	for _, p := range a.platforms {
		for {
			select {
			case evt := <-p.Events():
				a.handle(evt)
				continue
			default:
			}
			break
		}
	}
}

// Run consumes events from every platform until ctx ends.
func (a *App) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range a.platforms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case evt := <-p.Events():
					a.handle(evt)
				}
			}
		}()
	}
	wg.Wait()
}

func (a *App) handle(evt core.Event) {
	switch v := evt.(type) {
	case core.BatchEvent:
		if err := a.store.Apply(v); err != nil {
			a.log.Error().Err(err).Msg("store batch")
			return
		}
	case core.StatusEvent:
		a.mu.Lock()
		a.status[v.Platform] = v
		a.mu.Unlock()
	case core.SyncEvent:
		a.mu.Lock()
		a.sync[v.Platform] = v.Progress
		a.mu.Unlock()
	}
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// cutoff is the newest timestamp (unix ms) of incoming messages that may be shown now.
func (a *App) cutoff(now time.Time) int64 {
	if a.rules.Live() {
		return math.MaxInt64
	}
	return a.rules.Cutoff(now).UnixMilli()
}

func (a *App) Inbox(now time.Time) (Inbox, error) {
	chats, held, err := a.store.Summaries(a.cutoff(now))
	if err != nil {
		return Inbox{}, err
	}
	in := Inbox{Chats: chats, Held: held, Receipts: a.readReceipts, Status: map[core.PlatformID]core.StatusEvent{}, Sync: map[core.PlatformID]int{}}
	if !a.rules.Live() {
		in.Next = a.rules.Next(now)
	}
	used, extensions, err := a.store.UsageToday(now)
	if err != nil {
		return Inbox{}, err
	}
	in.Used = used
	if a.dailyLimit > 0 {
		in.Limit = a.dailyLimit + time.Duration(extensions)*ExtensionStep
	}
	a.mu.Lock()
	for k, v := range a.status {
		in.Status[k] = v
	}
	for k, v := range a.sync {
		in.Sync[k] = v
	}
	a.mu.Unlock()
	return in, nil
}

// Open loads the newest limit visible messages of a chat and marks it read locally. It also
// returns the read marker from before, so the UI can show where the new messages start.
// Read receipts are only sent to the platform when enabled in config.
func (a *App) Open(ctx context.Context, chat core.Chat, now time.Time, limit int) ([]core.Message, time.Time, error) {
	cut := a.cutoff(now)
	lastRead, err := a.store.LastRead(chat.Platform, chat.ID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if p, linked := a.platforms[chat.Platform]; a.readReceipts && linked {
		unread, err := a.store.Unread(chat.Platform, chat.ID, cut)
		if err != nil {
			return nil, time.Time{}, err
		}
		if len(unread) > 0 {
			if err := p.MarkRead(ctx, chat, unread); err != nil {
				a.log.Warn().Err(err).Str("chat", chat.ID).Msg("send read receipts")
			}
		}
	}
	msgs, err := a.store.Messages(chat.Platform, chat.ID, cut, limit)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(msgs) > 0 {
		if err := a.store.MarkRead(chat.Platform, chat.ID, msgs[len(msgs)-1].Timestamp); err != nil {
			return nil, time.Time{}, err
		}
	}
	return msgs, lastRead, nil
}

func (a *App) Send(ctx context.Context, chat core.Chat, text string) error {
	p, linked := a.platforms[chat.Platform]
	if !linked { // chats stay in the store after dmz logout without --purge
		return fmt.Errorf("%s is not linked: dmz login %s", chat.Platform, chat.Platform)
	}
	msg, err := p.Send(ctx, chat.ID, text)
	if err != nil {
		return err
	}
	if err := a.store.AddUsage(time.Now(), 0, 0, 1, 0); err != nil {
		a.log.Warn().Err(err).Msg("count sent message")
	}
	return a.store.Apply(core.BatchEvent{Platform: chat.Platform, Messages: []core.Message{msg}})
}

// RecordActive adds time spent with dmz in front of you.
func (a *App) RecordActive(now time.Time, d time.Duration) error {
	return a.store.AddUsage(now, d, 0, 0, 0)
}

// RecordOpen counts one launch of dmz.
func (a *App) RecordOpen(now time.Time) error { return a.store.AddUsage(now, 0, 1, 0, 0) }

// Extend adds ExtensionStep to today's budget. Extensions show up in dmz stats.
func (a *App) Extend(now time.Time) error { return a.store.AddUsage(now, 0, 0, 0, 1) }
