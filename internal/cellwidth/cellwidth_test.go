package cellwidth

import (
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

func TestDefaultMatchesUnicode(t *testing.T) {
	Set(Default)
	for _, s := range []string{"hello", "જય વિર જય જોગમાયા", "ક્ષ", "👨‍👩‍👧", "☺️", "👍🏽", "✅", "🇮🇳", "ä", "नमस्ते", "日本語", "📢 **તમામ વિદ્યાર્થી**"} {
		if got, want := String(s), uniseg.StringWidth(s); got != want {
			t.Errorf("String(%q) = %d, uniseg says %d", s, got, want)
		}
	}
}

func TestModelsFollowTheTerminal(t *testing.T) {
	defer Set(Default)
	Set(Model{SpacingMark: 0, Conjunct: true, JoinEmoji: false, VS16Wide: false, ToneJoins: false, IconWide: true})
	cases := map[string]int{"જો": 1, "ક્ષ": 1, "👨‍👩‍👧": 6, "☺️": 1, "👍🏽": 4, "\uf232": 2, "\U000f05a3 x": 4}
	for s, want := range cases {
		if got := String(s); got != want {
			t.Errorf("String(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestIconsTakeOneCellByDefault(t *testing.T) {
	Set(Default)
	for _, s := range []string{"\uf232", "\uf16d", "\U000f05a3"} {
		if got := String(s); got != 1 {
			t.Errorf("String(%q) = %d, want 1", s, got)
		}
	}
}

func TestEscapesTruncatePadWrap(t *testing.T) {
	Set(Default)
	styled := "\x1b[1mજય\x1b[0m વિર"
	if String(styled) != String("જય વિર") {
		t.Error("escape sequences must not count")
	}
	cut := Truncate("\x1b[31mજય વિર જય\x1b[0m", 5, "…")
	if String(cut) > 5 || !strings.HasSuffix(cut, "…\x1b[0m") {
		t.Errorf("Truncate = %q (%d cells)", cut, String(cut))
	}
	if Truncate("short", 10, "…") != "short" {
		t.Error("short strings are left alone")
	}
	if p := Pad("જો", 5); String(p) != 5 {
		t.Errorf("Pad width %d", String(p))
	}
	if c := Center("ab", 6); c != "  ab  " {
		t.Errorf("Center = %q", c)
	}
	if s := Skip("\x1b[31mab\x1b[0mcd", 3); s != "\x1b[31m\x1b[0md" {
		t.Errorf("Skip kept %q", s)
	}
	if s := Strip("\x1b[1mજય\x1b[0m વિર\x1b]8;;x\x07"); s != "જય વિર" {
		t.Errorf("Strip = %q", s)
	}
	if s := Skip("a👋b", 2); s != " b" { // the emoji's second cell becomes a space
		t.Errorf("Skip through a wide cluster = %q", s)
	}
	lines := Wrap("Chlo bhut kha liya ab thoda rest kr leti hu 👋", 12)
	for _, l := range lines {
		if String(l) > 12 {
			t.Errorf("wrapped line %q is %d cells", l, String(l))
		}
	}
	if strings.Join(lines, " ") != "Chlo bhut kha liya ab thoda rest kr leti hu 👋" {
		t.Errorf("wrap lost text: %q", lines)
	}
	if got := Wrap("https://instagram.com/reel/abcdefghijk", 10); len(got) < 4 {
		t.Errorf("long word not broken: %q", got)
	}
	if got := Wrap("👍", 1); len(got) != 1 || got[0] != "👍" {
		t.Errorf("wide cluster on a 1-cell line: %q", got)
	}
}

func TestParseColumn(t *testing.T) {
	for _, c := range []struct {
		in   string
		col  int
		done bool
		bad  bool
	}{
		{"\x1b[12;3R", 3, true, false},
		{"\x1b[12;", 0, false, false},       // still arriving
		{"junk\x1b[1;17R", 17, true, false}, // other input first
		{"\x1b[5R", 0, true, true},          // no column
	} {
		col, done, err := parseColumn([]byte(c.in))
		if col != c.col || done != c.done || (err != nil) != c.bad {
			t.Errorf("parseColumn(%q) = %d, %v, %v", c.in, col, done, err)
		}
	}
}
