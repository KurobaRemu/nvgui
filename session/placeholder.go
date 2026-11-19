package session

// placeholder

import (
	"context"
	"errors"

	"github.com/neovim/go-client/nvim"

	"nvgui/connection"
	"nvgui/diag"
	"nvgui/screen"
)

var errNotImplemented = errors.New("not implemented")

type Session struct{}

func Start(ctx context.Context, opt connection.Options, clock diag.Clock) (*Session, error) {
	return nil, errNotImplemented
}

func (s *Session) Latest() *screen.State { return nil }

func (s *Session) Nvim() *nvim.Nvim { return nil }

func (s *Session) Exited() <-chan struct{} { return nil }

func (s *Session) ExportDiag(dir string) error { return errNotImplemented }

func (s *Session) Stalls() <-chan int { return nil }

func (s *Session) Close() error { return errNotImplemented }
