package tui

import "github.com/mitanshpatelll/ttyl/internal/core"

// Nerd Font icons: Font Awesome code points, which every Nerd Font ships. With icons off
// (ui.icons = false in the config) each falls back to its plain form, often nothing.

var useIcons = true

// SetIcons turns Nerd Font icons on or off. Call it before the UI starts.
func SetIcons(on bool) { useIcons = on }

type icon struct{ glyph, plain string }

var (
	iconWhatsApp  = icon{"", ""}
	iconInstagram = icon{"", ""}
	iconTelegram  = icon{"", ""}
	iconUser      = icon{"", ""}
	iconGroup     = icon{"", ""}
	iconPhone     = icon{"", ""}
	iconSearch    = icon{"", "/"}
	iconCheck     = icon{"", "✓"}
	iconInbox     = icon{"", ""}
	iconInfo      = icon{"", ""}
	iconPhoto     = icon{"", ""}
	iconVideo     = icon{"", ""}
	iconVoice     = icon{"", ""}
	iconAudio     = icon{"", ""}
	iconFile      = icon{"", ""}
	iconLink      = icon{"", ""}
	iconSticker   = icon{"", ""}
	iconLocation  = icon{"", ""}
	iconContact   = icon{"", ""}
	iconPoll      = icon{"", ""}
	iconReel      = icon{"", ""}
	iconDeleted   = icon{"", ""}
)

var platformIcons = map[core.PlatformID]icon{
	core.WhatsApp:  iconWhatsApp,
	core.Instagram: iconInstagram,
	core.Telegram:  iconTelegram,
}

// sp is the icon and a space, or the plain fallback and a space, or "" when there is neither.
// The space matters: Nerd Font glyphs draw wider than their cell and need room on the right.
func (i icon) sp() string {
	switch {
	case useIcons:
		return i.glyph + " "
	case i.plain != "":
		return i.plain + " "
	}
	return ""
}

// kindIcon is the icon for a kind of message, if it has one.
func kindIcon(msg core.Message) icon {
	switch msg.Kind {
	case core.KindImage:
		return iconPhoto
	case core.KindVideo, core.KindGIF:
		return iconVideo
	case core.KindVoice:
		return iconVoice
	case core.KindAudio:
		return iconAudio
	case core.KindDocument:
		return iconFile
	case core.KindSticker:
		return iconSticker
	case core.KindLocation:
		return iconLocation
	case core.KindContact:
		return iconContact
	case core.KindPoll:
		return iconPoll
	case core.KindLink:
		if isShortVideo(msg.Meta) {
			return iconReel
		}
		return iconLink
	}
	return icon{}
}
