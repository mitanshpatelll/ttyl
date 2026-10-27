package app

import (
	"time"

	"github.com/mitanshpatelll/ttyl/internal/core"
	"github.com/mitanshpatelll/ttyl/internal/store"
)

// detailsLimit caps the shared items and people loaded for the details panel.
const detailsLimit = 60

// Details is what the panel next to a chat shows: what was shared and who talks there.
type Details struct {
	Shared []core.Message    // media, files and links, newest first
	Counts map[core.Kind]int // visible messages by kind
	People []store.Sender    // who wrote here (not you), most active first
}

// Details loads the panel for a chat, with the same delivery rules as the chat itself.
func (a *App) Details(chat core.Chat, now time.Time) (Details, error) {
	cut := a.cutoff(now)
	var d Details
	var err error
	if d.Shared, err = a.store.Shared(chat.Platform, chat.ID, cut, detailsLimit); err != nil {
		return Details{}, err
	}
	if d.Counts, err = a.store.KindCounts(chat.Platform, chat.ID, cut); err != nil {
		return Details{}, err
	}
	if d.People, err = a.store.Senders(chat.Platform, chat.ID, cut, detailsLimit); err != nil {
		return Details{}, err
	}
	return d, nil
}

// Pref reads a saved UI preference ("" when unset or unreadable).
func (a *App) Pref(key string) string {
	v, err := a.store.Pref(key)
	if err != nil {
		a.log.Warn().Err(err).Str("key", key).Msg("read preference")
	}
	return v
}

// SetPref saves a UI preference.
func (a *App) SetPref(key, value string) error { return a.store.SetPref(key, value) }
