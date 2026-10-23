// Package app wires platforms to the store and applies focus rules for the UI.
package app

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/rules"
	"github.com/mitanshpatelll/dmz/internal/store"
)

const historyLimit = 300

type App struct {
	store        *store.Store
	rules        rules.Rules
	readReceipts bool
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
}

func New(st *store.Store, r rules.Rules, readReceipts bool, log zerolog.Logger, platforms ...core.Platform) *App {
	a := &App{
		store:        st,
		rules:        r,
		readReceipts: readReceipts,
		platforms:    map[core.PlatformID]core.Platform{},
		log:          log,
		status:       map[core.PlatformID]core.StatusEvent{},
		sync:         map[core.PlatformID]int{},
		changed:      make(chan struct{}, 1),
	}
	for _, p := range platforms {
		a.platforms[p.ID()] = p
		a.status[p.ID()] = core.StatusEvent{Platform: p.ID(), State: core.StateConnecting}
	}
	return a
}

// Changed fires (coalesced) whenever platform events changed what the UI should show.
func (a *App) Changed() <-chan struct{} { return a.changed }

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

// Open loads a chat's visible history and marks it read locally. Read receipts are
// only sent to the platform when enabled in config.
func (a *App) Open(ctx context.Context, chat core.Chat, now time.Time) ([]core.Message, error) {
	cut := a.cutoff(now)
	if a.readReceipts {
		unread, err := a.store.Unread(chat.Platform, chat.ID, cut)
		if err != nil {
			return nil, err
		}
		if len(unread) > 0 {
			if err := a.platforms[chat.Platform].MarkRead(ctx, chat, unread); err != nil {
				a.log.Warn().Err(err).Str("chat", chat.ID).Msg("send read receipts")
			}
		}
	}
	msgs, err := a.store.Messages(chat.Platform, chat.ID, cut, historyLimit)
	if err != nil {
		return nil, err
	}
	if len(msgs) > 0 {
		if err := a.store.MarkRead(chat.Platform, chat.ID, msgs[len(msgs)-1].Timestamp); err != nil {
			return nil, err
		}
	}
	return msgs, nil
}

func (a *App) Send(ctx context.Context, chat core.Chat, text string) error {
	msg, err := a.platforms[chat.Platform].Send(ctx, chat.ID, text)
	if err != nil {
		return err
	}
	return a.store.Apply(core.BatchEvent{Platform: chat.Platform, Messages: []core.Message{msg}})
}
