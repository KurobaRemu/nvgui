package render_test

import (
	"fmt"
	"image"
	"image/color"
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

type scrollTapeCase struct {
	name   string
	deltas []float64
}

func scrollTapeState(seq uint64, prefix string) (*screen.State, *drawlist.List) {
	const rows = 16
	grid := newBlankGrid(40, rows, 0)
	for row := 0; row < rows; row++ {
		putRunes(grid.Cells[row], 0, fmt.Sprintf("%s_%02d_payload", prefix, row), 0)
	}
	state := &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101018},
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

func scrollTapeRasterizer(cellWidth, cellHeight float32) (*render.View, *ui.Tester, int, int) {
	width := int(math.Ceil(40 * float64(cellWidth)))
	height := int(math.Ceil(16 * float64(cellHeight)))
	view := render.NewView()
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	return view, tester, width, height
}

func halfHasTextPixels(img *image.RGBA, width, height int, background color.RGBA) bool {
	for _, bounds := range [2][2]int{{0, height / 2}, {height / 2, height}} {
		found := false
		for y := bounds[0]; y < bounds[1] && !found; y++ {
			for x := 0; x < width; x++ {
				px := img.RGBAAt(x, y)
				if math.Abs(float64(px.R)-float64(background.R)) > 40 ||
					math.Abs(float64(px.G)-float64(background.G)) > 40 ||
					math.Abs(float64(px.B)-float64(background.B)) > 40 {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func assertTapeHalves(t *testing.T, label string, frame renderlist.Frame, view *render.View, tester *ui.Tester, width, height int) {
	t.Helper()
	view.SetFrame(frame)
	tester.Frame()
	img := tester.Image()
	if img == nil {
		t.Fatalf("%s: Tester.Image() returned nil", label)
	}
	background := color.RGBA{R: 0x10, G: 0x10, B: 0x18, A: 255}
	if !halfHasTextPixels(img, width, height, background) {
		t.Fatalf("%s: blank half-screen in offscreen pixels", label)
	}
}

func runScrollTapeCase(t *testing.T, testCase scrollTapeCase) {
	t.Helper()
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)
	clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	cfg := render.DefaultAnimationConfig()
	cfg.CursorEnabled = false
	anim := render.NewAnimator(planner, cfg, clock)
	view, tester, width, height := scrollTapeRasterizer(cell.Width, cell.Height)
	var staticFrame renderlist.Frame

	state, list := scrollTapeState(1, "before")
	if _, _, err := anim.Submit(state, &screen.FrameMeta{Seq: 1}, list); err != nil {
		t.Fatalf("%s: Submit baseline: %v", testCase.name, err)
	}
	for index, delta := range testCase.deltas {
		seq := uint64(index + 2)
		state, list = scrollTapeState(seq, fmt.Sprintf("after_%d", index))
		meta := &screen.FrameMeta{
			Seq: seq,
			Viewports: map[int]screen.Viewport{
				1: {Grid: 1, TopLine: 0, BotLine: 16, ScrollDelta: delta, HasScrollDelta: true},
			},
		}
		frame, active, err := anim.Submit(state, meta, list)
		if err != nil || !active {
			t.Fatalf("%s: Submit delta=%v active=%v err=%v", testCase.name, delta, active, err)
		}
		offset, _, _ := anim.ScrollOffset(1)
		if math.Abs(delta) > 16 && math.Abs(float64(offset)) > float64(cfg.ScrollFarLines)+0.05 {
			t.Fatalf("%s: ScrollOffset=%v exceeds ScrollFarLines+0.05", testCase.name, offset)
		}
		assertTapeHalves(t, fmt.Sprintf("%s delta=%v submit", testCase.name, delta), frame, view, tester, width, height)
		if index < len(testCase.deltas)-1 {
			clock.Advance(16 * time.Millisecond)
			middle, _, err := anim.Step()
			if err != nil {
				t.Fatalf("%s: Step after delta=%v: %v", testCase.name, delta, err)
			}
			assertTapeHalves(t, fmt.Sprintf("%s delta=%v middle", testCase.name, delta), middle, view, tester, width, height)
			continue
		}
		for step := 0; step < 3; step++ {
			clock.Advance(16 * time.Millisecond)
			middle, _, err := anim.Step()
			if err != nil {
				t.Fatalf("%s: middle step %d: %v", testCase.name, step, err)
			}
			assertTapeHalves(t, fmt.Sprintf("%s delta=%v middle %d", testCase.name, delta, step), middle, view, tester, width, height)
		}
		staticFrame, err = planner.Plan(list)
		if err != nil {
			t.Fatalf("%s: static planner.Plan: %v", testCase.name, err)
		}
		for step := 0; step < 200; step++ {
			clock.Advance(16 * time.Millisecond)
			settled, active, err := anim.Step()
			if err != nil {
				t.Fatalf("%s: converge step %d: %v", testCase.name, step, err)
			}
			if active {
				continue
			}
			if !reflect.DeepEqual(settled, staticFrame) {
				t.Fatalf("%s: settled frame differs from static planner.Plan", testCase.name)
			}
			return
		}
		t.Fatalf("%s: scroll did not converge", testCase.name)
	}
}

func TestScrollSeamlessTapeZeroBlankTopAndBottom(t *testing.T) {
	for _, testCase := range []scrollTapeCase{
		{name: "single down", deltas: []float64{+8}},
		{name: "double Ctrl-D", deltas: []float64{+8, +8}},
		{name: "double Ctrl-U", deltas: []float64{-8, -8}},
		{name: "huge down", deltas: []float64{+50}},
		{name: "huge up", deltas: []float64{-50}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runScrollTapeCase(t, testCase)
		})
	}

	t.Run("float margins", func(t *testing.T) {
		resolver := mustResolver(t)
		cell := resolver.Cell()
		planner := render.NewPlanner(resolver)
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		cfg := render.DefaultAnimationConfig()
		cfg.CursorEnabled = false
		anim := render.NewAnimator(planner, cfg, clock)
		green := uint32(0x00ff00)
		buildFloat := func(seq uint64, prefix string) (*screen.State, *drawlist.List) {
			root := newBlankGrid(40, 16, 0)
			floatGrid := newBlankGrid(24, 10, 1)
			putRunes(floatGrid.Cells[0], 0, "┌──────────────────────┐", 2)
			for row := 1; row <= 8; row++ {
				putRunes(floatGrid.Cells[row], 0, "│", 2)
				putRunes(floatGrid.Cells[row], 23, "│", 2)
				putRunes(floatGrid.Cells[row], 1, fmt.Sprintf("%s_%02d", prefix, row), 3)
			}
			putRunes(floatGrid.Cells[9], 0, "└──────────────────────┘", 2)
			state := &screen.State{
				Seq: seq, Grids: map[int]*screen.Grid{1: root, 2: floatGrid},
				Attrs: map[int]screen.Attr{
					0: {}, 1: {"foreground": int64(0xcdd6f4), "background": int64(0x202030)},
					2: {"foreground": int64(0x4488ff), "background": int64(0x202030)},
					3: {"foreground": int64(green), "background": int64(0x202030)},
				},
				DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x101018, SP: 0xf38ba8},
				Placements: map[int]screen.Placement{
					2: {Kind: screen.PlacedFloat, Win: 1002, Anchor: "NW", ScreenRow: 2, ScreenCol: 4, ZIndex: 50, CompIndex: 10},
				},
				Cursor: screen.Cursor{Grid: 1, Row: 0, Col: 0}, Mode: "normal",
			}
			list, err := drawlist.Build(state)
			if err != nil {
				panic(err)
			}
			return state, list
		}
		state, list := buildFloat(1, "old")
		margins := map[int]screen.ViewportMargins{2: {Top: 1, Bottom: 1, Left: 1, Right: 1}}
		if _, _, err := anim.Submit(state, &screen.FrameMeta{Seq: 1, Margins: margins}, list); err != nil {
			t.Fatalf("float baseline: %v", err)
		}
		state, list = buildFloat(2, "new")
		meta := &screen.FrameMeta{
			Seq: 2, Margins: margins,
			Viewports: map[int]screen.Viewport{2: {Grid: 2, TopLine: 0, BotLine: 8, ScrollDelta: +4, HasScrollDelta: true}},
		}
		view, tester, _, _ := scrollTapeRasterizer(cell.Width, cell.Height)
		check := func(label string, frame renderlist.Frame) {
			view.SetFrame(frame)
			tester.Frame()
			img := tester.Image()
			if img == nil {
				t.Fatalf("%s: nil image", label)
			}
			innerTop := int(math.Floor(3 * float64(cell.Height)))
			innerMiddle := int(math.Floor(6 * float64(cell.Height)))
			innerLeft := int(math.Floor(5 * float64(cell.Width)))
			innerRight := int(math.Ceil(27 * float64(cell.Width)))
			greenPixels := 0
			for y := innerTop; y < innerMiddle; y++ {
				for x := innerLeft; x < innerRight; x++ {
					px := img.RGBAAt(x, y)
					if px.G > 180 && px.G > px.R+100 && px.G > px.B+100 {
						greenPixels++
					}
				}
			}
			if greenPixels == 0 {
				t.Fatalf("%s: old green inner text was covered by static fill", label)
			}
			for y := int(math.Floor(2 * float64(cell.Height))); y < int(math.Ceil(3*float64(cell.Height))); y++ {
				for x := int(math.Floor(4 * float64(cell.Width))); x < int(math.Ceil(28*float64(cell.Width))); x++ {
					px := img.RGBAAt(x, y)
					if px.G > 180 && px.G > px.R+100 && px.G > px.B+100 {
						t.Fatalf("%s: inner text overflowed top border at (%d,%d)", label, x, y)
					}
				}
			}
		}
		frame, active, err := anim.Submit(state, meta, list)
		if err != nil || !active {
			t.Fatalf("float scroll Submit active=%v err=%v", active, err)
		}
		check("float submit", frame)
		clock.Advance(16 * time.Millisecond)
		frame, _, err = anim.Step()
		if err != nil {
			t.Fatalf("float Step: %v", err)
		}
		check("float middle", frame)
	})
}

func TestScrollFractionalCellBoundaryTextAnimatesAndSettles(t *testing.T) {
	const gridID = 2
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1.25})
	if err != nil {
		t.Fatalf("text.NewResolver: %v", err)
	}
	planner := render.NewPlanner(resolver)
	cell := resolver.Cell()
	if cell.Width <= 0 || cell.Height <= 0 ||
		math.Abs(float64(cell.Width)-math.Trunc(float64(cell.Width))) < 0.001 ||
		math.Abs(float64(cell.Height)-math.Trunc(float64(cell.Height))) < 0.001 {
		t.Fatalf("text resolver cell is not fractional: %+v", cell)
	}
	cfg := render.DefaultAnimationConfig()
	cfg.CursorEnabled = false
	margins := map[int]screen.ViewportMargins{gridID: {Top: 1, Bottom: 1, Left: 1, Right: 1}}
	buildState := func(seq uint64, prefix, boundaryGlyph string, boundaryColumn int, clipped bool) (*screen.State, *drawlist.List) {
		root := newBlankGrid(40, 16, 0)
		floatGrid := newBlankGrid(24, 10, 1)
		putRunes(floatGrid.Cells[0], 0, "┌──────────────────────┐", 2)
		for row := 1; row <= 8; row++ {
			putRunes(floatGrid.Cells[row], 0, "│", 2)
			putRunes(floatGrid.Cells[row], 23, "│", 2)
			putRunes(floatGrid.Cells[row], 1, fmt.Sprintf("%s_%02d", prefix, row), 3)
		}
		putRunes(floatGrid.Cells[9], 0, "└──────────────────────┘", 2)
		putRunes(floatGrid.Cells[2], boundaryColumn, boundaryGlyph, 3)
		state := &screen.State{
			Seq: seq, Grids: map[int]*screen.Grid{1: root, 2: floatGrid},
			Attrs: map[int]screen.Attr{
				0: {}, 1: {"foreground": int64(0xcdd6f4), "background": int64(0x202030)},
				2: {"foreground": int64(0x4488ff), "background": int64(0x202030)},
				3: {"foreground": int64(0x00ff00), "background": int64(0x202030)},
			},
			DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x101018, SP: 0xf38ba8},
			Placements: map[int]screen.Placement{
				2: {Kind: screen.PlacedFloat, Win: 1002, Anchor: "NW", ScreenRow: 2, ScreenCol: 4, ZIndex: 50, CompIndex: 10},
			},
			Cursor: screen.Cursor{Grid: 1, Row: 0, Col: 0}, Mode: "normal",
		}
		list, buildErr := drawlist.Build(state)
		if buildErr != nil {
			t.Fatalf("drawlist.Build: %v", buildErr)
		}
		if clipped {
			for index := range list.Layers {
				if list.Layers[index].Grid == gridID {
					list.Layers[index].Clip = drawlist.Rect{X: 5, Y: 3, W: 20, H: 6}
				}
			}
		}
		return state, list
	}

	runCase := func(t *testing.T, boundaryColumn int, clipped bool) {
		t.Helper()
		clock := render.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		anim := render.NewAnimator(planner, cfg, clock)
		state, list := buildState(1, "before", "A", boundaryColumn, clipped)
		if _, _, err := anim.Submit(state, &screen.FrameMeta{Seq: 1, Margins: margins}, list); err != nil {
			t.Fatalf("baseline Submit: %v", err)
		}
		state, list = buildState(2, "after", "B", boundaryColumn, clipped)
		staticFrame, err := planner.Plan(list)
		if err != nil {
			t.Fatalf("planner.Plan: %v", err)
		}
		wantX := float32(4+boundaryColumn) * cell.Width
		staticCommands := findBoundaryCommands(staticFrame, wantX)
		if len(staticCommands) != 1 {
			t.Fatalf("static frame has %d boundary inner text commands, want 1", len(staticCommands))
		}
		staticText := staticCommands[0]
		meta := &screen.FrameMeta{
			Seq: 2, Margins: margins,
			Viewports: map[int]screen.Viewport{
				gridID: {Grid: gridID, TopLine: 0, BotLine: 8, ScrollDelta: +4, HasScrollDelta: true},
			},
		}
		frame, active, err := anim.Submit(state, meta, list)
		if err != nil || !active {
			t.Fatalf("scroll Submit active=%v err=%v", active, err)
		}
		checkAnimated := func(label string, frame renderlist.Frame) {
			commands := findBoundaryCommands(frame, wantX)
			if len(commands) == 0 {
				t.Fatalf("%s: boundary inner text missing", label)
			}
			for _, command := range commands {
				if !(command.Y > staticText.Y+0.5) {
					t.Fatalf("%s: boundary inner text frozen at Y=%v, static Y=%v", label, command.Y, staticText.Y)
				}
			}
		}
		checkAnimated("submit", frame)
		clock.Advance(16 * time.Millisecond)
		frame, _, err = anim.Step()
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		checkAnimated("middle", frame)
		converged := false
		for step := 0; step < 200; step++ {
			clock.Advance(16 * time.Millisecond)
			settled, active, err := anim.Step()
			if err != nil {
				t.Fatalf("converge Step %d: %v", step, err)
			}
			if active {
				continue
			}
			if !reflect.DeepEqual(settled, staticFrame) {
				t.Fatal("settled frame differs from static planner.Plan")
			}
			converged = true
			break
		}
		if !converged {
			t.Fatal("fractional-cell scroll did not converge")
		}
	}
	for _, testCase := range []struct {
		name           string
		boundaryColumn int
		clipped        bool
	}{
		{name: "plain float right inner boundary", boundaryColumn: 22, clipped: false},
		{name: "screen clipped float right inner boundary", boundaryColumn: 20, clipped: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runCase(t, testCase.boundaryColumn, testCase.clipped)
		})
	}
}

func findBoundaryCommands(frame renderlist.Frame, wantX float32) []renderlist.Command {
	result := make([]renderlist.Command, 0)
	for _, command := range frame.Commands {
		if command.Op != "glyphs" || command.Text != "B" {
			continue
		}
		if math.Abs(float64(command.X-wantX)) < 0.01 {
			result = append(result, command)
		}
	}
	return result
}
