package render

import (
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
	if got := metadataFor(stopped); len(got) != len(static.Commands) {
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
	if got := metadataFor(zeroDelta); !reflect.DeepEqual(got, animator.impl.latestMetas) {
		t.Fatalf("Step dt=0 metadata = %+v, want %+v", got, animator.impl.latestMetas)
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
	repeatedMetas := metadataFor(repeated)
	for index, meta := range repeatedMetas {
		if meta.hasCursorQuad || meta.cursorQuad != ([4]ui.Point{}) {
			t.Fatalf("repeat Submit metadata %d = %+v, want no cursor quad", index, meta)
		}
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
