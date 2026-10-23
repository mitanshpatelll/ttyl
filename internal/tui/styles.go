package tui

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
)

var (
	accent = lipgloss.AdaptiveColor{Light: "#5B4CDB", Dark: "#A79BFF"}
	faint  = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6E6E6E"}
	subtle = lipgloss.AdaptiveColor{Light: "#D0D0D0", Dark: "#3A3A3A"}
	green  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	yellow = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	red    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}

	senderColors = []lipgloss.AdaptiveColor{
		{Light: "#B35900", Dark: "#F0883E"},
		{Light: "#0969DA", Dark: "#58A6FF"},
		{Light: "#8250DF", Dark: "#D2A8FF"},
		{Light: "#BF3989", Dark: "#F778BA"},
		{Light: "#1A7F37", Dark: "#56D364"},
		{Light: "#0E7C86", Dark: "#39C5CF"},
	}

	dimStyle    = lipgloss.NewStyle().Foreground(faint)
	boldStyle   = lipgloss.NewStyle().Bold(true)
	accentStyle = lipgloss.NewStyle().Foreground(accent)
	tagStyle    = lipgloss.NewStyle().Foreground(accent).Italic(true)
	errorStyle  = lipgloss.NewStyle().Foreground(red)
	logoStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
	badgeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 1)
)

func paneStyle(active bool) lipgloss.Style {
	border := subtle
	if active {
		border = accent
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border)
}

// senderStyle gives each group member a stable color.
func senderStyle(id string) lipgloss.Style {
	h := fnv.New32a()
	h.Write([]byte(id))
	return lipgloss.NewStyle().Bold(true).Foreground(senderColors[h.Sum32()%uint32(len(senderColors))])
}
