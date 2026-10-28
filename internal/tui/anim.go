package tui

import (
	"fmt"
	"math"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"

	"github.com/mitanshpatelll/ttyl/internal/core"
)

// Motion. A few things move instead of jumping: the details panel slides in and out, the
// chat list slides in after the startup loader, the chat scrolls smoothly, the search box
// drops in and the loader's bars ease forward. Springs drive them at 60 frames a second,
// only while something is moving. Resizing the window stays instant, and
// [ui] animations = false turns it all off.

var useAnimations = true

// SetAnimations turns motion on or off. Call it before the UI starts.
func SetAnimations(on bool) { useAnimations = on }

const fps = 60

type frameMsg struct{}

// spring is one number heading for a target, critically damped: it eases in without bouncing.
type spring struct {
	pos, vel, target float64
	s                harmonica.Spring
}

func newSpring(speed float64) spring {
	return spring{s: harmonica.NewSpring(harmonica.FPS(fps), speed, 1)}
}

func (a *spring) snap(v float64) { a.pos, a.vel, a.target = v, 0, v }

// step moves one frame and reports whether it is still moving; close enough snaps to the end.
func (a *spring) step() bool {
	a.pos, a.vel = a.s.Update(a.pos, a.vel, a.target)
	if math.Abs(a.pos-a.target) < 0.4 && math.Abs(a.vel) < 2 {
		a.pos, a.vel = a.target, 0
		return false
	}
	return true
}

func (a spring) moving() bool { return a.pos != a.target || a.vel != 0 }
func (a spring) at() int      { return int(math.Round(a.pos)) }

type motion struct {
	on           bool   // springs follow the layout (animations on and started)
	ticking      bool   // a frame is due
	side, panel  spring // shown widths of the chat list and the details panel
	panelFull    int    // the panel's width when open: it slides, it doesn't squeeze
	scroll       spring // the chat's shown scroll position
	scrollKey    string // a different chat, width or history means jump, not scroll
	search       spring // 0 → 100 as the search box comes in
	bars         map[core.PlatformID]*spring
	twoPaneShown bool
}

func newMotion() motion {
	// Speeds: about a third of a second to settle, quick enough not to wait on.
	return motion{side: newSpring(18), panel: newSpring(18), scroll: newSpring(20), search: newSpring(22),
		bars: map[core.PlatformID]*spring{}}
}

// retarget points the springs at the new layout after an update. A resize, an unfocused
// terminal or animations off make everything jump there instead.
func (m *Model) retarget(msg tea.Msg) tea.Cmd {
	_, resized := msg.(tea.WindowSizeMsg)
	a := &m.anim
	jump := !useAnimations || !m.focused || resized || !a.on
	a.on = useAnimations

	side, panel := float64(m.targetLeftW()), float64(m.targetDetailsW())
	if !m.ready {
		side = 0 // the list slides in when the loader goes
	}
	// The sliding needs two panes before and after; one pane swaps at once.
	slide := m.twoPane() && a.twoPaneShown
	a.twoPaneShown = m.twoPane()
	if jump || !slide {
		a.side.snap(side)
		a.panel.snap(panel)
	} else {
		a.side.target, a.panel.target = side, panel
	}
	if panel > 0 {
		a.panelFull = int(panel)
	}

	if m.chatW() != m.chatView.Width { // the panes jumped: lay the chat out at its new width
		m.relayout()
	}
	key := m.scrollKeyNow()
	if jump || key != a.scrollKey {
		a.scroll.snap(float64(m.chatView.YOffset))
	} else {
		a.scroll.target = float64(m.chatView.YOffset)
	}
	a.scrollKey = key

	if m.focus != focusSearch || jump {
		a.search.snap(0)
		if m.focus == focusSearch {
			a.search.snap(100)
		}
	} else {
		a.search.target = 100
	}

	for id := range a.bars {
		if _, ok := m.inbox.Sync[id]; !ok {
			delete(a.bars, id)
		}
	}
	for id, s := range m.inbox.Sync {
		p, _ := syncPercent(s)
		b := a.bars[id]
		if b == nil {
			sp := newSpring(8) // bars take their time: they are only there to watch
			b = &sp
			b.snap(0)
			a.bars[id] = b
		}
		if jump {
			b.snap(float64(p))
		} else {
			b.target = float64(p)
		}
	}
	return m.nextFrame()
}

func (m *Model) moving() bool {
	a := &m.anim
	if a.side.moving() || a.panel.moving() || a.scroll.moving() || a.search.moving() {
		return true
	}
	for _, b := range a.bars {
		if b.moving() {
			return true
		}
	}
	return false
}

func (m *Model) nextFrame() tea.Cmd {
	if m.anim.ticking || !m.moving() {
		return nil
	}
	m.anim.ticking = true
	return tea.Tick(time.Second/fps, func(time.Time) tea.Msg { return frameMsg{} })
}

// frame moves every spring one step. When the panes change width the chat is laid out again
// and its scroll jumps with it: the text reflowed, so the old position means nothing.
func (m Model) frame() (tea.Model, tea.Cmd) {
	a := &m.anim
	a.ticking = false
	a.side.step()
	a.panel.step()
	a.search.step()
	for _, b := range a.bars {
		b.step()
	}
	if m.chatW() != m.chatView.Width {
		m.relayout()
	} else {
		a.scroll.step()
	}
	return m, m.nextFrame()
}

// relayout lays the chat out at the shown width; its scroll jumps along, since the text reflowed.
func (m *Model) relayout() {
	m.layout()
	m.anim.scroll.snap(float64(m.chatView.YOffset))
	m.anim.scrollKey = m.scrollKeyNow()
}

// scrollKeyNow names what the chat shows: when it changes, the scroll jumps instead of easing.
func (m Model) scrollKeyNow() string {
	if m.open == nil || len(m.messages) == 0 {
		return ""
	}
	return fmt.Sprint(chatKey(*m.open), m.chatView.Width, m.messages[0].ID)
}

// scrollY is the first chat line shown.
func (m Model) scrollY() int {
	if !m.anim.on {
		return m.chatView.YOffset
	}
	return m.anim.scroll.at()
}

// barPercent is how full a loader bar is drawn.
func (m Model) barPercent(id core.PlatformID, s core.SyncEvent) int {
	if b := m.anim.bars[id]; m.anim.on && b != nil {
		return b.at()
	}
	p, _ := syncPercent(s)
	return p
}

// searchIn is how far the search box has come in, 0 to 1.
func (m Model) searchIn() float64 {
	if !m.anim.on {
		return 1
	}
	return min(1, max(0, m.anim.search.pos/100))
}

// blend mixes two colors for this terminal's background: t = 0 is a, 1 is b.
func blend(a, b lipgloss.AdaptiveColor, t float64) lipgloss.Color {
	pick := func(c lipgloss.AdaptiveColor) string {
		if lipgloss.HasDarkBackground() {
			return c.Dark
		}
		return c.Light
	}
	var x, y [3]int
	fmt.Sscanf(pick(a), "#%02x%02x%02x", &x[0], &x[1], &x[2])
	fmt.Sscanf(pick(b), "#%02x%02x%02x", &y[0], &y[1], &y[2])
	mix := func(i int) int { return x[i] + int(math.Round(float64(y[i]-x[i])*t)) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", mix(0), mix(1), mix(2)))
}
