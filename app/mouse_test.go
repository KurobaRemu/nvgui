package app

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/diag"
	"nvgui/input"
	"nvgui/screen"
)

func evalInt(t *testing.T, shell *Shell, expr string) int {
	t.Helper()
	var value int
	if err := shell.impl.session.Nvim().Eval(expr, &value); err != nil {
		t.Fatalf("Eval(%s): %v", expr, err)
	}
	return value
}

func cellCenter(shell *Shell, row, col int) (float32, float32) {
	cell := shell.impl.cell()
	return (float32(col) + 0.5) * cell.Width, (float32(row) + 0.5) * cell.Height
}

func waitShellReady(t *testing.T, shell *Shell, tester *ui.Tester) {
	t.Helper()
	waitUntil(t, 5*time.Second, "initial committed frame", func() bool {
		tester.Frame()
		state := shell.impl.session.Latest()
		return state != nil && state.Grids[1] != nil && state.Grids[1].Width > 0 && state.Grids[1].Height > 0
	})
}

func TestAppMouseIntegrationSplitDragFloatScrollAndMeta(t *testing.T) {
	shell := startTestShell(t, &testClipboard{})
	cell := shell.impl.cell()
	winW := windowDimension(80, defaultColumns, cell.Width)
	winH := windowDimension(24, defaultRows, cell.Height)
	tester := ui.NewTester(shell.View, winW, winH)
	waitShellReady(t, shell, tester)

	nv := shell.impl.session.Nvim()

	// Ensure mouse is enabled across all modes and configure clean buffer lines.
	if err := nv.Command("set mouse=a"); err != nil {
		t.Fatalf("set mouse=a: %v", err)
	}

	// 1. Split window click focus switching (Neovim 0.12 grid=0).
	if err := nv.Command("vsplit"); err != nil {
		t.Fatalf("vsplit: %v", err)
	}
	var leftWin, rightWin int
	if err := nv.ExecLua(`
		local wins = vim.api.nvim_tabpage_list_wins(0)
		local left, right = wins[1], wins[2]
		if vim.api.nvim_win_get_position(left)[2] > vim.api.nvim_win_get_position(right)[2] then
			left, right = right, left
		end
		return {left, right}
	`, &[]*int{&leftWin, &rightWin}); err != nil {
		t.Fatalf("query split wins: %v", err)
	}
	waitUntil(t, 3*time.Second, "split redraw", func() bool {
		tester.Frame()
		return len(shell.impl.session.Latest().Placements) >= 2
	})

	// Click inside right split (col 60, row 5), then left split (col 10, row 5).
	rx, ry := cellCenter(shell, 5, 60)
	tester.ClickAt(rx, ry)
	waitUntil(t, 3*time.Second, "focus right split on click", func() bool {
		tester.Frame()
		return evalInt(t, shell, "win_getid()") == rightWin
	})

	lx, ly := cellCenter(shell, 5, 10)
	tester.ClickAt(lx, ly)
	waitUntil(t, 3*time.Second, "focus left split on click", func() bool {
		tester.Frame()
		return evalInt(t, shell, "win_getid()") == leftWin
	})

	// 2. Floating window hit detection via grid=0: mouse_enabled=false passthrough vs mouse_enabled=true hit.
	var passthroughFloat int
	if err := nv.ExecLua(`
		local buf = vim.api.nvim_create_buf(false, true)
		vim.api.nvim_buf_set_lines(buf, 0, -1, false, {'passthrough float'})
		return vim.api.nvim_open_win(buf, false, {
			relative = 'editor',
			row = 2,
			col = 50,
			width = 24,
			height = 6,
			style = 'minimal',
			border = 'single',
			focusable = true,
			mouse = false,
		})
	`, &passthroughFloat); err != nil {
		t.Fatalf("open mouse=false float: %v", err)
	}
	waitUntil(t, 3*time.Second, "mouse=false float redraw", func() bool {
		tester.Frame()
		state := shell.impl.session.Latest()
		for _, p := range state.Placements {
			if int(p.Win) == passthroughFloat {
				return !p.MouseEnabled
			}
		}
		return false
	})

	// Click inside the mouse=false float area (row 4, col 56): MUST pass through to rightWin underneath, NOT focus passthroughFloat!
	fx, fy := cellCenter(shell, 4, 56)
	tester.ClickAt(fx, fy)
	waitUntil(t, 3*time.Second, "click passes through mouse=false float to rightWin", func() bool {
		tester.Frame()
		return evalInt(t, shell, "win_getid()") == rightWin
	})
	if cur := evalInt(t, shell, "win_getid()"); cur == passthroughFloat {
		t.Fatalf("click hit mouse=false float %d instead of passing through to %d", passthroughFloat, rightWin)
	}

	// Now open a mouse=true float on top and verify clicking hits & focuses it!
	var interactiveFloat int
	if err := nv.ExecLua(`
		local buf = vim.api.nvim_create_buf(false, true)
		vim.api.nvim_buf_set_lines(buf, 0, -1, false, {'interactive float'})
		return vim.api.nvim_open_win(buf, false, {
			relative = 'editor',
			row = 10,
			col = 48,
			width = 24,
			height = 6,
			style = 'minimal',
			border = 'single',
			focusable = true,
			mouse = true,
		})
	`, &interactiveFloat); err != nil {
		t.Fatalf("open mouse=true float: %v", err)
	}
	waitUntil(t, 3*time.Second, "mouse=true float redraw", func() bool {
		tester.Frame()
		state := shell.impl.session.Latest()
		for _, p := range state.Placements {
			if int(p.Win) == interactiveFloat {
				return p.MouseEnabled
			}
		}
		return false
	})

	ifx, ify := cellCenter(shell, 12, 54)
	tester.ClickAt(ifx, ify)
	waitUntil(t, 3*time.Second, "click focuses mouse=true float", func() bool {
		tester.Frame()
		return evalInt(t, shell, "win_getid()") == interactiveFloat
	})

	// Close floats and collapse to single clean window for drag selection & scrolling tests.
	if err := nv.ExecLua(`
		local target, f1, f2 = ...
		if vim.api.nvim_win_is_valid(f1) then vim.api.nvim_win_close(f1, true) end
		if vim.api.nvim_win_is_valid(f2) then vim.api.nvim_win_close(f2, true) end
		vim.api.nvim_set_current_win(target)
		vim.cmd('only!')
	`, nil, leftWin, passthroughFloat, interactiveFloat); err != nil {
		t.Fatalf("close floats and only!: %v", err)
	}
	setBufferLine(t, shell, "hello world from nvgui mouse test")
	waitUntil(t, 3*time.Second, "single window redraw", func() bool {
		tester.Frame()
		return strings.Contains(bufferText(t, shell), "hello world")
	})

	// 3. Drag text selection across "world" (cols 6..10 on row 0).
	sx, sy := cellCenter(shell, 0, 6)
	mx, my := cellCenter(shell, 0, 8)
	ex, ey := cellCenter(shell, 0, 10)
	tester.Press(sx, sy)
	tester.Move(mx, my)
	tester.Move(ex, ey)
	tester.Release(ex, ey)
	waitUntil(t, 3*time.Second, "visual mode after drag", func() bool {
		tester.Frame()
		return evalString(t, shell, "mode()") == "v"
	})
	tester.Type("y")
	waitUntil(t, 3*time.Second, "yank dragged selection", func() bool {
		tester.Frame()
		return evalString(t, shell, `getreg('"')`) == "world"
	})

	// 4. mousemoveevent option_set sync + real <MouseMove> delivery in Neovim 0.12.
	if err := nv.ExecLua(`
		vim.o.mousemoveevent = true
		vim.g.nvgui_mouse_moves = 0
		vim.keymap.set('n', '<MouseMove>', function()
			vim.g.nvgui_mouse_moves = (vim.g.nvgui_mouse_moves or 0) + 1
		end)
	`, nil); err != nil {
		t.Fatalf("setup mousemoveevent: %v", err)
	}
	waitUntil(t, 3*time.Second, "FrameMeta.MouseMoveEvent=true", func() bool {
		tester.Frame()
		return shell.impl.mouse.MouseMoveEvent()
	})
	m1x, m1y := cellCenter(shell, 1, 2)
	m2x, m2y := cellCenter(shell, 1, 4)
	tester.Move(m1x, m1y)
	tester.Move(m2x, m2y)
	waitUntil(t, 3*time.Second, "Neovim received <MouseMove>", func() bool {
		tester.Frame()
		return evalInt(t, shell, "g:nvgui_mouse_moves") >= 1
	})
	if err := nv.Command("set nomousemoveevent"); err != nil {
		t.Fatalf("set nomousemoveevent: %v", err)
	}
	waitUntil(t, 3*time.Second, "FrameMeta.MouseMoveEvent=false", func() bool {
		tester.Frame()
		return !shell.impl.mouse.MouseMoveEvent()
	})

	// 5. mouse_off suppresses clicks in real Neovim; mouse_on restores them.
	if err := nv.Command("set mouse="); err != nil {
		t.Fatalf("set mouse=: %v", err)
	}
	waitUntil(t, 3*time.Second, "FrameMeta.MouseEnabled=false", func() bool {
		tester.Frame()
		return !shell.impl.mouse.Enabled()
	})
	beforeMouseSent := len(shell.impl.queue.SentMouse())
	cx, cy := cellCenter(shell, 0, 15)
	tester.ClickAt(cx, cy)
	tester.Frame()
	if after := len(shell.impl.queue.SentMouse()); after != beforeMouseSent {
		t.Fatalf("mouse_off allowed mouse RPCs: before=%d after=%d", beforeMouseSent, after)
	}
	if err := nv.Command("set mouse=a"); err != nil {
		t.Fatalf("restore mouse=a: %v", err)
	}
	waitUntil(t, 3*time.Second, "FrameMeta.MouseEnabled=true", func() bool {
		tester.Frame()
		return shell.impl.mouse.Enabled()
	})

	// 6. 30-step 120Hz trackpad scroll stream in real Neovim (sub-cell accumulation + fast flick + reversal + horizontal + key barrier).
	if err := nv.ExecLua(`
		local lines = {}
		for i = 1, 160 do
			lines[i] = string.format("line-%03d %s", i, string.rep("abcdefghij", 12))
		end
		vim.api.nvim_buf_set_lines(0, 0, -1, false, lines)
		vim.api.nvim_win_set_cursor(0, {1, 0})
		vim.wo.wrap = false
	`, nil); err != nil {
		t.Fatalf("populate scroll buffer: %v", err)
	}
	waitUntil(t, 3*time.Second, "scroll buffer at line 1", func() bool {
		tester.Frame()
		return evalInt(t, shell, "line('w0')") == 1
	})

	scrollX, scrollY := cellCenter(shell, 8, 20)
	sentBeforeStream := len(shell.impl.queue.SentMouse())

	// 15 sub-cell steps down (0.35 cell/step -> ~5 cell crossings) + 5 fast steps down (1.5 cells/step -> ~7 cell crossings).
	for step := 0; step < 15; step++ {
		tester.Scroll(scrollX, scrollY, 0, 0.35*cell.Height)
	}
	for step := 0; step < 5; step++ {
		tester.Scroll(scrollX, scrollY, 0, 1.5*cell.Height)
	}
	waitUntil(t, 3*time.Second, "buffer scrolled down", func() bool {
		tester.Frame()
		return evalInt(t, shell, "line('w0')") > 15
	})
	peakTopLine := evalInt(t, shell, "line('w0')")

	// Key barrier: record a marker in a Neovim variable via command-line/normal key or insert mark.
	tester.Type("m")
	tester.Type("a")

	// 6 steps reversing upward (-1.2 cells/step) + 4 horizontal steps right (+1.5 cells/step).
	for step := 0; step < 6; step++ {
		tester.Scroll(scrollX, scrollY, 0, -1.2*cell.Height)
	}
	for step := 0; step < 4; step++ {
		tester.Scroll(scrollX, scrollY, 1.5*cell.Width, 0)
	}
	waitUntil(t, 3*time.Second, "buffer reversed up and scrolled horizontally", func() bool {
		tester.Frame()
		return evalInt(t, shell, "line('w0')") < peakTopLine && evalInt(t, shell, "winsaveview().leftcol") > 0
	})

	sentDuringStream := len(shell.impl.queue.SentMouse()) - sentBeforeStream
	if sentDuringStream <= 0 || sentDuringStream >= 30 {
		t.Fatalf("30-step 120Hz trackpad stream dispatched %d mouse commands, want > 0 and < 30", sentDuringStream)
	}
	markLine := evalInt(t, shell, "line(\"'a\")")
	if markLine < 1 {
		t.Fatalf("interleaved key barrier mark 'a was lost: line=%d", markLine)
	}

	// Verify ExportDiag + ReplayBundle remain valid with recorded mouse trace entries.
	bundleDir := filepath.Join(t.TempDir(), "mouse-diag")
	if err := shell.ExportDiag(bundleDir); err != nil {
		t.Fatalf("ExportDiag after mouse session: %v", err)
	}
	bundle, err := diag.ReplayBundle(bundleDir)
	if err != nil {
		t.Fatalf("diag.ReplayBundle after mouse session: %v", err)
	}
	var sawMouseInBundle bool
	for _, ev := range bundle.InputEvents {
		if ev.Kind == diag.TraceCommand && strings.HasPrefix(ev.Text, "mouse:") {
			sawMouseInBundle = true
			break
		}
	}
	if !sawMouseInBundle {
		t.Fatalf("ExportDiag bundle missing mouse trace events (count=%d)", len(bundle.InputEvents))
	}
	var replayed int
	if err := input.Replay(bundle.InputEvents, func(ui.InputEvent) { replayed++ }); err != nil {
		t.Fatalf("input.Replay(bundle.InputEvents): %v", err)
	}
	if replayed != len(bundle.InputEvents) {
		t.Fatalf("replayed %d events, want %d", replayed, len(bundle.InputEvents))
	}
}

func TestAppSyncMouseStateIgnoresStaleSeqAndPreservesDrag(t *testing.T) {
	shell := startTestShell(t, &testClipboard{})
	impl := shell.impl
	cell := impl.cell()

	// Advance to Seq=1001 (well above any live startup frames) with MouseEnabled=true, MouseMoveEvent=true, 100x30 root grid.
	newerState := &screen.State{
		Seq: 1001,
		Grids: map[int]*screen.Grid{
			1: {Width: 100, Height: 30},
		},
	}
	newerMeta := &screen.FrameMeta{
		Seq:            1001,
		MouseEnabled:   true,
		MouseMoveEvent: true,
	}
	impl.syncMouseState(newerState, newerMeta)
	if !impl.mouse.Enabled() || !impl.mouse.MouseMoveEvent() {
		t.Fatalf("after Seq=1001 sync: enabled=%v move=%v, want true/true", impl.mouse.Enabled(), impl.mouse.MouseMoveEvent())
	}

	// Start a drag at (row=2, col=3).
	if !impl.mouse.Handle(ui.InputEvent{
		Kind:   ui.InputPointerDown,
		Button: 0,
		X:      3.5 * cell.Width,
		Y:      2.5 * cell.Height,
	}) {
		t.Fatal("PointerDown returned false")
	}
	if !impl.mouse.Dragging() {
		t.Fatal("expected Dragging()=true after PointerDown")
	}

	// Concurrent stale Seq=1000 updates (MouseEnabled=false, MouseMoveEvent=false, 40x12) must NEVER overwrite Seq=1001 or clear drag.
	staleState := &screen.State{
		Seq: 1000,
		Grids: map[int]*screen.Grid{
			1: {Width: 40, Height: 12},
		},
	}
	staleMeta := &screen.FrameMeta{
		Seq:            1000,
		MouseEnabled:   false,
		MouseMoveEvent: false,
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				impl.syncMouseState(staleState, staleMeta)
				impl.syncMouseState(newerState, newerMeta)
			}
		}()
	}
	wg.Wait()

	if !impl.mouse.Enabled() {
		t.Fatal("stale Seq=1000 overwrote MouseEnabled=true to false")
	}
	if !impl.mouse.MouseMoveEvent() {
		t.Fatal("stale Seq=1000 overwrote MouseMoveEvent=true to false")
	}
	if !impl.mouse.Dragging() {
		t.Fatal("stale Seq=1000 cleared active drag state")
	}

	// Verify GridBounds was NOT rolled back to 40x12 by stale Seq=1000: moving to col=90, row=25 stays unclamped at (25, 90).
	impl.mouse.Handle(ui.InputEvent{
		Kind:   ui.InputPointerMove,
		Button: 0,
		X:      90.5 * cell.Width,
		Y:      25.5 * cell.Height,
	})
	if row, col := impl.mouse.GridPos(); row != 25 || col != 90 {
		t.Fatalf("GridPos after stale 40x12 sync = (%d, %d), want (25, 90)", row, col)
	}

	// Newer Seq=1002 with MouseEnabled=false MUST take effect and clear drag cleanly.
	impl.syncMouseState(&screen.State{Seq: 1002, Grids: map[int]*screen.Grid{1: {Width: 100, Height: 30}}}, &screen.FrameMeta{
		Seq:            1002,
		MouseEnabled:   false,
		MouseMoveEvent: false,
	})
	if impl.mouse.Enabled() || impl.mouse.MouseMoveEvent() || impl.mouse.Dragging() {
		t.Fatalf("Seq=1002 did not disable mouse/clear drag: enabled=%v move=%v dragging=%v",
			impl.mouse.Enabled(), impl.mouse.MouseMoveEvent(), impl.mouse.Dragging())
	}
}
