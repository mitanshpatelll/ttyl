package tui

import (
	"hash/fnv"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// Palette. Every color adapts to light and dark terminals.
var (
	accent     = lipgloss.AdaptiveColor{Light: "#5B4CDB", Dark: "#A79BFF"}
	accentSoft = lipgloss.AdaptiveColor{Light: "#8C82E6", Dark: "#6E63C9"}
	text       = lipgloss.AdaptiveColor{Light: "#1F1F24", Dark: "#E6E6EB"}
	faint      = lipgloss.AdaptiveColor{Light: "#7A7A85", Dark: "#7C7C88"}
	subtle     = lipgloss.AdaptiveColor{Light: "#D5D5DE", Dark: "#34343D"}
	bubbleLine = lipgloss.AdaptiveColor{Light: "#C4C4CF", Dark: "#4A4A56"}
	selectedBg = lipgloss.AdaptiveColor{Light: "#ECEAFB", Dark: "#25223A"}
	green      = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	yellow     = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	red        = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}
	white      = lipgloss.Color("#FFFFFF")

	// People get stable colors (avatars, group sender names).
	personColors = []lipgloss.AdaptiveColor{
		{Light: "#B35900", Dark: "#D9822B"},
		{Light: "#0969DA", Dark: "#3B82D6"},
		{Light: "#8250DF", Dark: "#9A6BE8"},
		{Light: "#BF3989", Dark: "#D05A9E"},
		{Light: "#1A7F37", Dark: "#2EA043"},
		{Light: "#0E7C86", Dark: "#1F9AA5"},
	}

	dimStyle    = lipgloss.NewStyle().Foreground(faint)
	boldStyle   = lipgloss.NewStyle().Bold(true).Foreground(text)
	accentStyle = lipgloss.NewStyle().Foreground(accent)
	tagStyle    = lipgloss.NewStyle().Foreground(accent).Italic(true)
	errorStyle  = lipgloss.NewStyle().Foreground(red)
	keyStyle    = lipgloss.NewStyle().Foreground(text).Bold(true)
	logoStyle   = lipgloss.NewStyle().Bold(true).Foreground(white).Background(accent).Padding(0, 1)
	badgeStyle  = lipgloss.NewStyle().Bold(true).Foreground(white).Background(accent).Padding(0, 1)
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

// avatar is a three-cell colored block with the chat's initials.
func avatar(id, name string) string {
	return lipgloss.NewStyle().Width(3).Align(lipgloss.Center).Bold(true).
		Foreground(white).Background(personColor(id)).Render(initials(name))
}

// initials: "Weekend trip" → "WT", "Mom" → "M", "+15551234" → "#".
func initials(name string) string {
	var out []rune
	for _, word := range strings.Fields(name) {
		r := []rune(word)[0]
		if !unicode.IsLetter(r) {
			if len(out) == 0 && (r == '+' || unicode.IsDigit(r)) {
				return "#"
			}
			continue
		}
		out = append(out, unicode.ToUpper(r))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
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
