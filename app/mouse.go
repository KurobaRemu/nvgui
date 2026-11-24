package app

import (
	"github.com/egoist/mygo/ui"

	"nvgui/input"
	"nvgui/screen"
)

func (impl *shellImpl) handleInputEvent(ev ui.InputEvent) bool {
	if impl == nil {
		return false
	}
	switch ev.Kind {
	case ui.InputPointerDown, ui.InputPointerUp, ui.InputPointerMove, ui.InputScroll:
		return impl.handleMouseInput(ev)
	default:
		return impl.handler.Handle(ev)
	}
}

func (impl *shellImpl) handleMouseInput(ev ui.InputEvent) bool {
	if impl == nil || impl.mouse == nil || impl.checkOpen() != nil {
		return false
	}
	var state *screen.State
	var meta *screen.FrameMeta
	if impl.session != nil {
		state, meta = impl.session.LatestSnapshot()
	}
	impl.syncMouseState(state, meta)
	return impl.mouse.Handle(ev)
}

func (impl *shellImpl) syncMouseState(state *screen.State, meta *screen.FrameMeta) {
	if impl == nil || impl.mouse == nil {
		return
	}
	cell := impl.cell()
	var rootBounds input.GridBounds
	hasRootBounds := false
	if state != nil {
		if root := state.Grids[1]; root != nil && root.Width > 0 && root.Height > 0 {
			rootBounds = input.GridBounds{Cols: root.Width, Rows: root.Height}
			hasRootBounds = true
		}
	}
	var fallbackBounds input.GridBounds
	if !hasRootBounds {
		fallbackBounds = impl.fallbackMouseGridBounds()
	}

	impl.mouseSyncMu.Lock()
	defer impl.mouseSyncMu.Unlock()

	switch {
	case state == nil:
		if impl.mouseBoundsSeq == 0 {
			impl.mouseBounds = fallbackBounds
		}
	case state.Seq >= impl.mouseBoundsSeq:
		if hasRootBounds {
			impl.mouseBounds = rootBounds
		} else if impl.mouseBounds.Cols <= 0 || impl.mouseBounds.Rows <= 0 {
			impl.mouseBounds = fallbackBounds
		}
		impl.mouseBoundsSeq = state.Seq
	}
	impl.mouse.SetGeometry(input.CellSize{Width: cell.Width, Height: cell.Height}, impl.mouseBounds)

	if meta != nil && meta.Seq > impl.mouseMetaSeq {
		impl.mouse.SetEnabled(meta.MouseEnabled)
		impl.mouse.SetMouseMoveEvent(meta.MouseMoveEvent)
		impl.mouseMetaSeq = meta.Seq
	}
}

func (impl *shellImpl) fallbackMouseGridBounds() input.GridBounds {
	impl.mu.Lock()
	cols, rows := impl.lastColumns, impl.lastRows
	reqCols, reqRows := impl.req.Width, impl.req.Height
	impl.mu.Unlock()
	if cols > 0 && rows > 0 {
		return input.GridBounds{Cols: cols, Rows: rows}
	}
	if reqCols <= 0 {
		reqCols = defaultColumns
	}
	if reqRows <= 0 {
		reqRows = defaultRows
	}
	return input.GridBounds{Cols: reqCols, Rows: reqRows}
}
