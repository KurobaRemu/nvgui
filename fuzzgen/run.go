package fuzzgen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/neovim/go-client/nvim"

	"nvgui/connection"
	"nvgui/diag"
	"nvgui/drawlist"
	"nvgui/protocol"
	"nvgui/screen"
)

const (
	watchdogTimeout = 2 * time.Second
	tickInterval    = 100 * time.Millisecond
	barrierMethod   = "nvgui_fuzzgen_barrier"
	redrawQueueSize = 4096
)

const stepLua = `
local step, channel = ...
local api = vim.api
if step.kind == 'split' then
  api.nvim_command('split')
elseif step.kind == 'vsplit' then
  api.nvim_command('vsplit')
elseif step.kind == 'wincmd' then
  pcall(api.nvim_command, 'wincmd ' .. step.wincmd_arg)
elseif step.kind == 'close_win' then
  local count = 0
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    local config = api.nvim_win_get_config(win)
    if not config.relative or config.relative == '' then
      count = count + 1
    end
  end
  if count > 1 then
    api.nvim_command('close')
  end
elseif step.kind == 'float_open' then
  local buffer = api.nvim_create_buf(false, true)
  api.nvim_buf_set_lines(buffer, 0, -1, false, { step.float_tag, 'nvgui fuzzgen' })
  local config = {
    relative = 'editor', row = step.row, col = step.col,
    width = step.width, height = step.height, style = 'minimal',
  }
  if type(step.anchor) == 'string' and step.anchor ~= '' then config.anchor = step.anchor end
  if type(step.border) == 'string' and step.border ~= '' and step.border ~= 'none' then config.border = step.border end
  if type(step.zindex) == 'number' and step.zindex > 0 then config.zindex = step.zindex end
  _G.nvgui_fuzzgen_floats = _G.nvgui_fuzzgen_floats or {}
  _G.nvgui_fuzzgen_floats[step.float_tag] = api.nvim_open_win(buffer, false, config)
elseif step.kind == 'float_close' then
  local win = _G.nvgui_fuzzgen_floats and _G.nvgui_fuzzgen_floats[step.float_tag]
  if win and api.nvim_win_is_valid(win) then
    api.nvim_win_close(win, true)
  end
  if _G.nvgui_fuzzgen_floats then _G.nvgui_fuzzgen_floats[step.float_tag] = nil end
elseif step.kind == 'set_hl' then
  local attrs = {}
  if type(step.fg) == 'string' and step.fg ~= '' then attrs.fg = step.fg end
  if type(step.bg) == 'string' and step.bg ~= '' then attrs.bg = step.bg end
  if type(step.attr) == 'string' and step.attr ~= '' then attrs[step.attr] = true end
  api.nvim_set_hl(0, step.hl_group, attrs)
elseif step.kind == 'extmark' then
  local namespace = api.nvim_create_namespace('nvgui-fuzzgen')
  local lines = api.nvim_buf_line_count(0)
  local row = 0
  if lines > 0 then row = lines - 1 end
  local line = api.nvim_buf_get_lines(0, row, row + 1, false)[1] or ''
  local col = math.max(0, #line - 1)
  api.nvim_buf_set_extmark(0, namespace, row, col, {
    virt_text = { { step.text, step.hl_group } },
    virt_text_pos = 'overlay',
    hl_group = step.hl_group,
  })
elseif step.kind == 'redraw' then
  api.nvim_command('redraw!')
end
api.nvim_command('redraw!')
api.nvim__redraw({ flush = true })
vim.rpcnotify(channel, 'nvgui_fuzzgen_barrier')
`

type inboundBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *inboundBuffer) Write(chunk []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(chunk)
}

func (buffer *inboundBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.buf.Bytes()...)
}

type redrawMessage struct {
	args    []any
	barrier chan struct{}
	drain   chan struct{}
}

type runner struct {
	reducer *screen.Reducer
	redraws chan redrawMessage
	done    chan struct{}
	wg      sync.WaitGroup
	inbound inboundBuffer

	mu     sync.Mutex
	frames [][]byte
	stalls int
	err    error
	seq    uint64

	barrierMu sync.Mutex
	barriers  []chan struct{}

	stopOnce sync.Once
	stopErr  error
}

func newRunner() *runner {
	worker := &runner{
		reducer: screen.NewReducer(),
		redraws: make(chan redrawMessage, redrawQueueSize),
		done:    make(chan struct{}),
	}
	watchdog := diag.NewWatchdog(watchdogTimeout, nil, func(int) {
		worker.mu.Lock()
		worker.stalls++
		worker.mu.Unlock()
	})
	worker.wg.Add(2)
	go worker.consume(watchdog)
	go worker.tick(watchdog)
	return worker
}

func (worker *runner) onRedraw(args []any) {
	select {
	case worker.redraws <- redrawMessage{args: normalizeRedrawArgs(args)}:
	case <-worker.done:
	}
}

func (worker *runner) onBarrier() {
	barrier := closedBarrier()
	select {
	case worker.redraws <- redrawMessage{barrier: barrier}:
	case <-worker.done:
	}
}

func closedBarrier() chan struct{} {
	barrier := make(chan struct{})
	close(barrier)
	return barrier
}

func runScript(ctx context.Context, script *Script, stepTimeout time.Duration) (*RunResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("fuzzgen: context is nil")
	}
	if err := validateScript(script); err != nil {
		return nil, err
	}
	if stepTimeout <= 0 {
		stepTimeout = defaultStepTimeout
	}
	result := &RunResult{Seed: script.Seed}
	if ctx.Err() != nil {
		result.Inconclusive = true
		result.Reason = ctx.Err().Error()
		return result, nil
	}

	tempDir, err := os.MkdirTemp("", "nvgui-fuzzgen-")
	if err != nil {
		return nil, fmt.Errorf("fuzzgen: create isolation directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	for _, name := range []string{
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
	} {
		if err := os.Mkdir(filepath.Join(tempDir, name), 0o700); err != nil {
			return nil, fmt.Errorf("fuzzgen: create %s: %w", name, err)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker := newRunner()
	conn, err := connection.Start(runCtx, connection.Options{
		Args:     []string{"--clean", "-n"},
		Width:    script.InitWidth,
		Height:   script.InitHeight,
		Dir:      tempDir,
		Env:      isolatedEnv(tempDir),
		Tap:      &worker.inbound,
		OnRedraw: worker.onRedraw,
	})
	if err != nil {
		if ctx.Err() != nil {
			result.Inconclusive = true
			result.Reason = ctx.Err().Error()
			worker.close(nil)
			return result, nil
		}
		worker.close(nil)
		return nil, fmt.Errorf("fuzzgen: start nvim: %w", err)
	}
	if err := conn.Nvim().RegisterHandler(barrierMethod, worker.onBarrier); err != nil {
		worker.close(conn)
		return nil, fmt.Errorf("fuzzgen: register barrier: %w", err)
	}
	defer func() {
		stopErr := worker.close(conn)
		worker.export(result)
		if stopErr != nil && ctx.Err() != nil && isCancellation(ctx.Err(), stopErr) {
			result.Inconclusive = true
			result.Reason = appendReason(result.Reason, ctx.Err().Error())
		}
	}()

	if err := worker.runSteps(ctx, conn.Nvim(), script.Steps, stepTimeout, result); err != nil {
		var inconclusive *inconclusiveError
		if errors.As(err, &inconclusive) {
			result.Inconclusive = true
			result.Reason = inconclusive.reason
			return result, nil
		}
		return nil, err
	}
	if err := worker.currentError(); err != nil {
		return nil, err
	}
	return result, nil
}

func validateScript(script *Script) error {
	if script == nil {
		return fmt.Errorf("fuzzgen: script is nil")
	}
	if script.Version != 1 {
		return fmt.Errorf("fuzzgen: script version %d is unsupported, want 1", script.Version)
	}
	if script.InitWidth <= 0 || script.InitHeight <= 0 {
		return fmt.Errorf("fuzzgen: invalid initial size %dx%d", script.InitWidth, script.InitHeight)
	}
	return nil
}

func (worker *runner) runSteps(ctx context.Context, client *nvim.Nvim, steps []Step, stepTimeout time.Duration, result *RunResult) error {
	startup := worker.newBarrier()
	if err := client.ExecLua("vim.rpcnotify(..., '"+barrierMethod+"')", nil, client.ChannelID()); err != nil {
		return fmt.Errorf("fuzzgen: request startup barrier: %w", err)
	}
	if err := worker.wait(ctx, startup, time.Now().Add(stepTimeout), stepTimeout, "startup redraw flush"); err != nil {
		return err
	}
	for index, step := range steps {
		if err := ctx.Err(); err != nil {
			return inconclusive(err)
		}
		if err := worker.currentError(); err != nil {
			return err
		}
		deadline := time.Now().Add(stepTimeout)
		beforeSeq := worker.latestSeq()
		barrier := worker.newBarrier()
		if err := executeStep(client, step, client.ChannelID()); err != nil {
			if ctx.Err() != nil && isCancellation(ctx.Err(), err) {
				return inconclusive(ctx.Err())
			}
			return fmt.Errorf("fuzzgen: step %d (%s): %w", index+1, step.Kind, err)
		}
		label := fmt.Sprintf("step %d (%s)", index+1, step.Kind)
		if err := worker.wait(ctx, barrier, deadline, stepTimeout, label); err != nil {
			return err
		}
		if err := worker.waitForSeq(ctx, beforeSeq, deadline, stepTimeout, label); err != nil {
			return err
		}
		result.StepsExecuted++
	}
	return nil
}

func (worker *runner) newBarrier() chan struct{} {
	barrier := make(chan struct{})
	worker.barrierMu.Lock()
	worker.barriers = append(worker.barriers, barrier)
	worker.barrierMu.Unlock()
	return barrier
}

func (worker *runner) wait(ctx context.Context, barrier chan struct{}, deadline time.Time, timeout time.Duration, label string) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		if err := worker.currentError(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return inconclusive(err)
		}
		select {
		case <-barrier:
			if err := worker.currentError(); err != nil {
				return err
			}
			return nil
		case <-ctx.Done():
			return inconclusive(ctx.Err())
		case <-timer.C:
			return &inconclusiveError{
				reason: fmt.Sprintf("timed out after %s waiting for %s", timeout, label),
			}
		}
	}
}

func (worker *runner) waitForSeq(ctx context.Context, beforeSeq uint64, deadline time.Time, timeout time.Duration, label string) error {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		if err := worker.currentError(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return inconclusive(err)
		}
		if worker.latestSeq() > beforeSeq {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return inconclusive(ctx.Err())
		case <-timer.C:
			return &inconclusiveError{
				reason: fmt.Sprintf("timed out after %s waiting for reducer seq to advance past %d for %s", timeout, beforeSeq, label),
			}
		}
	}
}

func (worker *runner) close(conn *connection.Conn) error {
	worker.stopOnce.Do(func() {
		var closeErr error
		if conn != nil {
			closeErr = conn.Close()
			<-conn.Exited()
		}
		drained := make(chan struct{})
		select {
		case worker.redraws <- redrawMessage{drain: drained}:
			<-drained
		case <-worker.done:
		}
		close(worker.done)
		worker.wg.Wait()
		worker.mu.Lock()
		worker.stopErr = errors.Join(worker.err, cleanClose(closeErr))
		worker.mu.Unlock()
	})
	return worker.stopErr
}

func cleanClose(err error) error {
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return err
}

func (worker *runner) export(result *RunResult) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	result.Stalls = worker.stalls
	result.Inbound = worker.inbound.Bytes()
	result.CanonFrames = append([][]byte(nil), worker.frames...)
	result.Committed = len(result.CanonFrames)
}

func isCancellation(cause error, err error) bool {
	if cause == nil || err == nil || !errors.Is(cause, context.Canceled) &&
		!errors.Is(cause, context.DeadlineExceeded) {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "session closed") ||
		strings.Contains(err.Error(), "signal:") ||
		strings.Contains(err.Error(), "exit status")
}

func inconclusive(err error) error {
	return &inconclusiveError{reason: err.Error()}
}

type inconclusiveError struct {
	reason string
}

func (err *inconclusiveError) Error() string { return err.reason }

func isolatedEnv(tempDir string) []string {
	env := make([]string, 0, len(os.Environ())+8)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "XDG_") || strings.HasPrefix(item, "LANG=") ||
			strings.HasPrefix(item, "LC_ALL=") || strings.HasPrefix(item, "TZ=") {
			continue
		}
		env = append(env, item)
	}
	for _, name := range []string{
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
	} {
		env = append(env, name+"="+filepath.Join(tempDir, name))
	}
	return append(env, "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8", "TZ=UTC")
}

func normalizeRedrawArgs(args []any) []any {
	normalized := make([]any, len(args))
	for index, item := range args {
		normalized[index] = normalizeRedrawValue(item)
	}
	return normalized
}

func normalizeRedrawValue(value any) any {
	switch typed := value.(type) {
	case nvim.Window:
		return protocol.Handle{Kind: 2, ID: int64(typed)}
	case nvim.Buffer:
		return protocol.Handle{Kind: 0, ID: int64(typed)}
	case nvim.Tabpage:
		return protocol.Handle{Kind: 1, ID: int64(typed)}
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizeRedrawValue(item)
		}
		return result
	default:
		return value
	}
}

func executeStep(client *nvim.Nvim, step Step, channel int) error {
	if !slices.Contains(allKinds, step.Kind) {
		return fmt.Errorf("unknown step kind %q", step.Kind)
	}
	if step.Kind == OpResize {
		if err := client.TryResizeUI(step.Width, step.Height); err != nil {
			return err
		}
		return client.ExecLua(flushLua(channel), nil)
	}
	if step.Kind == OpInput {
		if _, err := client.Input(step.Keys); err != nil {
			return err
		}
		return client.ExecLua(flushLua(channel), nil)
	}
	payload := map[string]any{
		"kind":       string(step.Kind),
		"keys":       step.Keys,
		"width":      step.Width,
		"height":     step.Height,
		"row":        step.Row,
		"col":        step.Col,
		"anchor":     step.Anchor,
		"border":     step.Border,
		"zindex":     step.ZIndex,
		"hl_group":   step.HLGroup,
		"fg":         step.FG,
		"bg":         step.BG,
		"attr":       step.Attr,
		"text":       step.Text,
		"float_tag":  step.FloatTag,
		"wincmd_arg": step.WinCmdArg,
	}
	return client.ExecLua(stepLua, nil, payload, channel)
}

func flushLua(channel int) string {
	return "vim.api.nvim_command('redraw!'); vim.api.nvim__redraw({flush=true}); vim.rpcnotify(" +
		fmt.Sprint(channel) + ", '" + barrierMethod + "')"
}

func (worker *runner) consume(watchdog *diag.Watchdog) {
	defer worker.wg.Done()
	for {
		select {
		case incoming := <-worker.redraws:
			if incoming.barrier != nil {
				worker.completeBarrier()
				continue
			}
			if incoming.drain != nil {
				close(incoming.drain)
				return
			}
			worker.observe(incoming.args, watchdog)
		case <-worker.done:
			return
		}
	}
}

func (worker *runner) tick(watchdog *diag.Watchdog) {
	defer worker.wg.Done()
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			watchdog.Tick()
		case <-worker.done:
			return
		}
	}
}

func (worker *runner) observe(args []any, watchdog *diag.Watchdog) {
	events, err := protocol.DecodeRedraw(args)
	if err != nil {
		worker.setError(fmt.Errorf("decode redraw: %w", err))
		return
	}
	watchdog.Observe(events)
	committed, err := worker.reducer.Apply(events)
	if err != nil {
		worker.setError(fmt.Errorf("apply redraw: %w", err))
		return
	}
	for _, state := range committed {
		canonical, err := worker.frame(state)
		if err != nil {
			worker.setError(err)
			return
		}
		worker.mu.Lock()
		worker.frames = append(worker.frames, canonical)
		worker.seq = state.Seq
		worker.mu.Unlock()
	}
}

func (worker *runner) frame(state *screen.State) ([]byte, error) {
	canonical, err := screen.Canonical(state)
	if err != nil {
		return nil, fmt.Errorf("canonical frame %d: %w", state.Seq, err)
	}
	firstList, err := drawlist.Build(state)
	if err != nil {
		return nil, fmt.Errorf("drawlist frame %d: %w", state.Seq, err)
	}
	secondList, err := drawlist.Build(state)
	if err != nil {
		return nil, fmt.Errorf("drawlist rebuild frame %d: %w", state.Seq, err)
	}
	first, err := drawlist.JSON(firstList)
	if err != nil {
		return nil, fmt.Errorf("drawlist JSON frame %d: %w", state.Seq, err)
	}
	second, err := drawlist.JSON(secondList)
	if err != nil {
		return nil, fmt.Errorf("drawlist rebuild JSON frame %d: %w", state.Seq, err)
	}
	if !bytes.Equal(first, second) {
		return nil, fmt.Errorf("drawlist frame %d is not idempotent", state.Seq)
	}
	if len(screen.Layers(state)) == 0 {
		return nil, fmt.Errorf("frame %d has no layers", state.Seq)
	}
	return canonical, nil
}

func (worker *runner) setError(err error) {
	worker.mu.Lock()
	if worker.err == nil {
		worker.err = err
	}
	worker.mu.Unlock()
	worker.barrierMu.Lock()
	for _, barrier := range worker.barriers {
		close(barrier)
	}
	worker.barriers = nil
	worker.barrierMu.Unlock()
}

func (worker *runner) completeBarrier() {
	worker.barrierMu.Lock()
	if len(worker.barriers) == 0 {
		worker.barrierMu.Unlock()
		return
	}
	barrier := worker.barriers[0]
	worker.barriers = worker.barriers[1:]
	worker.barrierMu.Unlock()
	close(barrier)
}

func (worker *runner) currentError() error {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.err
}

func (worker *runner) latestSeq() uint64 {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.seq
}

func appendReason(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + "; " + addition
}
