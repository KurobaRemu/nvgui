package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
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
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/session"
	"nvgui/text"
)

var errShellClosed = errors.New("app: shell is closed")

type shellImpl struct {
	req            LaunchRequest
	clipboard      Clipboard
	ctx            context.Context
	cancel         context.CancelFunc
	session        *session.Session
	queue          *input.Queue
	handler        *input.Handler
	recorder       *input.TraceRecorder
	resolver       *text.Resolver
	planner        *render.Planner
	animator       *render.Animator
	animClock      *animClock
	view           *render.View
	window         *mygo.Window
	quitOnExit     bool
	onExit         func(*Shell)
	onShortcut     func(*ui.Context) bool
	onDrop         func([]string)
	workspace      *Workspace
	explicitOpenIn bool
	displayOffs    []func()

	animSnapMu            sync.Mutex
	mu                    sync.Mutex
	inputSyncMu           sync.Mutex
	services              ui.Services
	resizeCh              chan struct{}
	closed                bool
	closeErr              error
	lastSeq               uint64
	lastColumns           int
	lastRows              int
	pendingColumns        int
	pendingRows           int
	resizePending         bool
	inFlightColumns       int
	inFlightRows          int
	resizeInFlight        bool
	forwardedInputSeq     uint64
	lastGUIFont           string
	failedGUIFont         string
	scale                 float32
	fullScreen            bool
	scaleRebuildCount     uint64
	repaintCount          uint64
	forceFullRepaint      bool
	snapNextFrame         bool
	lastContentWidth      int
	lastContentHeight     int
	sawWindowState        bool
	lastWindowHidden      bool
	lastWindowActive      bool
	envGUIFontOverride    bool
	sawInitialNvimGUIFont bool
	initialNvimGUIFont    string
	lastAnimNow           time.Time
	explicitConfig        bool
	configDirty           bool
	configGeneration      uint64
	envConfigKeys         map[string]bool
	explicitKeys          map[string]bool
	nvimConfig            nvimConfigState
	syncCh                chan struct{}
	syncSem               chan struct{}
	snappingAnim          bool
	preSnapAnimConfig     render.AnimationConfig

	closeOnce sync.Once
	quitOnce  sync.Once
	closing   chan struct{}
	closeDone chan struct{}
	wg        sync.WaitGroup
	started   bool
}

func (impl *shellImpl) asyncTask(task func()) {
	impl.mu.Lock()
	if impl.closed {
		impl.mu.Unlock()
		return
	}
	impl.wg.Add(1)
	impl.mu.Unlock()
	go func() {
		defer impl.wg.Done()
		task()
	}()
}

type animClock struct {
	mu       sync.RWMutex
	fallback diag.Clock
	now      time.Time
}

type shellHooks struct {
	quitOnExit     bool
	workspaceGUI   bool
	workspace      *Workspace
	openInPolicy   OpenInPolicy
	explicitOpenIn bool
	onExit         func(*Shell)
	onShortcut     func(*ui.Context) bool
	onDrop         func([]string)
}

func newAnimClock(clock diag.Clock) *animClock {
	return &animClock{fallback: clock}
}

func (clock *animClock) Now() time.Time {
	clock.mu.RLock()
	now, fallback := clock.now, clock.fallback
	clock.mu.RUnlock()
	if !now.IsZero() {
		return now
	}
	if fallback != nil {
		return fallback.Now()
	}
	return time.Now()
}

func (clock *animClock) Sync(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

func newShell(req LaunchRequest, clock diag.Clock, clipboard Clipboard) *Shell {
	return &Shell{impl: &shellImpl{
		req:           req,
		clipboard:     clipboard,
		scale:         1,
		animClock:     newAnimClock(clock),
		envConfigKeys: envConfigKeys(req.Env),
		explicitKeys:  make(map[string]bool),
		syncCh:        make(chan struct{}, 1),
		syncSem:       newSyncSemaphore(),
		closing:       make(chan struct{}),
		closeDone:     make(chan struct{}),
		resizeCh:      make(chan struct{}, 1),
	}}
}

func newSyncSemaphore() chan struct{} {
	semaphore := make(chan struct{}, 1)
	semaphore <- struct{}{}
	return semaphore
}

func shellView(shell *Shell, c *ui.Context) {
	if shell == nil || shell.impl == nil {
		return
	}
	impl := shell.impl
	if c != nil {
		observeWindowState(shell, c)
		if impl.onShortcut != nil {
			impl.onShortcut(c)
		}
		now := c.Now()
		impl.mu.Lock()
		impl.services = c.Services()
		impl.mu.Unlock()
		impl.animClock.Sync(now)
		impl.refreshFrameWithContext(c, now, c.Instant())
	} else {
		impl.refreshFrameWithContext(nil, impl.animClock.Now(), false)
	}
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
	if s == nil || s.impl == nil {
		return errShellClosed
	}
	impl := s.impl
	impl.mu.Lock()
	if !impl.started || impl.closed || impl.session == nil {
		impl.mu.Unlock()
		return errShellClosed
	}
	select {
	case <-impl.session.Exited():
		impl.mu.Unlock()
		return errShellClosed
	default:
	}
	impl.recordResize(width, height)
	impl.mu.Unlock()
	cell := impl.cell()
	cols := cellsFor(width, cell.Width)
	rows := cellsFor(height, cell.Height)
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	impl.mu.Lock()
	if impl.closed {
		impl.mu.Unlock()
		return errShellClosed
	}
	select {
	case <-impl.session.Exited():
		impl.mu.Unlock()
		return errShellClosed
	default:
	}
	switch {
	case impl.resizePending:
		if cols == impl.pendingColumns && rows == impl.pendingRows {
			impl.mu.Unlock()
			return nil
		}
		impl.pendingColumns, impl.pendingRows = cols, rows
	case impl.resizeInFlight:
		if cols == impl.inFlightColumns && rows == impl.inFlightRows {
			impl.mu.Unlock()
			return nil
		}
		impl.pendingColumns, impl.pendingRows = cols, rows
		impl.resizePending = true
	default:
		if cols == impl.lastColumns && rows == impl.lastRows {
			impl.mu.Unlock()
			return nil
		}
		impl.pendingColumns, impl.pendingRows = cols, rows
		impl.resizePending = true
	}
	impl.mu.Unlock()
	select {
	case impl.resizeCh <- struct{}{}:
	default:
	}
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
	inputBox.Draw(func(p *ui.Painter, _ ui.Rect) { observePainterScale(s, p) })
	inputBox.Children(func() {
		impl.mu.Lock()
		view := impl.view
		impl.mu.Unlock()
		if view != nil {
			view.Draw(c).IgnoreInput(true)
		}
		if preedit := impl.handler.Preedit(); preedit != "" {
			impl.preeditLayer(c, preedit)
		}
	})
	if dropped := inputBox.DroppedFiles(); len(dropped) > 0 {
		paths := append([]string(nil), dropped...)
		impl.mu.Lock()
		onDrop := impl.onDrop
		impl.mu.Unlock()
		if onDrop != nil {
			impl.asyncTask(func() { onDrop(paths) })
		} else {
			impl.asyncTask(func() {
				if err := shellDropFiles(s, impl.ctx, paths); err != nil {
					fmt.Fprintln(os.Stderr, "nvgui: drop files:", err)
				}
			})
		}
	}
	return inputBox
}

func shellDropFiles(shell *Shell, ctx context.Context, paths []string) error {
	if shell == nil || shell.impl == nil {
		return errShellClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := formatDropCommand(paths); err != nil {
		return err
	}
	absolute := make([]string, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(shell.impl.req.WorkingDir, path)
		}
		absolute = append(absolute, path)
	}
	impl := shell.impl
	impl.mu.Lock()
	session := impl.session
	window := impl.window
	services := impl.services
	closed := impl.closed
	impl.mu.Unlock()
	if closed || session == nil {
		return errShellClosed
	}
	if err := dismissBlockingPrompt(ctx, impl, session); err != nil {
		return fmt.Errorf("app: drop files dismiss blocking prompt: %w", err)
	}
	payload, err := json.Marshal(absolute)
	if err != nil {
		return fmt.Errorf("app: encode drop paths: %w", err)
	}
	const code = `local paths = vim.json.decode(...)
local initial = vim.api.nvim_get_current_buf()
local removeInitial = vim.api.nvim_buf_get_name(initial) == '' and vim.api.nvim_buf_line_count(initial) == 1 and vim.api.nvim_buf_get_lines(initial, 0, -1, false)[1] == '' and vim.bo[initial].buftype == '' and not vim.bo[initial].modified
for _, path in ipairs(paths) do
  local target = vim.fn.bufadd(path)
  vim.api.nvim_set_option_value('buflisted', true, { buf = target })
  local windows = vim.fn.win_findbuf(target)
  if #windows > 0 then
    vim.api.nvim_set_current_win(windows[1])
  elseif target ~= vim.api.nvim_get_current_buf() then
    vim.api.nvim_set_current_buf(target)
  end
end
if removeInitial and vim.api.nvim_buf_is_valid(initial) and vim.bo[initial].buftype == '' and not vim.bo[initial].modified and vim.api.nvim_buf_get_name(initial) == '' and initial ~= vim.api.nvim_get_current_buf() then
  vim.api.nvim_buf_delete(initial, { force = true })
end`
	if err := execLuaBounded(ctx, impl, session, code, string(payload)); err != nil {
		return fmt.Errorf("app: drop files: %w", err)
	}
	invalidate(services, window)
	return nil
}

func dismissBlockingPrompt(ctx context.Context, impl *shellImpl, sess *session.Session) error {
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(chan struct {
		blocking bool
		err      error
	}, 1)
	go func() {
		mode, err := sess.Nvim().Mode()
		result <- struct {
			blocking bool
			err      error
		}{blocking: mode != nil && mode.Blocking, err: err}
	}()
	select {
	case probe := <-result:
		if probe.err != nil || !probe.blocking {
			return probe.err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-impl.closing:
		return errShellClosed
	case <-sess.Exited():
		return errShellClosed
	}
	return inputBounded(ctx, impl, sess, "\x1b")
}

func inputBounded(ctx context.Context, impl *shellImpl, sess *session.Session, keys string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(chan error, 1)
	go func() {
		_, err := sess.Nvim().Input(keys)
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-impl.closing:
		return errShellClosed
	case <-sess.Exited():
		return errShellClosed
	}
}

func execLuaBounded(ctx context.Context, impl *shellImpl, sess *session.Session, code string, args any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(chan error, 1)
	go func() { result <- sess.Nvim().ExecLua(code, nil, args) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-impl.closing:
		return errShellClosed
	case <-sess.Exited():
		return errShellClosed
	}
}

func (impl *shellImpl) preeditLayer(c *ui.Context, preedit string) {
	impl.mu.Lock()
	resolver := impl.resolver
	impl.mu.Unlock()
	if resolver == nil {
		return
	}
	caret := impl.caret()
	if caret.H <= 0 {
		caret.H = resolver.Cell().Height
	}
	font := resolver.FontFor(preedit, false, false)
	glyphs := resolver.Shape(preedit, font)
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
	return startShell(ctx, req, clock, clipboard, shellHooks{quitOnExit: true, workspaceGUI: !req.Headless})
}

func startShell(ctx context.Context, req LaunchRequest, clock diag.Clock, clipboard Clipboard, hooks shellHooks) (*Shell, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	guiFont := GUIFontFromEnv(req.Env)
	resolver, err := text.NewResolver(text.Config{GUIFont: guiFont, Scale: 1})
	if err != nil {
		guiFont = ""
		resolver, err = text.NewResolver(text.Config{Scale: 1})
		if err != nil {
			cancel()
			return nil, fmt.Errorf("app: create font resolver: %w", err)
		}
	}
	sess, err := session.Start(ctx, req.ConnectionOptions(), clock)
	if err != nil {
		cancel()
		return nil, err
	}
	shell := newShell(req, clock, clipboard)
	impl := shell.impl
	impl.quitOnExit = hooks.quitOnExit
	impl.onExit = hooks.onExit
	impl.onShortcut = hooks.onShortcut
	impl.workspace = hooks.workspace
	cfg := AnimationConfigFromEnv(req.Env)
	if policy, ok := parseOpenInPolicy(envValue(req.Env, "NVGUI_OPEN_IN")); ok {
		impl.nvimConfig.snapshot = nvimConfigSnapshotFrom(cfg, policy)
		impl.explicitOpenIn = true
	} else {
		impl.nvimConfig.snapshot = nvimConfigSnapshotFrom(cfg, defaultOpenInPolicy(req.Env))
		impl.explicitOpenIn = hooks.explicitOpenIn
		if hooksPolicy, ok := parseOpenInPolicy(string(hooks.openInPolicy)); ok {
			impl.nvimConfig.snapshot.OpenIn = hooksPolicy
		}
	}
	impl.ctx = ctx
	impl.cancel = cancel
	impl.lastColumns, impl.lastRows = -1, -1
	impl.resolver = resolver
	impl.planner = render.NewPlanner(resolver)
	impl.animator = render.NewAnimator(impl.planner, cfg, impl.animClock)
	impl.lastGUIFont = guiFont
	impl.envGUIFontOverride = guiFont != ""
	impl.view = render.NewView()
	impl.recorder = input.NewTraceRecorder()
	impl.queue = input.NewQueue(sess.Nvim(), impl.recorder)
	impl.handler = input.NewHandler(impl.queue)
	impl.session = sess
	impl.mu.Lock()
	impl.started = true
	impl.mu.Unlock()
	impl.wg.Add(5)
	if hooks.onDrop != nil {
		impl.onDrop = hooks.onDrop
	}
	go func() {
		defer impl.wg.Done()
		impl.watchResize()
	}()
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
	go func() {
		defer impl.wg.Done()
		impl.watchNvimConfig()
	}()
	if hooks.workspaceGUI {
		mygo.App.WhenReady(func() {
			impl.mu.Lock()
			closed := impl.closed
			impl.mu.Unlock()
			if closed {
				impl.quitOnce.Do(func() { mygo.App.Quit() })
				return
			}
			cell := impl.cell()
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
			offs := attachDisplayWindow(shell, window, nil)
			window.OnClose(func(*mygo.CloseEvent) {
				go func() { _ = shell.Close() }()
			})
			registerDisplayCleanup(shell, offs)
			width, height := window.ContentSize()
			if err := shell.Resize(width, height); err != nil && !isCleanSessionError(err) {
				impl.recordCloseError(fmt.Errorf("app: initial resize: %w", err))
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

func (impl *shellImpl) takePendingResize() (int, int, bool) {
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if impl.closed || !impl.resizePending {
		return 0, 0, false
	}
	cols, rows := impl.pendingColumns, impl.pendingRows
	impl.resizePending = false
	if cols == impl.lastColumns && rows == impl.lastRows {
		return 0, 0, false
	}
	impl.resizeInFlight = true
	impl.inFlightColumns, impl.inFlightRows = cols, rows
	return cols, rows, true
}

func (impl *shellImpl) watchResize() {
	for {
		cols, rows, ok := impl.takePendingResize()
		if !ok {
			select {
			case <-impl.closing:
				return
			case <-impl.ctx.Done():
				return
			case <-impl.session.Exited():
				return
			case <-impl.resizeCh:
				continue
			}
		}
		select {
		case <-impl.closing:
			impl.mu.Lock()
			impl.resizeInFlight = false
			impl.mu.Unlock()
			return
		case <-impl.session.Exited():
			impl.mu.Lock()
			impl.resizeInFlight = false
			impl.mu.Unlock()
			return
		default:
		}
		err := impl.session.Nvim().TryResizeUI(cols, rows)
		impl.mu.Lock()
		impl.resizeInFlight = false
		if err == nil {
			impl.lastColumns, impl.lastRows = cols, rows
		}
		impl.mu.Unlock()
		if err != nil {
			select {
			case <-impl.closing:
				return
			case <-impl.session.Exited():
				return
			default:
			}
			if isCleanSessionError(err) {
				return
			}
			impl.recordCloseError(fmt.Errorf("app: resize UI %dx%d: %w", cols, rows, err))
		}
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
	offs := detachDisplayCleanup(impl)
	impl.mu.Unlock()
	for _, off := range offs {
		off()
	}
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
		errors.Is(err, errShellClosed) ||
		strings.Contains(err.Error(), "exit status") || strings.Contains(err.Error(), "signal:") ||
		strings.Contains(err.Error(), "session closed")
}

func (impl *shellImpl) watchSession() {
	<-impl.session.Exited()
	impl.closeOnce.Do(impl.finishClose)
	if impl.onExit != nil {
		impl.onExit(&Shell{impl: impl})
	} else if impl.quitOnExit && !impl.req.Headless {
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
			if state == nil || state.Seq == seenSeq {
				continue
			}
			seenSeq = state.Seq
			impl.mu.Lock()
			window, services := impl.window, impl.services
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
	impl.mu.Lock()
	view, resolver, session, handler := impl.view, impl.resolver, impl.session, impl.handler
	impl.mu.Unlock()
	if view == nil || resolver == nil || session == nil || handler == nil {
		return ui.Rect{}
	}
	caret := view.Caret()
	if caret != (ui.Rect{}) {
		return caret
	}
	cell := resolver.Cell()
	return handler.CaretFor(session.Latest(), input.CellSize{Width: cell.Width, Height: cell.Height})
}

func (impl *shellImpl) refreshFrame() {
	impl.refreshFrameWithContext(nil, impl.animClock.Now(), false)
}

func cloneFrameMetaWithoutScroll(meta *screen.FrameMeta) *screen.FrameMeta {
	if meta == nil {
		return nil
	}
	clone := *meta
	if meta.Viewports != nil {
		clone.Viewports = make(map[int]screen.Viewport, len(meta.Viewports))
		for gridID, viewport := range meta.Viewports {
			viewport.ScrollDelta = 0
			viewport.HasScrollDelta = false
			clone.Viewports[gridID] = viewport
		}
	}
	return &clone
}

func sameAnimConfigSnapshot(a, b NvimConfigSnapshot) bool {
	return a.CursorAnimation == b.CursorAnimation &&
		a.CursorAnimationLength == b.CursorAnimationLength &&
		a.CursorAnimateCommandLine == b.CursorAnimateCommandLine &&
		a.CursorAnimateInsertMode == b.CursorAnimateInsertMode &&
		a.ScrollAnimation == b.ScrollAnimation &&
		a.ScrollAnimationLength == b.ScrollAnimationLength &&
		a.Ligatures == b.Ligatures
}

func (impl *shellImpl) refreshFrameWithContext(c *ui.Context, now time.Time, instant bool) {
	displayFrameComplete := false
	var forceFullRepaint, snapNextFrame bool
	restoreDisplayFlags := func() {
		impl.mu.Lock()
		impl.forceFullRepaint = impl.forceFullRepaint || forceFullRepaint
		impl.snapNextFrame = impl.snapNextFrame || snapNextFrame
		impl.mu.Unlock()
	}
	impl.mu.Lock()
	if impl.session == nil || impl.view == nil || impl.planner == nil || impl.animator == nil || impl.resolver == nil || impl.handler == nil {
		impl.mu.Unlock()
		return
	}
	forceFullRepaint, snapNextFrame = impl.consumeDisplayFlags()
	impl.mu.Unlock()
	if forceFullRepaint || snapNextFrame {
		defer func() {
			if !displayFrameComplete {
				restoreDisplayFlags()
			}
		}()
	}
	if snapNextFrame {
		instant = true
	}
	state, meta := impl.session.LatestSnapshot()
	if state == nil {
		return
	}
	impl.mu.Lock()
	session, view, planner, animator, resolver, handler := impl.session, impl.view, impl.planner, impl.animator, impl.resolver, impl.handler
	impl.mu.Unlock()
	if session == nil || view == nil || planner == nil || animator == nil || resolver == nil || handler == nil {
		return
	}
	if instant {
		meta = cloneFrameMetaWithoutScroll(meta)
	}

	impl.mu.Lock()
	snapshot := struct {
		newSeq                bool
		configDirty           bool
		generation            uint64
		lastAnimNow           time.Time
		active                bool
		lastGUIFont           string
		failedGUIFont         string
		envGUIFontOverride    bool
		sawInitialNvimGUIFont bool
		initialNvimGUIFont    string
		services              ui.Services
		window                *mygo.Window
		closed                bool
	}{
		newSeq:                state.Seq != impl.lastSeq,
		configDirty:           impl.configDirty,
		generation:            impl.configGeneration,
		lastAnimNow:           impl.lastAnimNow,
		active:                impl.animator.Active(),
		lastGUIFont:           impl.lastGUIFont,
		failedGUIFont:         impl.failedGUIFont,
		envGUIFontOverride:    impl.envGUIFontOverride,
		sawInitialNvimGUIFont: impl.sawInitialNvimGUIFont,
		initialNvimGUIFont:    impl.initialNvimGUIFont,
		services:              impl.services,
		window:                impl.window,
		closed:                impl.closed,
	}
	impl.mu.Unlock()
	if snapshot.closed {
		return
	}
	stepOnly := !instant && !forceFullRepaint && !snapshot.newSeq && !snapshot.configDirty && snapshot.active && now.After(snapshot.lastAnimNow)
	if !instant && !forceFullRepaint && !snapshot.newSeq && !snapshot.configDirty && (!snapshot.active || !now.After(snapshot.lastAnimNow)) {
		if c != nil && snapshot.active {
			c.AnimationFrame()
		}
		return
	}

	cellChanged := false
	fontGUIFont := ""
	if meta != nil {
		fontGUIFont = meta.GUIFont
	}
	if snapshot.envGUIFontOverride && meta != nil {
		initial := snapshot.initialNvimGUIFont
		if !snapshot.sawInitialNvimGUIFont {
			impl.mu.Lock()
			if !impl.sawInitialNvimGUIFont {
				impl.initialNvimGUIFont = meta.GUIFont
				impl.sawInitialNvimGUIFont = true
			}
			initial = impl.initialNvimGUIFont
			impl.mu.Unlock()
		}
		if meta.GUIFont == initial {
			fontGUIFont = ""
		} else {
			impl.mu.Lock()
			impl.envGUIFontOverride = false
			impl.mu.Unlock()
		}
	}
	for fontGUIFont != "" && fontGUIFont != snapshot.failedGUIFont {
		impl.mu.Lock()
		if impl.closed || impl.lastGUIFont == fontGUIFont {
			impl.mu.Unlock()
			break
		}
		scale := impl.scale
		impl.mu.Unlock()
		if scale <= 0 {
			scale = 1
		}
		resolver, err := text.NewResolver(text.Config{GUIFont: fontGUIFont, Scale: scale})
		if err != nil {
			impl.mu.Lock()
			if !impl.closed {
				impl.failedGUIFont = fontGUIFont
			}
			impl.mu.Unlock()
			break
		}
		impl.animSnapMu.Lock()
		impl.mu.Lock()
		if impl.closed {
			impl.mu.Unlock()
			impl.animSnapMu.Unlock()
			return
		}
		if scaleChanged(impl.scale, scale) {
			impl.forceFullRepaint = true
			impl.configDirty = true
			impl.configGeneration++
			impl.mu.Unlock()
			impl.animSnapMu.Unlock()
			invalidate(snapshot.services, snapshot.window)
			break
		}
		oldCell := impl.resolver.Cell()
		cfg := impl.currentAnimConfigLocked()
		nextPlanner := render.NewPlanner(resolver)
		nextPlanner.SetLigaturesEnabled(cfg.LigaturesEnabled)
		nextAnimator := render.NewAnimator(nextPlanner, cfg, impl.animClock)
		impl.resolver = resolver
		impl.planner = nextPlanner
		impl.animator = nextAnimator
		planner, animator = impl.planner, impl.animator
		impl.lastGUIFont = fontGUIFont
		impl.failedGUIFont = ""
		impl.configDirty = true
		impl.configGeneration++
		snapshot.configDirty = true
		snapshot.generation = impl.configGeneration
		cellChanged = resolver.Cell() != oldCell
		stepOnly = false
		impl.mu.Unlock()
		impl.animSnapMu.Unlock()
		break
	}

	if stepOnly {
		frame, active, err := animator.Step()
		if err != nil {
			return
		}
		impl.mu.Lock()
		impl.lastAnimNow = now
		impl.mu.Unlock()
		applyFrame(view, handler, state, frame, &displayFrameComplete)
		if cellChanged {
			impl.queueCellResize(true, snapshot.services, snapshot.window)
		}
		if c != nil && active {
			c.AnimationFrame()
		}
		return
	}
	list, err := drawlist.Build(state)
	if err != nil {
		return
	}

	var frame renderlist.Frame
	var active bool
	if instant {
		frame, active, err = func() (renderlist.Frame, bool, error) {
			impl.animSnapMu.Lock()
			defer impl.animSnapMu.Unlock()
			impl.mu.Lock()
			if impl.animator != nil {
				animator = impl.animator
			}
			savedCfg := animator.Config()
			instantCfg := savedCfg
			instantCfg.CursorEnabled = false
			instantCfg.ScrollEnabled = false
			animator.SetConfig(instantCfg)
			impl.snappingAnim = true
			impl.preSnapAnimConfig = savedCfg
			snapAnim := impl.nvimConfig.snapshot
			impl.mu.Unlock()
			defer func() {
				impl.mu.Lock()
				impl.snappingAnim = false
				if impl.animator == animator && animator.Config() == instantCfg && sameAnimConfigSnapshot(impl.nvimConfig.snapshot, snapAnim) {
					animator.SetConfig(savedCfg)
				}
				impl.mu.Unlock()
			}()
			return animator.Submit(state, meta, list)
		}()
	} else {
		frame, active, err = animator.Submit(state, meta, list)
	}
	if err != nil {
		return
	}
	impl.mu.Lock()
	impl.lastSeq = state.Seq
	impl.lastAnimNow = now
	if snapshot.configDirty && impl.configGeneration == snapshot.generation {
		impl.configDirty = false
	}
	impl.mu.Unlock()
	applyFrame(view, handler, state, frame, &displayFrameComplete)
	if instant || snapNextFrame {
		active = false
	}
	if cellChanged {
		impl.queueCellResize(true, snapshot.services, snapshot.window)
	}
	if c != nil && active && !instant && !snapNextFrame {
		c.AnimationFrame()
	}
}

func applyFrame(view *render.View, handler *input.Handler, state *screen.State, frame renderlist.Frame, complete *bool) {
	if len(frame.Commands) == 0 {
		return
	}
	view.SetFrame(frame)
	handler.SetMode(state.Mode)
	*complete = true
}

func (impl *shellImpl) cell() text.CellSize {
	impl.mu.Lock()
	resolver := impl.resolver
	impl.mu.Unlock()
	if resolver == nil {
		return text.CellSize{}
	}
	return resolver.Cell()
}

func (impl *shellImpl) queueCellResize(cellChanged bool, services ui.Services, window *mygo.Window) {
	cell := impl.cell()
	if cell.Width <= 0 || cell.Height <= 0 || !cellChanged {
		return
	}
	impl.mu.Lock()
	impl.lastColumns, impl.lastRows = -1, -1
	impl.mu.Unlock()
	if window != nil {
		width, height := window.ContentSize()
		if err := (&Shell{impl: impl}).Resize(width, height); err != nil && !isCleanSessionError(err) {
			impl.recordCloseError(fmt.Errorf("app: font resize: %w", err))
		}
		return
	}
	services.Invalidate()
}

var animationEnvKeys = []string{
	"NVGUI_CURSOR_ANIM",
	"NVGUI_CURSOR_ANIMATE_COMMAND_LINE",
	"NVGUI_CURSOR_ANIMATE_INSERT",
	"NVGUI_SCROLL_ANIM",
	"NVGUI_LIGATURES",
	"NVGUI_NO_ANIMATIONS",
}
