package app_test

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/screen"
)

func m2DisplayReplayLatest(t *testing.T, shell *app.Shell) *screen.State {
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

func TestPriorBug_Neovide2479_CrossDPIScaleChangeRelayoutsGridIdempotently(t *testing.T) {
	shell, clock := m2DisplayStartShell(t)
	tester := ui.NewTester(shell.View, 800, 480)
	if err := shell.Resize(800, 480); err != nil {
		t.Fatalf("Resize(800, 480): %v", err)
	}
	baseGrid := m2DisplayWaitGrid(t, shell, clock, tester, 800, 480, 1)
	base := shell.DisplayMetrics()

	tester.SetScale(2)
	tester.Frame()
	if err := shell.SetScale(2); err != nil {
		t.Fatalf("SetScale(2): %v", err)
	}
	metrics := shell.DisplayMetrics()
	if math.Abs(float64(metrics.Scale-2)) > 1e-4 || metrics.RebuildCount != 1 {
		t.Fatalf("after cross-DPI scale change: %+v, want Scale=2 RebuildCount=1", metrics)
	}
	if base.Cell.Width <= 0 || base.Cell.Height <= 0 || metrics.Cell.Width <= 0 || metrics.Cell.Height <= 0 {
		t.Fatalf("invalid cell sizes: base=%+v after=%+v", base.Cell, metrics.Cell)
	}
	if err := shell.SetScale(2); err != nil {
		t.Fatalf("idempotent SetScale(2): %v", err)
	}
	if got := shell.DisplayMetrics().RebuildCount; got != 1 {
		t.Fatalf("idempotent same-scale rebuild count = %d, want 1", got)
	}
	scaledGrid := m2DisplayWaitGrid(t, shell, clock, tester, 800, 480, 2)
	if metrics.Cell == base.Cell || (scaledGrid.Width == baseGrid.Width && scaledGrid.Height == baseGrid.Height) {
		t.Fatalf("2x resolver/grid did not relayout: base=%+v scaled=%+v, baseCell=%+v scaledCell=%+v", baseGrid, scaledGrid, base.Cell, metrics.Cell)
	}

	tester.SetScale(1.25)
	tester.Frame()
	if err := shell.SetScale(1.25); err != nil {
		t.Fatalf("SetScale(1.25): %v", err)
	}
	tester.SetScale(1)
	tester.Frame()
	if err := shell.SetScale(1); err != nil {
		t.Fatalf("SetScale(1): %v", err)
	}
	if got := shell.DisplayMetrics(); got.RebuildCount != 3 || got.Scale != 1 {
		t.Fatalf("return to 1x DisplayMetrics = %+v, want Scale=1 RebuildCount=3", got)
	}
	finalGrid := m2DisplayWaitGrid(t, shell, clock, tester, 800, 480, 1)
	final := shell.DisplayMetrics()
	wantColumns := int(math.Floor(float64(800) / float64(final.Cell.Width)))
	wantRows := int(math.Floor(float64(480) / float64(final.Cell.Height)))
	if finalGrid.Width != wantColumns || finalGrid.Height != wantRows {
		t.Fatalf("return to 1x grid = %dx%d, want %dx%d for cell %+v", finalGrid.Width, finalGrid.Height, wantColumns, wantRows, final.Cell)
	}
}

func m2DisplayWaitGrid(t *testing.T, shell *app.Shell, clock *fakeClock, tester *ui.Tester, width, height int, scale float32) *screen.Grid {
	t.Helper()
	cell := shell.DisplayMetrics().Cell
	if cell.Width <= 0 || cell.Height <= 0 {
		t.Fatalf("invalid cell at scale %v: %+v", scale, cell)
	}
	wantColumns := int(math.Floor(float64(width) / float64(cell.Width)))
	wantRows := int(math.Floor(float64(height) / float64(cell.Height)))
	var latest *screen.Grid
	if !m2DisplayWaitUntil(5*time.Second, func() bool {
		clock.Advance(10 * time.Millisecond)
		tester.Frame()
		state := m2DisplayReplayLatest(t, shell)
		if state == nil {
			return false
		}
		grid := state.Grids[1]
		if grid != nil {
			latest = grid
		}
		return grid != nil && (wantColumns < 1 || grid.Width == wantColumns) && (wantRows < 1 || grid.Height == wantRows)
	}) {
		t.Fatalf("grid at scale %v never reached %dx%d, last=%+v", scale, wantColumns, wantRows, latest)
	}
	return latest
}

func TestPriorBug_MacVim312_MacVim557_FullscreenAndSpaceReturnFullRepaintNoBlank(t *testing.T) {
	shell, clock := m2DisplayStartShell(t)
	tester := ui.NewTester(shell.View, 800, 480)
	m2DisplayInsertRows(t, shell, clock, tester, "m2_display_lru_row_1")
	tester.Advance(time.Second)
	tester.Frame()
	m2DisplayAssertNonBlank(t, tester, "initial")
	m2DisplayFlushMetadataLRU(t)

	before := shell.DisplayMetrics()
	shell.NotifyDisplayRecovery(app.DisplayRecoveryEnterFullScreen)
	tester.Frame()
	entered := shell.DisplayMetrics()
	if !entered.FullScreen || entered.RepaintCount <= before.RepaintCount {
		t.Fatalf("after fullscreen: %+v (before=%+v)", entered, before)
	}
	m2DisplayAssertNonBlank(t, tester, "fullscreen")

	shell.NotifyDisplayRecovery(app.DisplayRecoverySpaceReturn)
	tester.Frame()
	returned := shell.DisplayMetrics()
	if !returned.FullScreen || returned.RepaintCount <= entered.RepaintCount {
		t.Fatalf("after Space return: %+v (fullscreen=%+v)", returned, entered)
	}
	m2DisplayAssertNonBlank(t, tester, "space-return")

	steady := returned.RepaintCount
	for frame := 0; frame < 3; frame++ {
		tester.Frame()
		if got := shell.DisplayMetrics().RepaintCount; got != steady {
			t.Fatalf("steady frame %d incremented RepaintCount to %d, want %d", frame, got, steady)
		}
	}

	tester.SetWindowHidden(true)
	tester.Frame()
	tester.SetWindowHidden(false)
	tester.Frame()
	shown := shell.DisplayMetrics()
	if shown.RepaintCount <= steady {
		t.Fatalf("hidden->shown recovery RepaintCount = %d, want > %d", shown.RepaintCount, steady)
	}
	m2DisplayAssertNonBlank(t, tester, "window-unhidden")

	shell.NotifyDisplayRecovery(app.DisplayRecoveryLeaveFullScreen)
	tester.Frame()
	if got := shell.DisplayMetrics(); got.FullScreen {
		t.Fatalf("after LeaveFullScreen: %+v, want FullScreen=false", got)
	}
	m2DisplayAssertNonBlank(t, tester, "leave-fullscreen")
	tester.Advance(time.Second)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("steady state after fullscreen/Space recovery has FramePending() == true")
	}
}

func TestPriorBug_MacVim497_AbruptWindowManagerResizeSnapsAndRepaintsCleanly(t *testing.T) {
	shell, clock := m2DisplayStartShell(t)
	tester := ui.NewTester(shell.View, 800, 480)
	wantCfg := shell.AnimationConfig()
	m2DisplayInsertRows(t, shell, clock, tester, "m2_display_lru_row_1")
	tester.Type("G")
	for frame := 0; frame < 8; frame++ {
		clock.Advance(16 * time.Millisecond)
		tester.Advance(16 * time.Millisecond)
		tester.Frame()
	}
	m2DisplayAssertNonBlank(t, tester, "before-abrupt-resize")
	tester.Advance(time.Second)
	tester.Frame()
	m2DisplayFlushMetadataLRU(t)

	if err := shell.Resize(800, 480); err != nil {
		t.Fatalf("Resize(800, 480): %v", err)
	}
	if err := shell.Resize(400, 240); err != nil {
		t.Fatalf("Resize(400, 240) for automatic guard: %v", err)
	}
	autoBefore := shell.DisplayMetrics()
	tester.SetSize(400, 240)
	tester.Frame()
	m2DisplayAssertNonBlank(t, tester, "automatic-abrupt-resize")
	if got := shell.DisplayMetrics(); got.RepaintCount != autoBefore.RepaintCount {
		t.Fatalf("automatic resize guard incremented RepaintCount: %+v (before=%+v)", got, autoBefore)
	}
	tester.Advance(time.Second)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("automatic abrupt resize left FramePending() == true")
	}
	if got := shell.AnimationConfig(); got != wantCfg {
		t.Fatalf("automatic abrupt resize changed AnimationConfig: got %+v, want %+v", got, wantCfg)
	}
	m2DisplayFlushMetadataLRU(t)

	if err := shell.Resize(800, 480); err != nil {
		t.Fatalf("Resize(800, 480) before geometry jump: %v", err)
	}
	tester.SetSize(800, 480)
	tester.Advance(time.Second)
	tester.Frame()
	if err := shell.Resize(400, 240); err != nil {
		t.Fatalf("Resize(400, 240): %v", err)
	}
	before := shell.DisplayMetrics()
	shell.NotifyDisplayRecovery(app.DisplayRecoveryGeometryJump)
	tester.SetSize(400, 240)
	tester.Frame()
	m2DisplayAssertNonBlank(t, tester, "after-abrupt-resize")
	if got := shell.DisplayMetrics(); got.RepaintCount <= before.RepaintCount {
		t.Fatalf("geometry jump repaint count = %d, want > %d", got.RepaintCount, before.RepaintCount)
	}
	tester.Advance(time.Second)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("abrupt resize left FramePending() == true")
	}
	if got := shell.AnimationConfig(); got != wantCfg {
		t.Fatalf("geometry jump changed AnimationConfig: got %+v, want %+v", got, wantCfg)
	}
}
