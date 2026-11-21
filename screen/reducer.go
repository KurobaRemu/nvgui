package screen

import (
	"fmt"
	"sort"
	"sync/atomic"

	"nvgui/protocol"
)

type Reducer struct {
	working *State
	seq     uint64
	latest  atomic.Pointer[State]
}

func NewReducer() *Reducer {
	return &Reducer{working: &State{
		Grids:      map[int]*Grid{},
		Attrs:      map[int]Attr{0: {}},
		Placements: map[int]Placement{},
		Cursor:     Cursor{Grid: 1},
		Mode:       "normal",
	}}
}

func (r *Reducer) Apply(events []protocol.Event) ([]*State, error) {
	committed := make([]*State, 0)
	checkpoint := cloneState(r.working)
	committedLatest := r.latest.Load()
	committedSeq := r.seq
	restore := func() {
		r.working = cloneState(checkpoint)
		r.seq = committedSeq
		r.latest.Store(committedLatest)
	}
	for _, event := range events {
		switch typed := event.(type) {
		case protocol.Flush:
			r.seq++
			r.working.Seq = r.seq
			published := cloneState(r.working)
			r.latest.Store(published)
			checkpoint = cloneState(r.working)
			committedLatest = published
			committedSeq = r.seq
			committed = append(committed, published)
		default:
			if err := applyEvent(r.working, typed); err != nil {
				restore()
				return committed, err
			}
		}
	}
	return committed, nil
}

func (r *Reducer) Latest() *State { return r.latest.Load() }

type Layer struct {
	Grid                 int
	ScreenRow, ScreenCol int
}

func Layers(s *State) []Layer {
	if s == nil {
		return nil
	}
	result := make([]Layer, 0, len(s.Placements)+1)
	if _, visible := s.Grids[1]; visible {
		result = append(result, Layer{Grid: 1})
	}
	winGrids := make([]int, 0, len(s.Placements))
	for gridID, placement := range s.Placements {
		if gridID == 1 || placement.Kind != PlacedWin {
			continue
		}
		if _, visible := s.Grids[gridID]; !visible {
			continue
		}
		winGrids = append(winGrids, gridID)
	}
	sort.Ints(winGrids)
	for _, gridID := range winGrids {
		placement := s.Placements[gridID]
		result = append(result, Layer{Grid: gridID, ScreenRow: placement.Row, ScreenCol: placement.Col})
	}

	type composedLayer struct {
		compIndex int
		gridID    int
		layer     Layer
	}
	composed := make([]composedLayer, 0, len(s.Placements)+1)
	for gridID, placement := range s.Placements {
		if placement.Kind != PlacedFloat {
			continue
		}
		if _, visible := s.Grids[gridID]; !visible {
			continue
		}
		composed = append(composed, composedLayer{
			compIndex: placement.CompIndex,
			gridID:    gridID,
			layer: Layer{
				Grid:      gridID,
				ScreenRow: placement.ScreenRow,
				ScreenCol: placement.ScreenCol,
			},
		})
	}
	if s.Msg != nil {
		if _, placed := s.Placements[s.Msg.Grid]; !placed {
			if _, visible := s.Grids[s.Msg.Grid]; visible {
				composed = append(composed, composedLayer{
					compIndex: s.Msg.CompIndex,
					gridID:    s.Msg.Grid,
					layer:     Layer{Grid: s.Msg.Grid, ScreenRow: s.Msg.Row},
				})
			}
		}
	}
	sort.Slice(composed, func(left, right int) bool {
		if composed[left].compIndex != composed[right].compIndex {
			return composed[left].compIndex < composed[right].compIndex
		}
		return composed[left].gridID < composed[right].gridID
	})
	for _, layer := range composed {
		result = append(result, layer.layer)
	}
	return result
}

func applyEvent(state *State, event protocol.Event) error {
	switch event := event.(type) {
	case protocol.HlAttrDefine:
		return applyHlAttrDefine(state, event)
	case protocol.GridResize:
		return applyGridResize(state, event)
	case protocol.GridLine:
		return applyGridLine(state, event)
	case protocol.GridScroll:
		return applyGridScroll(state, event)
	case protocol.GridClear:
		return applyGridClear(state, event)
	case protocol.GridDestroy:
		delete(state.Grids, event.Grid)
		delete(state.Placements, event.Grid)
		return nil
	case protocol.DefaultColorsSet:
		state.DefaultColors = Colors{FG: event.RGBFG, BG: event.RGBBG, SP: event.RGBSP}
		return nil
	case protocol.ModeChange:
		state.Mode = event.Mode
		state.ModeIdx = event.ModeIdx
		return nil
	case protocol.GridCursorGoto:
		state.Cursor = Cursor{Grid: event.Grid, Row: event.Row, Col: event.Col}
		return nil
	case protocol.WinPos:
		state.Placements[event.Grid] = Placement{
			Kind:   PlacedWin,
			Win:    int64(event.Win),
			Row:    event.StartRow,
			Col:    event.StartCol,
			Width:  event.Width,
			Height: event.Height,
		}
		return nil
	case protocol.WinFloatPos:
		state.Placements[event.Grid] = Placement{
			Kind:         PlacedFloat,
			Win:          int64(event.Win),
			Anchor:       event.Anchor,
			AnchorGrid:   event.AnchorGrid,
			AnchorRow:    event.AnchorRow,
			AnchorCol:    event.AnchorCol,
			MouseEnabled: event.MouseEnabled,
			ZIndex:       event.ZIndex,
			CompIndex:    event.CompIndex,
			ScreenRow:    event.ScreenRow,
			ScreenCol:    event.ScreenCol,
		}
		return nil
	case protocol.WinExternalPos:
		state.Placements[event.Grid] = Placement{Kind: PlacedExternal, Win: int64(event.Win)}
		return nil
	case protocol.WinHide:
		delete(state.Placements, event.Grid)
		return nil
	case protocol.WinClose:
		if placement, found := state.Placements[event.Grid]; found && placement.Kind != PlacedWin {
			delete(state.Placements, event.Grid)
		}
		return nil
	case protocol.MsgSetPos:
		state.Msg = &MsgPlacement{
			Grid:      event.Grid,
			Row:       event.Row,
			Scrolled:  event.Scrolled,
			SepChar:   event.SepChar,
			ZIndex:    event.ZIndex,
			CompIndex: event.CompIndex,
		}
		return nil
	default:
		return nil
	}
}

func applyGridResize(state *State, event protocol.GridResize) error {
	if event.Width < 0 || event.Height < 0 {
		return fmt.Errorf("screen: grid %d has negative size %dx%d", event.Grid, event.Width, event.Height)
	}
	cells := make([][]Cell, event.Height)
	for row := range cells {
		cells[row] = make([]Cell, event.Width)
		for column := range cells[row] {
			cells[row][column] = Cell{Text: " "}
		}
	}
	if event.Grid > 1 {
		if old, found := state.Grids[event.Grid]; found {
			rows := event.Height
			if old.Height < rows {
				rows = old.Height
			}
			columns := event.Width
			if old.Width < columns {
				columns = old.Width
			}
			for row := 0; row < rows; row++ {
				copy(cells[row][:columns], old.Cells[row][:columns])
			}
		}
	}
	if state.Cursor.Grid == event.Grid {
		state.Cursor.Row = 0
		state.Cursor.Col = 0
	}
	state.Grids[event.Grid] = &Grid{Width: event.Width, Height: event.Height, Cells: cells}
	return nil
}

func applyHlAttrDefine(state *State, event protocol.HlAttrDefine) error {
	if event.ID < 0 {
		return fmt.Errorf("screen: highlight id %d is negative", event.ID)
	}
	if event.ID == 0 {
		if len(event.RGB) != 0 {
			return fmt.Errorf("screen: highlight id 0 must define empty attributes")
		}
		state.Attrs[0] = Attr{}
		return nil
	}
	state.Attrs[event.ID] = attrFromEvent(event)
	return nil
}

func applyGridLine(state *State, event protocol.GridLine) error {
	grid, err := gridAt(state, event.Grid)
	if err != nil {
		return err
	}
	if event.Row < 0 || event.Row >= grid.Height {
		return fmt.Errorf("screen: grid %d row %d outside height %d", event.Grid, event.Row, grid.Height)
	}
	column := event.ColStart
	for _, lineCell := range event.Cells {
		highlight := lineCell.HL
		repeat := lineCell.Repeat
		if repeat < 0 {
			return fmt.Errorf("screen: grid %d cell has negative repeat %d", event.Grid, repeat)
		}
		if column < 0 || column > grid.Width || repeat > grid.Width-column {
			return fmt.Errorf("screen: grid %d column %d with repeat %d outside width %d", event.Grid, column, repeat, grid.Width)
		}
		for offset := 0; offset < repeat; offset++ {
			grid.Cells[event.Row][column+offset] = Cell{Text: lineCell.Text, HL: highlight}
		}
		column += repeat
	}
	return nil
}

func applyGridScroll(state *State, event protocol.GridScroll) error {
	grid, err := gridAt(state, event.Grid)
	if err != nil {
		return err
	}
	if event.Cols != 0 {
		return fmt.Errorf("screen: grid %d scroll columns %d is unsupported", event.Grid, event.Cols)
	}
	if event.Top < 0 || event.Bot > grid.Height || event.Top > event.Bot ||
		event.Left < 0 || event.Right > grid.Width || event.Left > event.Right {
		return fmt.Errorf("screen: grid %d scroll region [%d,%d)x[%d,%d) outside %dx%d",
			event.Grid, event.Top, event.Bot, event.Left, event.Right, grid.Width, grid.Height)
	}
	regionHeight := event.Bot - event.Top
	shift := absoluteRows(event.Rows)
	if shift >= uint64(regionHeight) {
		return nil
	}
	step := int(shift)
	if event.Rows > 0 {
		for row := event.Top; row < event.Bot-step; row++ {
			copy(grid.Cells[row][event.Left:event.Right], grid.Cells[row+step][event.Left:event.Right])
		}
	} else {
		for row := event.Bot - 1; row >= event.Top+step; row-- {
			copy(grid.Cells[row][event.Left:event.Right], grid.Cells[row-step][event.Left:event.Right])
		}
	}
	return nil
}

func absoluteRows(rows int) uint64 {
	if rows >= 0 {
		return uint64(rows)
	}
	return uint64(-(rows + 1)) + 1
}

func applyGridClear(state *State, event protocol.GridClear) error {
	grid, err := gridAt(state, event.Grid)
	if err != nil {
		return err
	}
	for row := 0; row < grid.Height; row++ {
		for column := 0; column < grid.Width; column++ {
			grid.Cells[row][column] = Cell{Text: " "}
		}
	}
	return nil
}

func gridAt(state *State, gridID int) (*Grid, error) {
	grid, found := state.Grids[gridID]
	if !found || grid == nil {
		return nil, fmt.Errorf("screen: grid %d does not exist", gridID)
	}
	return grid, nil
}

func cloneState(source *State) *State {
	clone := *source
	clone.Grids = make(map[int]*Grid, len(source.Grids))
	for gridID, grid := range source.Grids {
		if grid == nil {
			clone.Grids[gridID] = nil
			continue
		}
		gridClone := &Grid{Width: grid.Width, Height: grid.Height, Cells: make([][]Cell, len(grid.Cells))}
		for row := range grid.Cells {
			gridClone.Cells[row] = append([]Cell(nil), grid.Cells[row]...)
		}
		clone.Grids[gridID] = gridClone
	}
	clone.Attrs = make(map[int]Attr, len(source.Attrs))
	for id, attr := range source.Attrs {
		clone.Attrs[id] = cloneAttr(attr)
	}
	clone.Placements = make(map[int]Placement, len(source.Placements))
	for gridID, placement := range source.Placements {
		clone.Placements[gridID] = placement
	}
	if source.Msg != nil {
		msg := *source.Msg
		clone.Msg = &msg
	}
	return &clone
}

func cloneAttr(source Attr) Attr {
	clone := make(Attr, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func attrFromEvent(event protocol.HlAttrDefine) Attr {
	attribute := make(Attr, len(event.RGB))
	for key, value := range event.RGB {
		attribute[key] = value
	}
	return attribute
}
