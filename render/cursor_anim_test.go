package render

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

func cursorAnimResolver(t *testing.T) *text.Resolver {
	t.Helper()
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func cursorAnimList(seq uint64, items ...drawlist.Item) *drawlist.List {
	return &drawlist.List{
		Version: 1,
		Seq:     seq,
		Width:   40,
		Height:  20,
		Layers: []drawlist.Layer{{
			Grid: 1, ScreenRow: 0, ScreenCol: 0, Width: 40, Height: 20,
			Clip:  drawlist.Rect{X: 0, Y: 0, W: 40, H: 20},
			Items: items,
		}},
	}
}

func cursorAnimState(seq uint64, row, col int, mode string) *screen.State {
	grid := &screen.Grid{Width: 40, Height: 20, Cells: make([][]screen.Cell, 20)}
	for rowIndex := range grid.Cells {
		grid.Cells[rowIndex] = make([]screen.Cell, 40)
		for colIndex := range grid.Cells[rowIndex] {
			grid.Cells[rowIndex][colIndex] = screen.Cell{Text: " "}
		}
	}
	return &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: row, Col: col},
		Mode:          mode,
	}
}

func cursorAnimItem(row, col int, attrs ...string) drawlist.Item {
	return drawlist.Item{Kind: "cursor", Row: row, Col: col, Width: 1, Height: 1, FG: 0x111111, BG: 0xeeeeee, Attrs: attrs}
}

func TestCursorAnimCornerOrderAndDirectionAlignment(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	first := cursorAnimList(1, cursorAnimItem(2, 2))
	if _, _, err := animator.Submit(cursorAnimState(1, 2, 2, "normal"), &screen.FrameMeta{Seq: 1}, first); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	second := cursorAnimList(2, cursorAnimItem(12, 30))
	_, active, err := animator.Submit(cursorAnimState(2, 12, 30, "normal"), &screen.FrameMeta{Seq: 2}, second)
	if err != nil || !active {
		t.Fatalf("Submit long jump = (active=%v, err=%v), want active", active, err)
	}
	dots := [4]float32{}
	for index := range dots {
		dots[index] = animator.impl.cursor.relativeCornerAlignment(index)
	}
	if !(dots[2] > dots[0]) {
		t.Fatalf("BR alignment %v is not ahead of TL alignment %v", dots[2], dots[0])
	}
	if !(animator.impl.cursor.corners[2].animLen <= animator.impl.cursor.corners[0].animLen) {
		t.Fatalf("leading BR length %v exceeds trailing TL length %v",
			animator.impl.cursor.corners[2].animLen, animator.impl.cursor.corners[0].animLen)
	}
}

func TestCursorAnimBeamAndUnderlineTargetQuads(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	for _, tc := range []struct {
		name  string
		attr  string
		mode  string
		wantW float32
		wantH float32
	}{
		{name: "beam", attr: "cursor_beam", mode: "insert", wantW: 1.5, wantH: resolver.Cell().Height},
		{name: "underline", attr: "cursor_underline", mode: "replace", wantW: resolver.Cell().Width, wantH: 1.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
			list := cursorAnimList(1, cursorAnimItem(4, 6, tc.attr))
			frame, _, err := animator.Submit(cursorAnimState(1, 4, 6, tc.mode), &screen.FrameMeta{Seq: 1}, list)
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			index := cursorCommandIndex(frame)
			rect := animator.impl.cursor.targetRect(frame, index)
			if !approxEqualCursor(rect.W, tc.wantW, 0.001) || !approxEqualCursor(rect.H, tc.wantH, 0.001) {
				t.Fatalf("target rect = %+v, want %vx%v", rect, tc.wantW, tc.wantH)
			}
			quad, ok := animator.CursorQuad()
			if !ok {
				t.Fatal("CursorQuad ok=false")
			}
			want := cursorRectQuad(rect)
			if quad != want {
				t.Fatalf("CursorQuad = %+v, want %+v", quad, want)
			}
		})
	}
}

func TestCursorAnimStoppedFrameRefreshesMetadataAndMatchesStatic(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	list := cursorAnimList(7, cursorAnimItem(3, 9))
	state := cursorAnimState(7, 3, 9, "normal")
	static, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	frame, active, err := animator.Submit(state, &screen.FrameMeta{Seq: 7}, list)
	if err != nil || active {
		t.Fatalf("cold Submit = (active=%v, err=%v)", active, err)
	}
	if !reflect.DeepEqual(frame, static) {
		t.Fatalf("stopped frame differs from static frame")
	}
	clearMetadataLRU(t)
	clock.Advance(16 * time.Millisecond)
	stopped, active, err := animator.Step()
	if err != nil || active {
		t.Fatalf("Step = (active=%v, err=%v)", active, err)
	}
	if !reflect.DeepEqual(stopped, static) {
		t.Fatal("Step stopped frame differs from static frame")
	}
	if got := metadataFor(stopped); !reflect.DeepEqual(got, metadataFor(static)) {
		t.Fatalf("Step metadata = %+v, want %+v", got, metadataFor(static))
	}
	if got := metadataFor(stopped).rows; len(got) != len(static.Commands) {
		t.Fatalf("metadata entries = %d, want %d", len(got), len(static.Commands))
	}
}

func TestCursorAnimZeroDeltaReturnsLatestQuadFrame(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	if _, _, err := animator.Submit(cursorAnimState(1, 2, 2, "normal"), &screen.FrameMeta{Seq: 1}, cursorAnimList(1, cursorAnimItem(2, 2))); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	if _, _, err := animator.Submit(cursorAnimState(2, 10, 20, "normal"), &screen.FrameMeta{Seq: 2}, cursorAnimList(2, cursorAnimItem(10, 20))); err != nil {
		t.Fatalf("Submit target: %v", err)
	}
	clock.Advance(16 * time.Millisecond)
	advanced, _, err := animator.Step()
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	quad, ok := animator.CursorQuad()
	if !ok {
		t.Fatal("CursorQuad ok=false in intermediate frame")
	}
	zeroDelta, active, err := animator.Step()
	if err != nil || !active {
		t.Fatalf("Step dt=0 = (active=%v, err=%v)", active, err)
	}
	if !reflect.DeepEqual(zeroDelta, advanced) {
		t.Fatal("Step dt=0 did not return latest animated frame")
	}
	if got, ok := animator.CursorQuad(); !ok || got != quad {
		t.Fatalf("Step dt=0 quad = (%+v, %v), want (%+v, true)", got, ok, quad)
	}
	if got := metadataFor(zeroDelta); !reflect.DeepEqual(got, animator.impl.latestMeta) {
		t.Fatalf("Step dt=0 metadata = %+v, want %+v", got, animator.impl.latestMeta)
	}
}

func TestCursorAnimRepeatedSubmitKeepsStoppedFrameAndFlightLength(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)

	firstState := cursorAnimState(1, 2, 2, "normal")
	firstList := cursorAnimList(1, cursorAnimItem(2, 2))
	if _, active, err := animator.Submit(firstState, &screen.FrameMeta{Seq: 1}, firstList); err != nil || active {
		t.Fatalf("cold Submit = (active=%v, err=%v)", active, err)
	}
	sameState := cursorAnimState(2, 2, 2, "normal")
	sameList := cursorAnimList(2, cursorAnimItem(2, 2))
	repeated, active, err := animator.Submit(sameState, &screen.FrameMeta{Seq: 2}, sameList)
	if err != nil {
		t.Fatalf("repeat Submit: %v", err)
	}
	static, err := planner.Plan(sameList)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if active || animator.Active() || animator.impl.cursor.active {
		t.Fatalf("repeat Submit activated animation: active=%v anim=%v cursor=%v", active, animator.Active(), animator.impl.cursor.active)
	}
	if !reflect.DeepEqual(repeated, static) {
		t.Fatalf("repeat Submit frame = %+v, want static %+v", repeated, static)
	}
	repeatedMeta := metadataFor(repeated)
	if repeatedMeta.hasCursorQuad || repeatedMeta.cursorIndex != 0 || repeatedMeta.cursorQuad != ([4]ui.Point{}) {
		t.Fatalf("repeat Submit metadata = %+v, want no cursor quad", repeatedMeta)
	}

	targetState := cursorAnimState(3, 12, 30, "normal")
	targetList := cursorAnimList(3, cursorAnimItem(12, 30))
	if _, active, err := animator.Submit(targetState, &screen.FrameMeta{Seq: 3}, targetList); err != nil || !active {
		t.Fatalf("long jump Submit = (active=%v, err=%v)", active, err)
	}
	wantLengths := [4]float32{}
	for index := range wantLengths {
		wantLengths[index] = animator.impl.cursor.corners[index].animLen
	}
	if wantLengths[0] <= 0 || wantLengths[2] >= wantLengths[0] || wantLengths[1] == wantLengths[3] {
		t.Fatalf("long jump lengths = %+v, want directional leading/trailing lengths", wantLengths)
	}
	clock.Advance(16 * time.Millisecond)
	if _, active, err := animator.Step(); err != nil || !active {
		t.Fatalf("mid-flight Step = (active=%v, err=%v)", active, err)
	}
	repeatFlightState := cursorAnimState(4, 12, 30, "normal")
	repeatFlightList := cursorAnimList(4, cursorAnimItem(12, 30))
	if _, active, err := animator.Submit(repeatFlightState, &screen.FrameMeta{Seq: 4}, repeatFlightList); err != nil || !active {
		t.Fatalf("same-target mid-flight Submit = (active=%v, err=%v)", active, err)
	}
	for index := range wantLengths {
		if got := animator.impl.cursor.corners[index].animLen; got != wantLengths[index] {
			t.Fatalf("corner %d animLen = %v, want %v", index, got, wantLengths[index])
		}
	}
}

func TestCursorAnimSetConfigDisabledSnapsWithoutExposingBusyCursor(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	startState := cursorAnimState(1, 2, 2, "normal")
	if _, _, err := animator.Submit(startState, &screen.FrameMeta{Seq: 1}, cursorAnimList(1, cursorAnimItem(2, 2))); err != nil {
		t.Fatalf("Submit visible: %v", err)
	}
	busyState := cursorAnimState(2, 19, 30, "normal")
	busyFrame, active, err := animator.Submit(busyState, &screen.FrameMeta{Seq: 2, Busy: true}, cursorAnimList(2, cursorAnimItem(19, 30)))
	if err != nil || active {
		t.Fatalf("Submit busy = (active=%v, err=%v)", active, err)
	}
	if cursorCommandIndex(busyFrame) >= 0 {
		t.Fatalf("busy frame exposed cursor command: %+v", busyFrame)
	}
	cfg := DisabledAnimationConfig()
	animator.SetConfig(cfg)
	if !animator.impl.cursor.suppressed || animator.impl.cursor.visible {
		t.Fatalf("SetConfig busy suppression = (suppressed=%v, visible=%v), want (true,false)",
			animator.impl.cursor.suppressed, animator.impl.cursor.visible)
	}
	if quad, ok := animator.CursorQuad(); ok {
		t.Fatalf("SetConfig exposed busy quad %+v", quad)
	}
	clock.Advance(16 * time.Millisecond)
	gotFrame, active, err := animator.Step()
	if err != nil || active {
		t.Fatalf("Step after SetConfig = (active=%v, err=%v)", active, err)
	}
	if cursorCommandIndex(gotFrame) >= 0 {
		t.Fatalf("Step after SetConfig exposed cursor command: %+v", gotFrame)
	}
}

func TestCursorAnimSetConfigDisabledSnapsVisibleFlight(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	if _, _, err := animator.Submit(cursorAnimState(1, 2, 2, "normal"), &screen.FrameMeta{Seq: 1}, cursorAnimList(1, cursorAnimItem(2, 2))); err != nil {
		t.Fatalf("Submit start: %v", err)
	}
	if _, active, err := animator.Submit(cursorAnimState(2, 12, 30, "normal"), &screen.FrameMeta{Seq: 2}, cursorAnimList(2, cursorAnimItem(12, 30))); err != nil || !active {
		t.Fatalf("Submit long jump = (active=%v, err=%v)", active, err)
	}
	clock.Advance(16 * time.Millisecond)
	if _, active, err := animator.Step(); err != nil || !active {
		t.Fatalf("mid-flight Step = (active=%v, err=%v)", active, err)
	}
	inFlightCorners := 0
	for index := range animator.impl.cursor.corners {
		corner := animator.impl.cursor.corners[index]
		if corner.springX.Position != 0 || corner.springY.Position != 0 ||
			corner.springX.Velocity != 0 || corner.springY.Velocity != 0 {
			inFlightCorners++
		}
	}
	if inFlightCorners == 0 {
		t.Fatal("no corner had in-flight spring residual before SetConfig")
	}
	animator.SetConfig(DisabledAnimationConfig())
	if animator.impl.cursor.active || animator.Active() {
		t.Fatalf("SetConfig left animation active: cursor=%v animator=%v", animator.impl.cursor.active, animator.Active())
	}
	for index := range animator.impl.cursor.corners {
		corner := animator.impl.cursor.corners[index]
		if corner.springX.Position != 0 || corner.springY.Position != 0 ||
			corner.springX.Velocity != 0 || corner.springY.Velocity != 0 {
			t.Fatalf("corner %d spring residual after SetConfig: %+v", index, corner)
		}
		if got, want := corner.current, animator.impl.cursor.cornerDestination(index, animator.impl.cursor.dest); got != want {
			t.Fatalf("corner %d current = %+v, want snapped destination %+v", index, got, want)
		}
	}
}

func TestCursorAnimInMsgGridGuardIgnoresZeroMsgGrid(t *testing.T) {
	state := cursorAnimState(1, 2, 2, "normal")
	state.Msg = &screen.MsgPlacement{Grid: 0}
	state.Cursor.Grid = 0
	if cursorInMsgGrid(state) {
		t.Fatal("zero msg grid matched non-root cursor grid as an active msg grid")
	}
	state.Msg.Grid = 3
	state.Cursor.Grid = 3
	if !cursorInMsgGrid(state) {
		t.Fatal("positive msg grid did not match its cursor grid")
	}
	state.Cursor.Grid = 1
	if cursorInMsgGrid(state) {
		t.Fatal("root grid was classified as a msg grid")
	}
}

func TestCursorAnimShapeHeightUsesRawCellHeight(t *testing.T) {
	resolver := cursorAnimResolver(t)
	planner := NewPlanner(resolver)
	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	startState := cursorAnimState(1, 2, 2, "normal")
	if _, _, err := animator.Submit(startState, &screen.FrameMeta{Seq: 1}, cursorAnimList(1, cursorAnimItem(2, 2))); err != nil {
		t.Fatalf("Submit normal: %v", err)
	}
	underlineState := cursorAnimState(2, 2, 3, "replace")
	_, active, err := animator.Submit(underlineState, &screen.FrameMeta{Seq: 2}, cursorAnimList(2, cursorAnimItem(2, 3, "cursor_underline")))
	if err != nil || !active {
		t.Fatalf("shape change Submit = (active=%v, err=%v), want active", active, err)
	}
	if got := animator.impl.cursor.prevHeight; !approxEqualCursor(got, resolver.Cell().Height, 0.001) {
		t.Fatalf("remembered height = %v, want raw cell height %v", got, resolver.Cell().Height)
	}
}

func approxEqualCursor(a, b, tolerance float32) bool {
	delta := a - b
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}

var _ = renderlist.Frame{}

func restrainedCursorList(seq uint64, width, height, row, col int) (*screen.State, *drawlist.List) {
	grid := &screen.Grid{Width: width, Height: height, Cells: make([][]screen.Cell, height)}
	for rowIndex := 0; rowIndex < height; rowIndex++ {
		grid.Cells[rowIndex] = make([]screen.Cell, width)
		for colIndex := range grid.Cells[rowIndex] {
			grid.Cells[rowIndex][colIndex] = screen.Cell{Text: " "}
		}
		line := fmt.Sprintf("restrained_cursor_line_%02d", rowIndex)
		for colIndex, character := range line {
			if colIndex >= width {
				break
			}
			grid.Cells[rowIndex][colIndex] = screen.Cell{Text: string(character)}
		}
	}
	state := &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: row, Col: col},
		Mode:          "normal",
	}
	list, err := drawlist.Build(state)
	if err != nil {
		panic(err)
	}
	return state, list
}

func restrainedProjectionSpan(quad [4]ui.Point, ux, uy float64) (float64, float64) {
	minProj, maxProj := math.Inf(1), math.Inf(-1)
	for _, point := range quad {
		projection := float64(point.X)*ux + float64(point.Y)*uy
		minProj = min(minProj, projection)
		maxProj = max(maxProj, projection)
	}
	return minProj, maxProj
}

func assertRestrainedCursorSnapped(t *testing.T, label string, anim *Animator, row, col int, cell ui.Point) {
	t.Helper()
	quad, ok := anim.CursorQuad()
	if !ok {
		t.Fatalf("%s: CursorQuad unavailable", label)
	}
	minX, minY, maxX, maxY := quadBounds(quad)
	wantX := float32(col) * cell.X
	wantY := float32(row) * cell.Y
	if math.Abs(float64(minX-wantX)) > 0.75 || math.Abs(float64(minY-wantY)) > 0.75 ||
		math.Abs(float64(maxX-wantX-cell.X)) > 0.75 || math.Abs(float64(maxY-wantY-cell.Y)) > 0.75 {
		t.Fatalf("%s: CursorQuad bounds = [%v,%v]x[%v,%v], want cell [%v,%v]x[%v,%v]: %+v",
			label, minX, maxX, minY, maxY, wantX, wantX+cell.X, wantY, wantY+cell.Y, quad)
	}
	if anim.impl.cursor.active {
		t.Fatalf("%s: cursor animation active during scroll", label)
	}
}

func TestCursorRestrainedElasticMaxStretchAndScrollNoZigZag(t *testing.T) {
	cfg := DefaultAnimationConfig()
	if cfg.CursorAnimationLength != 0.08 || cfg.CursorShortLength != 0.03 ||
		cfg.CursorTrailSize != 0.25 || cfg.ScrollAnimationLength != 0.18 {
		t.Fatalf("DefaultAnimationConfig() = %+v, want scheme B parameters", cfg)
	}
	resolver := cursorAnimResolver(t)
	cell := resolver.Cell()
	maxCell := math.Max(float64(cell.Width), float64(cell.Height))
	planner := NewPlanner(resolver)

	for _, jump := range []struct {
		name          string
		width, height int
		targetRow     int
		targetCol     int
	}{
		{name: "28x10", width: 40, height: 20, targetRow: 12, targetCol: 30},
		{name: "160x60", width: 180, height: 70, targetRow: 62, targetCol: 162},
	} {
		t.Run(jump.name, func(t *testing.T) {
			clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
			anim := NewAnimator(planner, cfg, clock)
			state, list := restrainedCursorList(1, jump.width, jump.height, 2, 2)
			if _, _, err := anim.Submit(state, &screen.FrameMeta{Seq: 1}, list); err != nil {
				t.Fatalf("baseline Submit: %v", err)
			}
			state, list = restrainedCursorList(2, jump.width, jump.height, jump.targetRow, jump.targetCol)
			if _, active, err := anim.Submit(state, &screen.FrameMeta{Seq: 2}, list); err != nil || !active {
				t.Fatalf("long jump Submit active=%v err=%v", active, err)
			}
			dx := float64(jump.targetCol-2) * float64(cell.Width)
			dy := float64(jump.targetRow-2) * float64(cell.Height)
			distance := math.Hypot(dx, dy)
			ux, uy := dx/distance, dy/distance
			staticSpan := float64(cell.Width)*math.Abs(ux) + float64(cell.Height)*math.Abs(uy)
			limit := 3.0 * maxCell
			sawElastic := false
			for elapsed := 0.0; elapsed <= 320; elapsed += 16 {
				if elapsed > 0 {
					clock.Advance(16 * time.Millisecond)
					if _, _, err := anim.Step(); err != nil {
						t.Fatalf("Step %vms: %v", elapsed, err)
					}
				}
				quad, ok := anim.CursorQuad()
				if !ok {
					t.Fatalf("CursorQuad at %vms unavailable", elapsed)
				}
				minProj, maxProj := restrainedProjectionSpan(quad, ux, uy)
				span := maxProj - minProj
				if span > limit {
					t.Fatalf("quad span at %vms = %.3f, exceeds 3*maxCell=%.3f: %+v", elapsed, span, limit, quad)
				}
				if elapsed == 16 && span > staticSpan+1.0 {
					sawElastic = true
				}
			}
			if !sawElastic {
				t.Fatalf("16ms quad lost restrained elastic stretch (staticSpan=%.3f)", staticSpan)
			}
		})
	}

	t.Run("scroll no zigzag", func(t *testing.T) {
		clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
		anim := NewAnimator(planner, cfg, clock)
		sequence := []struct {
			row, col    int
			scrollDelta float64
		}{
			{row: 2, col: 2, scrollDelta: 0},
			{row: 2, col: 28, scrollDelta: 4},
			{row: 2, col: 4, scrollDelta: 4},
			{row: 2, col: 32, scrollDelta: -4},
		}
		for index, item := range sequence {
			state, list := restrainedCursorList(uint64(index+1), 40, 20, item.row, item.col)
			meta := &screen.FrameMeta{Seq: uint64(index + 1)}
			if item.scrollDelta != 0 {
				meta.Viewports = map[int]screen.Viewport{
					1: {Grid: 1, TopLine: 0, BotLine: 20, ScrollDelta: item.scrollDelta, HasScrollDelta: true},
				}
			}
			frame, _, err := anim.Submit(state, meta, list)
			if err != nil {
				t.Fatalf("Submit %d: %v", index, err)
			}
			if cursorCommandIndex(frame) < 0 {
				t.Fatalf("Submit %d: missing cursor command", index)
			}
			assertRestrainedCursorSnapped(t, fmt.Sprintf("Submit %d", index), anim, item.row, item.col,
				ui.Point{X: cell.Width, Y: cell.Height})
			if index < len(sequence)-1 {
				clock.Advance(16 * time.Millisecond)
				stepFrame, _, err := anim.Step()
				if err != nil {
					t.Fatalf("Step %d: %v", index, err)
				}
				if cursorCommandIndex(stepFrame) < 0 {
					t.Fatalf("Step %d: missing cursor command", index)
				}
				assertRestrainedCursorSnapped(t, fmt.Sprintf("Step %d", index), anim, item.row, item.col,
					ui.Point{X: cell.Width, Y: cell.Height})
			}
		}
		lastRow, lastCol := sequence[len(sequence)-1].row, sequence[len(sequence)-1].col
		for step := 0; step < 200; step++ {
			clock.Advance(16 * time.Millisecond)
			_, active, err := anim.Step()
			if err != nil {
				t.Fatalf("scroll converge Step %d: %v", step, err)
			}
			if active {
				assertRestrainedCursorSnapped(t, fmt.Sprintf("scroll converge Step %d", step), anim, lastRow, lastCol,
					ui.Point{X: cell.Width, Y: cell.Height})
			} else {
				break
			}
		}
		if anim.impl.scroll[1].spring.Active() {
			t.Fatal("scroll spring did not settle before non-scroll cursor jump")
		}
		state, list := restrainedCursorList(100, 40, 20, 12, 4)
		if _, active, err := anim.Submit(state, &screen.FrameMeta{Seq: 100}, list); err != nil || !active {
			t.Fatalf("non-scroll jump Submit active=%v err=%v", active, err)
		}
		if !anim.impl.cursor.active {
			t.Fatal("non-scroll cursor jump did not restore restrained elastic animation")
		}
	})
}
