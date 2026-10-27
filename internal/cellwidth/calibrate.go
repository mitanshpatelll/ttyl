package cellwidth

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Probes are drawn once at startup; the cursor position after each tells how wide the
// terminal really made it.
var probes = []struct {
	name, text string
}{
	{"spacing mark", "જો"}, // consonant + vowel sign: 1 or 2 cells
	{"conjunct", "ક્ષ"},    // consonant + virama + consonant: 1 or 2
	{"emoji zwj", "👨‍👩‍👧"}, // 2 if joined, 6 if not
	{"vs16", "☺️"},         // 2 if the variation selector widens it, else 1
	{"skin tone", "👍🏽"},    // 2 if the tone merges, else 4
	{"icon", "\uf232"},     // a Nerd Font icon (private use): 1, or 2 in some setups
}

// Calibrate measures the terminal on in/out (a TTY) and returns the matching model and
// the raw widths. It draws on the current line and clears it again.
func Calibrate(in, out *os.File, timeout time.Duration) (Model, map[string]int, error) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return Default, nil, errors.New("not a terminal")
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return Default, nil, err
	}
	defer term.Restore(fd, state)
	defer fmt.Fprint(out, "\r\x1b[2K")

	widths := map[string]int{}
	for _, p := range probes {
		fmt.Fprintf(out, "\r\x1b[2K%s\x1b[6n", p.text)
		col, err := readColumn(fd, timeout)
		if err != nil {
			return Default, widths, fmt.Errorf("%s: %w", p.name, err)
		}
		widths[p.name] = col - 1
	}
	m := Model{
		SpacingMark: min(max(widths["spacing mark"]-1, 0), 1),
		Conjunct:    widths["conjunct"] == 1,
		JoinEmoji:   widths["emoji zwj"] == 2,
		VS16Wide:    widths["vs16"] == 2,
		ToneJoins:   widths["skin tone"] == 2,
		IconWide:    widths["icon"] == 2,
	}
	return m, widths, nil
}

// readColumn waits for the terminal's cursor position report, ESC [ row ; col R.
func readColumn(fd int, timeout time.Duration) (int, error) {
	var resp []byte
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 32)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return 0, errors.New("no answer from the terminal")
		}
		// Poll first, so nothing is left reading stdin once Bubble Tea takes over.
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(left/time.Millisecond)+1)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return 0, err
		}
		if n == 0 {
			continue
		}
		k, err := unix.Read(fd, buf)
		if err != nil {
			return 0, err
		}
		resp = append(resp, buf[:k]...)
		if col, done, err := parseColumn(resp); done {
			return col, err
		}
	}
}

// parseColumn reads the column from a cursor position report (ESC [ row ; col R), which may
// arrive in pieces or after other input. done is false until a whole report is there.
func parseColumn(resp []byte) (col int, done bool, err error) {
	s := string(resp)
	i := strings.LastIndexByte(s, 'R')
	if i < 0 {
		return 0, false, nil
	}
	start := strings.LastIndex(s[:i], "\x1b[")
	if start < 0 {
		return 0, true, fmt.Errorf("bad answer %q", s)
	}
	_, c, ok := strings.Cut(s[start+2:i], ";")
	if !ok {
		return 0, true, fmt.Errorf("bad answer %q", s)
	}
	col, err = strconv.Atoi(c)
	return col, true, err
}
