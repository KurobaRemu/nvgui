package app

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/diag"
	"nvgui/protocol"
	"nvgui/screen"
	"nvgui/text"
)

func zoomReplayLatest(t *testing.T, shell *Shell) *screen.State {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := shell.ExportDiag(dir); err != nil {
		return nil
	}
	states, err := diag.Replay(dir)
	if err != nil || len(states) == 0 {
		return nil
	}
	return states[len(states)-1]
}

func TestZoomTitleState_DualFactorOrthogonalAndValidation(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	tester := ui.NewTester(shell.View, 800, 480)
	waitUntil(t, 5*time.Second, "initial frame", func() bool {
		tester.Frame()
		return shell.impl.session.Latest() != nil
	})
	if err := shell.Resize(800, 480); err != nil {
		t.Fatalf("Resize(800, 480): %v", err)
	}
	waitUntil(t, 5*time.Second, "initial grid", func() bool {
		tester.Frame()
		st := zoomReplayLatest(t, shell)
		return st != nil && st.Grids[1] != nil && st.Grids[1].Width > 0
	})
	baseGrid := zoomReplayLatest(t, shell).Grids[1]
	baseMetrics := shell.DisplayMetrics()
	if math.Abs(float64(shell.Scale()-1.0)) > 1e-4 || math.Abs(float64(shell.ScaleFactor()-1.0)) > 1e-4 {
		t.Fatalf("initial Scale=%v ScaleFactor=%v, want 1.0 / 1.0", shell.Scale(), shell.ScaleFactor())
	}

	if err := shell.SetScaleFactor(1.5); err != nil {
		t.Fatalf("SetScaleFactor(1.5): %v", err)
	}
	zoomedMetrics := shell.DisplayMetrics()
	if math.Abs(float64(shell.ScaleFactor()-1.5)) > 1e-4 || math.Abs(float64(zoomedMetrics.ScaleFactor-1.5)) > 1e-4 {
		t.Fatalf("after SetScaleFactor(1.5): ScaleFactor=%v Metrics=%+v", shell.ScaleFactor(), zoomedMetrics)
	}
	if zoomedMetrics.Cell.Width <= baseMetrics.Cell.Width*1.3 || zoomedMetrics.Cell.Height <= baseMetrics.Cell.Height*1.3 {
		t.Fatalf("SetScaleFactor(1.5) did not enlarge cell: base=%+v zoomed=%+v", baseMetrics.Cell, zoomedMetrics.Cell)
	}
	if zoomedMetrics.RebuildCount != baseMetrics.RebuildCount+1 {
		t.Fatalf("RebuildCount = %d, want %d", zoomedMetrics.RebuildCount, baseMetrics.RebuildCount+1)
	}

	wantCols := cellsFor(800, zoomedMetrics.Cell.Width)
	wantRows := cellsFor(480, zoomedMetrics.Cell.Height)
	waitUntil(t, 5*time.Second, "grid resize after SetScaleFactor(1.5)", func() bool {
		tester.Frame()
		st := zoomReplayLatest(t, shell)
		if st == nil || st.Grids[1] == nil {
			return false
		}
		g := st.Grids[1]
		return g.Width == wantCols && g.Height == wantRows && (g.Width < baseGrid.Width || g.Height < baseGrid.Height)
	})

	// Cross-DPI SetScale(2.0) and observePainterScale must NEVER overwrite ScaleFactor (1.5).
	if err := shell.SetScale(2.0); err != nil {
		t.Fatalf("SetScale(2.0): %v", err)
	}
	tester.SetScale(2.0)
	tester.Frame()
	if math.Abs(float64(shell.Scale()-2.0)) > 1e-4 {
		t.Fatalf("Scale after SetScale(2.0) = %v, want 2.0", shell.Scale())
	}
	if math.Abs(float64(shell.ScaleFactor()-1.5)) > 1e-4 {
		t.Fatalf("ScaleFactor overwritten by SetScale(2.0)/painter: got %v, want 1.5", shell.ScaleFactor())
	}

	// Reject non-positive / non-finite values; clamp out-of-range positive values safely.
	for _, bad := range []float32{0, -1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if err := shell.SetScaleFactor(bad); err == nil {
			t.Fatalf("SetScaleFactor(%v) returned nil, want error", bad)
		}
		if math.Abs(float64(shell.ScaleFactor()-1.5)) > 1e-4 {
			t.Fatalf("ScaleFactor mutated after rejected %v: got %v", bad, shell.ScaleFactor())
		}
	}
	if err := shell.SetScaleFactor(99); err != nil {
		t.Fatalf("SetScaleFactor(99) failed instead of clamping: %v", err)
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-4.0)) > 1e-4 {
		t.Fatalf("SetScaleFactor(99) = %v, want clamped 4.0", got)
	}
	if err := shell.SetScaleFactor(0.1); err != nil {
		t.Fatalf("SetScaleFactor(0.1) failed instead of clamping: %v", err)
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-0.5)) > 1e-4 {
		t.Fatalf("SetScaleFactor(0.1) = %v, want clamped 0.5", got)
	}
}

func TestZoomTitleState_BidirectionalHotSync(t *testing.T) {
	// 1. Startup via NVGUI_SCALE_FACTOR.
	envShell := m4AnimStart(t, nil, m4AnimFilteredEnv("NVGUI_SCALE_FACTOR=1.5"))
	if got := envShell.ScaleFactor(); math.Abs(float64(got-1.5)) > 1e-4 {
		t.Fatalf("NVGUI_SCALE_FACTOR=1.5 startup ScaleFactor = %v, want 1.5", got)
	}
	snapEnv, err := envShell.SyncNvimConfig(context.Background())
	if err != nil {
		t.Fatalf("envShell.SyncNvimConfig: %v", err)
	}
	if math.Abs(float64(snapEnv.ScaleFactor-1.5)) > 1e-4 {
		t.Fatalf("envShell NvimConfig.ScaleFactor = %v, want 1.5", snapEnv.ScaleFactor)
	}
	var nvimFactor float64
	if err := envShell.impl.session.Nvim().Eval("get(g:, 'nvgui_scale_factor', 0)", &nvimFactor); err != nil || math.Abs(nvimFactor-1.5) > 1e-4 {
		t.Fatalf("g:nvgui_scale_factor = (%v, %v), want 1.5", nvimFactor, err)
	}

	// Invalid NVGUI_SCALE_FACTOR falls back to 1.0.
	badEnvShell := m4AnimStart(t, nil, m4AnimFilteredEnv("NVGUI_SCALE_FACTOR=-2"))
	if got := badEnvShell.ScaleFactor(); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Fatalf("NVGUI_SCALE_FACTOR=-2 ScaleFactor = %v, want 1.0", got)
	}

	// 2. Runtime Neovim -> Go hot update (:let g:nvgui_scale_factor = 1.25).
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	if _, err := shell.SyncNvimConfig(context.Background()); err != nil {
		t.Fatalf("initial SyncNvimConfig: %v", err)
	}
	baseCell := shell.DisplayMetrics().Cell
	if err := shell.impl.session.Nvim().Command("let g:nvgui_scale_factor = 1.25"); err != nil {
		t.Fatalf("Command(let g:nvgui_scale_factor = 1.25): %v", err)
	}
	waitUntil(t, 5*time.Second, "Neovim->Go scale_factor 1.25", func() bool {
		s, syncErr := shell.SyncNvimConfig(context.Background())
		return syncErr == nil && math.Abs(float64(s.ScaleFactor-1.25)) < 1e-4 && math.Abs(float64(shell.ScaleFactor()-1.25)) < 1e-4
	})
	if got := shell.DisplayMetrics().Cell; got.Width <= baseCell.Width || got.Height <= baseCell.Height {
		t.Fatalf("Cell did not grow after Neovim->Go 1.25 sync: base=%+v got=%+v", baseCell, got)
	}

	// Invalid Neovim value must be ignored safely.
	if err := shell.impl.session.Nvim().Command("let g:nvgui_scale_factor = -3"); err != nil {
		t.Fatalf("Command(invalid scale_factor): %v", err)
	}
	if _, err := shell.SyncNvimConfig(context.Background()); err != nil {
		t.Fatalf("SyncNvimConfig after invalid value: %v", err)
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-1.25)) > 1e-4 {
		t.Fatalf("invalid g:nvgui_scale_factor altered ScaleFactor to %v, want 1.25", got)
	}

	// Huge finite float64 (> math.MaxFloat32) must clamp to 4.0 instead of overflowing float32 to +Inf -> 1.0.
	if err := shell.impl.session.Nvim().Command("let g:nvgui_scale_factor = 1.0e308"); err != nil {
		t.Fatalf("Command(1.0e308 scale_factor): %v", err)
	}
	waitUntil(t, 5*time.Second, "Neovim->Go huge float64 clamped to 4.0", func() bool {
		s, syncErr := shell.SyncNvimConfig(context.Background())
		return syncErr == nil && math.Abs(float64(s.ScaleFactor-4.0)) < 1e-4 && math.Abs(float64(shell.ScaleFactor()-4.0)) < 1e-4
	})
	if err := shell.SetScaleFactor(1.25); err != nil {
		t.Fatalf("restore SetScaleFactor(1.25): %v", err)
	}
	waitUntil(t, 5*time.Second, "restore g:nvgui_scale_factor == 1.25", func() bool {
		_, _ = shell.SyncNvimConfig(context.Background())
		var got float64
		return shell.impl.session.Nvim().Eval("get(g:, 'nvgui_scale_factor', 0)", &got) == nil && math.Abs(got-1.25) < 1e-4
	})

	// 3. Go -> Neovim reverse sync via ZoomIn / ZoomOut / ZoomReset.
	if err := shell.ZoomIn(); err != nil {
		t.Fatalf("ZoomIn: %v", err)
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-1.35)) > 1e-4 {
		t.Fatalf("ZoomIn from 1.25 = %v, want 1.35", got)
	}
	waitUntil(t, 5*time.Second, "g:nvgui_scale_factor == 1.35", func() bool {
		_, _ = shell.SyncNvimConfig(context.Background())
		var got float64
		return shell.impl.session.Nvim().Eval("get(g:, 'nvgui_scale_factor', 0)", &got) == nil && math.Abs(got-1.35) < 1e-4
	})

	if err := shell.ZoomOut(); err != nil {
		t.Fatalf("ZoomOut: %v", err)
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-1.25)) > 1e-4 {
		t.Fatalf("ZoomOut from 1.35 = %v, want 1.25", got)
	}
	waitUntil(t, 5*time.Second, "g:nvgui_scale_factor == 1.25", func() bool {
		_, _ = shell.SyncNvimConfig(context.Background())
		var got float64
		return shell.impl.session.Nvim().Eval("get(g:, 'nvgui_scale_factor', 0)", &got) == nil && math.Abs(got-1.25) < 1e-4
	})

	if err := shell.ZoomReset(); err != nil {
		t.Fatalf("ZoomReset: %v", err)
	}
	if got := shell.ScaleFactor(); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Fatalf("ZoomReset = %v, want 1.0", got)
	}
	waitUntil(t, 5*time.Second, "g:nvgui_scale_factor == 1.0", func() bool {
		_, _ = shell.SyncNvimConfig(context.Background())
		var got float64
		return shell.impl.session.Nvim().Eval("get(g:, 'nvgui_scale_factor', 0)", &got) == nil && math.Abs(got-1.0) < 1e-4
	})
}

func TestZoomTitleState_SetTitleRealNvimAndCanonicalInvariant(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	tester := ui.NewTester(shell.View, 800, 480)
	waitUntil(t, 5*time.Second, "initial frame", func() bool {
		tester.Frame()
		return shell.impl.session.Latest() != nil
	})
	if got := shell.Title(); got != "nvgui" {
		t.Fatalf("initial Title() = %q, want \"nvgui\"", got)
	}

	if err := shell.impl.session.Nvim().Command("set title titlestring=hello-nvgui"); err != nil {
		t.Fatalf("set title titlestring=hello-nvgui: %v", err)
	}
	waitUntil(t, 5*time.Second, "title updated to hello-nvgui", func() bool {
		tester.Frame()
		return shell.Title() == "hello-nvgui"
	})

	// Empty titlestring must fall back to "nvgui".
	if err := shell.impl.session.Nvim().Command("set titlestring=\\ "); err != nil {
		t.Fatalf("set non-empty first: %v", err)
	}
	if err := shell.impl.session.Nvim().Command("let &titlestring = ''"); err != nil {
		t.Fatalf("clear titlestring: %v", err)
	}
	// Also test explicit empty FrameMeta fallback directly.
	shell.impl.syncWindowAppearance(nil, &screen.FrameMeta{HasTitle: true, Title: ""})
	if got := shell.Title(); got != "nvgui" {
		t.Fatalf("empty title fallback = %q, want \"nvgui\"", got)
	}

	// DefaultColors sync updates window background color consistently.
	shell.impl.syncWindowAppearance(&screen.State{DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e}}, nil)
	if got := shell.impl.windowBackgroundColor(); got != "#1e1e2e" {
		t.Fatalf("windowBackgroundColor after DefaultColors = %q, want \"#1e1e2e\"", got)
	}

	// Verify Committed Screen golden snapshot (Canonical JSON) is completely unaffected by set_title.
	rPlain := screen.NewReducer()
	rTitled := screen.NewReducer()
	base := []protocol.Event{
		protocol.GridResize{Grid: 1, Width: 20, Height: 6},
		protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "T", Repeat: 1}}},
	}
	s1, err := rPlain.Apply(append(append([]protocol.Event(nil), base...), protocol.Flush{}))
	if err != nil {
		t.Fatalf("rPlain.Apply: %v", err)
	}
	s2, err := rTitled.Apply(append(append([]protocol.Event(nil), base...),
		protocol.Unknown{Name: "set_title", Args: []any{"hello-nvgui"}},
		protocol.Flush{},
	))
	if err != nil {
		t.Fatalf("rTitled.Apply: %v", err)
	}
	j1, err := screen.Canonical(s1[0])
	if err != nil {
		t.Fatalf("Canonical(plain): %v", err)
	}
	j2, err := screen.Canonical(s2[0])
	if err != nil {
		t.Fatalf("Canonical(titled): %v", err)
	}
	if !bytes.Equal(j1, j2) {
		t.Fatalf("set_title polluted Committed Screen Canonical JSON:\n got %s\nwant %s", j2, j1)
	}
}

func TestZoomTitleState_WindowStateKeyBackgroundAndViewMenu(t *testing.T) {
	req := LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4AnimFilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	ws := NewWorkspace(WorkspaceOptions{BaseRequest: req, Headless: true, Clipboard: &testClipboard{}})
	t.Cleanup(func() { _ = ws.Close() })

	id1, sh1, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #1: %v", err)
	}
	id2, sh2, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #2: %v", err)
	}

	mainOpts := windowOptionsForShell(sh1, req, id1 == 1)
	if mainOpts.StateKey != "main" {
		t.Fatalf("main window StateKey = %q, want \"main\"", mainOpts.StateKey)
	}
	if mainOpts.BackgroundColor == "" {
		t.Fatal("main window BackgroundColor is empty")
	}
	secondOpts := windowOptionsForShell(sh2, req, id2 == 1)
	if secondOpts.StateKey != "" {
		t.Fatalf("second window StateKey = %q, want empty", secondOpts.StateKey)
	}
	if secondOpts.BackgroundColor == "" {
		t.Fatal("second window BackgroundColor is empty")
	}

	menu := ws.BuildMenu()
	zoomInItem := menu.ItemByID("zoom-in")
	zoomOutItem := menu.ItemByID("zoom-out")
	zoomResetItem := menu.ItemByID("zoom-reset")
	if zoomInItem == nil || zoomOutItem == nil || zoomResetItem == nil {
		t.Fatalf("View menu missing zoom items: in=%v out=%v reset=%v", zoomInItem, zoomOutItem, zoomResetItem)
	}
	if zoomInItem.Click == nil || zoomOutItem.Click == nil || zoomResetItem.Click == nil {
		t.Fatal("View menu zoom items must have non-nil Click handlers")
	}

	// Focus window 1 and route View menu clicks to window 1.
	if !ws.FocusWindow(id1) {
		t.Fatalf("FocusWindow(%d) failed", id1)
	}
	zoomInItem.Click(zoomInItem, nil)
	if math.Abs(float64(sh1.ScaleFactor()-1.1)) > 1e-4 {
		t.Fatalf("sh1 ScaleFactor after Zoom In menu = %v, want 1.1", sh1.ScaleFactor())
	}
	if math.Abs(float64(sh2.ScaleFactor()-1.0)) > 1e-4 {
		t.Fatalf("sh2 ScaleFactor unexpectedly changed to %v, want 1.0", sh2.ScaleFactor())
	}

	// Focus window 2 and route View menu clicks to window 2.
	if !ws.FocusWindow(id2) {
		t.Fatalf("FocusWindow(%d) failed", id2)
	}
	zoomInItem.Click(zoomInItem, nil)
	zoomInItem.Click(zoomInItem, nil)
	if math.Abs(float64(sh2.ScaleFactor()-1.2)) > 1e-4 {
		t.Fatalf("sh2 ScaleFactor after 2x Zoom In = %v, want 1.2", sh2.ScaleFactor())
	}
	zoomOutItem.Click(zoomOutItem, nil)
	if math.Abs(float64(sh2.ScaleFactor()-1.1)) > 1e-4 {
		t.Fatalf("sh2 ScaleFactor after Zoom Out = %v, want 1.1", sh2.ScaleFactor())
	}
	zoomResetItem.Click(zoomResetItem, nil)
	if math.Abs(float64(sh2.ScaleFactor()-1.0)) > 1e-4 {
		t.Fatalf("sh2 ScaleFactor after Actual Size = %v, want 1.0", sh2.ScaleFactor())
	}

	// Verify Cmd+= / Cmd+Shift+= / Cmd+- / Cmd+0 shortcuts and Pinch InputZoom on Shell.
	tester := ui.NewTester(sh2.View, 800, 480)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyEqual)
	if got := sh2.ScaleFactor(); math.Abs(float64(got-1.1)) > 1e-4 {
		t.Fatalf("Cmd+= ScaleFactor = %v, want 1.1", got)
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyEqual)
	if got := sh2.ScaleFactor(); math.Abs(float64(got-1.2)) > 1e-4 {
		t.Fatalf("Cmd+Shift+= ScaleFactor = %v, want 1.2", got)
	}
	tester.Key(ui.Cmd, ui.KeyMinus)
	if got := sh2.ScaleFactor(); math.Abs(float64(got-1.1)) > 1e-4 {
		t.Fatalf("Cmd+- ScaleFactor = %v, want 1.1", got)
	}
	tester.Key(ui.Cmd, ui.Key0)
	if got := sh2.ScaleFactor(); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Fatalf("Cmd+0 ScaleFactor = %v, want 1.0", got)
	}
	tester.Pinch(100, 100, 1.5)
	if got := sh2.ScaleFactor(); math.Abs(float64(got-1.5)) > 1e-4 {
		t.Fatalf("Pinch(1.5) ScaleFactor = %v, want 1.5", got)
	}

	// Verify Workspace.HandleShortcut routing directly.
	var shortcutHits int
	wsTester := ui.NewTester(func(c *ui.Context) {
		if ws.HandleShortcut(c, id1) {
			shortcutHits++
		}
		ui.Box(c).Fill()
	}, 200, 100)
	wsTester.Key(ui.Cmd, ui.KeyEqual)
	if shortcutHits != 1 || math.Abs(float64(sh1.ScaleFactor()-1.2)) > 1e-4 {
		t.Fatalf("Workspace.HandleShortcut Cmd+= hits=%d sh1.ScaleFactor=%v, want hits=1 1.2", shortcutHits, sh1.ScaleFactor())
	}
}

func TestZoomTitleState_ContinuousZoomAndConcurrentDPIScaleRace(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	tester := ui.NewTester(shell.View, 800, 480)
	waitUntil(t, 5*time.Second, "initial frame", func() bool {
		tester.Frame()
		return shell.impl.session.Latest() != nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, 64)

	// >= 20 alternating ZoomIn / ZoomOut / ZoomReset steps.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for step := 0; step < 24; step++ {
			var err error
			switch step % 5 {
			case 0, 1, 2:
				err = shell.ZoomIn()
			case 3:
				err = shell.ZoomOut()
			case 4:
				err = shell.ZoomReset()
			}
			if err != nil {
				errs <- fmt.Errorf("zoom step %d: %w", step, err)
				return
			}
		}
	}()

	// Concurrent backing DPI SetScale(1.0 / 2.0) + display recovery.
	wg.Add(1)
	go func() {
		defer wg.Done()
		scales := []float32{1.0, 2.0, 1.5, 2.0, 1.0}
		for step := 0; step < 20; step++ {
			if err := shell.SetScale(scales[step%len(scales)]); err != nil {
				errs <- fmt.Errorf("SetScale step %d: %w", step, err)
				return
			}
			_ = shell.DisplayMetrics()
		}
	}()

	// Concurrent frame rendering.
	wg.Add(1)
	go func() {
		defer wg.Done()
		localTester := ui.NewTester(shell.View, 800, 480)
		for step := 0; step < 16; step++ {
			localTester.Frame()
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	finalScale := shell.Scale()
	finalFactor := shell.ScaleFactor()
	metrics := shell.DisplayMetrics()
	if math.Abs(float64(metrics.Scale-finalScale)) > 1e-4 {
		t.Fatalf("DisplayMetrics.Scale = %v != Shell.Scale() = %v", metrics.Scale, finalScale)
	}
	if math.Abs(float64(metrics.ScaleFactor-finalFactor)) > 1e-4 {
		t.Fatalf("DisplayMetrics.ScaleFactor = %v != Shell.ScaleFactor() = %v", metrics.ScaleFactor, finalFactor)
	}
	wantResolver, err := text.NewResolver(text.Config{
		GUIFont:     shell.impl.lastGUIFont,
		Scale:       finalScale,
		ScaleFactor: finalFactor,
		LineSpace:   float32(shell.impl.lastLineSpace),
		LineHeight:  shell.impl.lineHeight,
	})
	if err != nil {
		t.Fatalf("text.NewResolver: %v", err)
	}
	wantCell := wantResolver.Cell()
	if metrics.Cell != wantCell {
		t.Fatalf("final Cell = %+v, want %+v (Scale=%v ScaleFactor=%v gotCfg=%+v wantCfg=%+v)", metrics.Cell, wantCell, finalScale, finalFactor, shell.impl.resolver.Config(), wantResolver.Config())
	}
}
