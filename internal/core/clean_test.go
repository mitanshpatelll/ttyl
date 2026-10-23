package core

import "testing"

func TestClean(t *testing.T) {
	cases := map[string]string{
		"hello\nworld\tok":                "hello\nworld\tok",
		"clip\x1b]52;c;ZXZpbA==\x07board": "clip]52;c;ZXZpbA==board",
		"\x1b[2J\x1b[31mred":              "[2J[31mred",
		"c1\u009bscsi":                    "c1scsi",
		"file‮gnp.exe":                    "filegnp.exe",
		"emoji 👍 and ünïcödé stay":        "emoji 👍 and ünïcödé stay",
		"\x00null\x7fdel":                 "nulldel",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
