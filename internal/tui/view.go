package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mitanshpatelll/dmz/internal/core"
)

var platformNames = map[core.PlatformID]string{core.WhatsApp: "WhatsApp"}

// Sizes: one header line, one footer line, two bordered panes in between.
func (m Model) leftInnerWidth() int  { return min(max(m.width*34/100, 24), 44) - 2 }
func (m Model) rightInnerWidth() int { return max(10, m.width-m.leftInnerWidth()-4) }
func (m Model) innerHeight() int     { return max(4, m.height-4) }

func (m Model) filterShown() bool { return m.focus == focusFilter || m.filter.Value() != "" }

// rows is how many two-line inbox entries fit.
func (m Model) rows() int {
	h := m.innerHeight()
	if m.filterShown() {
		h--
	}
	return max(1, h/2)
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	lw, rw, ih := m.leftInnerWidth(), m.rightInnerWidth(), m.innerHeight()
	left := paneStyle(m.focus == focusInbox || m.focus == focusFilter).Width(lw).Height(ih).Render(m.viewInbox(lw, ih))
	right := paneStyle(m.focus == focusChat || m.focus == focusCompose).Width(rw).Height(ih).Render(m.viewChat(rw, ih))
	return lipgloss.JoinVertical(lipgloss.Left, m.viewHeader(), lipgloss.JoinHorizontal(lipgloss.Top, left, right), m.viewFooter())
}

func (m Model) viewHeader() string {
	left := logoStyle.Render("dmz")
	ids := make([]core.PlatformID, 0, len(m.inbox.Status))
	for id := range m.inbox.Status {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		left += "  " + platformNames[id] + " " + statusText(m.inbox.Status[id])
		if p := m.inbox.Sync[id]; p > 0 && p < 100 {
			left += dimStyle.Render(fmt.Sprintf(" syncing %d%%", p))
		}
	}

	right := "live"
	if !m.inbox.Next.IsZero() {
		right = fmt.Sprintf("next drop %s", m.inbox.Next.Format("15:04"))
		if m.inbox.Held > 0 {
			right += fmt.Sprintf(" · %d held", m.inbox.Held)
		}
	}
	if m.inbox.Receipts {
		right += " · receipts on"
	} else {
		right += " · receipts off"
	}
	return spread(left, dimStyle.Render(right)+" ", m.width)
}

func statusText(s core.StatusEvent) string {
	var text string
	var color lipgloss.AdaptiveColor
	switch s.State {
	case core.StateConnected:
		text, color = "● online", green
	case core.StateConnecting:
		text, color = "○ connecting", yellow
	case core.StateDisconnected:
		text, color = "○ offline", yellow
	case core.StateLoggedOut:
		text, color = "✕ logged out", red
	}
	if s.Detail != "" && s.State != core.StateConnected {
		text += " (" + s.Detail + ")"
	}
	return lipgloss.NewStyle().Foreground(color).Render(text)
}

func (m Model) viewFooter() string {
	var hints string
	switch m.focus {
	case focusFilter:
		hints = "type to filter · enter keep · esc clear"
	case focusCompose:
		hints = "enter send · esc stop typing"
	case focusChat:
		hints = "j/k scroll · g/G top/bottom · i reply · esc inbox · q quit"
	default:
		hints = "j/k move · enter open · i reply · / filter · tab chat · q quit"
	}
	return spread(" "+dimStyle.Render(hints), errorStyle.Render(m.flash)+" ", m.width)
}

func (m Model) viewInbox(w, h int) string {
	var lines []string
	if m.filterShown() {
		lines = append(lines, m.filter.View())
	}
	if len(m.visible) == 0 {
		msg := "No chats yet.\n\nThe first sync after linking\ncan take a few minutes."
		if m.filter.Value() != "" {
			msg = "No matching chats."
		}
		lines = append(lines, "", dimStyle.Render(msg))
	}
	for i := m.offset; i < len(m.visible) && len(lines)+2 <= h; i++ {
		lines = append(lines, m.inboxRow(m.visible[i], i == m.cursor, w)...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) inboxRow(c core.ChatSummary, selected bool, w int) []string {
	marker := "  "
	if selected {
		marker = accentStyle.Render("▌ ")
	}
	when := shortTime(c.Last.Timestamp, time.Now())
	nameStyle := lipgloss.NewStyle()
	if c.Unread > 0 {
		nameStyle = boldStyle
	}
	if selected && (m.focus == focusInbox || m.focus == focusFilter) {
		nameStyle = nameStyle.Foreground(accent)
	}
	name := nameStyle.Render(ansi.Truncate(c.Title(), w-2-len(when)-1, "…"))

	badge := ""
	if c.Unread > 0 {
		badge = badgeStyle.Render(fmt.Sprint(c.Unread))
	}
	preview := ansi.Truncate(previewText(c), w-2-lipgloss.Width(badge)-1, "…")

	return []string{
		spread(marker+name, dimStyle.Render(when), w),
		spread(marker+dimStyle.Render(preview), badge, w),
	}
}

func (m Model) viewChat(w, h int) string {
	if m.open == nil {
		delivery := "live, messages arrive as they come"
		if !m.inbox.Next.IsZero() {
			delivery = fmt.Sprintf("held until %s (%d waiting); VIPs come through", m.inbox.Next.Format("15:04"), m.inbox.Held)
		}
		receipts := "off: nobody sees \"seen\" from dmz"
		if m.inbox.Receipts {
			receipts = "on"
		}
		help := strings.Join([]string{
			boldStyle.Render("No chat open"),
			"",
			"Pick one with j/k and enter.",
			"",
			dimStyle.Render("Delivery:      ") + delivery,
			dimStyle.Render("Read receipts: ") + receipts,
			dimStyle.Render("Feeds:         ") + "status updates and channels are never shown",
		}, "\n")
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, help)
	}

	title := boldStyle.Render(ansi.Truncate(m.open.Title(), w-12, "…"))
	kind := platformNames[m.open.Platform]
	if m.open.IsGroup {
		kind = "group · " + kind
	}
	divider := lipgloss.NewStyle().Foreground(subtle).Render(strings.Repeat("─", w))
	return strings.Join([]string{spread(title, dimStyle.Render(kind), w), m.chatView.View(), divider, m.compose.View()}, "\n")
}

// renderChat rebuilds the message log for the viewport.
func (m *Model) renderChat() {
	w := m.chatView.Width
	if w <= 0 || m.open == nil {
		return
	}
	if len(m.messages) == 0 {
		m.chatView.SetContent(dimStyle.Render("No messages yet."))
		return
	}
	var b strings.Builder
	var lastDay string
	now := time.Now()
	for _, msg := range m.messages {
		if day := dayLabel(msg.Timestamp, now); day != lastDay {
			lastDay = day
			b.WriteString(lipgloss.PlaceHorizontal(w, lipgloss.Center, dimStyle.Render("── "+day+" ──")) + "\n")
		}
		sender := senderStyle(msg.SenderID)
		if msg.FromMe {
			sender = boldStyle.Foreground(accent)
		}
		head := dimStyle.Render(msg.Timestamp.Format("15:04")) + " " + sender.Render(msg.Sender()) + " "
		text := msg.Body
		if tag := mediaTag(msg); tag != "" {
			text = strings.TrimSpace(tagStyle.Render(tag) + " " + text)
		}

		// Hanging indent: wrapped lines align under the first line's text.
		indent := lipgloss.Width(head)
		if indent > w/2 {
			b.WriteString(head + "\n")
			indent = 2
			head = strings.Repeat(" ", indent)
		}
		for i, line := range strings.Split(lipgloss.NewStyle().Width(w-indent).Render(text), "\n") {
			if i == 0 {
				b.WriteString(head)
			} else {
				b.WriteString(strings.Repeat(" ", indent))
			}
			b.WriteString(line + "\n")
		}
	}
	content := strings.TrimSuffix(b.String(), "\n")
	// Short chats sit at the bottom, next to the composer, like any chat app.
	if pad := m.chatView.Height - strings.Count(content, "\n") - 1; pad > 0 {
		content = strings.Repeat("\n", pad) + content
	}
	m.chatView.SetContent(content)
}

// mediaTag is the placeholder shown instead of media. Nothing autoplays or renders inline.
func mediaTag(msg core.Message) string {
	with := func(tag, detail string) string {
		if detail == "" {
			return "[" + tag + "]"
		}
		return "[" + tag + " " + detail + "]"
	}
	switch msg.Kind {
	case core.KindImage:
		return "[photo]"
	case core.KindVideo:
		return with("video", msg.Meta)
	case core.KindGIF:
		return "[gif]"
	case core.KindVoice:
		return with("voice", msg.Meta)
	case core.KindAudio:
		return with("audio", msg.Meta)
	case core.KindDocument:
		return with("file", msg.Meta)
	case core.KindSticker:
		return "[sticker]"
	case core.KindLocation:
		return with("location", msg.Meta)
	case core.KindContact:
		return with("contact", msg.Meta)
	case core.KindPoll:
		return "[poll]"
	case core.KindLink:
		if isShortVideo(msg.Meta) {
			return "[reel]"
		}
	}
	return ""
}

// isShortVideo spots reels/shorts links: shown as text, never previewed.
func isShortVideo(url string) bool {
	for _, s := range []string{"instagram.com/reel", "instagram.com/p/", "tiktok.com", "youtube.com/shorts", "facebook.com/reel", "fb.watch"} {
		if strings.Contains(url, s) {
			return true
		}
	}
	return false
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

func shortTime(t, now time.Time) string {
	switch {
	case sameDay(t, now):
		return t.Format("15:04")
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	case t.Year() == now.Year():
		return t.Format("02 Jan")
	}
	return t.Format("02/01/06")
}

func dayLabel(t, now time.Time) string {
	switch {
	case sameDay(t, now):
		return "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	return t.Format("Mon, 02 Jan 2006")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// spread puts left and right on one line of width w, truncating left if needed.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		left = ansi.Truncate(left, max(0, w-lipgloss.Width(right)-1), "…")
		gap = max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + strings.Repeat(" ", gap) + right
}
