package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"

	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/rules"
	"github.com/mitanshpatelll/dmz/internal/store"
)

func TestFileName(t *testing.T) {
	cases := map[string]string{
		"report.pdf":         "ABCDEF12-report.pdf",
		"../../.ssh/id_rsa":  "ABCDEF12-id_rsa",
		".hidden":            "ABCDEF12-hidden",
		`..\..\evil.command`: "ABCDEF12-evil.command",
		"":                   "ABCDEF12-attachment",
		"a:b\x00c.txt":       "ABCDEF12-a_b_c.txt",
	}
	for in, want := range cases {
		if got := fileName("ABCDEF1234567", in); got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
	}
}

type downloader struct{ core.Platform }

func (downloader) ID() core.PlatformID { return core.WhatsApp }
func (downloader) Download(context.Context, core.Message, []byte) ([]byte, string, error) {
	return []byte("jpeg bytes"), "../sunset.jpg", nil
}

func TestOpenMedia(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	msg := core.Message{Platform: core.WhatsApp, ChatID: "c", ID: "MSGID1234", SenderID: "c", Timestamp: time.Now(), Kind: core.KindImage, Media: []byte("h"), HasMedia: true}
	if err := st.Apply(core.BatchEvent{Platform: core.WhatsApp, Messages: []core.Message{msg}}); err != nil {
		t.Fatal(err)
	}
	var revealed string
	dir := t.TempDir()
	r, _ := rules.New(nil)
	a := New(Options{Store: st, Rules: r, MediaDir: filepath.Join(dir, "media"), Log: zerolog.Nop(),
		Reveal: func(p string) error { revealed = p; return nil }}, downloader{})

	path, err := a.OpenMedia(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "media", "MSGID123-sunset.jpg") || revealed != path {
		t.Errorf("path %q, revealed %q", path, revealed)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("saved file: %v, mode %v", err, info.Mode())
	}
	if runtime.GOOS == "darwin" {
		if _, err := unix.Getxattr(path, "com.apple.quarantine", make([]byte, 64)); err != nil {
			t.Errorf("no quarantine attribute: %v", err)
		}
	}
}
