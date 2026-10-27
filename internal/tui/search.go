package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

// The search modal: / opens it in the middle of the screen, over whatever is there. Results
// follow every key press, enter opens the chosen chat. It looks through every chat, not only
// the unread ones in the sidebar; w: i: t: keep it to one app.

const searchRowsMax = 8

// openSearch shows the modal with the most recent chats until something is typed.
func (m *Model) openSearch() tea.Cmd {
	m.back = m.focus
	m.focus = focusSearch
	m.search.Reset()
	m.findChats(false)
	m.layout()
	return m.search.Focus()
}

// closeSearch hides the modal and goes back to where it was opened.
func (m *Model) closeSearch() {
	m.search.Reset()
	m.search.Blur()
	m.found, m.foundSel = nil, 0
	m.setFocus(m.back)
}

// findChats runs the search again. keepSel holds on to the chosen chat (when the inbox
// reloads); otherwise the best match is chosen, as after typing.
func (m *Model) findChats(keepSel bool) {
	keep := ""
	if keepSel && m.foundSel < len(m.found) {
		keep = chatKey(m.found[m.foundSel].Chat)
	}
	m.found, m.foundSel = searchResults(m.inbox.Chats, m.search.Value()), 0
	for i, c := range m.found {
		if chatKey(c.Chat) == keep {
			m.foundSel = i
		}
	}
}

func (m *Model) moveFound(delta int) {
	if len(m.found) > 0 {
		m.foundSel = (m.foundSel + delta + len(m.found)) % len(m.found)
	}
}

// openFound opens the chosen chat. The sidebar keeps the selected chat even when it is read,
// so it shows up there too.
func (m *Model) openFound() tea.Cmd {
	if m.foundSel >= len(m.found) {
		return nil
	}
	m.selected = chatKey(m.found[m.foundSel].Chat)
	m.closeSearch()
	m.applyFilter()
	return m.openSelected(focusChat)
}

func (m Model) handleSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.closeSearch()
		return m, nil
	case "enter":
		return m, m.openFound()
	case "up", "ctrl+p", "shift+tab":
		m.moveFound(-1)
		return m, nil
	case "down", "ctrl+n", "tab":
		m.moveFound(1)
		return m, nil
	}
	var cmd tea.Cmd
	before := m.search.Value()
	m.search, cmd = m.search.Update(k)
	if m.search.Value() != before {
		m.findChats(false)
	}
	return m, cmd
}

// searchResults are the chats whose name matches, best first: the name starts with the
// query, then every word of the query starts a word of the name, then the words are
// anywhere in it. Within a rank the inbox order stays, newest first. An empty query
// matches every chat, so the modal opens on the recent ones.
func searchResults(chats []core.ChatSummary, v string) []core.ChatSummary {
	platform, q := searchQuery(v)
	words := strings.Fields(q)
	type hit struct {
		chat core.ChatSummary
		rank int
	}
	var hits []hit
	for _, c := range chats {
		if platform != "" && c.Platform != platform {
			continue
		}
		if rank, ok := matchRank(strings.ToLower(c.Title()), q, words); ok {
			hits = append(hits, hit{c, rank})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.rank - b.rank })
	out := make([]core.ChatSummary, len(hits))
	for i, h := range hits {
		out[i] = h.chat
	}
	return out
}

func matchRank(name, q string, words []string) (int, bool) {
	if len(words) == 0 || strings.HasPrefix(name, q) {
		return 0, true
	}
	rank := 1
	for _, w := range words {
		if !strings.Contains(name, w) {
			return 0, false
		}
		if !strings.HasPrefix(name, w) && !strings.Contains(name, " "+w) {
			rank = 2
		}
	}
	return rank, true
}

// The modal is centered and keeps one height while typing, so it doesn't jump around.
func (m Model) searchW() int {
	if w := min(72, m.width-6); w >= 40 {
		return w
	}
	return m.width
}

func (m Model) searchRows() int { return min(searchRowsMax, max(1, m.height-12)) }

// searchBox draws the modal: the query, the matching chats and the app prefixes.
func (m Model) searchBox() []string {
	w, rows := m.searchW(), m.searchRows()
	iw := w - 2
	platform, q := searchQuery(m.search.Value())

	input := " " + accentStyle.Render(iconSearch.sp()) + m.search.View()
	if platform != "" {
		chip := lipgloss.NewStyle().Foreground(platformColors[platform]).Bold(true).Render(platformIcons[platform].sp() + platformNames[platform] + " only")
		input = spread(input, chip+" ", iw)
	}

	label := "RECENT"
	if q != "" {
		label = fmt.Sprintf("%d MATCHES", len(m.found))
		if len(m.found) == 1 {
			label = "1 MATCH"
		}
	}
	pos := ""
	if len(m.found) > 0 {
		pos = dimStyle.Render(fmt.Sprintf("%d/%d", m.foundSel+1, len(m.found)))
	}
	// Line 3 becomes the rule under the query.
	body := []string{"", input, "", "", "", spread(" "+dimStyle.Bold(true).Render(label), pos+" ", iw)}

	var results []string
	if len(m.found) == 0 {
		msg := "No chats yet"
		if platform != "" {
			msg = "No " + platformNames[platform] + " chats yet"
		}
		if q != "" {
			msg = "No chat matches “" + strings.TrimSpace(m.search.Value()) + "”"
			if platform != "" {
				msg = "No " + platformNames[platform] + " chat matches “" + q + "”"
			}
		}
		results = append(results, "", dimStyle.Render(cw.Center(cw.Truncate(msg, iw-2, "…"), iw)))
	}
	first := max(0, m.foundSel-rows+1)
	for i := first; i < len(m.found) && i < first+rows; i++ {
		results = append(results, m.searchRow(m.found[i], i == m.foundSel, q, iw))
	}
	for len(results) < rows {
		results = append(results, "")
	}
	body = append(body, results...)

	prefixes := []string{}
	for _, p := range platformOrder {
		prefixes = append(prefixes, keyStyle.Render(strings.ToLower(platformTags[p][:1])+":")+" "+dimStyle.Render(platformNames[p]))
	}
	body = append(body, "", " "+strings.Join(prefixes, "   ")+dimStyle.Render("   one app only"))
	return strings.Split(panel("Search", dimStyle.Render("esc to close"), body, w, len(body)+2, true, 3), "\n")
}

// searchRow is one result: avatar, name with the match underlined, the last message if there
// is room, then the unread count and the app.
func (m Model) searchRow(c core.ChatSummary, selected bool, q string, w int) string {
	shade := lipgloss.TerminalColor(lipgloss.NoColor{})
	if selected {
		shade = selectedBg
	}
	bg := on(shade)
	plain := bg(lipgloss.NewStyle())
	edge := plain.Render(" ")
	if selected {
		edge = bg(accentStyle).Render("▌")
	}
	nameStyle := bg(lipgloss.NewStyle().Foreground(secondary))
	if c.Unread > 0 || selected {
		nameStyle = nameStyle.Foreground(text).Bold(true)
	}
	right := bg(lipgloss.NewStyle().Foreground(platformColors[c.Platform])).Render(platformIcons[c.Platform].sp() + platformTags[c.Platform])
	if c.Unread > 0 {
		right = badgeStyle.Render(fmt.Sprint(c.Unread)) + plain.Render("  ") + right
	}
	left := edge + plain.Render(" ") + avatar(chatKey(c.Chat), c.Title()) + plain.Render(" ")
	room := max(1, w-cw.String(left)-cw.String(right)-3)
	name := cw.Truncate(c.Title(), room, "…")
	line := left + highlightMatch(name, q, nameStyle)
	if rest := room - cw.String(name) - 2; rest >= 8 {
		line += bg(dimStyle).Render("  " + cw.Truncate(previewText(c), rest, "…"))
	}
	gap := max(1, w-cw.String(line)-cw.String(right)-1)
	return line + plain.Render(strings.Repeat(" ", gap)) + right + plain.Render(" ")
}

// highlightMatch underlines the first case-insensitive match of the query in s, or of its
// first word when the words are apart.
func highlightMatch(s, query string, base lipgloss.Style) string {
	lower := strings.ToLower(s)
	q := strings.ToLower(strings.TrimSpace(query))
	if !strings.Contains(lower, q) {
		q, _, _ = strings.Cut(q, " ")
	}
	i := strings.Index(lower, q)
	if q == "" || i < 0 || len(lower) != len(s) { // byte offsets only line up when lowering keeps lengths
		return base.Render(s)
	}
	return base.Render(s[:i]) + base.Underline(true).Foreground(accent).Render(s[i:i+len(q)]) + base.Render(s[i+len(q):])
}

// backdrop fades the screen behind the modal to one quiet color, keeping the sidebar shade.
func (m Model) backdrop(lines []string) []string {
	lw := m.leftW()
	side := lipgloss.NewStyle().Foreground(subtle).Background(sidebarBg)
	rest := lipgloss.NewStyle().Foreground(subtle)
	for i, l := range lines {
		plain := cw.Strip(l)
		lines[i] = side.Render(cw.Pad(cw.Truncate(plain, lw, ""), lw)) + rest.Render(cw.Skip(plain, lw))
	}
	return lines
}

// overlay draws block over base with its top left corner at x, y, cutting base lines by cell.
func overlay(base, block []string, x, y int) []string {
	for i, l := range block {
		row := y + i
		if row < 0 || row >= len(base) {
			continue
		}
		bw := cw.String(l)
		base[row] = cw.Pad(cw.Truncate(base[row], x, ""), x) + l + cw.Skip(base[row], x+bw)
	}
	return base
}
