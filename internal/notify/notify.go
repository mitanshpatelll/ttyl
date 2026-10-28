// Package notify shows desktop notifications the way the terminal prefers. Ghostty, iTerm2,
// WezTerm and kitty draw their own (escape codes OSC 777, 9 and 99): native notifications
// that name the terminal and bring its window back when clicked. Elsewhere on macOS ttyl
// asks osascript.
package notify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Output is the terminal the UI draws on, with one lock around every write: a notification's
// escape code then goes between two frames, never into the middle of one. It keeps Fd, so
// Bubble Tea still sees a terminal.
type Output struct {
	mu sync.Mutex
	f  *os.File
}

func NewOutput(f *os.File) *Output { return &Output{f: f} }

func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.f.Write(p)
}

func (o *Output) Read(p []byte) (int, error) { return o.f.Read(p) }
func (o *Output) Close() error               { return o.f.Close() }
func (o *Output) Fd() uintptr                { return o.f.Fd() }

// Method is how notifications are shown.
type Method string

const (
	OSC777 Method = "osc777" // Ghostty, WezTerm: title and body
	OSC9   Method = "osc9"   // iTerm2: one line
	OSC99  Method = "osc99"  // kitty
	System Method = "system" // osascript on macOS
	Off    Method = "off"
)

// Pick resolves the [notify] desktop setting: "auto" looks at the terminal ttyl runs in,
// "terminal" insists on escape codes, "system" on osascript, "off" shows nothing.
func Pick(setting string, env func(string) string) Method {
	switch setting {
	case "off":
		return Off
	case "system":
		return System
	}
	term, prog := env("TERM"), env("TERM_PROGRAM")
	switch {
	case prog == "ghostty" || term == "xterm-ghostty" || env("GHOSTTY_RESOURCES_DIR") != "":
		return OSC777
	case prog == "WezTerm":
		return OSC777
	case prog == "iTerm.app" || env("LC_TERMINAL") == "iTerm2":
		return OSC9
	case term == "xterm-kitty" || env("KITTY_WINDOW_ID") != "":
		return OSC99
	case setting == "terminal":
		return OSC777 // the most widely understood of the three
	case runtime.GOOS == "darwin":
		return System
	}
	return Off
}

// Notifier sends notifications by one method.
type Notifier struct {
	method Method
	out    *Output
	tmux   bool
	run    func(ctx context.Context, name string, args ...string) error // osascript, swappable in tests
}

func New(method Method, out *Output) *Notifier {
	return &Notifier{method: method, out: out, tmux: os.Getenv("TMUX") != "",
		run: func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		}}
}

func (n *Notifier) Method() Method { return n.method }

// Send shows one notification: a title (who) and a body (what they said).
func (n *Notifier) Send(title, body string) error {
	title, body = clean(title, 60), clean(body, 180)
	if n.method == System {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		script := fmt.Sprintf("display notification %s with title %s subtitle %s", quote(body), quote("ttyl"), quote(title))
		return n.run(ctx, "osascript", "-e", script)
	}
	seq := Sequence(n.method, title, body)
	if seq == "" {
		return nil
	}
	if n.tmux { // tmux passes it on to the real terminal (needs allow-passthrough on)
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	_, err := n.out.Write([]byte(seq))
	return err
}

// Sequence is the escape code that asks the terminal for a notification.
func Sequence(m Method, title, body string) string {
	switch m {
	case OSC777:
		return "\x1b]777;notify;" + title + ";" + body + "\x07"
	case OSC9:
		return "\x1b]9;" + title + ": " + body + "\x07"
	case OSC99:
		return "\x1b]99;i=ttyl:d=0;" + title + "\x1b\\" + "\x1b]99;i=ttyl:p=body;" + body + "\x1b\\"
	}
	return ""
}

// clean keeps a notification to one line of plain text: no control characters (which could
// end the escape code early), no ";" (OSC 777 separates fields with it), no Nerd Font icons
// (the notification's font doesn't have them), at most n characters.
func clean(s string, n int) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		switch {
		case r == ';':
			r = ','
		case unicode.IsSpace(r):
			r = ' '
		case unicode.IsControl(r), r >= 0xE000 && r <= 0xF8FF, r >= 0xF0000:
			continue
		}
		if r == ' ' && (b.Len() == 0 || strings.HasSuffix(b.String(), " ")) {
			continue
		}
		if count == n {
			return strings.TrimSpace(b.String()) + "…"
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}

// quote makes an AppleScript string literal.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
