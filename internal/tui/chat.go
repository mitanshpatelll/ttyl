package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mitanshpatelll/dmz/internal/core"
)

// sameRunGap: consecutive messages from one sender closer than this read as one run
// (no sender label, no spacer line).
const sameRunGap = 5 * time.Minute

func (m Model) viewChatPanel(ih int) string {
	w, active := m.rightInnerWidth()+2, m.focus == focusChat || m.focus == focusCompose
	if m.open == nil {
		return panel("dmz", "", m.viewNoChat(w-2, ih), w, ih+2, false, -1)
	}
	kind := platformNames[m.open.Platform]
	if m.open.IsGroup {
		kind = "group · " + kind
	}
	body := strings.Split(m.chatView.View(), "\n")
	body = append(body, "", m.viewComposer(w-2))
	return panel(m.open.Title(), dimStyle.Render(kind), body, w, ih+2, active, len(body)-2)
}

func (m Model) viewComposer(w int) string {
	if m.focus == focusCompose {
		return accentStyle.Render(" › ") + m.compose.View()
	}
	return dimStyle.Render("   press ") + keyStyle.Render("i") + dimStyle.Render(" to reply")
}

func (m Model) viewNoChat(w, h int) []string {
	delivery := "live, messages arrive as they come"
	if !m.inbox.Next.IsZero() {
		delivery = fmt.Sprintf("held until %s (%d waiting), VIPs come through", m.inbox.Next.Format("15:04"), m.inbox.Held)
	}
	receipts := "off, nobody sees \"seen\" from dmz"
	if m.inbox.Receipts {
		receipts = "on"
	}
	// Left-aligned lines, centered as one block, so the labels line up.
	block := lipgloss.JoinVertical(lipgloss.Left,
		logoStyle.Render("dmz"),
		"",
		boldStyle.Render("Your messages, without the feed."),
		dimStyle.Render("Pick a chat with ")+keyStyle.Render("j/k")+dimStyle.Render(" and ")+keyStyle.Render("enter")+dimStyle.Render("."),
		"",
		dimStyle.Render("Delivery       ")+delivery,
		dimStyle.Render("Read receipts  ")+receipts,
		dimStyle.Render("Feeds          ")+"stories and channels are never shown",
	)
	return strings.Split(lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, block), "\n")
}

// renderChat rebuilds the message log for the viewport and records where each message starts.
func (m *Model) renderChat() {
	w := m.chatView.Width
	if w <= 0 || m.open == nil {
		return
	}
	m.starts, m.newLine = m.starts[:0], -1
	if len(m.messages) == 0 {
		m.chatView.SetContent(lipgloss.Place(w, m.chatView.Height, lipgloss.Center, lipgloss.Center, dimStyle.Render("No messages yet. Say hi.")))
		return
	}
	if m.bubbleWidth != w || m.bubbles == nil {
		m.bubbles, m.bubbleWidth = map[string][]string{}, w
	}

	newCount := 0
	if !m.newFrom.IsZero() {
		for _, msg := range m.messages {
			if !msg.FromMe && msg.Timestamp.After(m.newFrom) {
				newCount++
			}
		}
	}
	now := time.Now()
	var lines []string
	var lastDay string
	newShown, afterBreak := false, true
	for i, msg := range m.messages {
		if day := dayLabel(msg.Timestamp, now); day != lastDay {
			lastDay = day
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, lipgloss.PlaceHorizontal(w, lipgloss.Center, dimStyle.Render("── "+day+" ──")))
			afterBreak = true
		}
		if newCount > 0 && !newShown && !msg.FromMe && msg.Timestamp.After(m.newFrom) {
			newShown = true
			label := fmt.Sprintf(" %d new message", newCount)
			if newCount > 1 {
				label += "s"
			}
			label += " "
			side := max(1, (w-lipgloss.Width(label))/2)
			lines = append(lines, "")
			m.newLine = len(lines)
			lines = append(lines, accentStyle.Render(strings.Repeat("─", side)+label+strings.Repeat("─", max(1, w-side-lipgloss.Width(label)))))
			afterBreak = true
		}
		sameRun := false
		if i > 0 && !afterBreak {
			prev := m.messages[i-1]
			sameRun = prev.SenderID == msg.SenderID && prev.FromMe == msg.FromMe && msg.Timestamp.Sub(prev.Timestamp) < sameRunGap
		}
		if !sameRun && len(lines) > 0 {
			lines = append(lines, "") // breathing room between speakers
		}
		afterBreak = false
		m.starts = append(m.starts, len(lines))
		selected := i == m.sel && m.focus == focusChat
		showSender := m.open.IsGroup && !msg.FromMe && !sameRun
		lines = append(lines, m.bubble(msg, w, selected, showSender)...)
	}
	// Short chats sit at the bottom, next to the composer, like any chat app.
	if pad := m.chatView.Height - len(lines); pad > 0 {
		lines = append(make([]string, pad), lines...)
		for i := range m.starts {
			m.starts[i] += pad
		}
		if m.newLine >= 0 {
			m.newLine += pad
		}
	}
	m.chatView.SetContent(strings.Join(lines, "\n"))
}

// bubble renders one message: theirs on the left in grey, yours on the right in the accent
// color, the time beside the last line, reactions underneath. The selected one gets a thick border.
func (m *Model) bubble(msg core.Message, w int, selected, showSender bool) []string {
	key := fmt.Sprint(msg.ID, selected, showSender, msg.Body, msg.Meta, msg.Edited, msg.Deleted, msg.Reactions, msg.SenderName)
	if cached, ok := m.bubbles[key]; ok {
		return cached
	}

	maxBubble := max(10, min(w-14, max(18, w*7/10)))
	content := msg.Body
	if tag := mediaTag(msg); tag != "" {
		style := tagStyle
		if msg.Deleted {
			style = dimStyle.Italic(true)
		}
		content = strings.TrimSpace(style.Render(tag) + " " + content)
	}
	content = ansi.Wrap(content, maxBubble-4, "")

	border, color := lipgloss.RoundedBorder(), bubbleLine
	if msg.FromMe {
		color = accentSoft
	}
	if selected {
		border, color = lipgloss.ThickBorder(), accent
	}
	box := strings.Split(lipgloss.NewStyle().Border(border).BorderForeground(color).Padding(0, 1).Foreground(text).Render(content), "\n")
	boxWidth := lipgloss.Width(box[0])

	stamp := msg.Timestamp.Format("15:04")
	if msg.Edited && !msg.Deleted {
		stamp = "edited " + stamp
	}
	stamp = dimStyle.Render(stamp)
	stampLine := len(box) - 2 // last line of text inside the border

	var out []string
	if showSender {
		out = append(out, " "+senderStyle(msg.SenderID).Render(ansi.Truncate(msg.Sender(), w-2, "…")))
	}
	for i, l := range box {
		switch {
		case !msg.FromMe && i == stampLine:
			out = append(out, " "+l+" "+stamp)
		case !msg.FromMe:
			out = append(out, " "+l)
		case i == stampLine:
			out = append(out, strings.Repeat(" ", max(0, w-boxWidth-lipgloss.Width(stamp)-2))+stamp+" "+l+" ")
		default:
			out = append(out, strings.Repeat(" ", max(0, w-boxWidth-1))+l+" ")
		}
	}
	if len(msg.Reactions) > 0 {
		parts := make([]string, len(msg.Reactions))
		for i, r := range msg.Reactions {
			parts[i] = r.Emoji
			if r.Count > 1 {
				parts[i] += fmt.Sprint(r.Count)
			}
		}
		reactions := dimStyle.Render(ansi.Truncate(strings.Join(parts, " "), maxBubble, "…"))
		if msg.FromMe {
			out = append(out, strings.Repeat(" ", max(0, w-lipgloss.Width(reactions)-2))+reactions+"  ")
		} else {
			out = append(out, "  "+reactions)
		}
	}
	m.bubbles[key] = out
	return out
}

// mediaTag is the placeholder shown instead of media. Nothing autoplays or renders inline.
func mediaTag(msg core.Message) string {
	with := func(tag, detail string) string {
		if detail == "" {
			return "[" + tag + "]"
		}
		return "[" + tag + " " + detail + "]"
	}
	if msg.Deleted {
		return "[deleted]"
	}
	switch msg.Kind {
	case core.KindImage:
		return with("photo", msg.Meta)
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
