package tui

import (
	"cmp"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/kyokomi/emoji/v2"

	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
)

// Emoji by name while typing, like Discord and Slack: ":jo" lists 😂 joy, 😹 joy_cat… above
// the input; tab or enter puts the chosen one in, and a finished ":joy:" turns into 😂 by itself.

type emojiHit struct{ name, emoji string }

const emojiRows = 6

// emojiIndex is every shortcode, sorted by name. Skin tone variants ("thumbsup_tone3") and
// the bare modifiers are left out, so a list shows different emoji, not five of one.
var emojiIndex = sync.OnceValue(func() []emojiHit {
	var out []emojiHit
	for code, e := range emoji.CodeMap() {
		name := strings.Trim(code, ":")
		if strings.HasPrefix(name, "skin-tone") || strings.Contains(name, "skin_tone") || isToneVariant(name) {
			continue
		}
		out = append(out, emojiHit{name, e})
	}
	slices.SortFunc(out, func(a, b emojiHit) int { return cmp.Compare(a.name, b.name) })
	return out
})

// emojiPopular come first among equally good matches: what people send most in chats.
var emojiPopular = func() map[string]int {
	names := strings.Fields(`joy heart +1 thumbsup sob smile pray fire rofl heart_eyes blush ok_hand wave tada 100
		laughing sweat_smile thinking eyes clap muscle grin wink kissing_heart relaxed sunglasses cry rage skull
		sparkles raised_hands partying_face pleading_face white_check_mark x star_struck upside_down_face hugs
		facepalm shrug see_no_evil v purple_heart broken_heart smirk unamused yum sleeping grinning smiley`)
	m := make(map[string]int, len(names))
	for i, n := range names {
		m[n] = i + 1
	}
	return m
}()

func isToneVariant(name string) bool {
	i := strings.LastIndex(name, "_tone")
	return i > 0 && len(name) == i+6 && name[i+5] >= '1' && name[i+5] <= '5'
}

func isEmojiNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '+' || r == '-'
}

// emojiQuery finds the ":name" being typed right before the cursor (pos, in runes): where its
// colon is and the name. The colon has to start a word, so "10:30" asks for nothing, and the
// name needs two characters.
func emojiQuery(v []rune, pos int) (start int, name string, ok bool) {
	i := pos
	for i > 0 && isEmojiNameRune(v[i-1]) {
		i--
	}
	if i == 0 || v[i-1] != ':' || pos-i < 2 || (i > 1 && !unicode.IsSpace(v[i-2])) {
		return 0, "", false
	}
	return i - 1, strings.ToLower(string(v[i:pos])), true
}

// emojiMatches ranks the names for a query: exact, then starting with it, then a word of the
// name starting with it ("cat" finds joy_cat), then anywhere; popular and shorter names
// first within each. Every emoji shows once, under its best name.
func emojiMatches(q string, limit int) []emojiHit {
	type hit struct {
		emojiHit
		rank int
	}
	var hits []hit
	for _, e := range emojiIndex() {
		rank := 0
		switch i := strings.Index(e.name, q); {
		case i < 0:
			continue
		case e.name == q:
			rank = 0
		case i == 0:
			rank = 1
		case strings.Contains(e.name, "_"+q) || strings.Contains(e.name, "-"+q):
			rank = 2
		default:
			rank = 3
		}
		hits = append(hits, hit{e, rank})
	}
	popular := func(name string) int {
		if p, ok := emojiPopular[name]; ok {
			return p
		}
		return len(emojiPopular) + 1
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(a.rank-b.rank, popular(a.name)-popular(b.name), len(a.name)-len(b.name), cmp.Compare(a.name, b.name))
	})
	seen := map[string]bool{}
	var out []emojiHit
	for _, h := range hits {
		if seen[h.emoji] {
			continue
		}
		seen[h.emoji] = true
		if out = append(out, h.emojiHit); len(out) == limit {
			break
		}
	}
	return out
}

// findEmoji updates the suggestions for what is typed, unless they were dismissed for it.
func (m *Model) findEmoji() {
	m.emoji, m.emojiSel = nil, 0
	if m.compose.Value() == m.emojiDismissed {
		return
	}
	m.emojiDismissed = ""
	if _, q, ok := emojiQuery([]rune(m.compose.Value()), m.compose.Position()); ok {
		m.emoji = emojiMatches(q, 30)
	}
}

// replaceBeforeCursor swaps the runes from start to the cursor for text.
func (m *Model) replaceBeforeCursor(start int, text string) {
	v, pos := []rune(m.compose.Value()), m.compose.Position()
	ins := []rune(text)
	m.compose.SetValue(string(slices.Concat(v[:start], ins, v[pos:])))
	m.compose.SetCursor(start + len(ins))
}

// pickEmoji puts the chosen emoji in place of ":name", with a space after it.
func (m *Model) pickEmoji() {
	start, _, ok := emojiQuery([]rune(m.compose.Value()), m.compose.Position())
	if !ok || m.emojiSel >= len(m.emoji) {
		return
	}
	text := m.emoji[m.emojiSel].emoji
	if rest := []rune(m.compose.Value())[m.compose.Position():]; len(rest) == 0 || rest[0] != ' ' {
		text += " "
	}
	m.replaceBeforeCursor(start, text)
	m.emoji = nil
}

// completeEmoji turns a just-finished ":joy:" into 😂.
func (m *Model) completeEmoji() {
	v, pos := []rune(m.compose.Value()), m.compose.Position()
	if pos < 2 || v[pos-1] != ':' {
		return
	}
	start, name, ok := emojiQuery(v, pos-1)
	if !ok {
		return
	}
	if e, ok := emoji.CodeMap()[":"+name+":"]; ok {
		m.replaceBeforeCursor(start, e)
	}
}

// emojiBox is the suggestion list over the bottom of the chat, lined up with the input: a
// heading and up to room-1 emoji, scrolled to keep the chosen one in view.
func (m Model) emojiBox(w, room int) []string {
	rows := min(len(m.emoji), emojiRows, room-1)
	if rows < 1 {
		return nil
	}
	_, q, _ := emojiQuery([]rune(m.compose.Value()), m.compose.Position())
	inner := w - 4
	line := func(shade lipgloss.TerminalColor, s string) string {
		return "  " + fill(s, inner, shade) + "  "
	}
	bg := on(sidebarBg)
	head := bg(dimStyle.Bold(true)).Render(" EMOJI MATCHING ") + bg(accentStyle).Render(":"+q)
	out := []string{line(sidebarBg, spread(head, bg(dimStyle).Render("tab pick "), inner))}
	first := max(0, m.emojiSel-rows+1)
	for i := first; i < first+rows; i++ {
		e, shade := m.emoji[i], lipgloss.TerminalColor(sidebarBg)
		edge := on(shade)(lipgloss.NewStyle()).Render(" ")
		if i == m.emojiSel {
			shade = selectedBg
			edge = on(shade)(accentStyle).Render("▌")
		}
		plain := on(shade)(lipgloss.NewStyle())
		name := highlightMatch(":"+e.name+":", q, plain.Foreground(text))
		out = append(out, line(shade, edge+plain.Render(" "+cw.Pad(e.emoji, 2)+"  ")+name))
	}
	return out
}
