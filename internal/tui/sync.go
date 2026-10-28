package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

// Catch-up loader. After a long time away the platforms deliver what you missed in bulk.
// At startup the chat list stays hidden behind a full-screen loader until every app has
// caught up, so it shows up complete instead of filling in under you. Syncs after that
// (a reconnect) leave the screen alone: the top bar spins and the welcome screen turns
// into a progress card.

const (
	settleTime  = 2 * time.Second  // quiet this long after the last app connects: a sync can start right after
	connectWait = 15 * time.Second // an app that can't connect holds up the list this long at most
)

// readyMsg asks for a fresh look at whether startup is over.
type readyMsg struct{}

// checkReady ends startup once every app is connected (or failed to) and none is syncing,
// and has stayed so for settleTime. A sync holds it up as long as it runs; the app drops a
// sync that stops reporting progress. It returns a later check while it waits.
func (m *Model) checkReady(now time.Time) tea.Cmd {
	if m.ready {
		return nil
	}
	if len(m.inbox.Status) == 0 { // nothing linked: nothing to wait for
		m.ready = true
		return nil
	}
	connecting := false
	for _, s := range m.inbox.Status {
		connecting = connecting || s.State == core.StateConnecting
	}
	switch {
	case m.syncing():
		m.settled = time.Time{}
		return nil // the end of the sync brings a refresh
	case connecting && now.Sub(m.started) < connectWait:
		m.settled = time.Time{}
		return m.checkIn(now, m.started.Add(connectWait))
	case m.settled.IsZero():
		m.settled = now
	}
	if now.Sub(m.settled) < settleTime {
		return m.checkIn(now, m.settled.Add(settleTime))
	}
	m.ready = true
	return nil
}

// checkIn schedules a readiness check at the given time, unless one is due by then already.
func (m *Model) checkIn(now, at time.Time) tea.Cmd {
	if !m.checkAt.IsZero() && !m.checkAt.After(at) && m.checkAt.After(now) {
		return nil
	}
	m.checkAt = at
	return tea.Tick(at.Sub(now), func(time.Time) tea.Msg { return readyMsg{} })
}

// viewStarting is the startup loader: every app with how far it is, nothing else.
func (m Model) viewStarting() string {
	title := "Connecting to your apps"
	if m.syncing() {
		title = "Catching up on what you missed"
	}
	head := []string{accentStyle.Bold(true).Render("ttyl"), "", m.spinFrame(lipgloss.NoColor{}) + " " + boldStyle.Render(title), ""}
	rows := m.progressRows(m.width, true)
	tail := []string{"", dimStyle.Render("Your chats show up when every app is done.")}
	block := slices.Concat(head, rows, tail)
	if len(block) > m.bodyH()+1 { // short window: the title and the apps
		block = slices.Concat(head[2:3], rows)
	}
	body := centerBlock(block, m.width, m.height-1)
	footer := spread(" "+fitHints(m.width-3, "enter", "show chats now", "q", "quit"), "", m.width)
	return strings.Join(append(body, cw.Pad(footer, m.width)), "\n")
}

// centerBlock places left-aligned lines as one block in the middle of w×h, each line w wide.
func centerBlock(block []string, w, h int) []string {
	blockW := 0
	for i, l := range block {
		block[i] = cw.Truncate(l, w-2, "…")
		blockW = max(blockW, cw.String(block[i]))
	}
	out := make([]string, 0, h)
	for range max(0, (h-len(block))/2) {
		out = append(out, strings.Repeat(" ", w))
	}
	for _, l := range block {
		out = append(out, cw.Center(cw.Pad(l, blockW), w))
	}
	for len(out) < h {
		out = append(out, strings.Repeat(" ", w))
	}
	return out[:h]
}

// loaderFrames is a one-cell spinner (bubbles' Dot pads each frame with a space).
var loaderFrames = spinner.Spinner{Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, FPS: time.Second / 10}

func (m Model) syncing() bool { return len(m.inbox.Sync) > 0 }

// loading: the spinner has something to show.
func (m Model) loading() bool { return m.syncing() || !m.ready }

// startSpinner animates the loader. The ticks stop by themselves once nothing is loading.
func (m *Model) startSpinner() tea.Cmd {
	if !m.loading() || m.spinning {
		return nil
	}
	m.spinning = true
	return m.spin.Tick
}

// spinFrame is the current spinner frame in the accent color on bg.
func (m Model) spinFrame(bg lipgloss.TerminalColor) string {
	return accentStyle.Background(bg).Render(m.spin.View())
}

// syncPercent is how far a catch-up is, or false while its size is unknown.
func syncPercent(s core.SyncEvent) (int, bool) {
	if s.Total <= 0 {
		return 0, false
	}
	return min(100, max(0, s.Done*100/s.Total)), true
}

// syncTag is a platform in the top bar while it catches up: spinner, tag, percent.
func (m Model) syncTag(id core.PlatformID, s core.SyncEvent, bg lipgloss.TerminalColor) string {
	tag := m.spinFrame(bg) + lipgloss.NewStyle().Foreground(faint).Background(bg).Render(" "+platformTags[id])
	if p, ok := syncPercent(s); ok {
		tag += lipgloss.NewStyle().Foreground(accent).Background(bg).Render(fmt.Sprintf(" %d%%", p))
	}
	return tag
}

// syncLabel says how far along a catch-up is in words: "768 of 1,234 messages".
func syncLabel(s core.SyncEvent) string {
	switch {
	case s.Total <= 0:
		return "loading " + s.Unit + "…"
	case s.Unit == "%":
		return fmt.Sprintf("%d%% of your history", min(s.Done, 100))
	}
	return commas(s.Done) + " of " + commas(s.Total) + " " + s.Unit
}

// commas groups thousands: 12345 -> "12,345".
func commas(n int) string {
	if n < 0 {
		return "-" + commas(-n)
	}
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// progressBar fills w cells in proportion to how far s is.
func progressBar(s core.SyncEvent, w int) string {
	p, _ := syncPercent(s)
	full := p * w / 100
	return accentStyle.Render(strings.Repeat("━", full)) + lipgloss.NewStyle().Foreground(subtle).Render(strings.Repeat("─", w-full))
}

// progressRows is a line per app: its name, a bar and how far it is. With all, every
// linked app is listed (connecting, done, offline); otherwise only the ones syncing.
func (m Model) progressRows(w int, all bool) []string {
	const nameW = 11 // "Instagram" and a gap
	type row struct {
		id    core.PlatformID
		bar   func(int) string
		label string
	}
	empty := func(n int) string { return lipgloss.NewStyle().Foreground(subtle).Render(strings.Repeat("─", n)) }
	var rows []row
	for _, id := range platformOrder {
		st, linked := m.inbox.Status[id]
		s, syncing := m.inbox.Sync[id]
		switch {
		case syncing:
			rows = append(rows, row{id, func(n int) string { return progressBar(s, n) }, dimStyle.Render(syncLabel(s))})
		case !all || !linked:
		case st.State == core.StateConnected:
			full := func(n int) string { return lipgloss.NewStyle().Foreground(green).Render(strings.Repeat("━", n)) }
			rows = append(rows, row{id, full, lipgloss.NewStyle().Foreground(green).Render(iconCheck.sp() + "up to date")})
		case st.State == core.StateConnecting:
			rows = append(rows, row{id, empty, dimStyle.Render("connecting…")})
		case st.State == core.StateLoggedOut:
			rows = append(rows, row{id, empty, lipgloss.NewStyle().Foreground(red).Render("logged out")})
		default:
			rows = append(rows, row{id, empty, lipgloss.NewStyle().Foreground(yellow).Render(cmpOr(st.Detail, "offline"))})
		}
	}
	labelW := 0
	for _, r := range rows {
		labelW = max(labelW, cw.String(r.label))
	}
	barW := min(24, w-4-nameW-2-labelW)
	var out []string
	for _, r := range rows {
		line := lipgloss.NewStyle().Foreground(platformColors[r.id]).Bold(true).Render(cw.Pad(platformNames[r.id], nameW))
		if barW >= 6 { // no bar when it would be a stub
			line += r.bar(barW) + "  "
		}
		out = append(out, line+r.label)
	}
	return out
}

// syncCard replaces the welcome text while catching up after startup: a bar per app syncing.
func (m Model) syncCard(w int) []string {
	block := []string{
		m.spinFrame(lipgloss.NoColor{}) + " " + boldStyle.Render("Catching up on what you missed"),
		"",
	}
	block = append(block, m.progressRows(w, false)...)
	return append(block, "", dimStyle.Render("Chats fill in as they arrive."), dimStyle.Render("You can open any of them meanwhile."))
}
