package connection

// placeholder

import (
	"context"
	"errors"
	"io"

	"github.com/neovim/go-client/nvim"
)

var errNotImplemented = errors.New("not implemented")

type Options struct {
	Command  string
	Args     []string
	Env      []string
	Dir      string
	Width    int
	Height   int
	Tap      io.Writer
	OnRedraw func(args []any)
}

type Conn struct{}

func Start(ctx context.Context, opt Options) (*Conn, error) {
	return nil, errNotImplemented
}

func (c *Conn) Nvim() *nvim.Nvim { return nil }

func (c *Conn) Exited() <-chan struct{} { return nil }

func (c *Conn) ExitCode() int { return -1 }

func (c *Conn) Close() error { return errNotImplemented }
