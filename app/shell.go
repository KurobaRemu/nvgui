package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neovim/go-client/msgpack/rpc"

	"nvgui/diag"
	"nvgui/drawlist"
	"nvgui/input"
	"nvgui/render"
	"nvgui/session"
	"nvgui/text"
)

var errShellClosed = errors.New("app: shell is closed")

type shellImpl struct {
	req       LaunchRequest
	clipboard Clipboard
	ctx       context.Context
	cancel    context.CancelFunc
	session   *session.Session
	queue     *input.Queue
	handler   *input.Handler
	recorder  *input.TraceRecorder
	resolver  *text.Resolver
	planner   *render.Planner
	view      *render.View
	window    *mygo.Window

	mu                sync.Mutex
	inputSyncMu       sync.Mutex
	services          ui.Services
	closed            bool
	closeErr          error
	lastSeq           uint64
	lastColumns       int
	lastRows          int
	forwardedInputSeq uint64

	closeOnce sync.Once
	quitOnce  sync.Once
	closing   chan struct{}
	closeDone chan struct{}
	wg        sync.WaitGroup
	started   bool
}

func newShell(req LaunchRequest, _ diag.Clock, clipboard Clipboard) *Shell {
	return &Shell{impl: &shellImpl{
		req:       req,
		clipboard: clipboard,
		closing:   make(chan struct{}),
		closeDone: make(chan struct{}),
	}}
}

func shellView(shell *Shell, c *ui.Context) {
	if shell == nil || shell.impl == nil {
		return
	}
	impl := shell.impl
	if c != nil {
		impl.mu.Lock()
		impl.services = c.Services()
		impl.mu.Unlock()
	}
	impl.refreshFrame()
	shell.AttachInput(c)
}

func shellWindow(shell *Shell) *mygo.Window {
	if shell == nil || shell.impl == nil {
		return nil
	}
	impl := shell.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return impl.window
}

func shellSessionExited(shell *Shell) <-chan struct{} {
	if shell == nil || shell.impl == nil || shell.impl.session == nil {
		return nil
	}
	return shell.impl.session.Exited()
}

func shellExportDiag(shell *Shell, dir string) error {
	if shell == nil || shell.impl == nil || shell.impl.session == nil {
		return errShellClosed
	}
	if err := shell.impl.checkOpen(); err != nil {
		return err
	}
	shell.impl.syncInputTrace()
	return shell.impl.session.ExportDiag(dir)
}

func (impl *shellImpl) syncInputTrace() {
	if impl == nil || impl.recorder == nil || impl.session == nil {
		return
	}
	impl.inputSyncMu.Lock()
	defer impl.inputSyncMu.Unlock()
	pending := []diag.TraceEvent{}
	latest := impl.forwardedInputSeq
	for _, event := range impl.recorder.Events() {
		if event.Seq <= impl.forwardedInputSeq {
			continue
		}
		pending = append(pending, event)
		if event.Seq > latest {
			latest = event.Seq
		}
	}
	sort.Slice(pending, func(left, right int) bool {
		return pending[left].Seq < pending[right].Seq
	})
	for _, event := range pending {
		impl.session.RecordInput(event)
	}
	impl.forwardedInputSeq = latest
}

func shellClose(shell *Shell) error {
	if shell == nil || shell.impl == nil {
		return nil
	}
	return shell.impl.close()
}

func (s *Shell) Resize(width, height int) error {
	if s == nil || s.impl == nil || !s.impl.started || s.impl.session == nil {
		return errShellClosed
	}
	if err := s.impl.checkOpen(); err != nil {
		return err
	}
	select {
	case <-s.impl.session.Exited():
		return errShellClosed
	default:
	}
	cell := s.impl.resolver.Cell()
	cols := cellsFor(width, cell.Width)
	rows := cellsFor(height, cell.Height)
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	s.impl.mu.Lock()
	same := cols == s.impl.lastColumns && rows == s.impl.lastRows
	s.impl.mu.Unlock()
	if same {
		return nil
	}
	if err := s.impl.session.Nvim().TryResizeUI(cols, rows); err != nil {
		return fmt.Errorf("app: resize UI %dx%d: %w", cols, rows, err)
	}
	s.impl.mu.Lock()
	s.impl.lastColumns, s.impl.lastRows = cols, rows
	s.impl.mu.Unlock()
	return nil
}

func cellsFor(pixels int, cell float32) int {
	if cell <= 0 || math.IsNaN(float64(cell)) || math.IsInf(float64(cell), 0) {
		return 1
	}
	return int(math.Floor(float64(pixels) / float64(cell)))
}

func (s *Shell) AttachInput(c *ui.Context) ui.Element {
	if s == nil || s.impl == nil || c == nil {
		return ui.Element{}
	}
	impl := s.impl
	inputBox := ui.Box(c).Fill().Focusable().AutoFocus().Label("nvgui-input").HandleInput(func(ev ui.InputEvent) bool {
		handled := s.HandleEditCommand(c, ev) || impl.handler.Handle(ev)
		impl.syncInputTrace()
		return handled
	}).TextCaret(impl.caret()).InputMethod(impl.handler.IMEAllowed())
	inputBox.Children(func() {
		impl.view.Draw(c).IgnoreInput(true)
		if preedit := impl.handler.Preedit(); preedit != "" {
			impl.preeditLayer(c, preedit)
		}
	})
	return inputBox
}

func (impl *shellImpl) preeditLayer(c *ui.Context, preedit string) {
	caret := impl.caret()
	if caret.H <= 0 && impl.resolver != nil {
		caret.H = impl.resolver.Cell().Height
	}
	font := impl.resolver.FontFor(preedit, false, false)
	glyphs := impl.resolver.Shape(preedit, font)
	width := float32(0)
	for _, glyph := range glyphs {
		width = max(width, glyph.X+glyph.Advance)
	}
	if width <= 0 {
		width = caret.W
	}
	metrics := font.MyGo().Metrics()
	foreground := ui.RGB(255, 255, 255)
	background := ui.RGB(32, 32, 40)
	ui.Box(c).Absolute().Left(caret.X).Top(caret.Y).Size(width, caret.H).IgnoreInput(true).
		Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: width, H: caret.H}, background, 0)
			p.Glyphs(glyphs, r.X, r.Y+metrics.Ascent, foreground)
			p.Fill(ui.Rect{X: r.X, Y: r.Y + caret.H - 1, W: width, H: 1}, foreground, 0)
		})
}

func Start(ctx context.Context, req LaunchRequest, clock diag.Clock, clipboard Clipboard) (*Shell, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	resolver, err := text.NewResolver(text.Config{Scale: 1})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("app: create font resolver: %w", err)
	}
	sess, err := session.Start(ctx, req.ConnectionOptions(), clock)
	if err != nil {
		cancel()
		return nil, err
	}
	shell := newShell(req, clock, clipboard)
	impl := shell.impl
	impl.ctx = ctx
	impl.cancel = cancel
	impl.lastColumns, impl.lastRows = -1, -1
	impl.resolver = resolver
	impl.planner = render.NewPlanner(resolver)
	impl.view = render.NewView()
	impl.recorder = input.NewTraceRecorder()
	impl.queue = input.NewQueue(sess.Nvim(), impl.recorder)
	impl.handler = input.NewHandler(impl.queue)
	impl.session = sess
	impl.mu.Lock()
	impl.started = true
	impl.mu.Unlock()
	impl.wg.Add(3)
	go func() {
		defer impl.wg.Done()
		_ = impl.queue.Run(ctx)
	}()
	go func() {
		defer impl.wg.Done()
		impl.watchSession()
	}()
	go func() {
		defer impl.wg.Done()
		impl.watchRedraw()
	}()
	if !req.Headless {
		mygo.App.WhenReady(func() {
			impl.mu.Lock()
			closed := impl.closed
			impl.mu.Unlock()
			if closed {
				impl.quitOnce.Do(func() { mygo.App.Quit() })
				return
			}
			cell := resolver.Cell()
			window := mygo.NewWindow(mygo.WindowOptions{
				Title:          "nvgui",
				Width:          windowDimension(req.Width, defaultColumns, cell.Width),
				Height:         windowDimension(req.Height, defaultRows, cell.Height),
				UseContentSize: true,
				Content:        ui.View(shell.View),
			})
			impl.mu.Lock()
			impl.window = window
			impl.mu.Unlock()
			window.OnResize(func() {
				width, height := window.ContentSize()
				_ = shell.Resize(width, height)
			})
			window.OnClose(func(*mygo.CloseEvent) {
				_ = shell.Close()
			})
			width, height := window.ContentSize()
			if err := shell.Resize(width, height); err != nil {
				if !isCleanSessionError(err) {
					impl.recordCloseError(fmt.Errorf("app: initial resize: %w", err))
				}
				return
			}
			if impl.isClosing() {
				impl.quitOnce.Do(func() { mygo.App.Quit() })
			}
		})
	}
	return shell, nil
}

func windowDimension(cells, fallback int, size float32) int {
	if cells <= 0 {
		cells = fallback
	}
	if size <= 0 {
		size = 8
	}
	return int(math.Ceil(float64(cells) * float64(size)))
}

func (impl *shellImpl) checkOpen() error {
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if impl.closed {
		return errShellClosed
	}
	return nil
}

func (impl *shellImpl) isClosing() bool {
	select {
	case <-impl.closing:
		return true
	default:
		return false
	}
}

func (impl *shellImpl) close() error {
	impl.closeOnce.Do(impl.finishClose)
	<-impl.closeDone
	impl.wg.Wait()
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return impl.closeErr
}

func (impl *shellImpl) finishClose() {
	defer close(impl.closeDone)
	impl.mu.Lock()
	if impl.closed {
		impl.mu.Unlock()
		return
	}
	impl.closed = true
	close(impl.closing)
	started := impl.started
	impl.mu.Unlock()
	if !started {
		return
	}
	if err := impl.queue.Close(); err != nil && !isCleanSessionError(err) {
		impl.recordCloseError(fmt.Errorf("app: close input queue: %w", err))
	}
	if err := impl.session.Close(); err != nil && !isCleanSessionError(err) {
		impl.recordCloseError(fmt.Errorf("app: close session: %w", err))
	}
	if impl.cancel != nil {
		impl.cancel()
	}
	<-impl.session.Exited()
}

func (impl *shellImpl) recordCloseError(err error) {
	impl.mu.Lock()
	defer impl.mu.Unlock()
	impl.closeErr = errors.Join(impl.closeErr, err)
}

func isCleanSessionError(err error) bool {
	if err == nil {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, rpc.ErrClosed) || errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "exit status") || strings.Contains(err.Error(), "signal:") ||
		strings.Contains(err.Error(), "session closed")
}

func (impl *shellImpl) watchSession() {
	<-impl.session.Exited()
	impl.closeOnce.Do(impl.finishClose)
	if !impl.req.Headless {
		impl.quitOnce.Do(func() { mygo.App.Quit() })
	}
}

func (impl *shellImpl) watchRedraw() {
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	var seenSeq uint64
	for {
		select {
		case <-impl.closing:
			return
		case <-impl.ctx.Done():
			impl.closeOnce.Do(impl.finishClose)
			return
		case <-ticker.C:
			state := impl.session.Latest()
			if state == nil {
				continue
			}
			if state.Seq == seenSeq {
				continue
			}
			seenSeq = state.Seq
			impl.mu.Lock()
			window := impl.window
			services := impl.services
			impl.mu.Unlock()
			impl.handler.SetMode(state.Mode)
			if window != nil {
				window.Invalidate()
			} else {
				services.Invalidate()
			}
		}
	}
}

func (impl *shellImpl) caret() ui.Rect {
	if impl.view == nil || impl.resolver == nil || impl.session == nil || impl.handler == nil {
		return ui.Rect{}
	}
	caret := impl.view.Caret()
	if caret != (ui.Rect{}) {
		return caret
	}
	cell := impl.resolver.Cell()
	return impl.handler.CaretFor(impl.session.Latest(), input.CellSize{Width: cell.Width, Height: cell.Height})
}

func (impl *shellImpl) refreshFrame() {
	if impl.session == nil || impl.view == nil || impl.planner == nil {
		return
	}
	state := impl.session.Latest()
	if state == nil {
		return
	}
	impl.mu.Lock()
	if state.Seq == impl.lastSeq {
		impl.mu.Unlock()
		return
	}
	impl.mu.Unlock()
	list, err := drawlist.Build(state)
	if err != nil {
		return
	}
	frame, err := impl.planner.Plan(list)
	if err != nil {
		return
	}
	impl.mu.Lock()
	impl.lastSeq = state.Seq
	impl.mu.Unlock()
	impl.view.SetFrame(frame)
	impl.handler.SetMode(state.Mode)
}
