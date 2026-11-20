package connection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/neovim/go-client/msgpack/rpc"
	"github.com/neovim/go-client/nvim"
)

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

type Conn struct {
	cmd      *exec.Cmd
	nvim     *nvim.Nvim
	exited   chan struct{}
	stdin    io.WriteCloser
	closeMu  sync.Mutex
	stateMu  sync.RWMutex
	exitCode int
	closed   bool
	closeErr error
}

func Start(ctx context.Context, opt Options) (*Conn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is nil")
	}
	command := opt.Command
	if command == "" {
		command = "nvim"
	}
	args := append([]string{"--embed"}, opt.Args...)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = opt.Env
	cmd.Dir = opt.Dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(err, cleanupError(stdin.Close()))
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, cleanupError(stdin.Close()), cleanupError(stdout.Close()))
	}

	var source io.Reader = stdout
	if opt.Tap != nil {
		source = io.TeeReader(stdout, opt.Tap)
	}
	conn := &Conn{
		cmd:      cmd,
		exited:   make(chan struct{}),
		stdin:    stdin,
		exitCode: -1,
	}
	client, err := nvim.New(source, stdin, stdin, func(string, ...any) {})
	if err != nil {
		return nil, errors.Join(
			err,
			cleanupError(stdin.Close()),
			cleanupError(stdout.Close()),
			cleanupError(cmd.Process.Kill()),
			cleanupError(cmd.Wait()),
		)
	}

	conn.nvim = client
	go conn.run(client.Serve)
	if err := client.RegisterHandler("redraw", func(redrawArgs ...[]any) {
		if opt.OnRedraw == nil {
			return
		}
		args := make([]any, len(redrawArgs))
		for index, group := range redrawArgs {
			args[index] = group
		}
		opt.OnRedraw(args)
	}); err != nil {
		return nil, conn.fail(err)
	}

	width, height := opt.Width, opt.Height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	options := map[string]any{
		"rgb":           true,
		"ext_linegrid":  true,
		"ext_multigrid": true,
		"ext_hlstate":   true,
	}
	if err := client.AttachUI(width, height, options); err != nil {
		return nil, conn.fail(err)
	}
	return conn, nil
}

func (c *Conn) run(serve func() error) {
	waitDone := make(chan error, 1)
	go func() { waitDone <- c.cmd.Wait() }()
	serveErr := serve()
	cleanupErr := c.nvim.Close()
	stdinErr := c.stdin.Close()
	waitErr := <-waitDone
	exitCode := -1
	if c.cmd.ProcessState != nil {
		exitCode = c.cmd.ProcessState.ExitCode()
	}
	combinedErr := errors.Join(
		cleanupError(serveErr),
		waitErr,
		cleanupError(cleanupErr),
		cleanupError(stdinErr),
	)
	c.stateMu.Lock()
	if c.exitCode == -1 {
		c.exitCode = exitCode
	}
	if combinedErr != nil {
		c.closeErr = errors.Join(c.closeErr, combinedErr)
	}
	c.stateMu.Unlock()
	close(c.exited)
}

func cleanupError(err error) error {
	if err == nil || errors.Is(err, rpc.ErrClosed) || errors.Is(err, io.EOF) ||
		errors.Is(err, fs.ErrClosed) || errors.Is(err, os.ErrProcessDone) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func (c *Conn) fail(cause error) error {
	if err := c.Close(); err != nil && !errors.Is(err, cause) {
		cause = errors.Join(cause, err)
	}
	return cause
}

func (c *Conn) Nvim() *nvim.Nvim { return c.nvim }

func (c *Conn) Exited() <-chan struct{} { return c.exited }

func (c *Conn) ExitCode() int {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.exitCode
}

func (c *Conn) Close() error {
	return c.closeWithTimeout(5 * time.Second)
}

func (c *Conn) closeWithTimeout(timeout time.Duration) error {
	c.closeMu.Lock()
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		c.closeMu.Unlock()
		<-c.exited
		c.stateMu.RLock()
		defer c.stateMu.RUnlock()
		return c.closeErr
	}
	c.closed = true
	c.stateMu.Unlock()
	c.closeMu.Unlock()
	select {
	case <-c.exited:
	default:
		closeErr := errors.Join(cleanupError(c.nvim.Close()), cleanupError(c.stdin.Close()))
		if closeErr != nil {
			c.stateMu.Lock()
			c.closeErr = errors.Join(c.closeErr, closeErr)
			c.stateMu.Unlock()
		}
		select {
		case <-c.exited:
		case <-time.After(timeout):
			killErr := cleanupError(c.cmd.Process.Kill())
			if killErr != nil {
				c.stateMu.Lock()
				c.closeErr = errors.Join(c.closeErr, killErr)
				c.stateMu.Unlock()
			}
			<-c.exited
		}
	}
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.closeErr
}
