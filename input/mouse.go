package input

import (
	"math"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// discreteWheelDIPs is the DIP scaling factor that mygo's macOS backend applies
// to discrete (non-precise) mouse wheel line deltas (internal/darwin/surface.go:853).
const (
	discreteWheelDIPs      = float32(40.0)
	maxScrollCellsPerEvent = 10000.0
)

func clampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloorInt32(v float64) int32 {
	floored := math.Floor(v)
	if floored < math.MinInt32 {
		return math.MinInt32
	}
	if floored > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(floored)
}

type mouseHandlerImpl struct {
	queue *Queue

	opMu sync.Mutex
	mu   sync.Mutex

	enabled        bool
	mouseMoveEvent bool
	cell           CellSize
	bounds         GridBounds

	dragging   bool
	dragButton string
	hasMoved   bool
	hasGridPos bool
	gridRow    int
	gridCol    int
	scrollPosX float32
	scrollPosY float32
}

func newMouseHandler(q *Queue) *MouseHandler {
	return &MouseHandler{
		impl: &mouseHandlerImpl{
			queue:   q,
			enabled: true,
		},
	}
}

func mouseButtonName(button int) (string, bool) {
	switch button {
	case 0:
		return "left", true
	case 1:
		return "right", true
	case 2:
		return "middle", true
	case 3:
		return "x1", true
	case 4:
		return "x2", true
	default:
		return "", false
	}
}

// FormatMouseModifiers formats mygo modifiers using Neovim's nvim_input_mouse
// single-letter modifier convention ("S", "C", "A", "D", or combinations).
func FormatMouseModifiers(mods ui.Modifiers) string {
	var builder strings.Builder
	if mods&ui.Shift != 0 {
		builder.WriteByte('S')
	}
	if mods&ui.Ctrl != 0 {
		builder.WriteByte('C')
	}
	if mods&ui.Alt != 0 {
		builder.WriteByte('A')
	}
	if mods&ui.Super != 0 {
		builder.WriteByte('D')
	}
	return builder.String()
}

func scrollDirectionAndCount(previous, current int32, forward, backward string) (string, int, bool) {
	if current > previous {
		return forward, int(int64(current) - int64(previous)), true
	}
	if current < previous {
		return backward, int(int64(previous) - int64(current)), true
	}
	return "", 0, false
}

func mouseHandlerSetEnabled(handler *MouseHandler, enabled bool) {
	if handler == nil || handler.impl == nil {
		return
	}
	impl := handler.impl
	impl.mu.Lock()
	impl.enabled = enabled
	if !enabled {
		impl.dragging = false
		impl.dragButton = ""
		impl.hasMoved = false
	}
	impl.mu.Unlock()
}

func mouseHandlerEnabled(handler *MouseHandler) bool {
	if handler == nil || handler.impl == nil {
		return false
	}
	handler.impl.mu.Lock()
	defer handler.impl.mu.Unlock()
	return handler.impl.enabled
}

func mouseHandlerSetMouseMoveEvent(handler *MouseHandler, enabled bool) {
	if handler == nil || handler.impl == nil {
		return
	}
	handler.impl.mu.Lock()
	handler.impl.mouseMoveEvent = enabled
	handler.impl.mu.Unlock()
}

func mouseHandlerMouseMoveEvent(handler *MouseHandler) bool {
	if handler == nil || handler.impl == nil {
		return false
	}
	handler.impl.mu.Lock()
	defer handler.impl.mu.Unlock()
	return handler.impl.mouseMoveEvent
}

func mouseHandlerSetGeometry(handler *MouseHandler, cell CellSize, bounds GridBounds) {
	if handler == nil || handler.impl == nil {
		return
	}
	handler.impl.mu.Lock()
	handler.impl.cell = cell
	handler.impl.bounds = bounds
	handler.impl.mu.Unlock()
}

func mouseHandlerDragging(handler *MouseHandler) bool {
	if handler == nil || handler.impl == nil {
		return false
	}
	handler.impl.mu.Lock()
	defer handler.impl.mu.Unlock()
	return handler.impl.dragging
}

func mouseHandlerGridPos(handler *MouseHandler) (int, int) {
	if handler == nil || handler.impl == nil {
		return 0, 0
	}
	handler.impl.mu.Lock()
	defer handler.impl.mu.Unlock()
	return handler.impl.gridRow, handler.impl.gridCol
}

func (impl *mouseHandlerImpl) pixelToGridLocked(x, y float32) (int, int, bool) {
	if impl.cell.Width <= 0 || impl.cell.Height <= 0 {
		return 0, 0, false
	}
	if impl.bounds.Cols <= 0 || impl.bounds.Rows <= 0 {
		return 0, 0, false
	}
	xf, yf := float64(x), float64(y)
	if math.IsNaN(xf) || math.IsInf(xf, 0) || math.IsNaN(yf) || math.IsInf(yf, 0) {
		return 0, 0, false
	}
	colF := clampFloat64(math.Floor(xf/float64(impl.cell.Width)), 0, float64(impl.bounds.Cols-1))
	rowF := clampFloat64(math.Floor(yf/float64(impl.cell.Height)), 0, float64(impl.bounds.Rows-1))
	return int(rowF), int(colF), true
}

func mouseHandlerHandle(handler *MouseHandler, ev ui.InputEvent) bool {
	if handler == nil || handler.impl == nil || handler.impl.queue == nil {
		return false
	}
	impl := handler.impl
	impl.opMu.Lock()
	defer impl.opMu.Unlock()

	switch ev.Kind {
	case ui.InputPointerDown:
		return impl.handlePointerDown(ev)
	case ui.InputPointerUp:
		return impl.handlePointerUp(ev)
	case ui.InputPointerMove:
		return impl.handlePointerMove(ev)
	case ui.InputScroll:
		return impl.handleScroll(ev)
	default:
		return false
	}
}

func (impl *mouseHandlerImpl) handlePointerDown(ev ui.InputEvent) bool {
	button, ok := mouseButtonName(ev.Button)
	if !ok {
		return false
	}
	impl.mu.Lock()
	if !impl.enabled {
		impl.mu.Unlock()
		return false
	}
	row, col, ok := impl.pixelToGridLocked(ev.X, ev.Y)
	if !ok {
		impl.mu.Unlock()
		return false
	}
	impl.gridRow = row
	impl.gridCol = col
	impl.hasGridPos = true
	impl.dragging = true
	impl.dragButton = button
	impl.hasMoved = false
	impl.mu.Unlock()

	cmd := MouseCommand{
		Button:   button,
		Action:   "press",
		Modifier: FormatMouseModifiers(ev.Mods),
		Grid:     0,
		Row:      row,
		Col:      col,
		Count:    1,
		Mods:     ev.Mods,
		Time:     ev.Time,
	}
	if err := impl.queue.EnqueueMouse(cmd); err != nil {
		impl.mu.Lock()
		impl.dragging = false
		impl.dragButton = ""
		impl.hasMoved = false
		impl.mu.Unlock()
		return false
	}
	return true
}

func (impl *mouseHandlerImpl) handlePointerUp(ev ui.InputEvent) bool {
	impl.mu.Lock()
	if ev.Canceled {
		if !impl.dragging {
			impl.mu.Unlock()
			return false
		}
		button := impl.dragButton
		row, col := impl.gridRow, impl.gridCol
		enabled := impl.enabled
		impl.dragging = false
		impl.dragButton = ""
		impl.hasMoved = false
		impl.mu.Unlock()
		if !enabled {
			return false
		}
		cmd := MouseCommand{
			Button:   button,
			Action:   "release",
			Modifier: FormatMouseModifiers(ev.Mods),
			Grid:     0,
			Row:      row,
			Col:      col,
			Count:    1,
			Mods:     ev.Mods,
			Canceled: true,
			Time:     ev.Time,
		}
		return impl.queue.EnqueueMouse(cmd) == nil
	}

	button, ok := mouseButtonName(ev.Button)
	if !ok {
		impl.mu.Unlock()
		return false
	}
	if !impl.enabled {
		if impl.dragging && impl.dragButton == button {
			impl.dragging = false
			impl.dragButton = ""
			impl.hasMoved = false
		}
		impl.mu.Unlock()
		return false
	}

	var row, col int
	if impl.hasMoved {
		row, col = impl.gridRow, impl.gridCol
	} else {
		var valid bool
		row, col, valid = impl.pixelToGridLocked(ev.X, ev.Y)
		if !valid {
			if impl.hasGridPos {
				row, col = impl.gridRow, impl.gridCol
			} else {
				impl.dragging = false
				impl.dragButton = ""
				impl.hasMoved = false
				impl.mu.Unlock()
				return false
			}
		} else {
			impl.gridRow = row
			impl.gridCol = col
			impl.hasGridPos = true
		}
	}
	impl.dragging = false
	impl.dragButton = ""
	impl.hasMoved = false
	impl.mu.Unlock()

	cmd := MouseCommand{
		Button:   button,
		Action:   "release",
		Modifier: FormatMouseModifiers(ev.Mods),
		Grid:     0,
		Row:      row,
		Col:      col,
		Count:    1,
		Mods:     ev.Mods,
		Time:     ev.Time,
	}
	return impl.queue.EnqueueMouse(cmd) == nil
}

func (impl *mouseHandlerImpl) handlePointerMove(ev ui.InputEvent) bool {
	impl.mu.Lock()
	if !impl.enabled {
		impl.mu.Unlock()
		return false
	}
	row, col, ok := impl.pixelToGridLocked(ev.X, ev.Y)
	if !ok {
		impl.mu.Unlock()
		return false
	}
	prevRow, prevCol := impl.gridRow, impl.gridCol
	hadPos := impl.hasGridPos
	impl.gridRow = row
	impl.gridCol = col
	impl.hasGridPos = true

	moved := !hadPos || row != prevRow || col != prevCol
	dragging := impl.dragging
	dragButton := impl.dragButton
	mouseMoveEvent := impl.mouseMoveEvent
	if dragging && moved {
		impl.hasMoved = true
	}
	impl.mu.Unlock()

	if !moved {
		return dragging || mouseMoveEvent
	}
	if dragging {
		cmd := MouseCommand{
			Button:   dragButton,
			Action:   "drag",
			Modifier: FormatMouseModifiers(ev.Mods),
			Grid:     0,
			Row:      row,
			Col:      col,
			Count:    1,
			Mods:     ev.Mods,
			Time:     ev.Time,
		}
		return impl.queue.EnqueueMouse(cmd) == nil
	}
	if mouseMoveEvent {
		cmd := MouseCommand{
			Button:   "move",
			Action:   "",
			Modifier: FormatMouseModifiers(ev.Mods),
			Grid:     0,
			Row:      row,
			Col:      col,
			Count:    1,
			Mods:     ev.Mods,
			Time:     ev.Time,
		}
		return impl.queue.EnqueueMouse(cmd) == nil
	}
	return false
}

func (impl *mouseHandlerImpl) handleScroll(ev ui.InputEvent) bool {
	dx, dy := float64(ev.DX), float64(ev.DY)
	if math.IsNaN(dx) || math.IsInf(dx, 0) || math.IsNaN(dy) || math.IsInf(dy, 0) {
		return false
	}

	impl.mu.Lock()
	if !impl.enabled {
		impl.mu.Unlock()
		return false
	}
	if row, col, ok := impl.pixelToGridLocked(ev.X, ev.Y); ok {
		impl.gridRow = row
		impl.gridCol = col
		impl.hasGridPos = true
	} else if !impl.hasGridPos {
		impl.mu.Unlock()
		return false
	}

	var deltaCellsX, deltaCellsY float64
	if ev.Precise {
		if impl.cell.Width <= 0 || impl.cell.Height <= 0 {
			impl.mu.Unlock()
			return false
		}
		deltaCellsX = dx / float64(impl.cell.Width)
		deltaCellsY = dy / float64(impl.cell.Height)
	} else {
		deltaCellsX = dx / float64(discreteWheelDIPs)
		deltaCellsY = dy / float64(discreteWheelDIPs)
	}
	if math.IsNaN(deltaCellsX) || math.IsInf(deltaCellsX, 0) || math.IsNaN(deltaCellsY) || math.IsInf(deltaCellsY, 0) {
		impl.mu.Unlock()
		return false
	}
	deltaCellsX = clampFloat64(deltaCellsX, -maxScrollCellsPerEvent, maxScrollCellsPerEvent)
	deltaCellsY = clampFloat64(deltaCellsY, -maxScrollCellsPerEvent, maxScrollCellsPerEvent)

	// mygo negates AppKit's scrollingDeltaX/Y in internal/darwin/surface.go:855
	// so positive DY moves the viewport down the content (Neovim wheel "down")
	// and positive DX moves the viewport right (Neovim wheel "right").
	prevY := clampFloorInt32(float64(impl.scrollPosY))
	nextPosY := float64(impl.scrollPosY) + deltaCellsY
	newY := clampFloorInt32(nextPosY)
	impl.scrollPosY = float32(nextPosY - float64(newY))

	prevX := clampFloorInt32(float64(impl.scrollPosX))
	nextPosX := float64(impl.scrollPosX) + deltaCellsX
	newX := clampFloorInt32(nextPosX)
	impl.scrollPosX = float32(nextPosX - float64(newX))

	row, col := impl.gridRow, impl.gridCol
	impl.mu.Unlock()

	mod := FormatMouseModifiers(ev.Mods)
	if dir, count, ok := scrollDirectionAndCount(prevY, newY, "down", "up"); ok {
		cmd := MouseCommand{
			Button:   "wheel",
			Action:   dir,
			Modifier: mod,
			Grid:     0,
			Row:      row,
			Col:      col,
			Count:    count,
			Mods:     ev.Mods,
			Time:     ev.Time,
		}
		if err := impl.queue.EnqueueMouse(cmd); err != nil {
			return false
		}
	}
	if dir, count, ok := scrollDirectionAndCount(prevX, newX, "right", "left"); ok {
		cmd := MouseCommand{
			Button:   "wheel",
			Action:   dir,
			Modifier: mod,
			Grid:     0,
			Row:      row,
			Col:      col,
			Count:    count,
			Mods:     ev.Mods,
			Time:     ev.Time,
		}
		if err := impl.queue.EnqueueMouse(cmd); err != nil {
			return false
		}
	}
	return true
}
