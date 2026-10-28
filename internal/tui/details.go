package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mitanshpatelll/ttyl/internal/app"
	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

// The details panel sits next to the open chat (30% of the chat area), like the contact
// info screen in WhatsApp or Instagram: who it is, what was shared and who talks there.
// space then e shows or hides it, and the choice is saved.

const (
	detailsPref    = "details" // "off" when hidden
	minDetailsW    = 28
	maxDetailsW    = 56
	minChatWithPan = 44 // narrower than this, the chat wins and the panel stays hidden
)

type detailsMsg struct {
	chatKey string
	details app.Details
	err     error
}

// detailsW is the panel width as shown: it slides toward targetDetailsW.
func (m Model) detailsW() int {
	if m.anim.on {
		return m.anim.panel.at()
	}
	return m.targetDetailsW()
}

// targetDetailsW is the panel width, 0 when it is hidden or there is no room.
func (m Model) targetDetailsW() int {
	if !m.showDetails {
		return 0
	}
	return m.detailsRoom()
}

// detailsRoom is how wide the panel can be next to the open chat, 0 when there is no room.
func (m Model) detailsRoom() int {
	if m.open == nil {
		return 0
	}
	rw := m.width - m.targetLeftW()
	dw := min(max(minDetailsW, rw*30/100), maxDetailsW)
	if rw-dw < minChatWithPan {
		return 0
	}
	return dw
}

// chatW is the width of the conversation, next to the panel when it shows.
func (m Model) chatW() int { return m.rightW() - m.detailsW() }

func (m Model) loadDetails() tea.Cmd {
	if m.open == nil || m.targetDetailsW() == 0 || m.inbox.Locked() {
		return nil
	}
	a, chat := m.app, *m.open
	return func() tea.Msg {
		d, err := a.Details(chat, time.Now())
		return detailsMsg{chatKey(chat), d, err}
	}
}

// toggleDetails shows or hides the panel and remembers the choice.
func (m *Model) toggleDetails() tea.Cmd {
	if m.open != nil && m.detailsRoom() == 0 { // leave the saved choice alone
		m.note = "window too narrow for details"
		return nil
	}
	m.showDetails = !m.showDetails
	m.layout()
	if m.showDetails && m.open == nil {
		m.note = "details show when a chat is open"
	}
	value := "on"
	if !m.showDetails {
		value = "off"
	}
	a := m.app
	save := func() tea.Msg {
		if err := a.SetPref(detailsPref, value); err != nil {
			return errMsg{err}
		}
		return nil
	}
	return tea.Batch(save, m.loadDetails())
}

// detailsHeading is the panel's part of the heading row.
func (m Model) detailsHeading(w int) string {
	bg := on(sidebarBg)
	return fill(bg(lipgloss.NewStyle()).Render(" ")+bg(dimStyle.Bold(true)).Render("DETAILS"), w, sidebarBg)
}

// detailsLines is the panel body: exactly h lines on the sidebar shade.
func (m Model) detailsLines(w, h int) []string {
	bg := on(sidebarBg)
	c := *m.open
	now := time.Now()
	center := func(s string) string {
		return bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", max(0, (w-cw.String(s))/2))) + s
	}

	// A large avatar, the name, and how to reach them.
	block := lipgloss.NewStyle().Bold(true).Foreground(white).Background(personColor(chatKey(c)))
	lines := []string{"",
		center(block.Render("        ")),
		center(block.Render("   " + cw.Pad(initialsOf(c.Title()), 2) + "   ")),
		center(block.Render("        ")),
		"",
		center(bg(boldStyle).Render(cw.Truncate(c.Title(), w-4, "…"))),
	}
	about := []string{platformIcons[c.Platform].sp() + platformNames[c.Platform]}
	if p := phone(c.ID); p != "" && !c.IsGroup {
		about = append(about, p)
	}
	ready := m.detKey == chatKey(c)
	if c.IsGroup {
		about[0] = iconGroup.sp() + "Group"
		if ready && len(m.det.People) > 0 {
			about = append(about, fmt.Sprintf("%d people", len(m.det.People)+1)) // and you
		}
	}
	lines = append(lines, center(bg(lipgloss.NewStyle().Foreground(platformColors[c.Platform])).Render(cw.Truncate(strings.Join(about, " · "), w-4, "…"))))

	if !ready {
		lines = append(lines, "", center(bg(dimStyle).Render("loading…")))
		return padLines(lines, w, h)
	}

	media, files, links := m.sharedLists(w, now)
	var people []string
	if c.IsGroup {
		for _, p := range m.det.People {
			name := p.Name
			if name == "" {
				name = core.FallbackName(p.ID)
			}
			people = append(people, m.panelRow(avatar(p.ID, name)+bg(lipgloss.NewStyle()).Render(" "), name, fmt.Sprint(p.Messages), w))
		}
	}
	counts := func(kinds ...core.Kind) int {
		n := 0
		for _, k := range kinds {
			n += m.det.Counts[k]
		}
		return n
	}
	sections := []struct {
		title string
		count int
		rows  []string
	}{
		{"MEDIA", counts(core.KindImage, core.KindVideo, core.KindGIF, core.KindAudio, core.KindVoice), media},
		{"LINKS", counts(core.KindLink), links},
		{"FILES", counts(core.KindDocument), files},
		{"PEOPLE", len(people), people},
	}
	var shown []int
	for i, s := range sections {
		if len(s.rows) > 0 {
			shown = append(shown, i)
		}
	}
	if len(shown) == 0 {
		lines = append(lines, "", "", center(bg(dimStyle).Render("Nothing shared yet")))
		return padLines(lines, w, h)
	}
	// Share the height: each section gets a heading and an even part of what is left.
	left := h - len(lines)
	for j, i := range shown {
		s := sections[i]
		rest := len(shown) - j
		room := (left - 2*rest) / rest
		if room < 1 {
			break
		}
		n := min(len(s.rows), room)
		lines = append(lines, "", m.panelHeading(s.title, s.count, w))
		lines = append(lines, s.rows[:n]...)
		left -= 2 + n
	}
	return padLines(lines, w, h)
}

// sharedLists turns the shared messages into panel rows: media, files and links.
func (m Model) sharedLists(w int, now time.Time) (media, files, links []string) {
	bg := on(sidebarBg)
	for _, msg := range m.det.Shared {
		ic := bg(lipgloss.NewStyle().Foreground(accent)).Render(kindIcon(msg).sp())
		row := m.panelRow(ic, sharedLabel(msg), shortTime(msg.Timestamp, now), w)
		switch msg.Kind {
		case core.KindLink:
			links = append(links, row)
		case core.KindDocument:
			files = append(files, row)
		default:
			media = append(media, row)
		}
	}
	return media, files, links
}

// sharedLabel names a shared item: "video 0:32 · beach day", "report.pdf", "github.com/x".
func sharedLabel(msg core.Message) string {
	caption := strings.Join(strings.Fields(msg.Body), " ")
	switch msg.Kind {
	case core.KindLink:
		return shortURL(msg.Meta)
	case core.KindDocument:
		return cmpOr(msg.Meta, caption, "file")
	}
	label := map[core.Kind]string{core.KindImage: "photo", core.KindVideo: "video", core.KindGIF: "gif", core.KindAudio: "audio", core.KindVoice: "voice"}[msg.Kind]
	if msg.Meta != "" {
		label += " " + msg.Meta
	}
	if caption != "" {
		label += " · " + caption
	}
	return label
}

// shortURL drops the parts of a link that don't help recognize it.
func shortURL(u string) string {
	for _, p := range []string{"https://", "http://", "www."} {
		u = strings.TrimPrefix(u, p)
	}
	return strings.TrimSuffix(u, "/")
}

// panelRow: a lead (icon or avatar), a label and something small on the right.
func (m Model) panelRow(lead, label, right string, w int) string {
	bg := on(sidebarBg)
	start := bg(lipgloss.NewStyle()).Render("  ") + lead
	r := bg(dimStyle).Render(right)
	room := w - cw.String(start) - cw.String(r) - 3
	l := start + bg(lipgloss.NewStyle().Foreground(secondary)).Render(cw.Truncate(label, max(1, room), "…"))
	gap := max(1, w-cw.String(l)-cw.String(r)-2)
	return l + bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap)) + r + bg(lipgloss.NewStyle()).Render("  ")
}

func (m Model) panelHeading(title string, count, w int) string {
	return m.panelRow(on(sidebarBg)(dimStyle.Bold(true)).Render(title), "", fmt.Sprint(count), w)
}

// padLines cuts or pads to h lines, each exactly w cells on the sidebar shade.
func padLines(lines []string, w, h int) []string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:h]
	for i := range lines {
		lines[i] = fill(lines[i], w, sidebarBg)
	}
	return lines
}

// cmpOr is the first non-empty value.
func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
