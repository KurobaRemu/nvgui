package render

import (
	"math"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

type gridScrollState struct {
	spring          Spring
	prevInner       []renderlist.Command
	prevInnerMetas  []rowMeta
	lastScrollDelta float32
	width           int
	height          int
	margins         screen.ViewportMargins
	viewport        screen.Viewport
}

type scrollLayer struct {
	grid         int
	width        int
	height       int
	screenRow    int
	screenCol    int
	innerClip    ui.Rect
	margins      screen.ViewportMargins
	prev         []renderlist.Command
	prevMetas    []rowMeta
	prevDelta    float32
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
		if index >= len(metas) || metas[index].grid != layer.grid || command.Op != "fill" {
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
		prev:         gridState.prevInner,
		prevMetas:    gridState.prevInnerMetas,
		prevDelta:    gridState.lastScrollDelta,
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
		if commandMeta.grid != gridID || marginsKeepCommandStatic(command, context, cell.Width, cell.Height) {
			continue
		}
		commands = append(commands, command)
		outMetas = append(outMetas, commandMeta)
	}
	return commands, outMetas
}

func scrollHistoryCommands(layer scrollLayer, cellWidth, cellHeight float32) ([]renderlist.Command, []rowMeta) {
	if layer.prevDelta == 0 || len(layer.prev) == 0 {
		return nil, nil
	}
	innerHeight := layer.height - layer.margins.Top - layer.margins.Bottom
	if innerHeight <= 0 || math.Abs(float64(layer.prevDelta)) > float64(innerHeight) {
		return nil, nil
	}
	historyOffset := layer.offsetPixels - layer.prevDelta*cellHeight
	if math.Abs(float64(historyOffset)) < 0.001 {
		return nil, nil
	}
	commands := make([]renderlist.Command, 0, len(layer.prev))
	metas := make([]rowMeta, 0, len(layer.prev))
	for index, command := range layer.prev {
		meta := rowMeta{}
		if index < len(layer.prevMetas) {
			meta = layer.prevMetas[index]
		}
		if meta.grid != layer.grid || marginsKeepCommandStatic(command, layer, cellWidth, cellHeight) {
			continue
		}
		shifted := command
		shifted.Y += historyOffset
		meta.clip = layer.innerClip
		commands = append(commands, shifted)
		metas = append(metas, meta)
	}
	return commands, metas
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
		if ok {
			layerBackgrounds[gridID] = color
			layerBackgroundsPresent[gridID] = true
		}
	}
	result := cloneFrame(frame)
	result.Commands = make([]renderlist.Command, 0, len(frame.Commands)+len(layerMap)*2)
	resultMetas := make([]rowMeta, 0, len(frame.Commands)+len(layerMap)*2)
	layerStarted := make(map[int]bool, len(layerMap))
	appendLayerPrefix := func(gridID int, layer scrollLayer) {
		if layerStarted[gridID] {
			return
		}
		layerStarted[gridID] = true
		if layerBackgroundsPresent[gridID] {
			result.Commands = append(result.Commands, renderlist.Command{
				Op: "fill",
				X:  layer.innerClip.X,
				Y:  layer.innerClip.Y,
				W:  layer.innerClip.W,
				H:  layer.innerClip.H,
				BG: layerBackgrounds[gridID],
			})
			resultMetas = append(resultMetas, rowMeta{grid: gridID, cellWidth: cellWidth})
		}
		history, historyMetas := scrollHistoryCommands(layer, cellWidth, cellHeight)
		result.Commands = append(result.Commands, history...)
		resultMetas = append(resultMetas, historyMetas...)
	}
	for index, command := range frame.Commands {
		commandMeta := rowMeta{}
		if index < len(metas) {
			commandMeta = metas[index]
		}
		layer, ok := layerMap[commandMeta.grid]
		if ok {
			appendLayerPrefix(commandMeta.grid, layer)
		}
		if !ok || marginsKeepCommandStatic(command, layer, cellWidth, cellHeight) {
			result.Commands = append(result.Commands, command)
			resultMetas = append(resultMetas, commandMeta)
			continue
		}
		shifted := command
		shifted.Y += layer.offsetPixels
		shiftedMeta := commandMeta
		shiftedMeta.clip = layer.innerClip
		result.Commands = append(result.Commands, shifted)
		resultMetas = append(resultMetas, shiftedMeta)
	}
	return result, resultMetas
}
