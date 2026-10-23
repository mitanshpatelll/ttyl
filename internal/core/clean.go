package core

import "strings"

// Clean removes characters that could take over the terminal or disguise text: C0 and C1
// control codes (escape sequences can set the clipboard, the window title or fake UI),
// DEL, and Unicode bidi overrides. Newlines and tabs stay. Every string from the network
// goes through it before it is stored or shown.
func Clean(s string) string {
	if strings.IndexFunc(s, unsafeRune) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafeRune(r) {
			return -1
		}
		return r
	}, s)
}

func unsafeRune(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069: // bidi embeddings, overrides, isolates
		return true
	}
	return false
}
