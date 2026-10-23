package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/dmz/internal/cellwidth"
	"github.com/mitanshpatelll/dmz/internal/core"
)

// composerH is the framed reply box under the messages.
const composerH = 3

// chatHeader: name, platform and what matters about the chat (phone, group, quiet mode).
func (m Model) chatHeader(w int) string {
	if m.open == nil {
		return " " + dimStyle.Render("no chat open")
	}
	style := boldStyle
	if m.focus == focusChat || m.focus == focusCompose {
		style = style.Foreground(accent)
	}
	var details []string
	switch {
	case m.open.IsGroup:
		details = append(details, "group")
	case m.open.Platform == core.WhatsApp && m.open.Name != "":
		if p := phone(m.open.ID); p != "" {
			details = append(details, p)
		}
	}
	if !m.inbox.Next.IsZero() {
		details = append(details, "quiet mode")
	}
	head := " " + style.Render(m.open.Title()) + " " + platformTag(m.open.Platform)
	if len(details) > 0 {
		head += dimStyle.Render(" · " + strings.Join(details, " · "))
	}
	return cw.Truncate(head, w, "…")
}

// phone formats a WhatsApp chat ID for reading: "919825011420@s.whatsapp.net" → "+91 98250 11420".
func phone(id string) string {
	user, server, _ := strings.Cut(id, "@")
	if server != "s.whatsapp.net" {
		return ""
	}
	if strings.HasPrefix(user, "91") && len(user) == 12 {
		return "+91 " + user[2:7] + " " + user[7:]
	}
	return "+" + user
}

// chatLines is the chat column: the visible messages and the composer, exactly h lines.
func (m Model) chatLines(w, h int) []string {
	if m.open == nil {
		return m.viewNoChat(w, h)
	}
	vh := h - composerH
	lines := make([]string, 0, h)
	for i := m.chatView.YOffset; i < m.chatView.YOffset+vh; i++ {
		if i >= 0 && i < len(m.chatContent) {
			lines = append(lines, m.chatContent[i])
		} else {
			lines = append(lines, "")
		}
	}
	return append(lines, m.composer(w)...)
}

// composer is the framed reply box: "reply to Mummy (WA)".
func (m Model) composer(w int) []string {
	focused := m.focus == focusCompose
	b := lipgloss.NewStyle().Foreground(subtle)
	content := dimStyle.Render("› press ") + keyStyle.Render("i") + dimStyle.Render(" to reply")
	if focused {
		b = b.Foreground(accent)
		content = accentStyle.Render("› ") + m.compose.View()
	}
	bw := w - 2
	title := dimStyle.Render("reply to "+cw.Truncate(m.open.Title(), max(4, bw-20), "…")+" (") + platformTag(m.open.Platform) + dimStyle.Render(")")
	fill := max(0, bw-5-cw.String(title))
	return []string{
		" " + b.Render("╭─ ") + title + " " + b.Render(strings.Repeat("─", fill)+"╮") + " ",
		" " + b.Render("│") + " " + cw.Pad(content, bw-4) + " " + b.Render("│") + " ",
		" " + b.Render("╰"+strings.Repeat("─", bw-2)+"╯") + " ",
	}
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
	block := []string{
		accentStyle.Bold(true).Render("dmz"),
		"",
		boldStyle.Render("Your messages, without the feed."),
		dimStyle.Render("Pick a chat with ") + keyStyle.Render("j/k") + dimStyle.Render(" and ") + keyStyle.Render("enter") + dimStyle.Render("."),
		"",
		dimStyle.Render("Delivery       ") + delivery,
		dimStyle.Render("Read receipts  ") + receipts,
		dimStyle.Render("Feeds          ") + "stories and channels are never shown",
	}
	// Left-aligned lines, centered as one block, so the labels line up.
	blockW := 0
	for _, l := range block {
		blockW = max(blockW, cw.String(l))
	}
	lines := make([]string, 0, h)
	for range max(0, (h-len(block))/2) {
		lines = append(lines, "")
	}
	for _, l := range block {
		lines = append(lines, cw.Center(cw.Pad(l, blockW), w))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

// renderChat lays out the message log and records where each message starts.
func (m *Model) renderChat() {
	w := m.chatView.Width
	if w <= 0 || m.open == nil {
		return
	}
	m.starts, m.newLine = m.starts[:0], -1
	if len(m.messages) == 0 {
		m.setChatContent(append(make([]string, m.chatView.Height/2), cw.Center(dimStyle.Render("No messages yet. Say hi."), w)))
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
	newShown := false
	for i, msg := range m.messages {
		if day := dayLabel(msg.Timestamp, now); day != lastDay {
			lastDay = day
			lines = append(lines, "", cw.Center(dimStyle.Render("── "+day+" ──"), w))
		}
		if newCount > 0 && !newShown && !msg.FromMe && msg.Timestamp.After(m.newFrom) {
			newShown = true
			label := fmt.Sprintf(" %d new message", newCount)
			if newCount > 1 {
				label += "s"
			}
			label += " "
			side := max(1, (w-2-cw.String(label))/2)
			lines = append(lines, "")
			m.newLine = len(lines)
			lines = append(lines, " "+accentStyle.Render(strings.Repeat("═", side)+label+strings.Repeat("═", max(1, w-2-side-cw.String(label)))))
		}
		lines = append(lines, "")
		m.starts = append(m.starts, len(lines))
		lines = append(lines, m.bubble(msg, w, i == m.sel && m.focus == focusChat)...)
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
	m.setChatContent(lines)
}

func (m *Model) setChatContent(lines []string) {
	m.chatContent = lines
	m.chatView.SetContent(strings.Join(lines, "\n")) // the viewport only keeps track of scrolling
}

type boxChars struct{ tl, tr, bl, br, h, v string }

var (
	roundBox = boxChars{"╭", "╮", "╰", "╯", "─", "│"}
	thickBox = boxChars{"┏", "┓", "┗", "┛", "━", "┃"}
)

// bubble draws one message: theirs on the left with the sender and time in the top border,
// yours on the right with the time. The selected one has a thick accent border.
func (m *Model) bubble(msg core.Message, w int, selected bool) []string {
	key := fmt.Sprint(msg.ID, selected, msg.Body, msg.Meta, msg.Edited, msg.Deleted, msg.Reactions, msg.SenderName)
	if cached, ok := m.bubbles[key]; ok {
		return cached
	}

	maxW := max(16, min(w-4, w*68/100))
	minW := min(maxW, max(20, w*40/100))

	tag := mediaTag(msg)
	plain := strings.TrimSpace(tag + " " + msg.Body)
	body := cw.Wrap(plain, maxW-4)
	if tag != "" && strings.HasPrefix(body[0], tag) { // style the placeholder, not the caption
		style := tagStyle
		if msg.Deleted {
			style = dimStyle.Italic(true)
		}
		body[0] = style.Render(tag) + strings.TrimPrefix(body[0], tag)
	}

	stamp := msg.Timestamp.Format("15:04")
	if msg.Edited && !msg.Deleted {
		stamp += " · edited"
	}
	label := dimStyle.Render(stamp)
	if !msg.FromMe {
		label = senderStyle(msg.SenderID).Render(cw.Truncate(msg.Sender(), max(4, maxW-18), "…")) + " " + label
	}

	contentW := 0
	for _, l := range body {
		contentW = max(contentW, cw.String(l))
	}
	bw := min(maxW, max(minW, contentW+4, cw.String(label)+7))

	chars, color := roundBox, bubbleLine
	if msg.FromMe {
		color = accentSoft
	}
	if selected {
		chars, color = thickBox, accent
	}
	b := lipgloss.NewStyle().Foreground(color)
	fill := strings.Repeat(chars.h, max(0, bw-5-cw.String(label)))
	var top string
	if msg.FromMe {
		top = b.Render(chars.tl+fill) + " " + label + " " + b.Render(chars.h+chars.tr)
	} else {
		top = b.Render(chars.tl+chars.h) + " " + label + " " + b.Render(fill+chars.tr)
	}
	box := []string{top}
	for _, l := range body {
		box = append(box, b.Render(chars.v)+" "+cw.Pad(l, bw-4)+" "+b.Render(chars.v))
	}
	box = append(box, b.Render(chars.bl+strings.Repeat(chars.h, bw-2)+chars.br))

	indent := "  "
	if msg.FromMe {
		indent = strings.Repeat(" ", max(0, w-bw-2))
	}
	out := make([]string, 0, len(box)+1)
	for _, l := range box {
		out = append(out, indent+l)
	}
	if len(msg.Reactions) > 0 {
		parts := make([]string, len(msg.Reactions))
		for i, r := range msg.Reactions {
			parts[i] = r.Emoji
			if r.Count > 1 {
				parts[i] += fmt.Sprint(r.Count)
			}
		}
		reactions := dimStyle.Render(cw.Truncate(strings.Join(parts, " "), bw, "…"))
		if msg.FromMe {
			out = append(out, strings.Repeat(" ", max(0, w-cw.String(reactions)-3))+reactions)
		} else {
			out = append(out, "   "+reactions)
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
