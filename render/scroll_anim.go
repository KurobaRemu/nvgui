package render

import (
	"math"
	"slices"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

const maxScrollSnapshots = 8

type scrollSnapshot struct {
	commands   []renderlist.Command
	metas      []rowMeta
	lineOffset float32
}

type gridScrollState struct {
	spring    Spring
	snapshots []scrollSnapshot
	width     int
	height    int
	margins   screen.ViewportMargins
	viewport  screen.Viewport
}

type scrollLayer struct {
	grid         int
	width        int
	height       int
	screenRow    int
	screenCol    int
	innerClip    ui.Rect
	margins      screen.ViewportMargins
	snapshots    []scrollSnapshot
	offsetPixels float32
}

func validMargins(margins screen.ViewportMargins, width, height int) screen.ViewportMargins {
	if margins.Top < 0 || margins.Bottom < 0 || margins.Left < 0 || margins.Right < 0 ||
		margins.Top+margins.Bottom >= height || margins.Left+margins.Right >= width {
		return screen.ViewportMargins{}
	}
	return margins
}

func inferredFloatMargins(state *screen.State, layer drawlist.Layer) screen.ViewportMargins {
	if state == nil || state.Placements[layer.Grid].Kind != screen.PlacedFloat || layer.Height < 3 {
		return screen.ViewportMargins{}
	}
	top, bottom, left, right := false, false, false, false
	for _, item := range layer.Items {
		if item.Kind != "box" {
			continue
		}
		if item.Row == 0 {
			top = true
		}
		if item.Row == layer.Height-1 {
			bottom = true
		}
		if layer.Width >= 3 && item.Col == 0 {
			left = true
		}
		if layer.Width >= 3 && item.Col == layer.Width-1 {
			right = true
		}
	}
	margins := screen.ViewportMargins{}
	if top && bottom {
		margins.Top, margins.Bottom = 1, 1
	}
	if left && right {
		margins.Left, margins.Right = 1, 1
	}
	return validMargins(margins, layer.Width, layer.Height)
}

func scrollMarginsFor(state *screen.State, meta *screen.FrameMeta, layer drawlist.Layer) screen.ViewportMargins {
	if meta != nil {
		if margins, ok := meta.Margins[layer.Grid]; ok {
			return validMargins(margins, layer.Width, layer.Height)
		}
	}
	return inferredFloatMargins(state, layer)
}

func intersectRects(left, right ui.Rect) ui.Rect {
	x0, y0 := max(left.X, right.X), max(left.Y, right.Y)
	x1, y1 := min(left.X+left.W, right.X+right.W), min(left.Y+left.H, right.Y+right.H)
	if x1 <= x0 || y1 <= y0 {
		return ui.Rect{}
	}
	return ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func scrollInnerClip(layer drawlist.Layer, margins screen.ViewportMargins, cellWidth, cellHeight float32, layerClip ui.Rect) ui.Rect {
	inner := ui.Rect{
		X: float32(layer.ScreenCol+margins.Left) * cellWidth,
		Y: float32(layer.ScreenRow+margins.Top) * cellHeight,
		W: float32(layer.Width-margins.Left-margins.Right) * cellWidth,
		H: float32(layer.Height-margins.Top-margins.Bottom) * cellHeight,
	}
	if layerClip.W > 0 && layerClip.H > 0 {
		return intersectRects(inner, layerClip)
	}
	return inner
}

func scrollCommandCellRange(command renderlist.Command, layer scrollLayer, cellWidth, cellHeight float32) (int, int, int, int) {
	if cellWidth <= 0 || cellHeight <= 0 {
		return 0, 0, 0, 0
	}
	cmdRow := int(math.Round(float64((command.Y - float32(layer.screenRow)*cellHeight) / cellHeight)))
	cmdCol := int(math.Round(float64((command.X - float32(layer.screenCol)*cellWidth) / cellWidth)))
	cmdRows := int(math.Round(float64(command.H / cellHeight)))
	cmdCols := int(math.Round(float64(command.W / cellWidth)))
	if cmdRows < 1 {
		cmdRows = 1
	}
	if cmdCols < 1 {
		cmdCols = 1
	}
	return cmdRow, cmdRow + cmdRows, cmdCol, cmdCol + cmdCols
}

func marginsKeepCommandStatic(command renderlist.Command, layer scrollLayer, cellWidth, cellHeight float32) bool {
	cmdRow, cmdEndRow, cmdCol, cmdEndCol := scrollCommandCellRange(command, layer, cellWidth, cellHeight)
	return cmdRow < layer.margins.Top || cmdEndRow > layer.height-layer.margins.Bottom ||
		cmdCol < layer.margins.Left || cmdEndCol > layer.width-layer.margins.Right ||
		command.Op == "cursor"
}

func rectsOverlap(left, right ui.Rect) bool {
	return left.X < right.X+right.W && right.X < left.X+left.W &&
		left.Y < right.Y+right.H && right.Y < left.Y+left.H
}

func scrollInnerBackground(frame renderlist.Frame, metas []rowMeta, layer scrollLayer) (uint32, bool) {
	commandRect := ui.Rect{X: layer.innerClip.X, Y: layer.innerClip.Y, W: layer.innerClip.W, H: layer.innerClip.H}
	for index, command := range frame.Commands {
		if index >= len(metas) || int(metas[index].grid) != layer.grid || command.Op != "fill" {
			continue
		}
		fillRect := ui.Rect{X: command.X, Y: command.Y, W: command.W, H: command.H}
		if rectsOverlap(fillRect, commandRect) {
			return command.BG, true
		}
	}
	return 0, false
}

func scrollLayerFor(state *screen.State, meta *screen.FrameMeta, drawLayer drawlist.Layer, gridState *gridScrollState, cellWidth, cellHeight float32) (scrollLayer, bool) {
	if gridState == nil || !gridState.spring.Active() {
		return scrollLayer{}, false
	}
	margins := scrollMarginsFor(state, meta, drawLayer)
	if margins != gridState.margins || gridState.width != drawLayer.Width || gridState.height != drawLayer.Height {
		return scrollLayer{}, false
	}
	layerClip := commandClip(drawLayer.Clip, text.CellSize{Width: cellWidth, Height: cellHeight}, true, true)
	innerClip := scrollInnerClip(drawLayer, margins, cellWidth, cellHeight, layerClip)
	if innerClip.W <= 0 || innerClip.H <= 0 {
		return scrollLayer{}, false
	}
	return scrollLayer{
		grid:         drawLayer.Grid,
		width:        drawLayer.Width,
		height:       drawLayer.Height,
		screenRow:    drawLayer.ScreenRow,
		screenCol:    drawLayer.ScreenCol,
		innerClip:    innerClip,
		margins:      margins,
		snapshots:    gridState.snapshots,
		offsetPixels: gridState.spring.Position * cellHeight,
	}, true
}

func scrollPreviousInner(frame renderlist.Frame, metas []rowMeta, drawLayer drawlist.Layer, gridID int, margins screen.ViewportMargins, cell text.CellSize) ([]renderlist.Command, []rowMeta) {
	if drawLayer.Width <= 0 || drawLayer.Height <= 0 || cell.Width <= 0 || cell.Height <= 0 {
		return nil, nil
	}
	layerClip := commandClip(drawLayer.Clip, cell, true, true)
	context := scrollLayer{
		grid:      gridID,
		width:     drawLayer.Width,
		height:    drawLayer.Height,
		screenRow: drawLayer.ScreenRow,
		screenCol: drawLayer.ScreenCol,
		innerClip: scrollInnerClip(drawLayer, margins, cell.Width, cell.Height, layerClip),
		margins:   margins,
	}
	commands := make([]renderlist.Command, 0, len(frame.Commands))
	outMetas := make([]rowMeta, 0, len(frame.Commands))
	for index, command := range frame.Commands {
		commandMeta := rowMeta{}
		if index < len(metas) {
			commandMeta = metas[index]
		}
		if int(commandMeta.grid) != gridID {
			continue
		}
		if command.Op == "fill" {
			clipped := intersectRects(commandRect(command), context.innerClip)
			if clipped.W <= 0 || clipped.H <= 0 {
				continue
			}
			snapshot := command
			snapshot.X, snapshot.Y, snapshot.W, snapshot.H = clipped.X, clipped.Y, clipped.W, clipped.H
			commands = append(commands, snapshot)
			outMetas = append(outMetas, commandMeta)
			continue
		}
		if marginsKeepCommandStatic(command, context, cell.Width, cell.Height) {
			continue
		}
		commands = append(commands, command)
		outMetas = append(outMetas, commandMeta)
	}
	return commands, outMetas
}

func scrollHistoryCommands(layer scrollLayer, cellWidth, cellHeight float32) ([]renderlist.Command, []rowMeta) {
	if len(layer.snapshots) == 0 {
		return nil, nil
	}
	commands := make([]renderlist.Command, 0)
	metas := make([]rowMeta, 0)
	for _, snapshot := range layer.snapshots {
		snapOffsetPixels := layer.offsetPixels + snapshot.lineOffset*cellHeight
		if snapOffsetPixels <= -layer.innerClip.H || snapOffsetPixels >= layer.innerClip.H {
			continue
		}
		for index, command := range snapshot.commands {
			meta := rowMeta{}
			if index < len(snapshot.metas) {
				meta = snapshot.metas[index]
			}
			if int(meta.grid) != layer.grid {
				continue
			}
			shifted := command
			shifted.Y += snapOffsetPixels
			if shifted.Y+shifted.H <= layer.innerClip.Y || shifted.Y >= layer.innerClip.Y+layer.innerClip.H {
				continue
			}
			meta.clip = layer.innerClip
			commands = append(commands, shifted)
			metas = append(metas, meta)
		}
	}
	return commands, metas
}

func scrollVisibleCommands(commands []renderlist.Command, metas []rowMeta, layer scrollLayer, offsetPixels float32) ([]renderlist.Command, []rowMeta) {
	result := make([]renderlist.Command, 0, len(commands))
	resultMetas := make([]rowMeta, 0, len(metas))
	for index, command := range commands {
		meta := rowMeta{}
		if index < len(metas) {
			meta = metas[index]
		}
		shifted := command
		shifted.Y += offsetPixels
		if shifted.Y+shifted.H <= layer.innerClip.Y || shifted.Y >= layer.innerClip.Y+layer.innerClip.H {
			continue
		}
		meta.clip = layer.innerClip
		result = append(result, shifted)
		resultMetas = append(resultMetas, meta)
	}
	return result, resultMetas
}

func scrollSegmentCommands(gridID int, commands []renderlist.Command, metas []rowMeta, layer scrollLayer, cellWidth, cellHeight float32, background uint32, backgroundPresent bool) ([]renderlist.Command, []rowMeta) {
	staticFills := make([]renderlist.Command, 0)
	staticFillsMetas := make([]rowMeta, 0)
	staticForeground := make([]renderlist.Command, 0)
	staticForegroundMetas := make([]rowMeta, 0)
	cursorCommands := make([]renderlist.Command, 0)
	cursorMetas := make([]rowMeta, 0)
	innerFills := make([]renderlist.Command, 0)
	innerFillsMetas := make([]rowMeta, 0)
	innerForeground := make([]renderlist.Command, 0)
	innerForegroundMetas := make([]rowMeta, 0)
	for index, command := range commands {
		meta := rowMeta{}
		if index < len(metas) {
			meta = metas[index]
		}
		if command.Op == "cursor" {
			cursorCommands = append(cursorCommands, command)
			cursorMetas = append(cursorMetas, meta)
			continue
		}
		static := layer.margins != (screen.ViewportMargins{}) &&
			marginsKeepCommandStatic(command, layer, cellWidth, cellHeight)
		if static {
			if command.Op == "fill" {
				staticFills = append(staticFills, command)
				staticFillsMetas = append(staticFillsMetas, meta)
			} else {
				staticForeground = append(staticForeground, command)
				staticForegroundMetas = append(staticForegroundMetas, meta)
			}
		}
		if command.Op == "fill" {
			clipped := intersectRects(commandRect(command), layer.innerClip)
			if clipped.W > 0 && clipped.H > 0 {
				innerFill := command
				innerFill.X, innerFill.Y, innerFill.W, innerFill.H = clipped.X, clipped.Y, clipped.W, clipped.H
				innerFills = append(innerFills, innerFill)
				innerFillsMetas = append(innerFillsMetas, meta)
			}
			continue
		}
		if !static {
			innerForeground = append(innerForeground, command)
			innerForegroundMetas = append(innerForegroundMetas, meta)
		}
	}

	result := make([]renderlist.Command, 0, len(commands)+len(layer.snapshots)*4+1)
	resultMetas := make([]rowMeta, 0, len(commands)+len(layer.snapshots)*4+1)
	result = append(result, staticFills...)
	resultMetas = append(resultMetas, staticFillsMetas...)
	if backgroundPresent {
		result = append(result, renderlist.Command{
			Op: "fill",
			X:  layer.innerClip.X,
			Y:  layer.innerClip.Y,
			W:  layer.innerClip.W,
			H:  layer.innerClip.H,
			BG: background,
		})
		resultMetas = append(resultMetas, rowMeta{grid: int32(gridID), cellWidth: cellWidth})
	}
	history, historyMetas := scrollHistoryCommands(layer, cellWidth, cellHeight)
	result = append(result, history...)
	resultMetas = append(resultMetas, historyMetas...)
	currentFills, currentFillsMetas := scrollVisibleCommands(innerFills, innerFillsMetas, layer, layer.offsetPixels)
	result = append(result, currentFills...)
	resultMetas = append(resultMetas, currentFillsMetas...)
	currentForeground, currentForegroundMetas := scrollVisibleCommands(innerForeground, innerForegroundMetas, layer, layer.offsetPixels)
	result = append(result, currentForeground...)
	resultMetas = append(resultMetas, currentForegroundMetas...)
	result = append(result, staticForeground...)
	resultMetas = append(resultMetas, staticForegroundMetas...)
	result = append(result, cursorCommands...)
	resultMetas = append(resultMetas, cursorMetas...)
	return result, resultMetas
}

func scrollAnimationFrame(frame renderlist.Frame, metas []rowMeta, state *screen.State, meta *screen.FrameMeta, list *drawlist.List, scroll map[int]*gridScrollState, cellWidth, cellHeight float32) (renderlist.Frame, []rowMeta) {
	if list == nil || len(scroll) == 0 {
		return frame, metas
	}
	layerMap := make(map[int]scrollLayer, len(list.Layers))
	for _, drawLayer := range list.Layers {
		layer, ok := scrollLayerFor(state, meta, drawLayer, scroll[drawLayer.Grid], cellWidth, cellHeight)
		if ok {
			layerMap[drawLayer.Grid] = layer
		}
	}
	if len(layerMap) == 0 {
		return frame, metas
	}
	layerBackgrounds := make(map[int]uint32, len(layerMap))
	layerBackgroundsPresent := make(map[int]bool, len(layerMap))
	for gridID, layer := range layerMap {
		color, ok := scrollInnerBackground(frame, metas, layer)
		layerBackgrounds[gridID] = color
		layerBackgroundsPresent[gridID] = ok
	}
	result := cloneFrame(frame)
	result.Commands = make([]renderlist.Command, 0, len(frame.Commands)+len(layerMap)*2)
	resultMetas := make([]rowMeta, 0, len(frame.Commands)+len(layerMap)*2)
	segmentCommands := make([]renderlist.Command, 0, len(frame.Commands))
	segmentMetas := make([]rowMeta, 0, len(frame.Commands))
	activeGrid := 0
	flush := func() {
		if len(segmentCommands) == 0 {
			return
		}
		if layer, ok := layerMap[activeGrid]; ok {
			commands, commandMetas := scrollSegmentCommands(activeGrid, segmentCommands, segmentMetas, layer, cellWidth, cellHeight, layerBackgrounds[activeGrid], layerBackgroundsPresent[activeGrid])
			result.Commands = append(result.Commands, commands...)
			resultMetas = append(resultMetas, commandMetas...)
		} else {
			result.Commands = append(result.Commands, segmentCommands...)
			resultMetas = append(resultMetas, segmentMetas...)
		}
		segmentCommands = segmentCommands[:0]
		segmentMetas = segmentMetas[:0]
	}
	for index, command := range frame.Commands {
		commandMeta := rowMeta{}
		if index < len(metas) {
			commandMeta = metas[index]
		}
		gridID := int(commandMeta.grid)
		_, active := layerMap[gridID]
		if active && activeGrid != gridID {
			flush()
			activeGrid = gridID
		} else if !active && activeGrid != 0 {
			flush()
			activeGrid = 0
		}
		segmentCommands = append(segmentCommands, command)
		segmentMetas = append(segmentMetas, commandMeta)
	}
	flush()
	return result, resultMetas
}

func trimScrollSnapshots(state *gridScrollState, position float32, innerHeight int) {
	if state == nil || innerHeight <= 0 {
		return
	}
	snapshots := slices.DeleteFunc(state.snapshots, func(snapshot scrollSnapshot) bool {
		return max(0, position)+snapshot.lineOffset <= -float32(innerHeight) ||
			min(0, position)+snapshot.lineOffset >= float32(innerHeight)
	})
	if len(snapshots) > maxScrollSnapshots {
		snapshots = append([]scrollSnapshot(nil), snapshots[len(snapshots)-maxScrollSnapshots:]...)
	}
	state.snapshots = snapshots
}
