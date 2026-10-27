// Package cellwidth measures how many terminal cells text takes, the way the terminal
// actually draws it.
//
// Terminals disagree on scripts like Gujarati or Hindi (do vowel signs and conjuncts take
// their own cell?) and on emoji sequences (is a family emoji one glyph or three?). A wrong
// guess pushes every following character on the line and breaks the layout, so ttyl probes
// the terminal once at startup (Calibrate) and measures everything with the result.
package cellwidth

import (
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Model describes how the terminal draws a grapheme cluster beyond its first character.
type Model struct {
	SpacingMark int  // cells per spacing vowel sign (Unicode Mc), e.g. ો in જો
	Conjunct    bool // consonants joined by a virama share one cell, e.g. ક્ષ
	JoinEmoji   bool // emoji joined by ZWJ draw as one glyph, e.g. 👨‍👩‍👧
	VS16Wide    bool // U+FE0F makes a narrow symbol wide, e.g. ☺️
	ToneJoins   bool // skin tone modifiers merge into the emoji, e.g. 👍🏽
	IconWide    bool // private-use characters (Nerd Font icons) take two cells
}

// Default matches the Unicode rules modern terminals (Ghostty, WezTerm, kitty) follow.
var Default = Model{SpacingMark: 1, JoinEmoji: true, VS16Wide: true, ToneJoins: true}

var current = Default

// Set installs the model used by every measurement. Call it once, before rendering starts.
func Set(m Model) { current = m }

func Current() Model { return current }

// String is the width of s in cells. ANSI escape sequences take no space.
func String(s string) int {
	w := 0
	forEach(s, func(seq string, isEscape bool) bool {
		if !isEscape {
			w += clusters(seq)
		}
		return true
	})
	return w
}

func clusters(s string) int {
	w := 0
	eachCluster(s, func(_ string, cw int) bool {
		w += cw
		return true
	})
	return w
}

// eachCluster walks the grapheme clusters of plain text with their widths. A cluster that
// ends in a virama is joined to the next one when the terminal draws conjuncts as one cell
// (uniseg splits Indic conjuncts that newer Unicode keeps together).
func eachCluster(s string, f func(c string, w int) bool) {
	state, afterVirama := -1, false
	for s != "" {
		var c string
		c, s, _, state = uniseg.FirstGraphemeClusterInString(s, state)
		w := current.cluster(c)
		if afterVirama && current.Conjunct {
			w = max(0, w-runeWidth([]rune(c)[0]))
		}
		r := []rune(c)
		afterVirama = isVirama(r[len(r)-1])
		if !f(c, w) {
			return
		}
	}
}

// isVirama matches the virama of the main Indic scripts (Devanagari … Malayalam).
func isVirama(r rune) bool { return r >= 0x0900 && r <= 0x0DFF && r&0x7f == 0x4d }

func (m Model) cluster(c string) int {
	runes := []rune(c)
	w := runeWidth(runes[0])
	if isPrivateUse(runes[0]) {
		w = 1
		if m.IconWide {
			w = 2
		}
	}
	joined := false
	for _, r := range runes[1:] {
		switch {
		case r == 0x200D: // zero width joiner
			joined = true
		case r == 0xFE0F:
			if m.VS16Wide && w == 1 {
				w = 2
			}
		case r >= 0xFE00 && r <= 0xFE0E, unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		case r >= 0x1F3FB && r <= 0x1F3FF: // skin tones
			if !m.ToneJoins {
				w += 2
			}
		case unicode.Is(unicode.Mc, r):
			w += m.SpacingMark
		case joined:
			if !m.JoinEmoji {
				w += runeWidth(r)
			}
			joined = false
		case r >= 0x1F1E6 && r <= 0x1F1FF: // second half of a flag
		default: // a consonant joined by a virama
			if !m.Conjunct {
				w += runeWidth(r)
			}
		}
	}
	return w
}

// isPrivateUse spots icon fonts' code points, which Unicode leaves to the font.
func isPrivateUse(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0xFFFFD) || (r >= 0x100000 && r <= 0x10FFFD)
}

func runeWidth(r rune) int {
	switch {
	case r < 0x20 || r == 0x7f:
		return 0
	case r >= 0x1F1E6 && r <= 0x1F1FF: // regional indicators: a flag takes two cells
		return 2
	}
	if w := runewidth.RuneWidth(r); w > 0 {
		return w
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	return 1
}

// forEach walks s, handing over runs of text and whole escape sequences (CSI and OSC).
func forEach(s string, f func(seq string, isEscape bool) bool) {
	for s != "" {
		i := strings.IndexByte(s, 0x1b)
		if i < 0 {
			f(s, false)
			return
		}
		if i > 0 && !f(s[:i], false) {
			return
		}
		n := escapeLen(s[i:])
		if !f(s[i:i+n], true) {
			return
		}
		s = s[i+n:]
	}
}

func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[': // CSI: parameters, then a final byte 0x40–0x7e
		for j := 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
	case ']': // OSC: ends with BEL or ESC \
		for j := 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
	default:
		return 2
	}
	return len(s)
}

// Truncate cuts s to at most n cells, ending with tail when something was cut. Escape
// sequences are kept, and styles are reset after a cut so they can't leak.
func Truncate(s string, n int, tail string) string {
	if String(s) <= n {
		return s
	}
	budget := n - String(tail)
	if budget < 0 {
		budget, tail = n, ""
	}
	var b strings.Builder
	used, styled := 0, false
	forEach(s, func(seq string, isEscape bool) bool {
		if isEscape {
			b.WriteString(seq)
			styled = true
			return true
		}
		fits := true
		eachCluster(seq, func(c string, cw int) bool {
			if used+cw > budget {
				fits = false
				return false
			}
			b.WriteString(c)
			used += cw
			return true
		})
		return fits
	})
	b.WriteString(tail)
	if styled {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// Pad fills s with spaces on the right up to n cells (truncating if it is longer).
func Pad(s string, n int) string {
	w := String(s)
	if w > n {
		return Truncate(s, n, "")
	}
	return s + strings.Repeat(" ", n-w)
}

// Center pads s on both sides to n cells.
func Center(s string, n int) string {
	w := String(s)
	if w >= n {
		return Truncate(s, n, "")
	}
	left := (n - w) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", n-w-left)
}

// Wrap breaks plain text (no escape sequences) into lines of at most n cells, at spaces
// where possible. Existing line breaks are kept.
func Wrap(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line, lineW, emitted := "", 0, false
		flush := func() {
			out = append(out, strings.TrimRight(line, " "))
			line, lineW, emitted = "", 0, true
		}
		for _, word := range strings.Split(para, " ") {
			ww := String(word)
			sep := 0
			if line != "" {
				sep = 1
			}
			if lineW+sep+ww <= n {
				if sep == 1 {
					line += " "
				}
				line += word
				lineW += sep + ww
				continue
			}
			if line != "" {
				flush()
			}
			for ww > n { // a word longer than a line: break it by clusters
				head := Truncate(word, n, "")
				if head == "" { // one cluster wider than the line: it gets a line of its own
					head, _, _, _ = uniseg.FirstGraphemeClusterInString(word, -1)
				}
				out = append(out, head)
				emitted = true
				word = word[len(head):]
				ww = String(word)
			}
			line, lineW = word, ww
		}
		if line != "" || !emitted {
			flush()
		}
	}
	return out
}
