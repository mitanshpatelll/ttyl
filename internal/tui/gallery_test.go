package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/ttyl/internal/app"
	"github.com/mitanshpatelll/ttyl/internal/core"
	"github.com/mitanshpatelll/ttyl/internal/rules"
	"github.com/mitanshpatelll/ttyl/internal/store"
)

// TestGallery prints every main screen for a visual check: TTYL_GALLERY=1 go test -run Gallery -v ./internal/tui
func TestGallery(t *testing.T) {
	if os.Getenv("TTYL_GALLERY") == "" {
		t.Skip("set TTYL_GALLERY=1 to print the screens")
	}
	if mode := os.Getenv("TTYL_GALLERY"); mode == "dark" || mode == "light" { // keep colors for screenshots
		lipgloss.SetColorProfile(termenv.TrueColor)
		lipgloss.SetHasDarkBackground(mode == "dark")
	}
	sizes := [][2]int{{170, 44}, {110, 32}, {80, 24}, {60, 20}, {40, 12}, {120, 14}, {220, 56}}
	if only := os.Getenv("TTYL_GALLERY_SIZE"); only != "" { // e.g. 60x20
		var w, h int
		fmt.Sscanf(only, "%dx%d", &w, &h)
		sizes = [][2]int{{w, h}}
	}
	for _, size := range sizes {
		gallery(t, size[0], size[1])
	}
}

// galleryApp is a store with a few chats that are hard to draw: long names, Gujarati, emoji,
// groups, a reel, a voice note; two platforms online and a 30 minute budget.
func galleryApp(t *testing.T) (*app.App, *onlinePlatform, *onlinePlatform) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ago := func(min int) time.Time { return now.Add(-time.Duration(min) * time.Minute) }
	wa, tg := core.WhatsApp, core.Telegram
	msg := func(p core.PlatformID, chat, id, sender, name string, fromMe bool, at time.Time, body string) core.Message {
		return core.Message{Platform: p, ChatID: chat, ID: id, SenderID: sender, SenderName: name, FromMe: fromMe, Timestamp: at, Kind: core.KindText, Body: body}
	}
	voice := msg(wa, "mom", "m4", "mom", "Mom", false, ago(20), "")
	voice.Kind, voice.Meta = core.KindVoice, "0:12"
	reel := msg(wa, "trip", "t3", "ann", "Ann", false, ago(50), "lol look https://instagram.com/reel/abc")
	reel.Kind, reel.Meta = core.KindLink, "https://instagram.com/reel/abc"
	st.Apply(core.BatchEvent{Platform: wa,
		Chats: []core.Chat{{Platform: wa, ID: "mom", Name: "Mom"}, {Platform: wa, ID: "trip", Name: "Weekend trip", IsGroup: true}},
		Messages: []core.Message{
			msg(wa, "mom", "m1", "mom", "Mom", false, ago(60*26), "Did you reach home safely?"),
			msg(wa, "mom", "m2", "me", "", true, ago(60*26-2), "Yes! Just got in, all good."),
			msg(wa, "mom", "m3", "me", "", true, ago(30), "What are you cooking tonight?"),
			voice,
			msg(wa, "mom", "m5", "mom", "Mom", false, ago(19), "Also, call your grandmother this weekend, she has been asking about you all week and wants to hear about the new job."),
			msg(wa, "trip", "t1", "raj", "Raj", false, ago(70), "Saturday 9am at the station?"),
			msg(wa, "trip", "t2", "me", "", true, ago(65), "Works for me"),
			reel,
			msg(wa, "trip", "t4", "raj", "Raj", false, ago(49), "haha"),
		},
		ReadUpTo: map[string]time.Time{"mom": ago(25), "trip": ago(60)},
		Receipts: []core.Receipt{{ChatID: "mom", IDs: []string{"m2"}, Status: core.Seen}, {ChatID: "mom", IDs: []string{"m3"}, Status: core.Delivered},
			{ChatID: "trip", IDs: []string{"t2"}, Status: core.Seen}},
	})
	st.Apply(core.BatchEvent{Platform: wa,
		Chats: []core.Chat{
			{Platform: wa, ID: "jay", Name: "જય વિર જય જોગમાયા", IsGroup: true},
			{Platform: wa, ID: "sem", Name: "📚 Semester 7 - B.E. Computer Engg", IsGroup: true},
			{Platform: wa, ID: "919825011420@s.whatsapp.net", Name: "Papa"},
		},
		Messages: []core.Message{
			msg(wa, "jay", "j1", "rk", "Rakesh Patel", false, ago(60*30), "જય માતાજી 🙏 કાલે સવારે આરતી છે"),
			msg(wa, "sem", "s1", "ap", "Er. Arpita A. Prajapati", false, ago(60*3), "📢 **તમામ વિદ્યાર્થીઓ** submit the assignment by Friday 🇮🇳"),
			msg(wa, "919825011420@s.whatsapp.net", "p1", "me", "", true, ago(60*5), "Su karvu che 👨‍👩‍👧 ☺️ 👍🏽"),
		},
		ReadUpTo: map[string]time.Time{"jay": now, "sem": now, "919825011420@s.whatsapp.net": now},
	})
	st.Apply(core.BatchEvent{Platform: tg, Chats: []core.Chat{{Platform: tg, ID: "u1", Name: "Dev chat", IsGroup: true}},
		Messages: []core.Message{msg(tg, "u1", "1", "u2", "Sam", false, ago(90), "deploy is green ✅")}, ReadUpTo: map[string]time.Time{"u1": now}})

	r, _ := rules.New(nil)
	wa1, tg1 := &onlinePlatform{id: wa, events: make(chan core.Event, 1)}, &onlinePlatform{id: tg, events: make(chan core.Event, 1)}
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop(), DailyLimit: 30 * time.Minute}, wa1, tg1)
	for _, p := range []*onlinePlatform{wa1, tg1} {
		p.events <- core.StatusEvent{Platform: p.id, State: core.StateConnected}
	}
	a.Drain() // both show as online
	t.Cleanup(func() { st.Close() })
	return a, wa1, tg1
}

func gallery(t *testing.T, width, height int) {
	a, wa1, tg1 := galleryApp(t)
	wa, tg := core.WhatsApp, core.Telegram
	{
		size := [2]int{width, height}
		m := readyNew(a)
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = step(t, m, RefreshMsg{})
		shot := func(label string) {
			checkFits(t, m, label)
			fmt.Printf("\n===== %s %dx%d =====\n%s\n", label, size[0], size[1], m.View())
		}
		shot("inbox, no chat open")
		m = step(t, m, key("enter"))
		shot("1:1 chat with new-messages line")
		m.notes.toast = &toast{id: 1, chat: m.inbox.Chats[len(m.inbox.Chats)-1], more: 1}
		shot("toast for another chat")
		m.notes.toast = nil
		m = step(t, m, key("esc"))
		m = step(t, m, key("j"))
		m = step(t, m, key("enter"))
		m = step(t, m, key("i"))
		for _, r := range "see you all!" {
			m = step(t, m, key(string(r)))
		}
		shot("group chat, typing")
		for _, r := range " :la" {
			m = step(t, m, key(string(r)))
		}
		shot("typing an emoji")
		m = step(t, m, key("esc"))
		m = step(t, m, key("esc"))
		m = step(t, m, key("esc"))
		m = step(t, m, key("/"))
		shot("search, just opened")
		for _, r := range "we" {
			m = step(t, m, key(string(r)))
		}
		shot("search, typing")
		m = step(t, m, key("esc"))
		m = step(t, m, key("?"))
		shot("help")

		wa1.events <- core.SyncEvent{Platform: wa, Active: true, Done: 768, Total: 1234, Unit: "messages"}
		tg1.events <- core.SyncEvent{Platform: tg, Active: true, Done: 3, Total: 40, Unit: "chats"}
		a.Drain()
		m = New(a)
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = step(t, m, RefreshMsg{})
		shot("catching up after a long break")
	}
	_ = strings.TrimSpace
}

// onlinePlatform is a fake that reports itself connected, for good-looking screenshots.
type onlinePlatform struct {
	fakePlatform
	id     core.PlatformID
	events chan core.Event
}

func (p *onlinePlatform) ID() core.PlatformID       { return p.id }
func (p *onlinePlatform) Events() <-chan core.Event { return p.events }

// TestMotionFrames prints every animation frame of the list and the details panel sliding
// in, for a GIF: TTYL_GALLERY_MOTION=dark go test -run MotionFrames -v ./internal/tui
func TestMotionFrames(t *testing.T) {
	if os.Getenv("TTYL_GALLERY_MOTION") == "" {
		t.Skip("set TTYL_GALLERY_MOTION=dark to print the frames")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	useAnimations = true
	defer func() { useAnimations = false }()
	a, _, _ := galleryApp(t)
	m := step(t, New(a), tea.WindowSizeMsg{Width: 110, Height: 28})
	m = step(t, m, RefreshMsg{})
	n := 0
	shot := func() {
		fmt.Printf("\n===== frame %03d =====\n%s\n", n, m.View())
		n++
	}
	run := func() {
		for m.anim.ticking {
			next, _ := m.Update(frameMsg{})
			m = next.(Model)
			shot()
		}
		for range 8 { // hold the end a moment
			shot()
		}
	}
	for range 8 {
		shot()
	}
	m = step(t, m, key("enter")) // past the loader
	shot()
	run()
	m = step(t, m, key("enter")) // open Mom
	shot()
	run()
	m = step(t, m, key("G"))
	m = step(t, m, key("g")) // scroll to the top and back
	shot()
	run()
	m = step(t, m, key("/"))
	shot()
	run()
}
