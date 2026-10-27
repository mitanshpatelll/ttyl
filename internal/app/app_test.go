package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/ttyl/internal/core"
	"github.com/mitanshpatelll/ttyl/internal/rules"
	"github.com/mitanshpatelll/ttyl/internal/store"
)

func TestSyncShowsOnlyLiveCatchUps(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r, _ := rules.New(nil)
	a := New(Options{Store: st, Rules: r, Log: zerolog.Nop()}, idlePlatform{events: make(chan core.Event)})
	sync := func(now time.Time) map[core.PlatformID]core.SyncEvent {
		t.Helper()
		in, err := a.Inbox(now)
		if err != nil {
			t.Fatal(err)
		}
		return in.Sync
	}

	running := core.SyncEvent{Platform: core.WhatsApp, Active: true, Done: 300, Total: 1200, Unit: "messages"}
	a.handle(running)
	if got := sync(time.Now()); got[core.WhatsApp] != running {
		t.Errorf("running catch-up = %+v, want %+v", got, running)
	}
	if got := sync(time.Now().Add(SyncTimeout + time.Second)); len(got) != 0 {
		t.Errorf("catch-up that stopped reporting still shown: %+v", got)
	}
	a.handle(core.StatusEvent{Platform: core.WhatsApp, State: core.StateDisconnected})
	if got := sync(time.Now()); len(got) != 0 {
		t.Errorf("catch-up shown while disconnected: %+v", got)
	}
	a.handle(core.StatusEvent{Platform: core.WhatsApp, State: core.StateConnected})
	a.handle(core.SyncEvent{Platform: core.WhatsApp})
	if got := sync(time.Now()); len(got) != 0 {
		t.Errorf("finished catch-up still shown: %+v", got)
	}
}
