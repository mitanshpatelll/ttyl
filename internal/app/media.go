package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mitanshpatelll/dmz/internal/core"
)

// mediaTTL is how long opened attachments stay in the cache folder.
const mediaTTL = 7 * 24 * time.Hour

// safeToOpen are file types handed straight to the default app. Anything else
// (scripts, .app bundles, archives, unknown types) is only revealed in Finder.
var safeToOpen = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true,
	".mp4": true, ".mov": true, ".m4v": true, ".3gp": true,
	".mp3": true, ".m4a": true, ".ogg": true, ".opus": true, ".wav": true, ".aac": true,
	".pdf": true, ".txt": true,
}

// OpenMedia downloads a message's attachment into the media folder and opens it.
// It returns the saved path.
func (a *App) OpenMedia(ctx context.Context, msg core.Message) (string, error) {
	dl, ok := a.platforms[msg.Platform].(core.MediaDownloader)
	if !ok {
		return "", fmt.Errorf("opening %s media is not supported yet", msg.Platform)
	}
	handle, err := a.store.Media(msg.Platform, msg.ChatID, msg.ID)
	if err != nil {
		return "", err
	}
	if handle == nil {
		return "", errors.New("this message has no attachment")
	}
	data, name, err := dl.Download(ctx, msg, handle)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(a.mediaDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(a.mediaDir, fileName(msg.ID, name))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	quarantine(path)
	return path, a.reveal(path)
}

// fileName makes a sender-chosen name safe to write: no directories, no hidden files, bounded length.
func fileName(id, name string) string {
	name = strings.TrimLeft(filepath.Base(strings.ReplaceAll(name, "\\", "/")), ".")
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == ':' {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		name = "attachment"
	}
	if r := []rune(name); len(r) > 100 {
		ext := filepath.Ext(name)
		name = string(r[:100-len([]rune(ext))]) + ext
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return id + "-" + name
}

// quarantine tags the file like a browser download, so Gatekeeper checks it before anything runs.
func quarantine(path string) {
	if runtime.GOOS == "darwin" {
		_ = unix.Setxattr(path, "com.apple.quarantine", fmt.Appendf(nil, "0081;%x;dmz;", time.Now().Unix()), 0)
	}
}

func reveal(path string) error {
	safe := safeToOpen[strings.ToLower(filepath.Ext(path))]
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin" && safe:
		cmd = exec.Command("open", path)
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", "-R", path)
	case safe:
		cmd = exec.Command("xdg-open", path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the opener
	return nil
}

// CleanMedia deletes cached attachments older than a week.
func (a *App) CleanMedia() {
	entries, err := os.ReadDir(a.mediaDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > mediaTTL {
			os.Remove(filepath.Join(a.mediaDir, e.Name()))
		}
	}
}
