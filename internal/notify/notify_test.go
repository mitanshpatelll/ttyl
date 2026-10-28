package notify

import (
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestPick(t *testing.T) {
	env := func(vars ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(vars); i += 2 {
				if vars[i] == k {
					return vars[i+1]
				}
			}
			return ""
		}
	}
	fallback := Off
	if runtime.GOOS == "darwin" {
		fallback = System
	}
	for _, c := range []struct {
		setting string
		env     func(string) string
		want    Method
	}{
		{"auto", env("TERM_PROGRAM", "ghostty"), OSC777},
		{"auto", env("TERM_PROGRAM", "tmux", "GHOSTTY_RESOURCES_DIR", "/x"), OSC777}, // Ghostty under tmux
		{"auto", env("TERM_PROGRAM", "iTerm.app"), OSC9},
		{"auto", env("TERM", "xterm-kitty"), OSC99},
		{"auto", env("TERM_PROGRAM", "WezTerm"), OSC777},
		{"auto", env("TERM_PROGRAM", "Apple_Terminal"), fallback},
		{"terminal", env("TERM_PROGRAM", "Apple_Terminal"), OSC777},
		{"system", env("TERM_PROGRAM", "ghostty"), System},
		{"off", env("TERM_PROGRAM", "ghostty"), Off},
	} {
		if got := Pick(c.setting, c.env); got != c.want {
			t.Errorf("Pick(%q, %s) = %s, want %s", c.setting, c.env("TERM_PROGRAM"), got, c.want)
		}
	}
}

func TestCleanKeepsEscapeCodesWhole(t *testing.T) {
	got := clean("Mom\x07; \x1b]0;evil\x07\nhi  there", 100)
	if strings.ContainsAny(got, "\x07\x1b;\n") || got != "Mom, ]0,evil hi there" {
		t.Errorf("clean = %q", got)
	}
	if got := clean(strings.Repeat("ab", 50), 10); got != "ababababab…" {
		t.Errorf("long = %q", got)
	}
	if got := Sequence(OSC777, "Mom · WhatsApp", "did you eat?"); got != "\x1b]777;notify;Mom · WhatsApp;did you eat?\x07" {
		t.Errorf("osc777 = %q", got)
	}
	if got := Sequence(OSC9, "Mom", "hi"); got != "\x1b]9;Mom: hi\x07" {
		t.Errorf("osc9 = %q", got)
	}
}

func TestSendWritesThroughTheOutput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out := NewOutput(w)
	n := New(OSC777, out)
	n.tmux = true
	if err := n.Send("Mom", "hi; there"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	got, _ := io.ReadAll(r)
	want := "\x1bPtmux;\x1b\x1b]777;notify;Mom;hi, there\x07\x1b\\"
	if string(got) != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
	if out.Fd() != w.Fd() {
		t.Error("Output must keep the terminal's Fd")
	}

	var ran []string
	n = New(System, out)
	n.run = func(_ context.Context, name string, args ...string) error {
		ran = append([]string{name}, args...)
		return nil
	}
	n.Send(`Mom "M"`, `say \hi`)
	if len(ran) != 3 || ran[0] != "osascript" || ran[2] != `display notification "say \\hi" with title "ttyl" subtitle "Mom \"M\""` {
		t.Errorf("osascript %q", ran)
	}
}
