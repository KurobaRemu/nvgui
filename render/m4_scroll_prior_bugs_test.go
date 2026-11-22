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
	"nvgui/screen"
)

func m4ScrollPriorState(seq uint64, rows int, prefix string) (*screen.State, *drawlist.List) {
	grid := newBlankGrid(40, rows, 0)
	for row := 0; row < rows; row++ {
		putRunes(grid.Cells[row], 0, fmt.Sprintf("%s_%02d_payload", prefix, row), 0)
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
	list, err := drawlist.Build(state)
	if err != nil {
		panic(err)
	}
	return state, list
}

func m4ScrollPriorMeta(seq uint64, delta float64) *screen.FrameMeta {
	return &screen.FrameMeta{
		Seq: seq,
		Viewports: map[int]screen.Viewport{
			1: {Grid: 1, TopLine: 10, BotLine: 26, ScrollDelta: delta, HasScrollDelta: true},
		},
	}
}

func TestPriorBug_Neovim8715_SmoothScrollScreenLinesAndFractionalDelta(t *testing.T) {
	for _, delta := range []float64{0.5, 1.5} {
		resolver := mustResolver(t)
		cell := resolver.Cell()
		planner := render.NewPlanner(resolver)
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		cfg := render.DefaultAnimationConfig()
		cfg.CursorEnabled = false
		disabled := cfg
		disabled.ScrollEnabled = false
		anim := render.NewAnimator(planner, cfg, clock)
		staticAnim := render.NewAnimator(planner, disabled, clock)

		state, list := m4ScrollPriorState(1, 12, "screen_line")
		before, err := screen.Canonical(state)
		if err != nil {
			t.Fatalf("Canonical: %v", err)
		}
		if _, _, err := anim.Submit(state, &screen.FrameMeta{Seq: 1}, list); err != nil {
			t.Fatalf("Submit baseline: %v", err)
		}
		meta := m4ScrollPriorMeta(2, delta)
		if _, active, err := anim.Submit(state, meta, list); err != nil || !active {
			t.Fatalf("Submit delta=%v: active=%v err=%v", delta, active, err)
		}
		static, _, err := staticAnim.Submit(state, meta, list)
		if err != nil {
			t.Fatalf("disabled Submit: %v", err)
		}
		clock.Advance(16 * time.Millisecond)
		frame, active, err := anim.Step()
		if err != nil || !active {
			t.Fatalf("Step delta=%v: active=%v err=%v", delta, active, err)
		}
		offset, pixels, active := anim.ScrollOffset(1)
		if !active || math.Abs(float64(offset)) < 0.05 || math.Abs(float64(pixels)) < 0.05*float64(cell.Height) {
			t.Fatalf("delta=%v offset=(%v,%v,%v), want fractional non-zero", delta, offset, pixels, active)
		}
		staticY, ok := m4FindGlyphY(static, "screen_line_05_payload")
		if !ok {
			t.Fatal("static frame missing screen_line_05_payload")
		}
		animatedY, ok := m4FindGlyphY(frame, "screen_line_05_payload")
		if !ok || math.Abs(float64(animatedY-staticY)) < 0.01 {
			t.Fatalf("delta=%v screen line Y did not move: static=%v animated=%v found=%v", delta, staticY, animatedY, ok)
		}
		converged := false
		for step := 0; step < 200; step++ {
			clock.Advance(16 * time.Millisecond)
			settled, still, err := anim.Step()
			if err != nil {
				t.Fatalf("Step %d delta=%v: %v", step, delta, err)
			}
			if still {
				continue
			}
			if !reflect.DeepEqual(settled, static) {
				t.Fatalf("delta=%v settled frame differs from disabled frame", delta)
			}
			converged = true
			break
		}
		if !converged {
			t.Fatalf("delta=%v did not converge within 200 steps", delta)
		}
		after, err := screen.Canonical(state)
		if err != nil || string(before) != string(after) {
			t.Fatalf("delta=%v modified screen.State: before=%q after=%q err=%v", delta, before, after, err)
		}
	}
}

func m4ScrollFloatState(seq uint64, prefix string) (*screen.State, *drawlist.List) {
	root := newBlankGrid(40, 14, 0)
	flt := newBlankGrid(24, 8, 1)
	putRunes(flt.Cells[0], 0, "┌──────────────────────┐", 2)
	putRunes(flt.Cells[0], 2, "TITLE", 2)
	for row := 1; row <= 6; row++ {
		putRunes(flt.Cells[row], 0, "│", 2)
		putRunes(flt.Cells[row], 23, "│", 2)
		putRunes(flt.Cells[row], 1, fmt.Sprintf("%s_row_%02d_text", prefix, row), 3)
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
			2: {Kind: screen.PlacedFloat, Win: 1002, Anchor: "NW", ScreenRow: 2, ScreenCol: 4, ZIndex: 50, CompIndex: 10},
		},
		Cursor: screen.Cursor{Grid: 1, Row: 0, Col: 0},
		Mode:   "normal",
	}
	list, err := drawlist.Build(state)
	if err != nil {
		panic(err)
	}
	return state, list
}

func m4ScrollFloatMeta(seq uint64, delta float64) *screen.FrameMeta {
	return &screen.FrameMeta{
		Seq:     seq,
		Margins: map[int]screen.ViewportMargins{2: {Top: 1, Bottom: 1, Left: 1, Right: 1}},
		Viewports: map[int]screen.Viewport{
			2: {Grid: 2, TopLine: 5, BotLine: 11, ScrollDelta: delta, HasScrollDelta: true},
		},
	}
}

func TestPriorBug_Neovide1446_FloatingWindowBorderStableDuringSmoothScroll(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorEnabled = false
	anim := render.NewAnimator(planner, cfg, clock)

	state, list := m4ScrollFloatState(1, "prev_inner")
	if _, _, err := anim.Submit(state, &screen.FrameMeta{Seq: 1, Margins: m4ScrollFloatMeta(1, 0).Margins}, list); err != nil {
		t.Fatalf("Submit baseline: %v", err)
	}
	state, list = m4ScrollFloatState(2, "curr_inner")
	before, err := screen.Canonical(state)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	static, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan static: %v", err)
	}
	topY, bottomY := float32(2)*cell.Height, float32(9)*cell.Height
	leftX, rightX := float32(4)*cell.Width, float32(27)*cell.Width
	innerY, ok := m4FindGlyphY(static, "curr_inner_row_03_text")
	if !ok {
		t.Fatal("static frame missing curr_inner_row_03_text")
	}
	if _, active, err := anim.Submit(state, m4ScrollFloatMeta(2, 2), list); err != nil || !active {
		t.Fatalf("Submit float scroll: active=%v err=%v", active, err)
	}
	innerClip := ui.Rect{
		X: float32(4+1) * cell.Width,
		Y: float32(2+1) * cell.Height,
		W: float32(24-2) * cell.Width,
		H: float32(8-2) * cell.Height,
	}
	imgW := int(math.Ceil(40 * float64(cell.Width)))
	imgH := int(math.Ceil(14 * float64(cell.Height)))
	view := render.NewView()
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, imgW, imgH)
	sawInnerShift := false
	converged := false
	for step := 0; step < 120; step++ {
		clock.Advance(16 * time.Millisecond)
		frame, still, err := anim.Step()
		if err != nil {
			t.Fatalf("Step %d: %v", step, err)
		}
		sideBorders := 0
		for _, command := range frame.Commands {
			if command.Op == "glyphs" && command.Text == "TITLE" && !approxEqual(command.Y, topY, 0.01) {
				t.Fatalf("step %d: TITLE Y=%v, want %v", step, command.Y, topY)
			}
			if command.Op == "glyphs" && command.Text == "FOOT" && !approxEqual(command.Y, bottomY, 0.01) {
				t.Fatalf("step %d: FOOT Y=%v, want %v", step, command.Y, bottomY)
			}
			if command.Op == "box" && command.Rune == "│" {
				sideBorders++
				if !approxEqual(command.X, leftX, 0.01) && !approxEqual(command.X, rightX, 0.01) {
					t.Fatalf("step %d: side border X=%v, want %v or %v", step, command.X, leftX, rightX)
				}
				nearestRow := int(math.Round(float64(command.Y / cell.Height)))
				if nearestRow < 3 || nearestRow > 8 || !approxEqual(command.Y, float32(nearestRow)*cell.Height, 0.01) {
					t.Fatalf("step %d: side border Y=%v shifted outside unshifted rows [3,8]", step, command.Y)
				}
			}
			if command.Op == "glyphs" && command.Text == "curr_inner_row_03_text" {
				visibleTop := max(command.Y, innerClip.Y)
				visibleBottom := min(command.Y+command.H, innerClip.Y+innerClip.H)
				visibleLeft := max(command.X, innerClip.X)
				visibleRight := min(command.X+command.W, innerClip.X+innerClip.W)
				if visibleBottom <= visibleTop || visibleRight <= visibleLeft {
					t.Fatalf("step %d: inner text command %+v escapes innerClip %+v", step, command, innerClip)
				}
				if command.X > innerClip.X+innerClip.W || command.Y > innerClip.Y+innerClip.H {
					t.Fatalf("step %d: inner text command %+v starts outside innerClip %+v", step, command, innerClip)
				}
			}
		}
		if sideBorders != 12 {
			t.Fatalf("step %d: found %d side border commands, want exactly 12", step, sideBorders)
		}
		if animatedY, ok := m4FindGlyphY(frame, "curr_inner_row_03_text"); ok && math.Abs(float64(animatedY-innerY)) > 0.5 {
			sawInnerShift = true
		}
		if step == 0 || step == 2 {
			view.SetFrame(frame)
			tester.Frame()
			img := tester.Image()
			if img == nil {
				t.Fatalf("step %d: Tester.Image() returned nil", step)
			}
			floatX0 := int(math.Floor(4 * float64(cell.Width)))
			floatX1 := int(math.Ceil(28 * float64(cell.Width)))
			topY0 := int(math.Floor(float64(topY)))
			topY1 := int(math.Floor(float64(topY+cell.Height))) - 1
			botY0 := int(math.Ceil(float64(bottomY))) + 1
			botY1 := int(math.Ceil(float64(bottomY + cell.Height)))
			for y := topY0; y < topY1 && y < imgH; y++ {
				for x := floatX0; x < floatX1 && x < imgW; x++ {
					px := img.RGBAAt(x, y)
					if px.G > 180 && px.G > px.R+100 && px.G > px.B+100 {
						t.Fatalf("step %d: inner text bled onto top border at (%d,%d): %+v", step, x, y, px)
					}
				}
			}
			for y := botY0; y < botY1 && y < imgH; y++ {
				for x := floatX0; x < floatX1 && x < imgW; x++ {
					px := img.RGBAAt(x, y)
					if px.G > 180 && px.G > px.R+100 && px.G > px.B+100 {
						t.Fatalf("step %d: inner text bled onto bottom border at (%d,%d): %+v", step, x, y, px)
					}
				}
			}
		}
		if !still {
			if !reflect.DeepEqual(frame, static) {
				t.Fatalf("step %d: settled frame differs from static frame", step)
			}
			converged = true
			break
		}
	}
	if !converged {
		t.Fatal("float scroll did not converge within 120 steps")
	}
	if !sawInnerShift {
		t.Fatal("inner float text never shifted during smooth scroll")
	}
	after, err := screen.Canonical(state)
	if err != nil || string(before) != string(after) {
		t.Fatalf("modified screen.State: before=%q after=%q err=%v", before, after, err)
	}
}

func TestPriorBug_Neovide1304_ViewportBoundsCoherentAcrossSmoothScroll(t *testing.T) {
	planner := render.NewPlanner(mustResolver(t))
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorEnabled = false
	cfg.ScrollFarLines = 10
	anim := render.NewAnimator(planner, cfg, clock)
	state, list := m4ScrollPriorState(1, 12, "bounds")
	before, err := screen.Canonical(state)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if _, _, err := anim.Submit(state, &screen.FrameMeta{Seq: 1}, list); err != nil {
		t.Fatalf("Submit baseline: %v", err)
	}
	metas := []*screen.FrameMeta{
		m4ScrollPriorMeta(2, 3),
		m4ScrollPriorMeta(3, -2),
		m4ScrollPriorMeta(4, 500),
		m4ScrollPriorMeta(5, -500),
	}
	for index, meta := range metas {
		if _, active, err := anim.Submit(state, meta, list); err != nil || !active {
			t.Fatalf("Submit %d: active=%v err=%v", index, active, err)
		}
		if index >= 2 {
			offset, _, _ := anim.ScrollOffset(1)
			if math.Abs(float64(offset)) > 10.05 {
				t.Fatalf("far jump %d offset=%v exceeds ScrollFarLines=10 + tolerance", index, offset)
			}
		}
		previous := float32(math.Inf(1))
		converged := false
		for step := 0; step < 200; step++ {
			clock.Advance(16 * time.Millisecond)
			if _, still, err := anim.Step(); err != nil || !still {
				if err != nil {
					t.Fatalf("Step %d/%d: %v", index, step, err)
				}
				converged = true
				break
			}
			offset, pixels, active := anim.ScrollOffset(1)
			if !active {
				t.Fatalf("Step %d/%d inactive before convergence", index, step)
			}
			if math.IsNaN(float64(offset)) || math.IsInf(float64(offset), 0) || math.Abs(float64(offset)) > 10.05 ||
				math.IsNaN(float64(pixels)) || math.IsInf(float64(pixels), 0) {
				t.Fatalf("Step %d/%d unbounded offset=(%v,%v)", index, step, offset, pixels)
			}
			current := float32(math.Abs(float64(offset)))
			if current > previous+0.01 {
				t.Fatalf("Step %d/%d offset grew %v -> %v", index, step, previous, current)
			}
			previous = current
			for _, viewport := range meta.Viewports {
				if !viewport.HasScrollDelta || viewport.ScrollDelta == 0 {
					t.Fatalf("Step %d/%d FrameMeta.Viewports mutated: %+v", index, step, viewport)
				}
			}
		}
		if !converged {
			t.Fatalf("scroll %d did not converge within 200 steps", index)
		}
		offset, pixels, active := anim.ScrollOffset(1)
		if active || offset != 0 || pixels != 0 {
			t.Fatalf("scroll %d settled offset=(%v,%v,%v), want (0,0,false)", index, offset, pixels, active)
		}
	}
	after, err := screen.Canonical(state)
	if err != nil || string(before) != string(after) {
		t.Fatalf("modified screen.State: before=%q after=%q err=%v", before, after, err)
	}
}
