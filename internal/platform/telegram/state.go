package telegram

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// peer is what ttyl remembers about a Telegram user, group or channel. Access hashes are needed
// to address users and channels; they are useless without the session, which lives in the keychain.
type peer struct {
	Hash      int64  `json:"h,omitempty"`
	Name      string `json:"n,omitempty"`
	Group     bool   `json:"g,omitempty"`
	Broadcast bool   `json:"b,omitempty"` // channel posts: a feed, never shown
}

// state is kept in telegram.json next to the other data.
type state struct {
	path string

	mu    sync.Mutex
	Peers map[string]peer `json:"peers"`
	Seen  map[string]int  `json:"seen"` // newest message ID fetched per chat, for catch-up
	dirty bool
}

func loadState(path string) (*state, error) {
	s := &state{path: path, Peers: map[string]peer{}, Seen: map[string]int{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Peers == nil {
		s.Peers = map[string]peer{}
	}
	if s.Seen == nil {
		s.Seen = map[string]int{}
	}
	return s, nil
}

func (s *state) peer(id string) peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Peers[id]
}

// learn merges what an update says about a peer. A zero hash never replaces a known one
// (Telegram sends "min" objects without usable hashes).
func (s *state) learn(id string, p peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.Peers[id]
	if p.Hash == 0 {
		p.Hash = old.Hash
	}
	if p.Name == "" {
		p.Name = old.Name
	}
	if !ok || old != p {
		s.Peers[id] = p
		s.dirty = true
	}
}

func (s *state) seen(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Seen[id]
}

func (s *state) markSeen(id string, msgID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msgID > s.Seen[id] {
		s.Seen[id] = msgID
		s.dirty = true
	}
}

func (s *state) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
