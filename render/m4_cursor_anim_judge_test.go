package render_test

import (
	"bytes"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

func m4CursorFixture(seq uint64, row, col int, mode string, msg *screen.MsgPlacement) (*screen.State, *drawlist.List) {
	const (
		w = 40
		h = 20
	)
	grid := newBlankGrid(w, h, 0)
	for r := 0; r < h; r++ {
		putRunes(grid.Cells[r], 0, "cursor_test_line_content_0123456789", 0)
	}
	grids := map[int]*screen.Grid{1: grid}
	cursorGrid := 1
	if msg != nil && msg.Grid != 1 {
		msgGrid := newBlankGrid(w, 2, 0)
		putRunes(msgGrid.Cells[0], 0, ":cmdline_prompt", 0)
		grids[msg.Grid] = msgGrid
		cursorGrid = msg.Grid
	}
	state := &screen.State{
		Seq:           seq,
		Grids:         grids,
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e, SP: 0xf38ba8},
		Placements:    map[int]screen.Placement{},
		Msg:           msg,
		Cursor:        screen.Cursor{Grid: cursorGrid, Row: row, Col: col},
		Mode:          mode,
	}
	dl, err := drawlist.Build(state)
	if err != nil {
		panic(err)
	}
	return state, dl
}

func m4QuadFinite(q [4]ui.Point) bool {
	for _, pt := range q {
		if math.IsNaN(float64(pt.X)) || math.IsInf(float64(pt.X), 0) ||
			math.IsNaN(float64(pt.Y)) || math.IsInf(float64(pt.Y), 0) {
			return false
		}
	}
	return true
}

func m4QuadBounds(q [4]ui.Point) (minX, minY, maxX, maxY float32) {
	minX, maxX = q[0].X, q[0].X
	minY, maxY = q[0].Y, q[0].Y
	for _, pt := range q[1:] {
		if pt.X < minX {
			minX = pt.X
		}
		if pt.X > maxX {
			maxX = pt.X
		}
		if pt.Y < minY {
			minY = pt.Y
		}
		if pt.Y > maxY {
			maxY = pt.Y
		}
	}
	return minX, minY, maxX, maxY
}

func m4AssertQuadMatchesCell(t *testing.T, q [4]ui.Point, row, col int, cell text.CellSize) {
	t.Helper()
	if !m4QuadFinite(q) {
		t.Fatalf("CursorQuad has non-finite coordinates: %+v", q)
	}
	wantX := float32(col) * cell.Width
	wantY := float32(row) * cell.Height
	minX, minY, maxX, maxY := m4QuadBounds(q)
	if !approxEqual(minX, wantX, 0.75) || !approxEqual(minY, wantY, 0.75) ||
		!approxEqual(maxX, wantX+cell.Width, 0.75) || !approxEqual(maxY, wantY+cell.Height, 0.75) {
		t.Fatalf("CursorQuad bounds = [%v,%v]x[%v,%v], want cell (%d,%d) [%v,%v]x[%v,%v]",
			minX, maxX, minY, maxY, row, col, wantX, wantX+cell.Width, wantY, wantY+cell.Height)
	}
}

func m4AssertFrameFinite(t *testing.T, frame renderlist.Frame) {
	t.Helper()
	for i, cmd := range frame.Commands {
		for _, v := range []float32{cmd.X, cmd.Y, cmd.W, cmd.H} {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("command %d (%s) has non-finite geometry: %+v", i, cmd.Op, cmd)
			}
		}
	}
}

func TestM4Cursor_ColdStartSnapsDirectly(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	anim := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)

	state, dl := m4CursorFixture(1, 10, 20, "normal", nil)
	_, active, err := anim.Submit(state, &screen.FrameMeta{Seq: 1}, dl)
	if err != nil {
		t.Fatalf("cold-start Submit: %v", err)
	}
	if active || anim.Active() {
		t.Fatalf("cold-start cursor animated (active=%v anim.Active()=%v), want immediate snap", active, anim.Active())
	}
	quad, ok := anim.CursorQuad()
	if !ok {
		t.Fatal("cold-start CursorQuad() ok = false, want true")
	}
	m4AssertQuadMatchesCell(t, quad, 10, 20, cell)
}

func TestM4Cursor_LongJumpTrailPixelsAndSettledEquality(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	anim := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)

	s1, dl1 := m4CursorFixture(1, 2, 2, "normal", nil)
	if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}

	s2, dl2 := m4CursorFixture(2, 12, 30, "normal", nil)
	disabledFrame, err := planner.Plan(dl2)
	if err != nil {
		t.Fatalf("planner.Plan(dl2): %v", err)
	}

	_, submitActive, err := anim.Submit(s2, &screen.FrameMeta{Seq: 2}, dl2)
	if err != nil {
		t.Fatalf("Submit 2: %v", err)
	}
	if !submitActive || !anim.Active() {
		t.Fatalf("long cursor jump Submit active=%v anim.Active()=%v, want true", submitActive, anim.Active())
	}

	// Step 1 (16ms): verify active, trailing polygon (leading corners ahead of trailing corners), and pixel change.
	clock.Advance(16 * time.Millisecond)
	mid1, active1, err := anim.Step()
	if err != nil {
		t.Fatalf("Step 1: %v", err)
	}
	if !active1 {
		t.Fatal("long cursor jump settled after only 16ms, want active intermediate frame")
	}
	q1, ok := anim.CursorQuad()
	if !ok || !m4QuadFinite(q1) {
		t.Fatalf("Step 1 CursorQuad = (%+v, %v), want valid finite quad", q1, ok)
	}

	// Project corners along jump direction u = normalize(target - start).
	// A trailing quad stretches along u well beyond a single cell's static projection.
	dx := float64(28) * float64(cell.Width)
	dy := float64(10) * float64(cell.Height)
	dist := math.Hypot(dx, dy)
	ux, uy := dx/dist, dy/dist
	minProj, maxProj := math.Inf(1), math.Inf(-1)
	for _, pt := range q1 {
		proj := float64(pt.X)*ux + float64(pt.Y)*uy
		if proj < minProj {
			minProj = proj
		}
		if proj > maxProj {
			maxProj = proj
		}
	}
	staticSpan := float64(cell.Width)*math.Abs(ux) + float64(cell.Height)*math.Abs(uy)
	if maxProj-minProj <= staticSpan+1.0 {
		t.Fatalf("CursorQuad at 16ms shows no trail along jump vector: span=%.2f <= static=%.2f (quad=%+v)", maxProj-minProj, staticSpan, q1)
	}

	// Render two distinct intermediate frames and confirm pixels progress.
	imgW := int(math.Ceil(40 * float64(cell.Width)))
	imgH := int(math.Ceil(20 * float64(cell.Height)))
	view := render.NewView()
	view.SetFrame(mid1)
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, imgW, imgH)
	img1 := tester.Image()
	if img1 == nil {
		t.Fatal("mid1 Tester.Image() returned nil")
	}
	pix1 := append([]byte(nil), img1.Pix...)

	clock.Advance(32 * time.Millisecond)
	mid2, _, err := anim.Step()
	if err != nil {
		t.Fatalf("Step 2: %v", err)
	}
	view.SetFrame(mid2)
	tester.Frame()
	img2 := tester.Image()
	if img2 == nil {
		t.Fatal("mid2 Tester.Image() returned nil")
	}
	if bytes.Equal(pix1, img2.Pix) {
		t.Fatal("cursor animation intermediate frames produced identical pixels between 16ms and 48ms")
	}

	// Step to convergence.
	var settled renderlist.Frame
	converged := false
	for i := 0; i < 200; i++ {
		clock.Advance(16 * time.Millisecond)
		f, act, err := anim.Step()
		if err != nil {
			t.Fatalf("Step %d: %v", i, err)
		}
		if !act {
			settled = f
			converged = true
			break
		}
	}
	if !converged || anim.Active() {
		t.Fatalf("long cursor jump did not converge within 200 steps (active=%v)", anim.Active())
	}
	if !reflect.DeepEqual(settled, disabledFrame) {
		t.Fatal("settled cursor animation frame differs from disabled-animation frame")
	}
	qFinal, ok := anim.CursorQuad()
	if !ok {
		t.Fatal("settled CursorQuad() ok = false, want true")
	}
	m4AssertQuadMatchesCell(t, qFinal, 12, 30, cell)
}

func TestM4Cursor_ShortJumpConvergesFasterThanLongJump(t *testing.T) {
	resolver := mustResolver(t)
	planner := render.NewPlanner(resolver)

	measureSteps := func(targetCol int) int {
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		anim := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)
		s1, dl1 := m4CursorFixture(1, 5, 5, "normal", nil)
		if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
			t.Fatalf("Submit 1: %v", err)
		}
		s2, dl2 := m4CursorFixture(2, 5, targetCol, "normal", nil)
		act, err := func() (bool, error) {
			_, a, e := anim.Submit(s2, &screen.FrameMeta{Seq: 2}, dl2)
			return a, e
		}()
		if err != nil {
			t.Fatalf("Submit 2 (col=%d): %v", targetCol, err)
		}
		if !act {
			return 0
		}
		for step := 1; step <= 200; step++ {
			clock.Advance(16 * time.Millisecond)
			_, still, err := anim.Step()
			if err != nil {
				t.Fatalf("Step (col=%d): %v", targetCol, err)
			}
			if !still {
				return step
			}
		}
		t.Fatalf("jump to col %d failed to converge within 200 steps", targetCol)
		return 200
	}

	shortSteps := measureSteps(6) // same row, 1 col (<= 2 cols)
	longSteps := measureSteps(35) // same row, 30 cols
	if shortSteps <= 0 || longSteps <= 0 {
		t.Fatalf("expected positive animation steps: short=%d long=%d", shortSteps, longSteps)
	}
	if !(shortSteps < longSteps) {
		t.Fatalf("short jump did not converge faster than long jump: shortSteps=%d >= longSteps=%d", shortSteps, longSteps)
	}
}

func TestM4Cursor_IdleGapDoesNotSwallowNextJump(t *testing.T) {
	resolver := mustResolver(t)
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	anim := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)

	s1, dl1 := m4CursorFixture(1, 1, 1, "normal", nil)
	if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}

	// Simulate 5 seconds of complete idle before next jump.
	clock.Advance(5 * time.Second)

	s2, dl2 := m4CursorFixture(2, 10, 25, "normal", nil)
	_, activeSubmit, err := anim.Submit(s2, &screen.FrameMeta{Seq: 2}, dl2)
	if err != nil {
		t.Fatalf("Submit after 5s idle: %v", err)
	}
	if !activeSubmit {
		t.Fatal("jump after 5s idle was swallowed on Submit (active=false)")
	}
	clock.Advance(16 * time.Millisecond)
	_, activeStep, err := anim.Step()
	if err != nil {
		t.Fatalf("Step after 5s idle: %v", err)
	}
	if !activeStep {
		t.Fatal("jump after 5s idle was swallowed on first 16ms Step (active=false)")
	}
}

func TestM4Cursor_CommandLineSnapDefaultAndToggle(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	// Default config: CursorAnimateCommandLine == false (neovide#341).
	t.Run("default snaps entering and leaving cmdline and msg_set_pos", func(t *testing.T) {
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		anim := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)

		s1, dl1 := m4CursorFixture(1, 2, 2, "normal", nil)
		if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
			t.Fatalf("Submit normal: %v", err)
		}

		// Enter cmdline_normal mode.
		s2, dl2 := m4CursorFixture(2, 19, 10, "cmdline_normal", nil)
		_, active, err := anim.Submit(s2, &screen.FrameMeta{Seq: 2}, dl2)
		if err != nil {
			t.Fatalf("Submit cmdline_normal: %v", err)
		}
		if active || anim.Active() {
			t.Fatal("entering cmdline_normal animated under default CursorAnimateCommandLine=false")
		}
		if q, ok := anim.CursorQuad(); !ok {
			t.Fatal("CursorQuad() ok=false in cmdline_normal")
		} else {
			m4AssertQuadMatchesCell(t, q, 19, 10, cell)
		}

		// Leave cmdline_normal back to normal.
		s3, dl3 := m4CursorFixture(3, 8, 22, "normal", nil)
		_, active, err = anim.Submit(s3, &screen.FrameMeta{Seq: 3}, dl3)
		if err != nil {
			t.Fatalf("Submit back to normal: %v", err)
		}
		if active || anim.Active() {
			t.Fatal("leaving cmdline_normal animated under default CursorAnimateCommandLine=false")
		}

		// Jump to msg_set_pos grid.
		msg := &screen.MsgPlacement{Grid: 3, Row: 18, Scrolled: false, SepChar: " ", ZIndex: 200, CompIndex: 10}
		s4, dl4 := m4CursorFixture(4, 0, 5, "normal", msg)
		_, active, err = anim.Submit(s4, &screen.FrameMeta{Seq: 4}, dl4)
		if err != nil {
			t.Fatalf("Submit msg_set_pos grid: %v", err)
		}
		if active || anim.Active() {
			t.Fatal("jumping to msg_set_pos grid animated under default CursorAnimateCommandLine=false")
		}
	})

	t.Run("CursorAnimateCommandLine=true enables cmdline animation", func(t *testing.T) {
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		cfg := render.DefaultAnimationConfig()
		cfg.CursorAnimateCommandLine = true
		anim := render.NewAnimator(planner, cfg, clock)

		s1, dl1 := m4CursorFixture(1, 2, 2, "normal", nil)
		if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
			t.Fatalf("Submit 1: %v", err)
		}
		s2, dl2 := m4CursorFixture(2, 19, 10, "cmdline_normal", nil)
		_, active, err := anim.Submit(s2, &screen.FrameMeta{Seq: 2}, dl2)
		if err != nil {
			t.Fatalf("Submit 2: %v", err)
		}
		if !active {
			t.Fatal("CursorAnimateCommandLine=true did not animate entering cmdline_normal")
		}
	})
}

func TestM4Cursor_BusySuppressesCmdlineAndSnapsOnRecovery(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorAnimateCommandLine = true // even with cmdline enabled, Busy must suppress
	anim := render.NewAnimator(planner, cfg, clock)

	s1, dl1 := m4CursorFixture(1, 2, 2, "normal", nil)
	if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1, Busy: false}, dl1); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}

	// Busy == true and cursor jumps to bottom cmdline row (neovim#25980).
	s2, dl2 := m4CursorFixture(2, 19, 30, "normal", nil)
	_, activeBusy, err := anim.Submit(s2, &screen.FrameMeta{Seq: 2, Busy: true}, dl2)
	if err != nil {
		t.Fatalf("Submit busy: %v", err)
	}
	if activeBusy || anim.Active() {
		t.Fatal("cursor animated while meta.Busy == true")
	}
	if q, ok := anim.CursorQuad(); ok {
		_, minY, _, _ := m4QuadBounds(q)
		if minY > 15*cell.Height {
			t.Fatalf("busy cursor jumped down to cmdline row (quad=%+v)", q)
		}
	}

	// Busy == false recovery: cursor snaps directly to new target without flying from cmdline.
	s3, dl3 := m4CursorFixture(3, 8, 15, "normal", nil)
	_, activeRec, err := anim.Submit(s3, &screen.FrameMeta{Seq: 3, Busy: false}, dl3)
	if err != nil {
		t.Fatalf("Submit recovery: %v", err)
	}
	if activeRec || anim.Active() {
		t.Fatal("cursor animated on Busy==false recovery instead of snapping directly to target")
	}
	qRec, ok := anim.CursorQuad()
	if !ok {
		t.Fatal("CursorQuad() ok=false after Busy==false recovery")
	}
	m4AssertQuadMatchesCell(t, qRec, 8, 15, cell)
}

func TestM4Cursor_OffScreenBoundsAndMidFlightDisable(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	anim := render.NewAnimator(planner, render.DefaultAnimationConfig(), clock)

	// Off-screen negative and out-of-bounds cursor coordinates (neovim#279): zero panic, zero NaN/Inf.
	for i, pos := range [][2]int{{-3, -5}, {500, 500}, {5, 10}} {
		s, dl := m4CursorFixture(uint64(i+1), pos[0], pos[1], "normal", nil)
		f, _, err := anim.Submit(s, &screen.FrameMeta{Seq: uint64(i + 1)}, dl)
		if err != nil {
			t.Fatalf("Submit off-screen %v: %v", pos, err)
		}
		m4AssertFrameFinite(t, f)
		if q, ok := anim.CursorQuad(); ok && !m4QuadFinite(q) {
			t.Fatalf("off-screen %v produced non-finite quad: %+v", pos, q)
		}
		clock.Advance(16 * time.Millisecond)
		sf, _, err := anim.Step()
		if err != nil {
			t.Fatalf("Step off-screen %v: %v", pos, err)
		}
		m4AssertFrameFinite(t, sf)
	}

	// Mid-flight SetConfig(DisabledAnimationConfig()) must immediately settle to target.
	sStart, dlStart := m4CursorFixture(10, 1, 1, "normal", nil)
	if _, _, err := anim.Submit(sStart, &screen.FrameMeta{Seq: 10}, dlStart); err != nil {
		t.Fatalf("Submit start: %v", err)
	}
	for i := 0; i < 200 && anim.Active(); i++ {
		clock.Advance(16 * time.Millisecond)
		if _, _, err := anim.Step(); err != nil {
			t.Fatalf("settle start: %v", err)
		}
	}

	sTarget, dlTarget := m4CursorFixture(11, 15, 35, "normal", nil)
	_, active, err := anim.Submit(sTarget, &screen.FrameMeta{Seq: 11}, dlTarget)
	if err != nil || !active {
		t.Fatalf("Submit target: active=%v err=%v, want active=true", active, err)
	}
	clock.Advance(16 * time.Millisecond)
	_, active, err = anim.Step()
	if err != nil || !active {
		t.Fatalf("mid-flight Step: active=%v err=%v, want active=true", active, err)
	}

	anim.SetConfig(render.DisabledAnimationConfig())
	gotFrame, stepActive, err := anim.Step()
	if err != nil {
		t.Fatalf("Step after SetConfig(Disabled): %v", err)
	}
	if stepActive || anim.Active() {
		t.Fatalf("animation still active after SetConfig(Disabled): stepActive=%v anim.Active()=%v", stepActive, anim.Active())
	}
	wantTarget, err := planner.Plan(dlTarget)
	if err != nil {
		t.Fatalf("Plan target after SetConfig(Disabled): %v", err)
	}
	if !reflect.DeepEqual(gotFrame, wantTarget) {
		t.Fatal("frame after mid-flight SetConfig(Disabled) differs from static target frame")
	}
	q, ok := anim.CursorQuad()
	if !ok {
		t.Fatal("CursorQuad() ok = false after mid-flight SetConfig(Disabled), want true")
	}
	m4AssertQuadMatchesCell(t, q, 15, 35, cell)
}
