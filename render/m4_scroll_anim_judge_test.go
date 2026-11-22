package render_test

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/renderlist"
	"nvgui/screen"
)

func m4ScrollGridState(seq uint64, rows int, prefix string) (*screen.State, *drawlist.List) {
	const w = 40
	grid := newBlankGrid(w, rows, 0)
	for r := 0; r < rows; r++ {
		putRunes(grid.Cells[r], 0, fmt.Sprintf("%s_%02d_payload", prefix, r), 0)
	}
	state := &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e, SP: 0xf38ba8},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: 0},
		Mode:          "normal",
	}
	dl, err := drawlist.Build(state)
	if err != nil {
		panic(err)
	}
	return state, dl
}

func m4FindGlyphY(frame renderlist.Frame, text string) (float32, bool) {
	for _, cmd := range frame.Commands {
		if cmd.Op == "glyphs" && cmd.Text == text {
			return cmd.Y, true
		}
	}
	return 0, false
}

func TestM4Scroll_DownAndUpSmoothDecayAndSettledEquality(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	runDirection := func(delta float64) (firstOffsetLines float32) {
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		cfg := render.DefaultAnimationConfig()
		cfg.CursorEnabled = false // isolate scroll animation
		anim := render.NewAnimator(planner, cfg, clock)

		s1, dl1 := m4ScrollGridState(1, 16, "prev_line")
		if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
			t.Fatalf("Submit baseline: %v", err)
		}

		s2, dl2 := m4ScrollGridState(2, 16, "curr_line")
		staticFrame, err := planner.Plan(dl2)
		if err != nil {
			t.Fatalf("Plan static: %v", err)
		}
		staticY, ok := m4FindGlyphY(staticFrame, "curr_line_05_payload")
		if !ok {
			t.Fatal("static frame missing target line glyphs command")
		}

		meta2 := &screen.FrameMeta{
			Seq: 2,
			Viewports: map[int]screen.Viewport{
				1: {Grid: 1, TopLine: 10, BotLine: 26, ScrollDelta: delta, HasScrollDelta: true},
			},
		}
		_, submitActive, err := anim.Submit(s2, meta2, dl2)
		if err != nil {
			t.Fatalf("Submit scroll delta=%v: %v", delta, err)
		}
		if !submitActive || !anim.Active() {
			t.Fatalf("scroll delta=%v Submit active=%v, want true", delta, submitActive)
		}

		clock.Advance(16 * time.Millisecond)
		mid1, act1, err := anim.Step()
		if err != nil {
			t.Fatalf("Step 1 delta=%v: %v", delta, err)
		}
		if !act1 {
			t.Fatalf("scroll delta=%v settled after 16ms", delta)
		}
		offLines, offPix, offAct := anim.ScrollOffset(1)
		if !offAct || offLines == 0 || offPix == 0 {
			t.Fatalf("Step 1 ScrollOffset(1) = (%v, %v, %v), want active non-zero", offLines, offPix, offAct)
		}
		if !approxEqual(offPix, offLines*cell.Height, 1.0) {
			t.Fatalf("ScrollOffset pixels=%v not proportional to lines*cellH=%v", offPix, offLines*cell.Height)
		}
		firstOffsetLines = offLines

		midY, ok := m4FindGlyphY(mid1, "curr_line_05_payload")
		if !ok {
			t.Fatal("mid1 frame missing target line glyphs command")
		}
		if delta > 0 && !(midY > staticY+0.5) {
			t.Fatalf("scroll down (delta=%v): midY=%v <= staticY=%v, want positive visual Y shift gliding upward", delta, midY, staticY)
		}
		if delta < 0 && !(midY < staticY-0.5) {
			t.Fatalf("scroll up (delta=%v): midY=%v >= staticY=%v, want negative visual Y shift gliding downward", delta, midY, staticY)
		}

		// Monotonic magnitude decay until settled.
		prevAbs := float32(math.Abs(float64(offLines)))
		var settled renderlist.Frame
		converged := false
		for step := 0; step < 200; step++ {
			clock.Advance(16 * time.Millisecond)
			f, act, err := anim.Step()
			if err != nil {
				t.Fatalf("Step %d delta=%v: %v", step, delta, err)
			}
			ol, op, oa := anim.ScrollOffset(1)
			if act {
				curAbs := float32(math.Abs(float64(ol)))
				if !(curAbs < prevAbs) {
					t.Fatalf("scroll offset did not decay monotonically at step %d: %v -> %v", step, prevAbs, curAbs)
				}
				prevAbs = curAbs
			} else {
				if oa || ol != 0 || op != 0 {
					t.Fatalf("settled ScrollOffset(1) = (%v, %v, %v), want (0, 0, false)", ol, op, oa)
				}
				settled = f
				converged = true
				break
			}
		}
		if !converged {
			t.Fatalf("scroll delta=%v did not converge within 200 steps", delta)
		}
		if !reflect.DeepEqual(settled, staticFrame) {
			t.Fatalf("settled scroll delta=%v frame differs from disabled-animation frame", delta)
		}
		return firstOffsetLines
	}

	downLines := runDirection(3.0)
	upLines := runDirection(-3.0)
	if (downLines > 0) == (upLines > 0) {
		t.Fatalf("scroll down (%v) and scroll up (%v) have the same sign, want opposite signs", downLines, upLines)
	}
}

func TestM4Scroll_FractionalDeltaAndFarLinesClamp(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	// Fractional smoothscroll delta (0.5 and 1.5 lines, neovim#8715) must not truncate to 0.
	for _, frac := range []float64{0.5, 1.5} {
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		cfg := render.DefaultAnimationConfig()
		cfg.CursorEnabled = false
		anim := render.NewAnimator(planner, cfg, clock)

		s1, dl1 := m4ScrollGridState(1, 12, "frac")
		if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
			t.Fatalf("Submit baseline: %v", err)
		}
		s2, dl2 := m4ScrollGridState(2, 12, "frac")
		meta := &screen.FrameMeta{
			Seq: 2,
			Viewports: map[int]screen.Viewport{
				1: {Grid: 1, TopLine: 0, BotLine: 12, ScrollDelta: frac, HasScrollDelta: true},
			},
		}
		_, act, err := anim.Submit(s2, meta, dl2)
		if err != nil {
			t.Fatalf("Submit frac=%v: %v", frac, err)
		}
		if !act {
			t.Fatalf("fractional scroll_delta=%v was truncated to inactive", frac)
		}
		clock.Advance(16 * time.Millisecond)
		if _, _, err := anim.Step(); err != nil {
			t.Fatalf("Step frac=%v: %v", frac, err)
		}
		offLines, offPix, offAct := anim.ScrollOffset(1)
		if !offAct || math.Abs(float64(offLines)) < 0.05 || math.Abs(float64(offPix)) < 0.05*float64(cell.Height) {
			t.Fatalf("fractional scroll_delta=%v offset = (%v lines, %v px, active=%v), want non-zero fractional offset", frac, offLines, offPix, offAct)
		}
	}

	// Huge jump must be clamped by ScrollFarLines.
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorEnabled = false
	cfg.ScrollFarLines = 5
	anim := render.NewAnimator(planner, cfg, clock)

	s1, dl1 := m4ScrollGridState(1, 12, "far")
	if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
		t.Fatalf("Submit baseline: %v", err)
	}
	s2, dl2 := m4ScrollGridState(2, 12, "far")
	metaFar := &screen.FrameMeta{
		Seq: 2,
		Viewports: map[int]screen.Viewport{
			1: {Grid: 1, TopLine: 500, BotLine: 512, ScrollDelta: 500.0, HasScrollDelta: true},
		},
	}
	_, act, err := anim.Submit(s2, metaFar, dl2)
	if err != nil || !act {
		t.Fatalf("Submit far scroll: active=%v err=%v", act, err)
	}
	offLines, _, _ := anim.ScrollOffset(1)
	if math.Abs(float64(offLines)) > float64(cfg.ScrollFarLines)+0.05 {
		t.Fatalf("far scroll offsetLines = %v, exceeds ScrollFarLines=%d", offLines, cfg.ScrollFarLines)
	}
}

func TestM4Scroll_FloatMarginsBorderStableAndInnerClip(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorEnabled = false
	anim := render.NewAnimator(planner, cfg, clock)

	buildFloatState := func(seq uint64, innerPrefix string) (*screen.State, *drawlist.List) {
		root := newBlankGrid(40, 14, 0)
		flt := newBlankGrid(24, 8, 1)
		// Top border (row 0) & bottom border (row 7) & side borders (cols 0 and 23): HL 2 (blue).
		putRunes(flt.Cells[0], 0, "┌──────────────────────┐", 2)
		putRunes(flt.Cells[0], 2, "TITLE", 2)
		for r := 1; r <= 6; r++ {
			putRunes(flt.Cells[r], 0, "│", 2)
			putRunes(flt.Cells[r], 23, "│", 2)
			// Inner content: HL 3 (vivid green 0x00ff00).
			putRunes(flt.Cells[r], 2, fmt.Sprintf("%s_row_%02d_text", innerPrefix, r), 3)
		}
		putRunes(flt.Cells[7], 0, "└──────────────────────┘", 2)
		putRunes(flt.Cells[7], 2, "FOOT", 2)

		state := &screen.State{
			Seq:   seq,
			Grids: map[int]*screen.Grid{1: root, 2: flt},
			Attrs: map[int]screen.Attr{
				0: {},
				1: {"foreground": int64(0xcdd6f4), "background": int64(0x202030)},
				2: {"foreground": int64(0x4488ff), "background": int64(0x202030)},
				3: {"foreground": int64(0x00ff00), "background": int64(0x202030)},
			},
			DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x0a1020, SP: 0xf38ba8},
			Placements: map[int]screen.Placement{
				2: {
					Kind: screen.PlacedFloat, Win: 1002, Anchor: "NW",
					ScreenRow: 2, ScreenCol: 4, ZIndex: 50, CompIndex: 10,
				},
			},
			Cursor: screen.Cursor{Grid: 1, Row: 0, Col: 0},
			Mode:   "normal",
		}
		dl, err := drawlist.Build(state)
		if err != nil {
			t.Fatalf("drawlist.Build: %v", err)
		}
		return state, dl
	}

	s1, dl1 := buildFloatState(1, "prev_inner")
	margins := map[int]screen.ViewportMargins{
		2: {Top: 1, Bottom: 1, Left: 1, Right: 1},
	}
	if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1, Margins: margins}, dl1); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}

	s2, dl2 := buildFloatState(2, "curr_inner")
	staticFrame, err := planner.Plan(dl2)
	if err != nil {
		t.Fatalf("Plan static: %v", err)
	}
	topBorderY := float32(2) * cell.Height
	botBorderY := float32(9) * cell.Height
	leftBorderX := float32(4) * cell.Width
	rightBorderX := float32(27) * cell.Width
	staticInnerY, ok := m4FindGlyphY(staticFrame, "curr_inner_row_03_text")
	if !ok {
		t.Fatal("static float frame missing curr_inner_row_03_text")
	}

	meta2 := &screen.FrameMeta{
		Seq:     2,
		Margins: margins,
		Viewports: map[int]screen.Viewport{
			2: {Grid: 2, TopLine: 5, BotLine: 11, ScrollDelta: 2.0, HasScrollDelta: true},
		},
	}
	_, active, err := anim.Submit(s2, meta2, dl2)
	if err != nil || !active {
		t.Fatalf("Submit float scroll: active=%v err=%v, want active=true", active, err)
	}

	imgW := int(math.Ceil(40 * float64(cell.Width)))
	imgH := int(math.Ceil(14 * float64(cell.Height)))
	view := render.NewView()
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, imgW, imgH)

	sawInnerShift := false
	for step := 0; step < 120; step++ {
		clock.Advance(16 * time.Millisecond)
		frame, still, err := anim.Step()
		if err != nil {
			t.Fatalf("Step %d: %v", step, err)
		}
		// 1. Border commands ("TITLE", "FOOT", top/bottom/left/right box border runes) must have ZERO pixel Y offset.
		sideBorders := 0
		for _, cmd := range frame.Commands {
			if cmd.Op == "glyphs" && cmd.Text == "TITLE" {
				if !approxEqual(cmd.Y, topBorderY, 0.01) {
					t.Fatalf("step %d: top border TITLE shifted to Y=%v, want %v (neovide#1446)", step, cmd.Y, topBorderY)
				}
			}
			if cmd.Op == "glyphs" && cmd.Text == "FOOT" {
				if !approxEqual(cmd.Y, botBorderY, 0.01) {
					t.Fatalf("step %d: bottom border FOOT shifted to Y=%v, want %v (neovide#1446)", step, cmd.Y, botBorderY)
				}
			}
			if cmd.Op == "box" && (cmd.Rune == "┌" || cmd.Rune == "┐" || cmd.Rune == "─") && cmd.Y < float32(5)*cell.Height {
				if !approxEqual(cmd.Y, topBorderY, 0.01) {
					t.Fatalf("step %d: top border box %q shifted to Y=%v, want %v", step, cmd.Rune, cmd.Y, topBorderY)
				}
			}
			if cmd.Op == "box" && (cmd.Rune == "└" || cmd.Rune == "┘" || cmd.Rune == "─") && cmd.Y > float32(5)*cell.Height {
				if !approxEqual(cmd.Y, botBorderY, 0.01) {
					t.Fatalf("step %d: bottom border box %q shifted to Y=%v, want %v", step, cmd.Rune, cmd.Y, botBorderY)
				}
			}
			if cmd.Op == "box" && cmd.Rune == "│" {
				sideBorders++
				if !approxEqual(cmd.X, leftBorderX, 0.01) && !approxEqual(cmd.X, rightBorderX, 0.01) {
					t.Fatalf("step %d: side border box \"│\" at unexpected X=%v (want %v or %v)", step, cmd.X, leftBorderX, rightBorderX)
				}
				nearestRow := int(math.Round(float64(cmd.Y / cell.Height)))
				wantSideY := float32(nearestRow) * cell.Height
				if nearestRow < 3 || nearestRow > 8 || !approxEqual(cmd.Y, wantSideY, 0.01) {
					t.Fatalf("step %d: side border box \"│\" shifted to Y=%v, want unshifted row in [3,8] (%v) (neovide#1446)", step, cmd.Y, wantSideY)
				}
			}
		}
		if sideBorders < 12 {
			t.Fatalf("step %d: found only %d side border \"│\" commands, want >= 12", step, sideBorders)
		}
		if innerY, ok := m4FindGlyphY(frame, "curr_inner_row_03_text"); ok && math.Abs(float64(innerY-staticInnerY)) > 0.5 {
			sawInnerShift = true
		}

		// 2. Render intermediate frame and assert vivid green inner text NEVER bleeds into top/bottom border rows.
		if step == 0 || step == 2 {
			view.SetFrame(frame)
			tester.Frame()
			img := tester.Image()
			if img == nil {
				t.Fatalf("step %d: Tester.Image() returned nil", step)
			}
			floatX0 := int(math.Floor(4 * float64(cell.Width)))
			floatX1 := int(math.Ceil(28 * float64(cell.Width)))
			topY0 := int(math.Floor(float64(topBorderY)))
			topY1 := int(math.Floor(float64(topBorderY+cell.Height))) - 1
			botY0 := int(math.Ceil(float64(botBorderY))) + 1
			botY1 := int(math.Ceil(float64(botBorderY + cell.Height)))
			for y := topY0; y < topY1 && y < imgH; y++ {
				for x := floatX0; x < floatX1 && x < imgW; x++ {
					px := img.RGBAAt(x, y)
					if px.G > 180 && px.G > px.R+100 && px.G > px.B+100 {
						t.Fatalf("step %d: inner scrolled text bled onto top border at (%d,%d): %+v", step, x, y, px)
					}
				}
			}
			for y := botY0; y < botY1 && y < imgH; y++ {
				for x := floatX0; x < floatX1 && x < imgW; x++ {
					px := img.RGBAAt(x, y)
					if px.G > 180 && px.G > px.R+100 && px.G > px.B+100 {
						t.Fatalf("step %d: inner scrolled text bled onto bottom border at (%d,%d): %+v", step, x, y, px)
					}
				}
			}
		}
		if !still {
			break
		}
	}
	if !sawInnerShift {
		t.Fatal("inner float text never shifted during smooth scroll animation")
	}
}

func TestM4Scroll_RowCacheReusedAcrossScrollFrames(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorEnabled = false
	anim := render.NewAnimator(planner, cfg, clock)

	const rows = 20
	s1, dl1 := m4ScrollGridState(1, rows, "cache_line")
	f1, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1)
	if err != nil {
		t.Fatalf("Submit 1: %v", err)
	}

	imgW := int(math.Ceil(40 * float64(cell.Width)))
	imgH := int(math.Ceil(float64(rows) * float64(cell.Height)))
	view := render.NewView()
	view.SetFrame(f1)
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, imgW, imgH)
	_ = tester.Image()
	sWarm := view.ShapedRows()
	if sWarm < rows {
		t.Fatalf("initial ShapedRows = %d, want >= %d", sWarm, rows)
	}

	// Trigger smooth scroll with identical line content; step 8 animated frames.
	s2, dl2 := m4ScrollGridState(2, rows, "cache_line")
	meta2 := &screen.FrameMeta{
		Seq: 2,
		Viewports: map[int]screen.Viewport{
			1: {Grid: 1, TopLine: 3, BotLine: 23, ScrollDelta: 3.0, HasScrollDelta: true},
		},
	}
	f2, active, err := anim.Submit(s2, meta2, dl2)
	if err != nil || !active {
		t.Fatalf("Submit scroll: active=%v err=%v", active, err)
	}
	view.SetFrame(f2)
	tester.Frame()
	_ = tester.Image()
	sAfterFirst := view.ShapedRows()

	for step := 0; step < 8; step++ {
		clock.Advance(16 * time.Millisecond)
		frame, still, err := anim.Step()
		if err != nil {
			t.Fatalf("Step %d: %v", step, err)
		}
		view.SetFrame(frame)
		tester.Frame()
		_ = tester.Image()
		if !still {
			break
		}
	}
	sAfterAnim := view.ShapedRows()
	if delta := sAfterAnim - sAfterFirst; delta > 4 {
		t.Fatalf("rowCache churned during smooth scroll: ShapedRows grew by %d (%d -> %d) across pure Y-offset frames, want <= 4", delta, sAfterFirst, sAfterAnim)
	}
}

func TestM4Scroll_DisabledZeroOffset(t *testing.T) {
	resolver := mustResolver(t)
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.ScrollEnabled = false
	cfg.CursorEnabled = false
	anim := render.NewAnimator(planner, cfg, clock)

	s1, dl1 := m4ScrollGridState(1, 10, "dis")
	if _, _, err := anim.Submit(s1, &screen.FrameMeta{Seq: 1}, dl1); err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	s2, dl2 := m4ScrollGridState(2, 10, "dis")
	want, err := planner.Plan(dl2)
	if err != nil {
		t.Fatalf("Plan static: %v", err)
	}
	meta2 := &screen.FrameMeta{
		Seq: 2,
		Viewports: map[int]screen.Viewport{
			1: {Grid: 1, TopLine: 4, BotLine: 14, ScrollDelta: 4.0, HasScrollDelta: true},
		},
	}
	got, active, err := anim.Submit(s2, meta2, dl2)
	if err != nil {
		t.Fatalf("Submit 2: %v", err)
	}
	if active || anim.Active() {
		t.Fatalf("ScrollEnabled=false returned active=%v anim.Active()=%v, want false", active, anim.Active())
	}
	if ol, op, oa := anim.ScrollOffset(1); oa || ol != 0 || op != 0 {
		t.Fatalf("ScrollEnabled=false ScrollOffset(1) = (%v, %v, %v), want (0, 0, false)", ol, op, oa)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("ScrollEnabled=false output frame differs from Planner.Plan(dl)")
	}
}
