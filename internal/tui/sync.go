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
// While any of them reports progress the top bar spins, the welcome screen turns into a
// progress card and an empty chat list says it is syncing.

// loaderFrames is a one-cell spinner (bubbles' Dot pads each frame with a space).
var loaderFrames = spinner.Spinner{Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, FPS: time.Second / 10}

func (m Model) syncing() bool { return len(m.inbox.Sync) > 0 }

// startSpinner animates the loader. The ticks stop by themselves once nothing is syncing.
func (m *Model) startSpinner() tea.Cmd {
	if !m.syncing() || m.spinning {
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

// syncCard replaces the welcome text while catching up: one progress bar per platform.
func (m Model) syncCard(w int) []string {
	ids := make([]core.PlatformID, 0, len(m.inbox.Sync))
	labelW := 0
	for id, s := range m.inbox.Sync {
		ids = append(ids, id)
		labelW = max(labelW, cw.String(syncLabel(s)))
	}
	slices.Sort(ids)
	const nameW = 11 // "Instagram" and a gap
	barW := min(24, max(6, w-4-nameW-2-labelW))
	block := []string{
		m.spinFrame(lipgloss.NoColor{}) + " " + boldStyle.Render("Catching up on what you missed"),
		"",
	}
	for _, id := range ids {
		s := m.inbox.Sync[id]
		name := lipgloss.NewStyle().Foreground(platformColors[id]).Bold(true).Render(cw.Pad(platformNames[id], nameW))
		block = append(block, name+progressBar(s, barW)+"  "+dimStyle.Render(syncLabel(s)))
	}
	return append(block, "", dimStyle.Render("Chats fill in as they arrive."), dimStyle.Render("You can open any of them meanwhile."))
}
