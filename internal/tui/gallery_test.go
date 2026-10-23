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

	"github.com/mitanshpatelll/dmz/internal/app"
	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/rules"
	"github.com/mitanshpatelll/dmz/internal/store"
)

// TestGallery prints every main screen for a visual check: DMZ_GALLERY=1 go test -run Gallery -v ./internal/tui
func TestGallery(t *testing.T) {
	if os.Getenv("DMZ_GALLERY") == "" {
		t.Skip("set DMZ_GALLERY=1 to print the screens")
	}
	if mode := os.Getenv("DMZ_GALLERY"); mode == "dark" || mode == "light" { // keep colors for screenshots
		lipgloss.SetColorProfile(termenv.TrueColor)
		lipgloss.SetHasDarkBackground(mode == "dark")
	}
	for _, size := range [][2]int{{110, 32}, {80, 24}} {
		gallery(t, size[0], size[1])
	}
}

func gallery(t *testing.T, width, height int) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
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
	})
	st.Apply(core.BatchEvent{Platform: tg, Chats: []core.Chat{{Platform: tg, ID: "u1", Name: "Dev chat", IsGroup: true}},
		Messages: []core.Message{msg(tg, "u1", "1", "u2", "Sam", false, ago(90), "deploy is green ✅")}, ReadUpTo: map[string]time.Time{"u1": now}})

	r, _ := rules.New(nil)
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop(), DailyLimit: 30 * time.Minute}, &fakePlatform{})
	{
		size := [2]int{width, height}
		m := New(a)
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = step(t, m, RefreshMsg{})
		shot := func(label string) {
			checkFits(t, m, label)
			fmt.Printf("\n===== %s %dx%d =====\n%s\n", label, size[0], size[1], m.View())
		}
		shot("inbox, no chat open")
		m = step(t, m, key("enter"))
		shot("1:1 chat with new-messages line")
		m = step(t, m, key("esc"))
		m = step(t, m, key("j"))
		m = step(t, m, key("enter"))
		m = step(t, m, key("i"))
		for _, r := range "see you all!" {
			m = step(t, m, key(string(r)))
		}
		shot("group chat, typing")
		m = step(t, m, key("esc"))
		m = step(t, m, key("esc"))
		m = step(t, m, key("/"))
		for _, r := range "we" {
			m = step(t, m, key(string(r)))
		}
		shot("search")
		m = step(t, m, key("esc"))
		m = step(t, m, key("?"))
		shot("help")
	}
	_ = strings.TrimSpace
}
