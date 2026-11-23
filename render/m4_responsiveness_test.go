package render

import (
	"fmt"
	"image"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
)

func m4ResponsivenessResolver(t *testing.T) *Planner {
	t.Helper()
	return NewPlanner(cursorAnimResolver(t))
}

func m4ResponsivenessClock() (*ManualClock, time.Time) {
	start := time.Unix(1_700_000_000, 0).UTC()
	return NewManualClock(start), start
}

func m4ResponsivenessCursorQuadAtCell(t *testing.T, animator *Animator, row, col int) {
	t.Helper()
	cell := animator.impl.planner.resolver.Cell()
	quad, ok := animator.CursorQuad()
	if !ok {
		t.Fatal("CursorQuad unavailable")
	}
	wantX, wantY := float32(col)*cell.Width, float32(row)*cell.Height
	minX, minY, maxX, maxY := quadBounds(quad)
	if math.Abs(float64(minX-wantX)) > 0.75 || math.Abs(float64(minY-wantY)) > 0.75 ||
		math.Abs(float64(maxX-wantX-cell.Width)) > 0.75 ||
		math.Abs(float64(maxY-wantY-cell.Height)) > 0.75 {
		t.Fatalf("cursor quad = [%v,%v]x[%v,%v], want cell (%d,%d)", minX, maxX, minY, maxY, row, col)
	}
}

func m4ResponsivenessSubmitCursor(t *testing.T, animator *Animator, seq uint64, row, col int, mode string) bool {
	t.Helper()
	_, active, err := animator.Submit(cursorAnimState(seq, row, col, mode), &screen.FrameMeta{Seq: seq},
		cursorAnimList(seq, cursorAnimItem(row, col)))
	if err != nil {
		t.Fatalf("Submit seq %d: %v", seq, err)
	}
	return active
}

func m4ResponsivenessExpectAnimated(t *testing.T, animator *Animator, seq uint64, row, col int, mode, label string) {
	t.Helper()
	if !m4ResponsivenessSubmitCursor(t, animator, seq, row, col, mode) {
		t.Fatalf("%s did not animate", label)
	}
}

func TestM4Responsiveness_InsertModeSnapsImmediately(t *testing.T) {
	if DefaultAnimationConfig().CursorAnimateInsertMode {
		t.Fatal("CursorAnimateInsertMode default = true, want false")
	}
	planner := m4ResponsivenessResolver(t)
	clock, _ := m4ResponsivenessClock()
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	m4ResponsivenessSubmitCursor(t, animator, 1, 2, 2, "normal")
	clock.Advance(30 * time.Millisecond)
	if active := m4ResponsivenessSubmitCursor(t, animator, 2, 2, 3, "insert"); active || animator.impl.cursor.active {
		t.Fatalf("insert-mode typing animated: active=%v cursor=%v", active, animator.impl.cursor.active)
	}
	m4ResponsivenessCursorQuadAtCell(t, animator, 2, 3)
}

func TestM4Responsiveness_HeldJKTracksCursorLineAndIsolatedJumpsAnimate(t *testing.T) {
	planner := m4ResponsivenessResolver(t)
	clock, _ := m4ResponsivenessClock()
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	sequence := [][2]int{{2, 2}, {3, 2}, {4, 7}, {5, 1}, {6, 9}, {5, 3}, {4, 1}}
	for index, position := range sequence {
		if index > 0 {
			clock.Advance(30 * time.Millisecond)
		}
		active := m4ResponsivenessSubmitCursor(t, animator, uint64(index+1), position[0], position[1], "normal")
		if index > 1 && (active || animator.impl.cursor.active) {
			t.Fatalf("held j step %d animated instead of snapping: active=%v", index, active)
		}
		if index > 1 {
			m4ResponsivenessCursorQuadAtCell(t, animator, position[0], position[1])
		}
	}

	clock, _ = m4ResponsivenessClock()
	animator = NewAnimator(planner, DefaultAnimationConfig(), clock)
	m4ResponsivenessSubmitCursor(t, animator, 1, 2, 2, "normal")
	m4ResponsivenessExpectAnimated(t, animator, 2, 12, 30, "normal", "isolated long jump")

	clock, _ = m4ResponsivenessClock()
	animator = NewAnimator(planner, DefaultAnimationConfig(), clock)
	m4ResponsivenessSubmitCursor(t, animator, 1, 2, 2, "normal")
	m4ResponsivenessSubmitCursor(t, animator, 2, 2, 2, "normal")
	clock.Advance(30 * time.Millisecond)
	m4ResponsivenessExpectAnimated(t, animator, 3, 2, 3, "normal", "isolated jump after unchanged redraw")

	clock, _ = m4ResponsivenessClock()
	animator = NewAnimator(planner, DefaultAnimationConfig(), clock)
	m4ResponsivenessSubmitCursor(t, animator, 1, 2, 2, "normal")
	m4ResponsivenessExpectAnimated(t, animator, 2, 2, 35, "normal", "same-row long jump")

	clock, _ = m4ResponsivenessClock()
	animator = NewAnimator(planner, DefaultAnimationConfig(), clock)
	m4ResponsivenessSubmitCursor(t, animator, 1, 2, 2, "normal")
	clock.Advance(30 * time.Millisecond)
	m4ResponsivenessExpectAnimated(t, animator, 2, 3, 2, "normal", "isolated vertical short jump")

	clock, _ = m4ResponsivenessClock()
	animator = NewAnimator(planner, DefaultAnimationConfig(), clock)
	m4ResponsivenessSubmitCursor(t, animator, 1, 2, 2, "normal")
	clock.Advance(30 * time.Millisecond)
	m4ResponsivenessExpectAnimated(t, animator, 2, 18, 35, "normal", "consecutive G jump")
	clock.Advance(30 * time.Millisecond)
	m4ResponsivenessExpectAnimated(t, animator, 3, 1, 1, "normal", "consecutive gg jump")
}

func m4ResponsivenessScrollList(seq uint64, rows, startRow int, prefix string, cursorRow, cursorCol int, margins bool) (*screen.State, *drawlist.List) {
	grid := &screen.Grid{Width: 40, Height: rows, Cells: make([][]screen.Cell, rows)}
	for rowIndex := range grid.Cells {
		grid.Cells[rowIndex] = make([]screen.Cell, 40)
		for colIndex := range grid.Cells[rowIndex] {
			grid.Cells[rowIndex][colIndex] = screen.Cell{Text: " "}
		}
		line := fmt.Sprintf("%s_%02d_payload", prefix, startRow+rowIndex)
		for colIndex, character := range line {
			if colIndex >= 40 {
				break
			}
			grid.Cells[rowIndex][colIndex].Text = string(character)
		}
	}
	state := &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}, 1: {"background": int64(0x202030)}},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101018},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: cursorRow, Col: cursorCol},
		Mode:          "normal",
	}
	for colIndex := range grid.Cells[cursorRow] {
		grid.Cells[cursorRow][colIndex].HL = 1
	}
	if margins {
		_ = screen.ViewportMargins{Top: 1, Bottom: 1, Left: 1, Right: 1}
	}
	list, err := drawlist.Build(state)
	if err != nil {
		panic(err)
	}
	return state, list
}

func m4ResponsivenessScrollMeta(seq uint64, delta float64, margins bool) *screen.FrameMeta {
	meta := &screen.FrameMeta{Seq: seq, Viewports: map[int]screen.Viewport{
		1: {Grid: 1, TopLine: 0, BotLine: 16, ScrollDelta: delta, HasScrollDelta: true},
	}}
	if margins {
		meta.Margins = map[int]screen.ViewportMargins{
			1: {Top: 1, Bottom: 1, Left: 1, Right: 1},
		}
	}
	return meta
}

func m4ResponsivenessCursorY(frame renderlist.Frame) (float32, bool) {
	for _, command := range frame.Commands {
		if command.Op == "cursor" {
			return command.Y, true
		}
	}
	return 0, false
}

func m4ResponsivenessFrameDelta(frame renderlist.Frame, wantOp, wantText string, baseline float32) (float32, bool) {
	for _, command := range frame.Commands {
		if command.Op != wantOp || (wantText != "" && command.Text != wantText) {
			continue
		}
		return command.Y - baseline, true
	}
	return 0, false
}

func m4ResponsivenessCursorLineDelta(frame renderlist.Frame, baseline, cellWidth, cellHeight float32) (float32, bool) {
	const cursorLineBackground = 0x202030
	closest := float32(0)
	found := false
	for _, command := range frame.Commands {
		if command.Op != "fill" || command.BG != cursorLineBackground || command.W <= 0 || command.H <= 0 {
			continue
		}
		if math.Abs(float64(command.H-cellHeight)) > 2 || math.Abs(float64(command.W-cellWidth)) > 2 {
			continue
		}
		delta := command.Y - baseline
		if !found || math.Abs(float64(delta)) < math.Abs(float64(closest)) {
			closest, found = delta, true
		}
	}
	return closest, found
}

func TestM4Responsiveness_ScrollCursorMatchesCursorLineOffsetAndSettles(t *testing.T) {
	planner := m4ResponsivenessResolver(t)
	cell := planner.resolver.Cell()
	clock, _ := m4ResponsivenessClock()
	cfg := DefaultAnimationConfig()
	cfg.CursorEnabled = false
	animator := NewAnimator(planner, cfg, clock)
	state, list := m4ResponsivenessScrollList(1, 16, 0, "before", 5, 3, false)
	if _, _, err := animator.Submit(state, &screen.FrameMeta{Seq: 1}, list); err != nil {
		t.Fatalf("baseline Submit: %v", err)
	}
	state, list = m4ResponsivenessScrollList(2, 16, 0, "after", 5, 3, false)
	static, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	staticCursorY, staticCursorOK := m4ResponsivenessCursorY(static)
	if !staticCursorOK {
		t.Fatal("static frame missing cursor")
	}
	staticCursorLineY := float32(5) * cell.Height
	meta := m4ResponsivenessScrollMeta(2, 3, false)
	frame, active, err := animator.Submit(state, meta, list)
	if err != nil || !active {
		t.Fatalf("scroll Submit: active=%v err=%v", active, err)
	}
	lineDelta, lineOK := m4ResponsivenessFrameDelta(frame, "glyphs", "after_05_payload", 5*cell.Height)
	cursorDelta, cursorOK := m4ResponsivenessFrameDelta(frame, "cursor", "", staticCursorY)
	cursorLineDelta, cursorLineOK := m4ResponsivenessCursorLineDelta(frame, staticCursorLineY, 40*cell.Width, cell.Height)
	if !lineOK || !cursorOK {
		t.Fatalf("scroll frame missing target glyphs/cursor: line=%v cursor=%v", lineOK, cursorOK)
	}
	if !cursorLineOK {
		t.Fatal("scroll frame missing independent cursorline fill")
	}
	if math.Abs(float64(lineDelta-cursorDelta)) > 0.001 || math.Abs(float64(lineDelta-cursorLineDelta)) > 0.001 {
		t.Fatalf("scroll offsets disagree: line=%v cursor=%v cursorline=%v", lineDelta, cursorDelta, cursorLineDelta)
	}
	var settled renderlist.Frame
	for step := 0; step < 200; step++ {
		clock.Advance(16 * time.Millisecond)
		settled, active, err = animator.Step()
		if err != nil {
			t.Fatalf("Step %d: %v", step, err)
		}
		if !active {
			break
		}
	}
	if active || !reflect.DeepEqual(settled, static) {
		t.Fatal("settled scroll frame differs from static planner frame")
	}

	clock, _ = m4ResponsivenessClock()
	animator = NewAnimator(planner, cfg, clock)
	state, list = m4ResponsivenessScrollList(1, 16, 0, "margin", 15, 3, true)
	if _, _, err := animator.Submit(state, m4ResponsivenessScrollMeta(1, 0, true), list); err != nil {
		t.Fatalf("margin baseline Submit: %v", err)
	}
	state, list = m4ResponsivenessScrollList(2, 16, 0, "margin", 15, 3, true)
	marginStatic, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("margin Plan: %v", err)
	}
	staticMarginCursorY, _ := m4ResponsivenessCursorY(marginStatic)
	frame, _, err = animator.Submit(state, m4ResponsivenessScrollMeta(2, 3, true), list)
	if err != nil {
		t.Fatalf("margin scroll Submit: %v", err)
	}
	marginCursorY, found := m4ResponsivenessCursorY(frame)
	if !found || math.Abs(float64(marginCursorY-staticMarginCursorY)) > 0.001 {
		t.Fatalf("margin cursor moved: got %v, want %v", marginCursorY, staticMarginCursorY)
	}
}

func m4ResponsivenessAssertScreenHalvesNonBlank(t *testing.T, img *image.RGBA, step int) {
	t.Helper()
	halfNonBlank := func(top, bottom int) bool {
		for y := top; y < bottom; y++ {
			for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
				pixel := img.RGBAAt(x, y)
				if pixel.R != 0x10 || pixel.G != 0x10 || pixel.B != 0x18 {
					return true
				}
			}
		}
		return false
	}
	if !halfNonBlank(img.Rect.Min.Y, img.Rect.Min.Y+img.Rect.Dy()/2) ||
		!halfNonBlank(img.Rect.Min.Y+img.Rect.Dy()/2, img.Rect.Max.Y) {
		t.Fatalf("scroll frame %d left one half of the screen blank", step)
	}
}

func m4ResponsivenessDrawnArea(frame renderlist.Frame) int {
	count := 0
	for _, command := range frame.Commands {
		if command.Op == "fill" || command.Op == "glyphs" || command.Op == "cursor" || command.Op == "box" {
			count++
		}
	}
	return count
}

func m4ResponsivenessFillArea(frame renderlist.Frame, clip ui.Rect) float64 {
	rows := metadataFor(frame).rows
	type rect struct{ x0, y0, x1, y1 float64 }
	fills := make([]rect, 0, len(frame.Commands))
	edges := make([]float64, 0, 2*len(frame.Commands)+2)
	for index, command := range frame.Commands {
		if command.Op != "fill" || command.W <= 0 || command.H <= 0 {
			continue
		}
		visible := commandRect(command)
		meta := rowMeta{}
		if index < len(rows) {
			meta = rows[index]
		}
		if meta.clip.W > 0 && meta.clip.H > 0 {
			visible = intersectRects(visible, meta.clip)
		}
		if clip.W > 0 && clip.H > 0 {
			visible = intersectRects(visible, clip)
		}
		if visible.W > 0 && visible.H > 0 {
			fills = append(fills, rect{
				x0: float64(visible.X), y0: float64(visible.Y),
				x1: float64(visible.X + visible.W), y1: float64(visible.Y + visible.H),
			})
			edges = append(edges, float64(visible.Y), float64(visible.Y+visible.H))
		}
	}
	if len(fills) == 0 {
		return 0
	}
	if clip.W > 0 && clip.H > 0 {
		edges = append(edges, float64(clip.Y), float64(clip.Y+clip.H))
	}
	sort.Float64s(edges)
	area := 0.0
	for edgeIndex := 1; edgeIndex < len(edges); edgeIndex++ {
		bottom, top := edges[edgeIndex-1], edges[edgeIndex]
		if top <= bottom {
			continue
		}
		midpoint := (bottom + top) / 2
		intervals := make([][2]float64, 0, len(fills))
		for _, fill := range fills {
			if fill.y0 > midpoint || midpoint >= fill.y1 {
				continue
			}
			x0, x1 := fill.x0, fill.x1
			if clip.W > 0 && clip.H > 0 {
				x0 = max(x0, float64(clip.X))
				x1 = min(x1, float64(clip.X+clip.W))
			}
			if x1 > x0 {
				intervals = append(intervals, [2]float64{x0, x1})
			}
		}
		sort.Slice(intervals, func(left, right int) bool { return intervals[left][0] < intervals[right][0] })
		mergedRight := math.Inf(-1)
		for _, interval := range intervals {
			if interval[1] <= mergedRight {
				continue
			}
			left := max(interval[0], mergedRight)
			area += (top - bottom) * (interval[1] - left)
			mergedRight = interval[1]
		}
	}
	return area
}

func m4ResponsivenessFloatState(seq uint64, startRow int) (*screen.State, *drawlist.List) {
	root := &screen.Grid{Width: 40, Height: 16, Cells: make([][]screen.Cell, 16)}
	for rowIndex := range root.Cells {
		root.Cells[rowIndex] = make([]screen.Cell, 40)
		for colIndex := range root.Cells[rowIndex] {
			root.Cells[rowIndex][colIndex] = screen.Cell{Text: " "}
		}
	}
	floatGrid := &screen.Grid{Width: 30, Height: 14, Cells: make([][]screen.Cell, 14)}
	for rowIndex := range floatGrid.Cells {
		floatGrid.Cells[rowIndex] = make([]screen.Cell, 30)
		for colIndex := range floatGrid.Cells[rowIndex] {
			floatGrid.Cells[rowIndex][colIndex] = screen.Cell{Text: " ", HL: 1}
		}
	}
	for rowIndex := 1; rowIndex < 13; rowIndex++ {
		line := fmt.Sprintf("float_%02d_text", startRow+rowIndex)
		for colIndex, character := range line {
			if colIndex+1 >= 29 {
				break
			}
			floatGrid.Cells[rowIndex][colIndex+1] = screen.Cell{Text: string(character), HL: 1}
		}
	}
	state := &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: root, 2: floatGrid},
		Attrs:         map[int]screen.Attr{0: {}, 1: {"background": int64(0x304060)}},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101018},
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedFloat, ScreenRow: 1, ScreenCol: 5, ZIndex: 50, CompIndex: 10},
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

func m4ResponsivenessFloatMeta(seq uint64, delta float64) *screen.FrameMeta {
	return &screen.FrameMeta{
		Seq:     seq,
		Margins: map[int]screen.ViewportMargins{2: {Top: 1, Bottom: 1, Left: 1, Right: 1}},
		Viewports: map[int]screen.Viewport{
			2: {Grid: 2, TopLine: 0, BotLine: 16, ScrollDelta: delta, HasScrollDelta: true},
		},
	}
}

func TestM4Responsiveness_ScrollHistoryNonOverlappingSpans(t *testing.T) {
	spans := []verticalSpan{{top: 0, bottom: 100}}
	spans = subtractVerticalSpan(spans, verticalSpan{top: 20, bottom: 40})
	if len(spans) != 2 || spans[0] != (verticalSpan{top: 0, bottom: 20}) ||
		spans[1] != (verticalSpan{top: 40, bottom: 100}) {
		t.Fatalf("middle subtraction = %+v, want two disjoint outer spans", spans)
	}
	spans = subtractVerticalSpan(spans, verticalSpan{top: 10, bottom: 50})
	if len(spans) != 2 || spans[0] != (verticalSpan{top: 0, bottom: 10}) ||
		spans[1] != (verticalSpan{top: 50, bottom: 100}) {
		t.Fatalf("overlapping subtraction = %+v, want [0,10) and [50,100)", spans)
	}
	spans = subtractVerticalSpan(spans, verticalSpan{top: 0, bottom: 100})
	if len(spans) != 0 {
		t.Fatalf("full subtraction = %+v, want no uncovered spans", spans)
	}
	spans = subtractVerticalSpan([]verticalSpan{{top: 0, bottom: 10}}, verticalSpan{top: 20, bottom: 30})
	if len(spans) != 1 || spans[0] != (verticalSpan{top: 0, bottom: 10}) {
		t.Fatalf("disjoint subtraction = %+v, want unchanged span", spans)
	}
}

func TestM4Responsiveness_ContinuousScrollBoundsDrawAreaAndShaping(t *testing.T) {
	planner := m4ResponsivenessResolver(t)
	cell := planner.resolver.Cell()
	clock, _ := m4ResponsivenessClock()
	cfg := DefaultAnimationConfig()
	cfg.CursorEnabled = false
	animator := NewAnimator(planner, cfg, clock)
	view := NewView()
	width := int(math.Ceil(40 * float64(cell.Width)))
	height := int(math.Ceil(16 * float64(cell.Height)))
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	state, list := m4ResponsivenessScrollList(1, 16, 0, "base", 0, 0, false)
	baseline, _, err := animator.Submit(state, &screen.FrameMeta{Seq: 1}, list)
	if err != nil {
		t.Fatalf("baseline Submit: %v", err)
	}
	view.SetFrame(baseline)
	tester.Frame()
	if tester.Image() == nil {
		t.Fatal("warm-up image unavailable")
	}
	beforeShaping := view.ShapedRows()
	baselineRows := metadataFor(baseline).rows
	seenCommand, seenMeta := renderlist.Command{}, rowMeta{}
	for index, command := range baseline.Commands {
		if command.Op == "glyphs" && index < len(baselineRows) {
			seenCommand, seenMeta = command, baselineRows[index]
			break
		}
	}
	if seenCommand.Text == "" {
		t.Fatal("baseline frame missing a glyphs command for row-move shaping check")
	}
	beforeMoveCalls := view.impl.cache.coreTextShapeCalls()
	_, _ = view.impl.cache.glyphRun(seenCommand, seenMeta)
	if view.impl.cache.coreTextShapeCalls() != beforeMoveCalls {
		t.Fatal("initial text shaping was not cached before row move")
	}
	movedMeta := seenMeta
	movedMeta.row++
	_, _ = view.impl.cache.glyphRun(seenCommand, movedMeta)
	if view.impl.cache.coreTextShapeCalls() != beforeMoveCalls {
		t.Fatal("moving existing text to a new row called CoreText again")
	}
	beforeScrollCalls := view.impl.cache.coreTextShapeCalls()
	baselineLimit := len(baseline.Commands)*2 + 4
	maxDrawCommands := 0
	maxFillArea := 0.0
	innerClip := layerClipForTest(planner, 16)
	innerArea := float64(innerClip.W * innerClip.H)
	sawLargeOffset := false
	sawFractionalOffset := false
	sawReverseOffset := false
	observe := func(label string, frame renderlist.Frame, step int) {
		view.SetFrame(frame)
		tester.Frame()
		img := tester.Image()
		if img == nil {
			t.Fatalf("%s %d rendered nil image", label, step)
		}
		if label != "float margins" {
			m4ResponsivenessAssertScreenHalvesNonBlank(t, img, step)
		}
		if commands := m4ResponsivenessDrawnArea(frame); commands > maxDrawCommands {
			maxDrawCommands = commands
		}
		if commands := m4ResponsivenessDrawnArea(frame); commands > baselineLimit {
			t.Fatalf("%s %d draw commands = %d, want <= %d", label, step, commands, baselineLimit)
		}
		area := m4ResponsivenessFillArea(frame, innerClip)
		if area > maxFillArea {
			maxFillArea = area
		}
		if area > innerArea*2.001 {
			t.Fatalf("%s %d fill area = %.2f, want <= root fill + 1.0x innerClip %.2f", label, step, area, innerArea*2)
		}
	}
	for step := 0; step < 8; step++ {
		delta := 6.0
		if step == 5 {
			delta = -16.0
		}
		state, list = m4ResponsivenessScrollList(uint64(step+2), 16, step+2, "base", 0, 0, false)
		meta := m4ResponsivenessScrollMeta(uint64(step+2), delta, false)
		frame, active, err := animator.Submit(state, meta, list)
		if err != nil || !active {
			t.Fatalf("scroll Submit %d: active=%v err=%v", step, active, err)
		}
		if _, pixels, ok := animator.ScrollOffset(1); ok {
			if math.Abs(float64(pixels)) >= float64(innerClip.H) {
				sawLargeOffset = true
			}
			if delta < 0 && pixels < 0 {
				sawReverseOffset = true
			}
			if float32(int(pixels)) != pixels {
				sawFractionalOffset = true
			}
		}
		observe("Submit", frame, step)
		clock.Advance(8 * time.Millisecond)
		for inner := 0; inner < 3; inner++ {
			frame, active, err = animator.Step()
			if err != nil || !active {
				t.Fatalf("intermediate Step %d.%d: active=%v err=%v", step, inner, active, err)
			}
			if _, pixels, ok := animator.ScrollOffset(1); ok && float32(int(pixels)) != pixels {
				sawFractionalOffset = true
			}
			observe("Step", frame, step)
			clock.Advance(8 * time.Millisecond)
		}
	}
	if !sawLargeOffset {
		t.Fatal("continuous scroll never reached offsetLines >= innerClip.H in cell rows")
	}
	if !sawFractionalOffset {
		t.Fatal("continuous scroll never produced a fractional offsetPixels frame")
	}
	if !sawReverseOffset {
		t.Fatal("continuous scroll never produced a reverse offset frame")
	}
	if growth := view.impl.cache.coreTextShapeCalls() - beforeScrollCalls; growth == 0 || growth > 32 {
		t.Fatalf("scroll added %d CoreText shape calls, want only new entering text", growth)
	}
	if growth := view.ShapedRows() - beforeShaping; growth > baselineLimit*2 {
		t.Fatalf("scroll rowCache reshaped %d rows, want <= %d", growth, baselineLimit)
	}

	clock, _ = m4ResponsivenessClock()
	animator = NewAnimator(planner, cfg, clock)
	floatState, floatList := m4ResponsivenessFloatState(1, 0)
	if _, _, err := animator.Submit(floatState, m4ResponsivenessFloatMeta(1, 0), floatList); err != nil {
		t.Fatalf("float baseline Submit: %v", err)
	}
	floatState, floatList = m4ResponsivenessFloatState(2, 2)
	floatFrame, active, err := animator.Submit(floatState, m4ResponsivenessFloatMeta(2, 3), floatList)
	if err != nil || !active {
		t.Fatalf("float scroll Submit: active=%v err=%v", active, err)
	}
	observe("float margins", floatFrame, 8)
	foundInnerShift := false
	for _, command := range floatFrame.Commands {
		if command.Op == "glyphs" && strings.HasPrefix(command.Text, "float_") {
			baselineY := float32(2) * cell.Height
			if math.Abs(float64(command.Y-baselineY)) > 0.001 {
				foundInnerShift = true
			}
		}
	}
	if !foundInnerShift {
		t.Fatal("PlacedFloat inner text remained static during margins scroll")
	}
	if maxDrawCommands > baselineLimit {
		t.Fatalf("scroll draw commands = %d exceeded bounded history (baseline %d, limit %d)",
			maxDrawCommands, len(baseline.Commands), baselineLimit)
	}
}

func layerClipForTest(planner *Planner, rows int) ui.Rect {
	return ui.Rect{W: 40 * planner.resolver.Cell().Width, H: float32(rows) * planner.resolver.Cell().Height}
}

func TestM4Responsiveness_SameDirectionScrollKeepsVelocity(t *testing.T) {
	planner := m4ResponsivenessResolver(t)
	clock, _ := m4ResponsivenessClock()
	cfg := DefaultAnimationConfig()
	cfg.CursorEnabled = false
	animator := NewAnimator(planner, cfg, clock)
	state, list := m4ResponsivenessScrollList(1, 16, 0, "momentum", 0, 0, false)
	if _, _, err := animator.Submit(state, &screen.FrameMeta{Seq: 1}, list); err != nil {
		t.Fatalf("baseline Submit: %v", err)
	}
	submit := func(seq uint64, delta float64) {
		state, list = m4ResponsivenessScrollList(seq, 16, 0, "momentum", 0, 0, false)
		meta := m4ResponsivenessScrollMeta(seq, delta, false)
		if _, _, err := animator.Submit(state, meta, list); err != nil {
			t.Fatalf("Submit delta=%v: %v", delta, err)
		}
	}
	submit(2, 2)
	clock.Advance(16 * time.Millisecond)
	if _, _, err := animator.Step(); err != nil {
		t.Fatalf("Step: %v", err)
	}
	velocity := animator.impl.scroll[1].spring.Velocity
	if velocity == 0 {
		t.Fatal("scroll spring has zero velocity after first animated step")
	}
	submit(3, 2)
	if got := animator.impl.scroll[1].spring.Velocity; got != velocity {
		t.Fatalf("same-direction scroll velocity = %v, want preserved %v", got, velocity)
	}
	submit(4, -2)
	if got := animator.impl.scroll[1].spring.Velocity; got != 0 {
		t.Fatalf("reverse scroll velocity = %v, want 0", got)
	}
}

func TestM4Responsiveness_ViewDrawFillsNonCellMargins(t *testing.T) {
	planner := m4ResponsivenessResolver(t)
	cell := planner.resolver.Cell()
	const bg = uint32(0x123456)
	frame := renderlist.Frame{Version: 1, Seq: 1, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 4 * cell.Width, H: 3 * cell.Height, BG: bg},
	}}
	width := int(4*cell.Width + 7)
	height := int(3*cell.Height + 5)
	view := NewView()
	view.SetFrame(frame)
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	img := tester.Image()
	if img == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	want := color(bg)
	for _, point := range [][2]int{{width - 1, height - 1}, {width - 1, 0}, {0, height - 1}, {width / 2, height - 1}} {
		pixel := img.RGBAAt(point[0], point[1])
		got := ui.Color{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A}
		if got != want {
			t.Fatalf("edge pixel %v = %+v, want root background %+v", point, got, want)
		}
	}
}

func TestM4Responsiveness_ViewBackgroundUsesLargestRootFill(t *testing.T) {
	cell := m4ResponsivenessResolver(t).resolver.Cell()
	const rootBG = uint32(0x123456)
	const lineBG = uint32(0x654321)
	const floatBG = uint32(0xabcdef)
	frame := renderlist.Frame{Version: 1, Seq: 1, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: cell.Width, H: cell.Height, BG: lineBG},
		{Op: "fill", X: 0, Y: cell.Height, W: 40 * cell.Width, H: 15 * cell.Height, BG: rootBG},
		{Op: "fill", X: 0, Y: 0, W: 2 * cell.Width, H: 2 * cell.Height, BG: floatBG},
	}}
	metas := []rowMeta{
		{grid: 1, row: 0, cellWidth: cell.Width},
		{grid: 1, row: 1, cellWidth: cell.Width},
		{grid: 2, row: 0, cellWidth: cell.Width},
	}
	if got, ok := viewBackground(frame, metas); !ok || got != rootBG {
		t.Fatalf("viewBackground = (%#x,%v), want largest root fill %#x", got, ok, rootBG)
	}
	width := int(40*cell.Width + 7)
	height := int(16*cell.Height + 5)
	view := NewView()
	view.SetFrame(frame)
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	img := tester.Image()
	if img == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	want := color(rootBG)
	for _, point := range [][2]int{{width - 1, height - 1}, {width - 1, 0}, {0, height - 1}} {
		pixel := img.RGBAAt(point[0], point[1])
		got := ui.Color{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A}
		if got != want {
			t.Fatalf("edge pixel %v = %+v, want largest root background %+v", point, got, want)
		}
	}
}
