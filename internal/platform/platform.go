// Package platform holds what every adapter needs beyond core.Platform: linking an account from the terminal.
package platform

import (
	"context"
	"io"

	"github.com/mitanshpatelll/dmz/internal/core"
)

// Terminal is the plain (non-TUI) terminal used while linking an account.
type Terminal interface {
	io.Writer
	// Prompt asks for one line; hidden input is not echoed (codes, passwords, cookies).
	Prompt(label string, hidden bool) (string, error)
}

// Account is a platform adapter that can link and unlink the user's account.
type Account interface {
	core.Platform
	LoggedIn() bool
	Login(ctx context.Context, t Terminal) error
	// Logout unlinks dmz on the platform's side where possible and deletes the local session.
	Logout(ctx context.Context) error
}
