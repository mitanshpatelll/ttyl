package tui

import (
	"hash/fnv"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// Palette. Every color adapts to light and dark terminals. Areas are told apart by shade,
// not by boxes: the sidebar, the selected row and the message input each have a background.
var (
	accent     = lipgloss.AdaptiveColor{Light: "#5B4CDB", Dark: "#A79BFF"}
	accentSoft = lipgloss.AdaptiveColor{Light: "#8C82E6", Dark: "#6E63C9"}
	text       = lipgloss.AdaptiveColor{Light: "#1F1F24", Dark: "#ECECF1"}
	secondary  = lipgloss.AdaptiveColor{Light: "#4E5058", Dark: "#B5BAC1"} // previews: readable, quieter than text
	faint      = lipgloss.AdaptiveColor{Light: "#6D6F78", Dark: "#949BA4"} // timestamps, hints
	subtle     = lipgloss.AdaptiveColor{Light: "#D5D5DE", Dark: "#3A3C45"}
	bubbleLine = lipgloss.AdaptiveColor{Light: "#C4C4CF", Dark: "#4A4A56"}
	sidebarBg  = lipgloss.AdaptiveColor{Light: "#F2F3F5", Dark: "#1E1F24"}
	selectedBg = lipgloss.AdaptiveColor{Light: "#DCDDF5", Dark: "#33344A"}
	inputBg    = lipgloss.AdaptiveColor{Light: "#EBEDEF", Dark: "#2A2B33"}
	green      = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	yellow     = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	red        = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F23F43"}
	seenBlue   = lipgloss.AdaptiveColor{Light: "#0969DA", Dark: "#53BDEB"} // the blue ticks
	white      = lipgloss.Color("#FFFFFF")

	// People get stable, bright colors (avatars and names).
	personColors = []lipgloss.AdaptiveColor{
		{Light: "#B35900", Dark: "#F0A35E"},
		{Light: "#0969DA", Dark: "#5DADF5"},
		{Light: "#8250DF", Dark: "#B794F6"},
		{Light: "#BF3989", Dark: "#F47FB8"},
		{Light: "#1A7F37", Dark: "#57D17A"},
		{Light: "#0E7C86", Dark: "#4FD1D9"},
	}

	dimStyle    = lipgloss.NewStyle().Foreground(faint)
	boldStyle   = lipgloss.NewStyle().Bold(true).Foreground(text)
	accentStyle = lipgloss.NewStyle().Foreground(accent)
	tagStyle    = lipgloss.NewStyle().Foreground(accent).Italic(true)
	errorStyle  = lipgloss.NewStyle().Foreground(red)
	keyStyle    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	logoStyle   = lipgloss.NewStyle().Bold(true).Foreground(white).Background(accent).Padding(0, 1)
	badgeStyle  = lipgloss.NewStyle().Bold(true).Foreground(white).Background(red).Padding(0, 1)
)

func personColor(id string) lipgloss.AdaptiveColor {
	h := fnv.New32a()
	h.Write([]byte(id))
	return personColors[h.Sum32()%uint32(len(personColors))]
}

// senderStyle gives each group member a stable color.
func senderStyle(id string) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(personColor(id))
}

// initialsOf: "Weekend trip" → "WT", "Mummy" → "MU", "+15551234" → "#". Scripts without
// capitals (Gujarati, Hindi) give one letter per word.
func initialsOf(name string) string {
	var out []rune
	var first []rune // the word the first initial came from
	for _, word := range strings.Fields(name) {
		letters := []rune(word)
		if !unicode.IsLetter(letters[0]) {
			if len(out) == 0 && (letters[0] == '+' || unicode.IsDigit(letters[0])) {
				return "#"
			}
			continue
		}
		if first == nil {
			first = letters
		}
		out = append(out, unicode.ToUpper(letters[0]))
		if len(out) == 2 {
			return string(out)
		}
	}
	if len(out) == 0 {
		return "?"
	}
	if len(first) > 1 && unicode.IsLetter(first[1]) && unicode.ToUpper(first[1]) != unicode.ToLower(first[1]) {
		out = append(out, unicode.ToUpper(first[1]))
	}
	return string(out)
}

// keyHints renders "key label" pairs with the keys bright and the labels quiet.
func keyHints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, keyStyle.Render(pairs[i])+" "+dimStyle.Render(pairs[i+1]))
	}
	return strings.Join(parts, dimStyle.Render("  ·  "))
}
