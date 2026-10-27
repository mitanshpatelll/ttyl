package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

var platformTags = map[core.PlatformID]string{
	core.WhatsApp:  "WA",
	core.Telegram:  "TG",
	core.Instagram: "IG",
}

var platformColors = map[core.PlatformID]lipgloss.AdaptiveColor{
	core.WhatsApp:  {Light: "#1A7F37", Dark: "#3FB950"},
	core.Telegram:  {Light: "#0969DA", Dark: "#58A6FF"},
	core.Instagram: {Light: "#BF3989", Dark: "#F778BA"},
}

// platformOrder is the order of the sidebar sections.
var platformOrder = []core.PlatformID{core.WhatsApp, core.Instagram, core.Telegram}

func platformRank(p core.PlatformID) int {
	if i := slices.Index(platformOrder, p); i >= 0 {
		return i
	}
	return len(platformOrder)
}

// searchPrefixes narrow a search to one platform: "w: mom", "ig: sam".
var searchPrefixes = map[string]core.PlatformID{
	"w": core.WhatsApp, "wa": core.WhatsApp, "whatsapp": core.WhatsApp,
	"i": core.Instagram, "ig": core.Instagram, "insta": core.Instagram, "instagram": core.Instagram,
	"t": core.Telegram, "tg": core.Telegram, "telegram": core.Telegram,
}

// searchQuery splits a search into an optional platform and the lowercased name to look for.
func searchQuery(v string) (core.PlatformID, string) {
	q := strings.TrimSpace(v)
	if prefix, rest, ok := strings.Cut(q, ":"); ok {
		if p, ok := searchPrefixes[strings.ToLower(strings.TrimSpace(prefix))]; ok {
			return p, strings.ToLower(strings.TrimSpace(rest))
		}
	}
	return "", strings.ToLower(q)
}

func (m Model) searching() bool { return strings.TrimSpace(m.filter.Value()) != "" }

func platformTag(p core.PlatformID) string {
	return lipgloss.NewStyle().Foreground(platformColors[p]).Render(platformIcons[p].sp() + platformTags[p])
}

// avatar is a colored block with initials, the way a chat app shows a profile picture.
func avatar(id, name string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(white).Background(personColor(id)).Render(" " + cw.Pad(initialsOf(name), 2) + " ")
}

// inboxHeader is the sidebar heading under the top bar, or the search box while searching.
func (m Model) inboxHeader(w int) string {
	bg := on(sidebarBg)
	pos := ""
	if len(m.visible) > 0 {
		pos = bg(dimStyle).Render(fmt.Sprintf("%d/%d ", m.cursor+1, len(m.visible)))
	}
	var left string
	if m.filterShown() {
		left = bg(lipgloss.NewStyle()).Render(" ") + bg(accentStyle).Render(iconSearch.sp()) + m.filter.View()
	} else {
		style := bg(dimStyle.Bold(true))
		if m.focus == focusInbox {
			style = style.Foreground(accent)
		}
		label := "UNREAD"
		if m.showAll {
			label = "ALL CHATS"
		}
		left = bg(lipgloss.NewStyle()).Render(" ") + style.Render(label)
	}
	gap := max(1, w-cw.String(left)-cw.String(pos))
	return fill(left+bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap))+pos, w, sidebarBg)
}

// The sidebar is a list of sections, one per platform: a heading, then its chats, or a note
// when it has none to show.
type sideKind int

const (
	sideHeading sideKind = iota
	sideChat
	sideNote
)

type sideItem struct {
	kind     sideKind
	platform core.PlatformID
	chat     int // index into m.visible
}

// height in lines: a heading has a blank line on each side, a chat its two lines and a spacer.
func (it sideItem) height() int {
	if it.kind == sideNote {
		return 2
	}
	return 3
}

// sections are the platforms with a heading: every linked one (so "all caught up" shows),
// or while searching only those with matches.
func (m Model) sections() []core.PlatformID {
	has := map[core.PlatformID]bool{}
	for _, c := range m.visible {
		has[c.Platform] = true
	}
	if !m.searching() {
		for p := range m.inbox.Status {
			has[p] = true
		}
	}
	var out []core.PlatformID
	for _, p := range platformOrder {
		if has[p] {
			out = append(out, p)
		}
	}
	return out
}

func (m Model) sideItems() []sideItem {
	var items []sideItem
	for _, p := range m.sections() {
		items = append(items, sideItem{kind: sideHeading, platform: p})
		n := 0
		for i, c := range m.visible {
			if c.Platform == p {
				items = append(items, sideItem{kind: sideChat, platform: p, chat: i})
				n++
			}
		}
		if n == 0 {
			items = append(items, sideItem{kind: sideNote, platform: p})
		}
	}
	return items
}

// inboxLines is the sidebar body: exactly h lines, all on the sidebar shade.
func (m Model) inboxLines(w, h int) []string {
	bg := on(sidebarBg)
	var lines []string
	switch {
	case len(m.inbox.Chats) == 0 && !m.searching():
		msg := []string{bg(boldStyle).Render("Waiting for your chats"), "", bg(dimStyle).Render("The first sync after linking"), bg(dimStyle).Render("can take a few minutes.")}
		if m.syncing() {
			msg = []string{m.spinFrame(sidebarBg) + bg(boldStyle).Render(" Syncing your chats"), "", bg(dimStyle).Render("Hang tight, this can take"), bg(dimStyle).Render("a few minutes.")}
		}
		lines = centerLines(msg, w, h, sidebarBg)
	case len(m.visible) == 0 && m.searching():
		lines = centerLines([]string{bg(dimStyle).Render("No chat matches “" + strings.TrimSpace(m.filter.Value()) + "”")}, w, h, sidebarBg)
	default:
		y := 0
		for _, it := range m.sideItems() {
			if y >= m.offset+h {
				break
			}
			if y+it.height() > m.offset {
				for i, l := range m.sideItem(it, w) {
					if y+i >= m.offset && y+i < m.offset+h {
						lines = append(lines, l)
					}
				}
			}
			y += it.height()
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = fill(lines[i], w, sidebarBg)
	}
	return lines[:h]
}

// centerLines puts a few lines in the middle of an area, each centered on its own.
func centerLines(msg []string, w, h int, shade lipgloss.TerminalColor) []string {
	var lines []string
	for range (h - len(msg)) / 2 {
		lines = append(lines, "")
	}
	for _, l := range msg {
		pad := max(0, (w-cw.String(l))/2)
		lines = append(lines, on(shade)(lipgloss.NewStyle()).Render(strings.Repeat(" ", pad))+l)
	}
	return lines
}

func (m Model) sideItem(it sideItem, w int) []string {
	switch it.kind {
	case sideHeading:
		return []string{"", m.sectionHeading(it.platform, w), ""}
	case sideNote:
		bg := on(sidebarBg)
		note := bg(dimStyle).Render("No chats yet")
		if !m.showAll {
			note = bg(lipgloss.NewStyle().Foreground(green)).Render(iconCheck.sp()) + bg(dimStyle).Render("All caught up")
		}
		return []string{bg(lipgloss.NewStyle()).Render("       ") + note, ""}
	}
	return append(m.inboxRow(m.visible[it.chat], it.chat == m.cursor, w), "")
}

// sectionHeading: the platform's icon and name in its color, and how many messages are new.
func (m Model) sectionHeading(p core.PlatformID, w int) string {
	bg := on(sidebarBg)
	title := bg(lipgloss.NewStyle().Foreground(platformColors[p]).Bold(true)).Render(platformIcons[p].sp() + platformNames[p])
	unread := 0
	for _, c := range m.inbox.Chats {
		if c.Platform == p {
			unread += c.Unread
		}
	}
	right := ""
	if unread > 0 {
		right = bg(lipgloss.NewStyle().Foreground(secondary)).Render(fmt.Sprintf("%d new", unread))
	}
	left := bg(lipgloss.NewStyle()).Render("  ") + title
	gap := max(1, w-cw.String(left)-cw.String(right)-2)
	return left + bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap)) + right + bg(lipgloss.NewStyle()).Render("  ")
}

// viewInbox is the sidebar as one string (used by tests).
func (m Model) viewInbox(w, h int) string { return strings.Join(m.inboxLines(w, h), "\n") }

// inboxRow: avatar, name and time; then the preview and the unread count. The selected row
// is a lighter shade with an accent edge.
func (m Model) inboxRow(c core.ChatSummary, selected bool, w int) []string {
	shade := lipgloss.TerminalColor(sidebarBg)
	if selected {
		shade = selectedBg
	}
	bg := on(shade)
	plain := bg(lipgloss.NewStyle())
	edge := plain.Render(" ")
	if selected {
		edge = bg(accentStyle).Render("▌")
	}

	when := bg(dimStyle).Render(shortTime(c.Last.Timestamp, time.Now()))
	nameStyle := bg(lipgloss.NewStyle().Foreground(secondary))
	if c.Unread > 0 || selected {
		nameStyle = nameStyle.Foreground(text).Bold(true)
	}
	nameRoom := w - 7 - cw.String(when) - 3
	_, query := searchQuery(m.filter.Value())
	name := highlightMatch(cw.Truncate(c.Title(), max(1, nameRoom), "…"), query, nameStyle)

	pill := ""
	if c.Unread > 0 {
		pill = badgeStyle.Render(fmt.Sprint(c.Unread))
	}
	previewStyle := bg(dimStyle)
	if c.Unread > 0 {
		previewStyle = bg(lipgloss.NewStyle().Foreground(secondary))
	}
	preview := previewStyle.Render(cw.Truncate(previewText(c), max(1, w-7-cw.String(pill)-3), "…"))

	// Name and preview start in the same column; time and unread count end in the same column.
	line := func(left, right string) string {
		gap := max(1, w-cw.String(left)-cw.String(right)-2)
		return left + plain.Render(strings.Repeat(" ", gap)) + right + plain.Render("  ")
	}
	return []string{
		line(edge+plain.Render(" ")+avatar(chatKey(c.Chat), c.Title())+plain.Render(" ")+name, when),
		line(edge+plain.Render("      ")+preview, pill),
	}
}

// highlightMatch underlines the first case-insensitive match of query in s.
func highlightMatch(s, query string, base lipgloss.Style) string {
	q := strings.ToLower(strings.TrimSpace(query))
	lower := strings.ToLower(s)
	i := strings.Index(lower, q)
	if q == "" || i < 0 || len(lower) != len(s) { // byte offsets only line up when lowering keeps lengths
		return base.Render(s)
	}
	return base.Render(s[:i]) + base.Underline(true).Foreground(accent).Render(s[i:i+len(q)]) + base.Render(s[i+len(q):])
}

func previewText(c core.ChatSummary) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(mediaTag(c.Last)+" "+c.Last.Body)), " ")
	switch {
	case c.Last.FromMe:
		return "You: " + text
	case c.IsGroup:
		return c.Last.Sender() + ": " + text
	}
	return text
}
