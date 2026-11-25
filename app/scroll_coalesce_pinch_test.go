package app

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/screen"
)

type fakePinchTimer struct {
	mu      sync.Mutex
	delay   time.Duration
	fn      func()
	stopped bool
	fired   bool
}

func (t *fakePinchTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := !t.stopped && !t.fired
	t.stopped = true
	return wasActive
}

func (t *fakePinchTimer) Fire() bool {
	t.mu.Lock()
	if t.stopped || t.fired {
		t.mu.Unlock()
		return false
	}
	t.fired = true
	fn := t.fn
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
	return true
}

func (t *fakePinchTimer) Active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.stopped && !t.fired
}

func TestScrollCoalesce_RealNeovimTreesitterContextPreservesSmoothScroll(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv("NVGUI_SCROLL_ANIM=1"))
	impl := shell.impl
	baseTime := time.Unix(1_700_000_000, 0).UTC()
	impl.animClock.Sync(baseTime)

	if err := shell.Resize(800, 480); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	waitUntil(t, 5*time.Second, "initial state ready", func() bool {
		st, meta := impl.session.LatestSnapshot()
		return st != nil && meta != nil && len(meta.Viewports) > 0
	})

	// Populate buffer and enable smoothscroll so Neovim emits realistic win_viewport scroll_delta.
	const setupLua = `
local lines = {}
for i = 1, 160 do
  lines[i] = string.format("line_%03d_context_scroll_test", i)
end
vim.api.nvim_buf_set_lines(0, 0, -1, false, lines)
vim.opt.smoothscroll = true
vim.api.nvim_win_set_cursor(0, {1, 0})
vim.cmd('redraw')
`
	if err := impl.session.Nvim().ExecLua(setupLua, nil); err != nil {
		t.Fatalf("ExecLua setup: %v", err)
	}

	// Wait for setup flushes to settle and render once so impl.lastSeq matches the baseline.
	waitUntil(t, 5*time.Second, "buffer populated in screen state", func() bool {
		st := impl.session.Latest()
		if st == nil {
			return false
		}
		for _, g := range st.Grids {
			if g == nil || g.Height == 0 {
				continue
			}
			for _, row := range g.Cells {
				line := make([]byte, 0, len(row))
				for _, c := range row {
					line = append(line, c.Text...)
				}
				if string(line[:min(len(line), 8)]) == "line_001" {
					return true
				}
			}
		}
		return false
	})

	impl.refreshFrameWithContext(nil, baseTime, true)
	baseState, baseMeta := impl.session.LatestSnapshot()
	if baseState == nil || baseMeta == nil {
		t.Fatal("baseline snapshot nil")
	}
	mainGrid := 0
	for gridID, vp := range baseMeta.Viewports {
		if vp.LineCount >= 160 {
			mainGrid = gridID
			break
		}
	}
	if mainGrid == 0 {
		for gridID := range baseMeta.Viewports {
			mainGrid = gridID
			break
		}
	}
	if mainGrid == 0 {
		t.Fatalf("could not locate main window grid in Viewports: %+v", baseMeta.Viewports)
	}
	seq0 := baseState.Seq
	impl.mu.Lock()
	impl.lastSeq = seq0
	impl.mu.Unlock()

	// Simulate nvim-treesitter-context:
	// Flush N: main window scrolls down 5 lines (emits win_viewport with ScrollDelta=+5).
	// Flush N+1: immediately open/update top floating window (redraws float; main grid gets no ScrollDelta).
	const scrollAndFloatLua = `
vim.cmd("normal! 5\005")
vim.cmd("redraw")
local buf = vim.api.nvim_create_buf(false, true)
vim.api.nvim_buf_set_lines(buf, 0, -1, false, { "func treesitter_context_header() {" })
local win = vim.api.nvim_open_win(buf, false, {
  relative = "win",
  win = 0,
  row = 0,
  col = 0,
  width = 40,
  height = 1,
  style = "minimal",
  focusable = false,
  zindex = 20,
})
vim.cmd("redraw")
vim.api.nvim_win_set_config(win, {
  relative = "win",
  win = 0,
  row = 0,
  col = 0,
  width = 42,
  height = 1,
})
vim.cmd("redraw")
`
	if err := impl.session.Nvim().ExecLua(scrollAndFloatLua, nil); err != nil {
		t.Fatalf("ExecLua scroll+float: %v", err)
	}

	// Wait for >= 2 flushes AND verify the latest single flush has ScrollDelta==0 on mainGrid (the exact bug condition),
	// WITHOUT calling refreshFrameWithContext in between.
	waitUntil(t, 5*time.Second, "multi-flush scroll + float update", func() bool {
		st, meta := impl.session.LatestSnapshot()
		if st == nil || meta == nil || st.Seq < seq0+2 {
			return false
		}
		hasFloat := false
		for _, p := range st.Placements {
			if p.Kind == screen.PlacedFloat {
				hasFloat = true
				break
			}
		}
		if !hasFloat {
			return false
		}
		vp, ok := meta.Viewports[mainGrid]
		return ok && !vp.HasScrollDelta && vp.ScrollDelta == 0
	})

	// Single refreshFrameWithContext call must coalesce (seq0, latestSeq] and produce a non-zero scroll animation offset.
	nextTime := baseTime.Add(16 * time.Millisecond)
	impl.animClock.Sync(nextTime)
	impl.refreshFrameWithContext(nil, nextTime, false)

	impl.mu.Lock()
	offsetLines, offsetPx, active := impl.animator.ScrollOffset(mainGrid)
	lastSeq := impl.lastSeq
	impl.mu.Unlock()
	if !active || math.Abs(float64(offsetLines)) < 1.0 || math.Abs(float64(offsetPx)) < 1.0 {
		t.Fatalf("animator.ScrollOffset(grid %d) = (lines=%v, px=%v, active=%v), want active with non-zero offset", mainGrid, offsetLines, offsetPx, active)
	}
	if lastSeq < seq0+2 {
		t.Fatalf("impl.lastSeq = %d, want >= %d", lastSeq, seq0+2)
	}

	// Subsequent frame at same seq must NOT re-inject the aggregated ScrollDelta.
	stepTime := nextTime.Add(16 * time.Millisecond)
	impl.animClock.Sync(stepTime)
	impl.refreshFrameWithContext(nil, stepTime, false)
	impl.mu.Lock()
	nextOffset, _, _ := impl.animator.ScrollOffset(mainGrid)
	impl.mu.Unlock()
	if math.Abs(float64(nextOffset)) >= math.Abs(float64(offsetLines)) {
		t.Fatalf("scroll offset did not decay on next frame: before=%v after=%v", offsetLines, nextOffset)
	}
}

func TestScrollCoalesce_SameSeqRepaintDoesNotReinjectScrollDelta(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv("NVGUI_SCROLL_ANIM=1"))
	impl := shell.impl
	baseTime := time.Unix(1_700_000_000, 0).UTC()
	impl.animClock.Sync(baseTime)

	if err := shell.Resize(800, 480); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	waitUntil(t, 5*time.Second, "initial state ready", func() bool {
		st, meta := impl.session.LatestSnapshot()
		return st != nil && meta != nil && len(meta.Viewports) > 0
	})

	const setupLua = `
local lines = {}
for i = 1, 160 do
  lines[i] = string.format("line_%03d_same_seq_repaint_test", i)
end
vim.api.nvim_buf_set_lines(0, 0, -1, false, lines)
vim.opt.smoothscroll = true
vim.api.nvim_win_set_cursor(0, {1, 0})
vim.cmd('redraw')
`
	if err := impl.session.Nvim().ExecLua(setupLua, nil); err != nil {
		t.Fatalf("ExecLua setup: %v", err)
	}
	waitUntil(t, 5*time.Second, "buffer populated", func() bool {
		st := impl.session.Latest()
		if st == nil {
			return false
		}
		for _, g := range st.Grids {
			if g == nil || g.Height == 0 {
				continue
			}
			for _, row := range g.Cells {
				line := make([]byte, 0, len(row))
				for _, c := range row {
					line = append(line, c.Text...)
				}
				if string(line[:min(len(line), 8)]) == "line_001" {
					return true
				}
			}
		}
		return false
	})

	impl.refreshFrameWithContext(nil, baseTime, true)
	baseState, baseMeta := impl.session.LatestSnapshot()
	if baseState == nil || baseMeta == nil {
		t.Fatal("baseline snapshot nil")
	}
	mainGrid := 0
	for gridID, vp := range baseMeta.Viewports {
		if vp.LineCount >= 160 {
			mainGrid = gridID
			break
		}
	}
	if mainGrid == 0 {
		t.Fatalf("could not locate main window grid in Viewports: %+v", baseMeta.Viewports)
	}
	seq0 := baseState.Seq
	impl.mu.Lock()
	impl.lastSeq = seq0
	impl.mu.Unlock()

	if _, err := impl.session.Nvim().Input("<C-d>"); err != nil {
		t.Fatalf("Input <C-d>: %v", err)
	}
	var seq1 uint64
	waitUntil(t, 5*time.Second, "scroll flush with non-zero ScrollDelta", func() bool {
		st, meta := impl.session.LatestSnapshot()
		if st == nil || meta == nil || st.Seq <= seq0 {
			return false
		}
		vp, ok := meta.Viewports[mainGrid]
		if !ok || !vp.HasScrollDelta || vp.ScrollDelta == 0 {
			return false
		}
		seq1 = st.Seq
		return true
	})

	t1 := baseTime.Add(16 * time.Millisecond)
	impl.animClock.Sync(t1)
	impl.refreshFrameWithContext(nil, t1, false)
	impl.mu.Lock()
	offset1, _, active1 := impl.animator.ScrollOffset(mainGrid)
	lastSeq1 := impl.lastSeq
	impl.mu.Unlock()
	if !active1 || math.Abs(float64(offset1)) < 1.0 || lastSeq1 != seq1 {
		t.Fatalf("after first consume: offset=%v active=%v lastSeq=%d (want active, |offset|>=1, seq=%d)", offset1, active1, lastSeq1, seq1)
	}

	impl.mu.Lock()
	impl.configDirty = true
	impl.configGeneration++
	impl.mu.Unlock()
	impl.refreshFrameWithContext(nil, t1, false)
	impl.mu.Lock()
	offsetConfigDirty, _, _ := impl.animator.ScrollOffset(mainGrid)
	impl.mu.Unlock()
	if gotSeq := impl.session.Latest().Seq; gotSeq != seq1 {
		t.Fatalf("unexpected seq change during configDirty check: got %d want %d", gotSeq, seq1)
	}
	if math.Abs(float64(offsetConfigDirty-offset1)) > 1e-3 {
		t.Fatalf("configDirty repaint at same Seq=%d re-injected ScrollDelta: before=%v after=%v", seq1, offset1, offsetConfigDirty)
	}

	t2 := t1.Add(16 * time.Millisecond)
	impl.animClock.Sync(t2)
	impl.mu.Lock()
	impl.forceFullRepaint = true
	impl.snapNextFrame = false
	impl.mu.Unlock()
	impl.refreshFrameWithContext(nil, t2, false)
	impl.mu.Lock()
	offsetForceRepaint, _, _ := impl.animator.ScrollOffset(mainGrid)
	impl.mu.Unlock()
	if gotSeq := impl.session.Latest().Seq; gotSeq != seq1 {
		t.Fatalf("unexpected seq change during forceFullRepaint check: got %d want %d", gotSeq, seq1)
	}
	if math.Abs(float64(offsetForceRepaint)) >= math.Abs(float64(offset1)) {
		t.Fatalf("forceFullRepaint at same Seq=%d re-injected ScrollDelta: before=%v after=%v", seq1, offset1, offsetForceRepaint)
	}
}

func TestPinchZoomThrottle_DeterministicClockSettleKeyboardAndClose(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	impl := shell.impl

	var timersMu sync.Mutex
	var createdTimers []*fakePinchTimer
	impl.mu.Lock()
	impl.newPinchTimerFn = func(d time.Duration, fn func()) pinchTimer {
		ft := &fakePinchTimer{delay: d, fn: fn}
		timersMu.Lock()
		createdTimers = append(createdTimers, ft)
		timersMu.Unlock()
		return ft
	}
	impl.mu.Unlock()

	now := time.Unix(1_700_000_000, 0).UTC()
	impl.animClock.Sync(now)

	// Invalid InputZoom values must still be consumed (return true) without mutating ScaleFactor.
	for _, bad := range []float32{0, -1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if handled := impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: bad}); !handled {
			t.Fatalf("handleZoomInput(InputZoom, %v) = false, want true", bad)
		}
		if got := shell.ScaleFactor(); math.Abs(float64(got-1.0)) > 1e-4 {
			t.Fatalf("invalid InputZoom(%v) mutated ScaleFactor to %v", bad, got)
		}
	}

	baseRebuilds := shell.DisplayMetrics().RebuildCount

	// Simulate 120Hz (8ms per step) continuous 30-step pinch gesture with 1.01 per step.
	const steps = 30
	const stepScale = float32(1.01)
	expected := 1.0
	for i := 0; i < steps; i++ {
		if i > 0 {
			now = now.Add(8 * time.Millisecond)
			impl.animClock.Sync(now)
		}
		expected = clampUserScaleFactorFloat64(expected * float64(stepScale))
		if handled := impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: stepScale}); !handled {
			t.Fatalf("step %d: handleZoomInput returned false", i)
		}
	}

	rebuildsBeforeSettle := shell.DisplayMetrics().RebuildCount - baseRebuilds
	if rebuildsBeforeSettle == 0 || rebuildsBeforeSettle > 10 {
		t.Fatalf("throttled RebuildCount = %d for %d pinch events, want in [1, 10]", rebuildsBeforeSettle, steps)
	}

	// Gesture ends: fire the latest active settle timer after 50ms.
	now = now.Add(pinchSettleDelay)
	impl.animClock.Sync(now)
	timersMu.Lock()
	var latestTimer *fakePinchTimer
	activeTimers := 0
	for _, ft := range createdTimers {
		if ft.Active() {
			activeTimers++
			latestTimer = ft
		}
	}
	timersMu.Unlock()
	if activeTimers > 1 {
		t.Fatalf("active pending settle timers = %d, want <= 1", activeTimers)
	}
	if latestTimer != nil {
		if latestTimer.delay != pinchSettleDelay {
			t.Fatalf("settle timer delay = %v, want %v", latestTimer.delay, pinchSettleDelay)
		}
		latestTimer.Fire()
	}

	totalRebuilds := shell.DisplayMetrics().RebuildCount - baseRebuilds
	if totalRebuilds > 10 {
		t.Fatalf("total RebuildCount after settle = %d, want <= 10", totalRebuilds)
	}
	gotFinal := shell.ScaleFactor()
	if math.Abs(float64(gotFinal)-expected) > 1e-4 {
		t.Fatalf("ScaleFactor after settle = %.6f, want %.6f", gotFinal, expected)
	}

	// Interleaving with keyboard ZoomIn / ZoomReset must cancel pending pinch timer cleanly.
	now = now.Add(8 * time.Millisecond)
	impl.animClock.Sync(now)
	if !impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: 1.01}) {
		t.Fatal("pending pinch event not handled")
	}
	timersMu.Lock()
	pendingBeforeKey := createdTimers[len(createdTimers)-1]
	timersMu.Unlock()
	if !pendingBeforeKey.Active() {
		t.Fatal("expected pending settle timer after throttled pinch step")
	}

	if err := shell.ZoomReset(); err != nil {
		t.Fatalf("ZoomReset: %v", err)
	}
	if pendingBeforeKey.Active() {
		t.Fatal("ZoomReset did not cancel pending pinch settle timer")
	}
	// Even if a stale callback ran concurrently, generation guard must ignore it.
	if pendingBeforeKey.fn != nil {
		pendingBeforeKey.fn()
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Fatalf("ScaleFactor after ZoomReset + stale timer = %v, want 1.0", got)
	}

	// Interleaving with ZoomIn similarly cancels pending pinch.
	if !impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: 1.005}) {
		t.Fatal("sub-threshold pinch event not handled")
	}
	timersMu.Lock()
	pendingBeforeZoomIn := createdTimers[len(createdTimers)-1]
	timersMu.Unlock()
	if !pendingBeforeZoomIn.Active() {
		t.Fatal("expected pending settle timer for sub-threshold pinch")
	}
	if err := shell.ZoomIn(); err != nil {
		t.Fatalf("ZoomIn: %v", err)
	}
	if pendingBeforeZoomIn.Active() {
		t.Fatal("ZoomIn did not cancel pending pinch settle timer")
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-1.1)) > 1e-4 {
		t.Fatalf("ScaleFactor after ZoomIn = %v, want 1.1", got)
	}

	// Close() must stop any pending pinch timer and leave zero dangling timers.
	if !impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: 1.005}) {
		t.Fatal("pre-close pinch event not handled")
	}
	timersMu.Lock()
	pendingBeforeClose := createdTimers[len(createdTimers)-1]
	timersMu.Unlock()
	if !pendingBeforeClose.Active() {
		t.Fatal("expected pending settle timer before Close()")
	}
	if err := shell.Close(); err != nil {
		t.Fatalf("shell.Close(): %v", err)
	}
	if pendingBeforeClose.Active() {
		t.Fatal("shell.Close() left pending pinch settle timer active")
	}
	if pendingBeforeClose.fn != nil {
		pendingBeforeClose.fn()
	}
}

func TestPinchZoomThrottle_SubEpsilonMicroStepsAccumulateAndConverge(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	impl := shell.impl

	var timersMu sync.Mutex
	var createdTimers []*fakePinchTimer
	impl.mu.Lock()
	impl.newPinchTimerFn = func(d time.Duration, fn func()) pinchTimer {
		ft := &fakePinchTimer{delay: d, fn: fn}
		timersMu.Lock()
		createdTimers = append(createdTimers, ft)
		timersMu.Unlock()
		return ft
	}
	impl.mu.Unlock()

	latestActiveTimer := func() *fakePinchTimer {
		timersMu.Lock()
		defer timersMu.Unlock()
		var active *fakePinchTimer
		count := 0
		for _, ft := range createdTimers {
			if ft.Active() {
				count++
				active = ft
			}
		}
		if count > 1 {
			t.Fatalf("active pending settle timers = %d, want <= 1", count)
		}
		return active
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	impl.animClock.Sync(now)

	const stepScale = float32(1.00008)
	if scaleChanged(1.0, roundScaleFactorFloat64(clampUserScaleFactorFloat64(float64(stepScale)))) {
		t.Fatalf("test setup error: single step %v already crosses scaleEpsilon", stepScale)
	}

	baseRebuilds := shell.DisplayMetrics().RebuildCount
	expectedShort := 1.0
	for step := 0; step < 5; step++ {
		if step > 0 {
			now = now.Add(8 * time.Millisecond)
			impl.animClock.Sync(now)
		}
		expectedShort = clampUserScaleFactorFloat64(expectedShort * float64(stepScale))
		if !impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: stepScale}) {
			t.Fatalf("short step %d: handleZoomInput returned false", step)
		}
	}
	if got := shell.DisplayMetrics().RebuildCount - baseRebuilds; got != 0 {
		t.Fatalf("short micro-pinch rebuilt %d times before settle, want 0", got)
	}
	shortTimer := latestActiveTimer()
	if shortTimer == nil {
		t.Fatal("expected pending settle timer after 5 sub-epsilon pinch steps")
	}
	now = now.Add(pinchSettleDelay)
	impl.animClock.Sync(now)
	shortTimer.Fire()
	if got := shell.ScaleFactor(); math.Abs(float64(got)-expectedShort) > 1e-5 {
		t.Fatalf("ScaleFactor after 5 micro-steps settle = %.6f, want %.6f", got, expectedShort)
	}

	if err := shell.ZoomReset(); err != nil {
		t.Fatalf("ZoomReset: %v", err)
	}
	baseRebuilds = shell.DisplayMetrics().RebuildCount
	expectedLong := 1.0
	for step := 0; step < 300; step++ {
		now = now.Add(8 * time.Millisecond)
		impl.animClock.Sync(now)
		expectedLong = clampUserScaleFactorFloat64(expectedLong * float64(stepScale))
		if !impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: stepScale}) {
			t.Fatalf("long step %d: handleZoomInput returned false", step)
		}
	}
	rebuildsDuring := shell.DisplayMetrics().RebuildCount - baseRebuilds
	if rebuildsDuring < 1 || rebuildsDuring > 10 {
		t.Fatalf("300 micro-steps RebuildCount during gesture = %d, want in [1, 10]", rebuildsDuring)
	}
	now = now.Add(pinchSettleDelay)
	impl.animClock.Sync(now)
	if longTimer := latestActiveTimer(); longTimer != nil {
		longTimer.Fire()
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got)-expectedLong) > 1e-4 {
		t.Fatalf("ScaleFactor after 300 micro-steps settle = %.6f, want %.6f", got, expectedLong)
	}

	if err := shell.SetScaleFactor(maxUserScaleFactor); err != nil {
		t.Fatalf("SetScaleFactor(max): %v", err)
	}
	if !impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: stepScale}) {
		t.Fatal("clamped pinch event not handled")
	}
	if latestActiveTimer() != nil {
		t.Fatal("clamped pinch at max left an active settle timer")
	}
	impl.mu.Lock()
	activeAtClamp := impl.pinchActive
	impl.mu.Unlock()
	if activeAtClamp {
		t.Fatal("clamped pinch at max left pinchActive=true, want false")
	}
	if err := shell.Close(); err != nil {
		t.Fatalf("shell.Close(): %v", err)
	}
}

func TestPinchZoomThrottle_RealTimerConcurrentCloseRace(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	impl := shell.impl

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 24; i++ {
			_ = impl.handleZoomInput(ui.InputEvent{Kind: ui.InputZoom, Scale: 1.005})
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 6; i++ {
			_ = shell.SetScale(2.0)
			_ = shell.SetScale(1.0)
		}
	}()
	wg.Wait()
	if err := shell.Close(); err != nil {
		t.Fatalf("shell.Close() after real-timer pinch race: %v", err)
	}
}
