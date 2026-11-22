package render

import (
	"math"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

type cursorCorner struct {
	springX   Spring
	springY   Spring
	current   ui.Point
	prevDest  ui.Point
	animLen   float32
	relativeX float32
	relativeY float32
}

type cursorAnimation struct {
	corners    [4]cursorCorner
	dest       ui.Point
	frac       ui.Point
	dim        ui.Point
	cell       text.CellSize
	prevCenter ui.Point
	rawRect    ui.Rect
	shape      uint16
	target     [4]ui.Point
	hasTarget  bool
	visible    bool
	prevMode   string
	prevInMsg  bool
	prevBusy   bool
	prevHeight float32
	hasPrev    bool
	active     bool
	suppressed bool
}

type cursorInput struct {
	state *screen.State
	meta  *screen.FrameMeta
	list  *drawlist.List
	frame renderlist.Frame
	metas []rowMeta
	cfg   AnimationConfig
	cell  text.CellSize
}

type cursorTargetState struct {
	shape   uint16
	rect    ui.Rect
	corners [4]ui.Point
	valid   bool
}

func newCursorAnimation() *cursorAnimation {
	animation := &cursorAnimation{}
	for index := range animation.corners {
		animation.corners[index].prevDest = ui.Point{X: -1000, Y: -1000}
		animation.corners[index].animLen = defaultAnimationConfig().CursorAnimationLength
	}
	animation.setShape(0, ui.Point{})
	return animation
}

func (animation *cursorAnimation) targetState() cursorTargetState {
	return cursorTargetState{
		shape:   animation.shape,
		rect:    animation.rawRect,
		corners: animation.target,
		valid:   animation.hasTarget && animation.visible,
	}
}

func (animation *cursorAnimation) targetChanged(previous cursorTargetState) bool {
	if !animation.hasTarget || !animation.visible {
		return false
	}
	return !previous.valid || previous.shape != animation.shape ||
		previous.rect != animation.rawRect || previous.corners != animation.target
}

func cursorShapeFromMetas(frame renderlist.Frame, metas []rowMeta) uint16 {
	for index := len(frame.Commands) - 1; index >= 0; index-- {
		command := frame.Commands[index]
		if command.Op != "cursor" {
			continue
		}
		if index >= len(metas) {
			return 0
		}
		return metas[index].attrMask & (attrCursorBeam | attrCursorUnderline)
	}
	return 0
}

func cursorCommandIndex(frame renderlist.Frame) int {
	for index := len(frame.Commands) - 1; index >= 0; index-- {
		if frame.Commands[index].Op == "cursor" {
			return index
		}
	}
	return -1
}

func (animation *cursorAnimation) setShape(shape uint16, dimensions ui.Point) {
	animation.shape = shape
	for index := range animation.corners {
		corner := &animation.corners[index]
		standardX, standardY := cursorStandardCorner(index)
		relativeX, relativeY := standardX, standardY
		if shape&attrCursorBeam != 0 && dimensions.X > 0 {
			relativeX = (standardX+0.5)*(min(dimensions.X, 1.5)/dimensions.X) - 0.5
		} else if shape&attrCursorUnderline != 0 && dimensions.Y > 0 {
			relativeY = -((-standardY+0.5)*(min(dimensions.Y, 1.5)/dimensions.Y) - 0.5)
		}
		corner.relativeX = relativeX
		corner.relativeY = relativeY
	}
}

func cursorStandardCorner(index int) (float32, float32) {
	switch index {
	case 0:
		return -0.5, -0.5
	case 1:
		return 0.5, -0.5
	case 2:
		return 0.5, 0.5
	default:
		return -0.5, 0.5
	}
}

func (animation *cursorAnimation) cornerDestination(index int, destination ui.Point) ui.Point {
	corner := &animation.corners[index]
	return ui.Point{
		X: destination.X + corner.relativeX*animation.dim.X,
		Y: destination.Y + corner.relativeY*animation.dim.Y,
	}
}

func (animation *cursorAnimation) targetRect(frame renderlist.Frame, index int) ui.Rect {
	command := frame.Commands[index]
	rect := ui.Rect{X: command.X, Y: command.Y, W: command.W, H: command.H}
	if !cursorFiniteRect(rect) || rect.W <= 0 || rect.H <= 0 {
		return ui.Rect{}
	}
	if animation.shape&attrCursorBeam != 0 {
		rect.W = min(rect.W, 1.5)
	} else if animation.shape&attrCursorUnderline != 0 {
		barHeight := min(rect.H, 1.5)
		rect.Y += rect.H - barHeight
		rect.H = barHeight
	}
	return rect
}

func (animation *cursorAnimation) setTarget(frame renderlist.Frame, index int) bool {
	rect := animation.targetRect(frame, index)
	fullRect := cursorRawRect(frame, index)
	if rect.W <= 0 || rect.H <= 0 {
		animation.hasTarget = false
		animation.visible = false
		animation.target = [4]ui.Point{}
		animation.active = false
		return false
	}
	animation.target = cursorRectQuad(rect)
	animation.hasTarget = true
	animation.visible = true
	animation.rawRect = fullRect
	animation.dim = ui.Point{X: fullRect.W, Y: fullRect.H}
	animation.dest = ui.Point{X: fullRect.X + fullRect.W/2, Y: fullRect.Y + fullRect.H/2}
	animation.frac = ui.Point{
		X: float32(math.Mod(float64(fullRect.X), 1)),
		Y: float32(math.Mod(float64(fullRect.Y), 1)),
	}
	return true
}

func cursorRawRect(frame renderlist.Frame, index int) ui.Rect {
	command := frame.Commands[index]
	return ui.Rect{X: command.X, Y: command.Y, W: command.W, H: command.H}
}

func cursorFiniteRect(rect ui.Rect) bool {
	for _, value := range []float32{rect.X, rect.Y, rect.W, rect.H} {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func cursorRectQuad(rect ui.Rect) [4]ui.Point {
	return [4]ui.Point{
		{X: rect.X, Y: rect.Y},
		{X: rect.X + rect.W, Y: rect.Y},
		{X: rect.X + rect.W, Y: rect.Y + rect.H},
		{X: rect.X, Y: rect.Y + rect.H},
	}
}

func (animation *cursorAnimation) setShapeFromInput(input cursorInput, index int) {
	shape := cursorShapeFromMetas(input.frame, input.metas)
	rect := cursorRawRect(input.frame, index)
	if !cursorFiniteRect(rect) {
		rect = ui.Rect{}
	}
	animation.cell = input.cell
	animation.setShape(shape, ui.Point{X: rect.W, Y: rect.H})
}

func cursorCmdlineMode(mode string) bool {
	switch mode {
	case "cmdline_normal", "cmdline_insert", "cmdline_replace":
		return true
	default:
		return false
	}
}

func cursorMsgGrid(state *screen.State) int {
	if state != nil && state.Msg != nil {
		return state.Msg.Grid
	}
	return 0
}

func cursorInMsgGrid(state *screen.State) bool {
	if state == nil {
		return false
	}
	msgGrid := cursorMsgGrid(state)
	return msgGrid > 0 && state.Cursor.Grid != 1 && state.Cursor.Grid == msgGrid
}

func (animation *cursorAnimation) immediate(input cursorInput, index int, busy bool) bool {
	cfg := input.cfg
	if !cfg.CursorEnabled || cfg.CursorAnimationLength <= 0 {
		return true
	}
	if !animation.hasPrev || !animation.hasTarget {
		return true
	}
	if busy {
		return true
	}
	if animation.prevBusy {
		return true
	}
	state := input.state
	mode := ""
	if state != nil {
		mode = state.Mode
	}
	if !cfg.CursorAnimateInsertMode && (mode == "insert" || mode == "cmdline_insert") {
		return true
	}
	if !cfg.CursorAnimateCommandLine {
		if cursorCmdlineMode(mode) != cursorCmdlineMode(animation.prevMode) {
			return true
		}
		if cursorInMsgGrid(state) != animation.prevInMsg {
			return true
		}
	}
	rawRect := cursorRawRect(input.frame, index)
	if !cursorFiniteRect(rawRect) || rawRect.H <= 0 || animation.prevHeight <= 0 ||
		math.Abs(float64(rawRect.H-animation.prevHeight)) > 0.001 {
		return true
	}
	return false
}

func (animation *cursorAnimation) jump(cfg AnimationConfig) {
	centerDelta := ui.Point{X: animation.dest.X - animation.prevCenter.X, Y: animation.dest.Y - animation.prevCenter.Y}
	if math.IsNaN(float64(centerDelta.X)) || math.IsInf(float64(centerDelta.X), 0) ||
		math.IsNaN(float64(centerDelta.Y)) || math.IsInf(float64(centerDelta.Y), 0) ||
		animation.cell.Width <= 0 || animation.cell.Height <= 0 {
		for index := range animation.corners {
			animation.corners[index].animLen = cfg.CursorAnimationLength
		}
		return
	}
	shortJump := math.Abs(float64(centerDelta.X))/float64(animation.cell.Width) <= 2.001 &&
		math.Abs(float64(centerDelta.Y))/float64(animation.cell.Height) <= 0.001
	if shortJump {
		length := min(cfg.CursorAnimationLength, cfg.CursorShortLength)
		for index := range animation.corners {
			animation.corners[index].animLen = length
		}
		return
	}
	alignments := [4]float32{}
	minAlignment, maxAlignment := float32(math.Inf(1)), float32(math.Inf(-1))
	for index := range animation.corners {
		alignment := animation.alignment(index)
		alignments[index] = alignment
		minAlignment = min(minAlignment, alignment)
		maxAlignment = max(maxAlignment, alignment)
	}
	rangeAlignment := maxAlignment - minAlignment
	trailSize := min(max(cfg.CursorTrailSize, 0), 1)
	leading := cfg.CursorAnimationLength * (1 - trailSize)
	trailing := cfg.CursorAnimationLength
	for index := range animation.corners {
		alignment := float32(1)
		if rangeAlignment > 0 && !math.IsNaN(float64(rangeAlignment)) && !math.IsInf(float64(rangeAlignment), 0) {
			alignment = min(max((alignments[index]-minAlignment)/rangeAlignment, 0), 1)
		}
		animation.corners[index].animLen = trailing + (leading-trailing)*alignment
	}
}

func (animation *cursorAnimation) alignment(index int) float32 {
	cornerDestination := animation.cornerDestination(index, animation.dest)
	travelX := cornerDestination.X - animation.corners[index].prevDest.X
	travelY := cornerDestination.Y - animation.corners[index].prevDest.Y
	travelLength := math.Hypot(float64(travelX), float64(travelY))
	corner := &animation.corners[index]
	cornerLength := math.Hypot(float64(corner.relativeX), float64(corner.relativeY))
	if travelLength <= 1e-6 || cornerLength <= 1e-6 {
		return 0
	}
	return float32((float64(travelX)*float64(corner.relativeX) +
		float64(travelY)*float64(corner.relativeY)) / (travelLength * cornerLength))
}

func (animation *cursorAnimation) relativeCornerAlignment(index int) float32 {
	return animation.alignment(index)
}

func (animation *cursorAnimation) snap() {
	for index := range animation.corners {
		corner := &animation.corners[index]
		corner.springX.Reset()
		corner.springY.Reset()
		corner.current = animation.cornerDestination(index, animation.dest)
		corner.prevDest = corner.current
	}
	animation.prevCenter = animation.dest
	animation.active = false
}

func (animation *cursorAnimation) update(dt float32) bool {
	if !animation.hasTarget || !animation.visible || !animation.active {
		return false
	}
	animating := false
	for index := range animation.corners {
		corner := &animation.corners[index]
		destination := animation.cornerDestination(index, animation.dest)
		if destination != corner.prevDest {
			corner.springX.Position = destination.X - corner.current.X
			corner.springY.Position = destination.Y - corner.current.Y
			corner.prevDest = destination
			animation.prevCenter = animation.dest
		}
		animating = corner.springX.Update(dt, corner.animLen) || animating
		animating = corner.springY.Update(dt, corner.animLen) || animating
		corner.current = ui.Point{
			X: destination.X - corner.springX.Position,
			Y: destination.Y - corner.springY.Position,
		}
	}
	if !animating {
		animation.snap()
	}
	return animation.active
}

func (animation *cursorAnimation) snappedCorner(index int) ui.Point {
	corner := animation.corners[index].current
	return ui.Point{
		X: float32(math.Round(float64(corner.X-animation.frac.X))) + animation.frac.X,
		Y: float32(math.Round(float64(corner.Y-animation.frac.Y))) + animation.frac.Y,
	}
}

func (animation *cursorAnimation) quad() ([4]ui.Point, bool) {
	if !animation.hasTarget || !animation.visible || animation.suppressed {
		return [4]ui.Point{}, false
	}
	if !animation.active {
		return animation.target, true
	}
	var quad [4]ui.Point
	for index := range quad {
		quad[index] = animation.snappedCorner(index)
	}
	return quad, true
}

func (animation *cursorAnimation) remember(input cursorInput, index int, busy bool) {
	state := input.state
	animation.prevMode = ""
	animation.prevInMsg = false
	if state != nil {
		animation.prevMode = state.Mode
		animation.prevInMsg = cursorInMsgGrid(state)
	}
	animation.prevBusy = busy
	rawRect := cursorRawRect(input.frame, index)
	if cursorFiniteRect(rawRect) {
		animation.prevHeight = rawRect.H
	} else {
		animation.prevHeight = 0
	}
	animation.hasPrev = true
}

func cursorAnimationFrame(animation *cursorAnimation, frame renderlist.Frame, metas []rowMeta) (renderlist.Frame, frameMetadata) {
	result := cloneFrame(frame)
	frameMeta := frameMetadata{rows: append([]rowMeta(nil), metas...)}
	index := cursorCommandIndex(result)
	if index < 0 {
		return result, frameMeta
	}
	quad, ok := animation.quad()
	if !ok {
		result.Commands = append(result.Commands[:index], result.Commands[index+1:]...)
		if len(frameMeta.rows) > index {
			frameMeta.rows = append(frameMeta.rows[:index], frameMeta.rows[index+1:]...)
		}
		return result, frameMeta
	}
	if !animation.active {
		return result, frameMeta
	}
	minX, minY, maxX, maxY := quadBounds(quad)
	result.Commands[index].X = minX
	result.Commands[index].Y = minY
	result.Commands[index].W = maxX - minX
	result.Commands[index].H = maxY - minY
	frameMeta.hasCursorQuad = true
	frameMeta.cursorIndex = index
	frameMeta.cursorQuad = quad
	return result, frameMeta
}

func quadBounds(quad [4]ui.Point) (minX, minY, maxX, maxY float32) {
	minX, maxX = quad[0].X, quad[0].X
	minY, maxY = quad[0].Y, quad[0].Y
	for _, point := range quad[1:] {
		minX = min(minX, point.X)
		maxX = max(maxX, point.X)
		minY = min(minY, point.Y)
		maxY = max(maxY, point.Y)
	}
	return minX, minY, maxX, maxY
}
