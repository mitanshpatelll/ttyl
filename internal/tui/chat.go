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

const (
	composerH = 2               // a spacer and the message input
	textCol   = 8               // message text starts here: edge, space, avatar (4), two spaces
	maxTextW  = 100             // on very wide screens lines stop here, so they stay easy to read
	groupGap  = 7 * time.Minute // messages from one person closer than this share a header
)

// chatHeader: name, platform and what matters about the chat, then the unusual state.
func (m Model) chatHeader(w int) string {
	if m.open == nil {
		return spread(" "+dimStyle.Render("no chat open"), m.statusRight(w-16), w)
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
	lead := "@ "
	if useIcons {
		lead = iconUser.sp()
		if m.open.IsGroup {
			lead = iconGroup.sp()
		}
	}
	head := " " + dimStyle.Render(lead) + style.Render(m.open.Title()) + "  " + platformTag(m.open.Platform)
	if len(details) > 0 {
		head += dimStyle.Render(" · " + strings.Join(details, " · "))
	}
	// The status gives way before the name does.
	name := cw.String(" " + lead + m.open.Title())
	return spread(head, m.statusRight(w-min(name, 24)-2), w)
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

// chatLines is the conversation: the visible messages and the input, exactly h lines.
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
	lines = append(lines, "")
	if box := m.emojiBox(w, len(lines)); m.focus == focusCompose && len(box) > 0 {
		copy(lines[len(lines)-len(box):], box) // over the newest messages, right above the input
	}
	return append(lines, m.composer(w))
}

// composer is the message input: one shaded line, "Message Mummy".
func (m Model) composer(w int) string {
	bg := on(inputBg)
	inner := w - 4
	var content string
	switch {
	case m.focus == focusCompose:
		content = bg(accentStyle).Render("› ") + m.compose.View()
	case inner >= 40:
		content = bg(dimStyle).Render("Message "+cw.Truncate(m.open.Title(), inner-30, "…")+"   ·   press ") +
			bg(keyStyle).Render("i") + bg(dimStyle).Render(" to type")
	default: // the footer says how
		content = bg(dimStyle).Render("Message " + cw.Truncate(m.open.Title(), max(4, inner-10), "…"))
	}
	return "  " + bg(lipgloss.NewStyle()).Render(" ") + fill(content, inner-1, inputBg) + "  "
}

// viewNoChat is the welcome screen: what is unread, how to find anyone, and the focus rules.
func (m Model) viewNoChat(w, h int) []string {
	delivery := "live"
	if !m.inbox.Next.IsZero() {
		delivery = fmt.Sprintf("held until %s · %d waiting", m.inbox.Next.Format("15:04"), m.inbox.Held)
	}
	receipts := "off, nobody sees \"seen\""
	if m.inbox.Receipts {
		receipts = "on"
	}
	chats, msgs := 0, 0
	for _, c := range m.inbox.Chats {
		if c.Unread > 0 {
			chats++
			msgs += c.Unread
		}
	}
	summary := lipgloss.NewStyle().Foreground(green).Render(iconCheck.sp()) + boldStyle.Render("All caught up")
	if chats > 0 {
		plural := func(n int, word string) string {
			if n == 1 {
				return "1 " + word
			}
			return fmt.Sprintf("%d %ss", n, word)
		}
		summary = accentStyle.Render(iconInbox.sp()) + boldStyle.Render(plural(msgs, "new message")+" in "+plural(chats, "chat"))
	}
	var apps []string
	for _, p := range platformOrder {
		apps = append(apps, keyStyle.Render(strings.ToLower(platformTags[p][:1])+":")+" "+platformTag(p))
	}
	// The whole card when it fits; on a short window the rules go, then the keys.
	head := []string{accentStyle.Bold(true).Render("ttyl"), "", summary}
	keys := []string{"",
		keyStyle.Render("/") + dimStyle.Render(" search anyone, or one app with"),
		"  " + strings.Join(apps, "   "),
		keyStyle.Render("j/k") + dimStyle.Render(" move  ") + keyStyle.Render("enter") + dimStyle.Render(" open  ") + keyStyle.Render("a") + dimStyle.Render(" all chats"),
	}
	rules := []string{"",
		dimStyle.Render("Delivery       ") + delivery,
		dimStyle.Render("Read receipts  ") + receipts,
		dimStyle.Render("Feeds          ") + "never shown",
	}
	block := slices.Concat(head, keys, rules)
	switch {
	case m.syncing():
		block = m.syncCard(w)
	case len(block) > h:
		block = slices.Concat(head, keys)
		if len(block) > h {
			block = head[2:]
		}
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
		lines = append(lines, cw.Center(cw.Pad(cw.Truncate(l, w-2, "…"), min(blockW, w-2)), w))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

// renderChat lays out the conversation and records where each message starts.
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

	now := time.Now()
	var lines []string
	var lastDay string
	newShown := false
	lastMine := -1 // your newest message says in words how far it got
	for i, msg := range m.messages {
		if msg.FromMe && !msg.Deleted {
			lastMine = i
		}
	}
	for i, msg := range m.messages {
		breakHere := false
		if day := dayLabel(msg.Timestamp, now); day != lastDay {
			lastDay = day
			lines = append(lines, "", rule(dimStyle.Render(day), w, faint))
			breakHere = true
		}
		if !m.newFrom.IsZero() && !newShown && !msg.FromMe && msg.Timestamp.After(m.newFrom) {
			newShown = true
			lines = append(lines, "")
			m.newLine = len(lines)
			newLabel := lipgloss.NewStyle().Bold(true).Foreground(white).Background(red).Render(" NEW ")
			lines = append(lines, " "+lipgloss.NewStyle().Foreground(red).Render(strings.Repeat("─", max(1, w-7)))+newLabel+" ")
			breakHere = true
		}
		header := true
		if i > 0 && !breakHere {
			prev := m.messages[i-1]
			header = prev.SenderID != msg.SenderID || prev.FromMe != msg.FromMe || msg.Timestamp.Sub(prev.Timestamp) >= groupGap
		}
		if header {
			lines = append(lines, "") // space between people
		}
		m.starts = append(m.starts, len(lines))
		lines = append(lines, m.message(msg, w, header, i == m.sel && m.focus == focusChat, i == lastMine, now)...)
	}
	// Short chats sit at the bottom, next to the input, like any chat app.
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

// rule is a full-width line with a label in the middle: "──── Today ────".
func rule(label string, w int, color lipgloss.TerminalColor) string {
	lw := cw.String(label) + 2
	side := max(1, (w-2-lw)/2)
	line := lipgloss.NewStyle().Foreground(color)
	return " " + line.Render(strings.Repeat("─", side)) + " " + label + " " + line.Render(strings.Repeat("─", max(1, w-2-side-lw))) + " "
}

func (m *Model) setChatContent(lines []string) {
	m.chatContent = lines
	m.chatView.SetContent(strings.Join(lines, "\n")) // the viewport only keeps track of scrolling
}

// message renders one message Discord style. The first of a run gets a header (avatar,
// bold colored name, time); its text and any follow-ups sit underneath, aligned with the name.
// The selected message gets an accent edge on every line.
func (m *Model) message(msg core.Message, w int, header, selected, lastMine bool, now time.Time) []string {
	key := fmt.Sprint(msg.ID, header, selected, msg.Body, msg.Meta, msg.Edited, msg.Deleted, msg.Reactions, msg.SenderName, sameDay(msg.Timestamp, now), msg.Status, lastMine)
	if cached, ok := m.bubbles[key]; ok {
		return cached
	}
	edge := " "
	if selected {
		edge = accentStyle.Render("▌")
	}
	indent := edge + strings.Repeat(" ", textCol-1)

	var out []string
	if header {
		name, avatarID := msg.Sender(), msg.SenderID
		nameStyle := lipgloss.NewStyle().Bold(true).Foreground(personColor(avatarID))
		if msg.FromMe {
			name, avatarID = "You", "me"
			nameStyle = nameStyle.Foreground(accent)
		}
		avatarBlock := lipgloss.NewStyle().Bold(true).Foreground(white).Background(personColor(avatarID)).Render(" " + cw.Pad(initialsOf(name), 2) + " ")
		if msg.FromMe {
			avatarBlock = lipgloss.NewStyle().Bold(true).Foreground(white).Background(accentSoft).Render(" ME ")
		}
		when := dimStyle.Render(stamp(msg.Timestamp, now))
		room := w - textCol - cw.String(when) - 3
		out = append(out, edge+" "+avatarBlock+"  "+nameStyle.Render(cw.Truncate(name, max(4, room), "…"))+"  "+when)
	}

	tag := mediaTag(msg)
	body := cw.Wrap(strings.TrimSpace(tag+" "+msg.Body), max(10, min(w-textCol-2, maxTextW)))
	textStyle := lipgloss.NewStyle().Foreground(text)
	for i, l := range body {
		styled := textStyle.Render(l)
		if i == 0 && tag != "" && strings.HasPrefix(l, tag) { // the placeholder stands out from the caption
			style := tagStyle
			if msg.Deleted {
				style = dimStyle.Italic(true)
			}
			styled = style.Render(tag) + textStyle.Render(strings.TrimPrefix(l, tag))
		}
		out = append(out, indent+styled)
	}
	if msg.Edited && !msg.Deleted {
		last := out[len(out)-1]
		if cw.String(last)+9 <= w {
			out[len(out)-1] = last + dimStyle.Render(" (edited)")
		} else {
			out = append(out, indent+dimStyle.Render("(edited)"))
		}
	}
	if msg.FromMe && !msg.Deleted {
		mark := ticks(msg.Status)
		if lastMine {
			mark += " " + deliveryStyle(msg.Status).Render(deliveryWords[msg.Status])
		}
		if last := out[len(out)-1]; cw.String(last)+2+cw.String(mark) <= w {
			out[len(out)-1] = last + "  " + mark
		} else {
			out = append(out, indent+mark)
		}
	}
	if len(msg.Reactions) > 0 {
		parts := make([]string, len(msg.Reactions))
		for i, r := range msg.Reactions {
			parts[i] = r.Emoji
			if r.Count > 1 {
				parts[i] += " " + fmt.Sprint(r.Count)
			}
		}
		out = append(out, indent+dimStyle.Render(cw.Truncate(strings.Join(parts, "   "), w-textCol-2, "…")))
	}
	m.bubbles[key] = out
	return out
}

var deliveryWords = map[core.Delivery]string{core.Sent: "Sent", core.Delivered: "Delivered", core.Seen: "Seen"}

func deliveryStyle(s core.Delivery) lipgloss.Style {
	if s == core.Seen {
		return lipgloss.NewStyle().Foreground(seenBlue)
	}
	return dimStyle
}

// ticks shows how far a message of yours got, like WhatsApp: ✓ sent, ✓✓ delivered, and
// blue ✓✓ once seen.
func ticks(s core.Delivery) string {
	if s == core.Sent {
		return dimStyle.Render("✓")
	}
	return deliveryStyle(s).Render("✓✓")
}

// mediaTag is the placeholder shown instead of media: " voice 0:12", or "[voice 0:12]"
// without icons. Nothing autoplays or renders inline.
func mediaTag(msg core.Message) string {
	with := func(ic icon, tag, detail string) string {
		if detail != "" {
			tag += " " + detail
		}
		if useIcons {
			return ic.glyph + " " + tag
		}
		return "[" + tag + "]"
	}
	if msg.Deleted {
		return with(iconDeleted, "message deleted", "")
	}
	ic := kindIcon(msg)
	switch msg.Kind {
	case core.KindImage:
		return with(ic, "photo", msg.Meta)
	case core.KindVideo:
		return with(ic, "video", msg.Meta)
	case core.KindGIF:
		return with(ic, "gif", "")
	case core.KindVoice:
		return with(ic, "voice", msg.Meta)
	case core.KindAudio:
		return with(ic, "audio", msg.Meta)
	case core.KindDocument:
		return with(ic, "file", msg.Meta)
	case core.KindSticker:
		return with(ic, "sticker", "")
	case core.KindLocation:
		return with(ic, "location", msg.Meta)
	case core.KindContact:
		return with(ic, "contact", msg.Meta)
	case core.KindPoll:
		return with(ic, "poll", "")
	case core.KindLink:
		if isShortVideo(msg.Meta) {
			return with(ic, "reel", "")
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
