package drawlist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"nvgui/screen"
)

var boxArms = [128]string{
	"0101", "0202", "1010", "2020", "", "", "", "", "", "", "", "", "0110", "0210", "0120", "0220",
	"0011", "0012", "0021", "0022", "1100", "1200", "2100", "2200", "1001", "1002", "2001", "2002", "1110", "1210", "2110", "1120",
	"2120", "2210", "1220", "2220", "1011", "1012", "2011", "1021", "2021", "2012", "1022", "2022", "0111", "0112", "0211", "0212",
	"0121", "0122", "0221", "0222", "1101", "1102", "1201", "1202", "2101", "2102", "2201", "2202", "1111", "1112", "1211", "1212",
	"2111", "1121", "2121", "2112", "2211", "1122", "1221", "2212", "1222", "2122", "2221", "2222", "", "", "", "",
	"0303", "3030", "0310", "0130", "0330", "0013", "0031", "0033", "1300", "3100", "3300", "1003", "3001", "3003", "1310", "3130",
	"3330", "1013", "3031", "3033", "0313", "0131", "0333", "1303", "3101", "3303", "1313", "3131", "3333", "", "", "",
	"", "", "", "", "0001", "1000", "0100", "0010", "0002", "2000", "0200", "0020", "0201", "1020", "0102", "2010",
}

type resolvedStyle struct {
	FG, BG, SP int64
	Blend      int
	Attrs      []string
}

func Build(state *screen.State) (*List, error) {
	if state == nil {
		return nil, fmt.Errorf("drawlist: nil State")
	}
	root := state.Grids[1]
	if root == nil {
		return nil, fmt.Errorf("drawlist: missing root grid 1")
	}

	layers := screen.Layers(state)
	result := &List{
		Version: 1,
		Seq:     state.Seq,
		Width:   root.Width,
		Height:  root.Height,
		Layers:  make([]Layer, len(layers)),
	}
	styles := make(map[int]resolvedStyle)
	for i, layer := range layers {
		grid := state.Grids[layer.Grid]
		if grid == nil {
			return nil, fmt.Errorf("drawlist: layer %d references missing grid %d", i, layer.Grid)
		}
		items, err := buildItems(state, grid, styles)
		if err != nil {
			return nil, err
		}
		if layer.Grid == state.Cursor.Grid {
			items = append(items, buildCursorItem(state, grid, styles))
		}
		clip, err := visibleRect(layer.ScreenRow, layer.ScreenCol, grid.Width, grid.Height, root.Width, root.Height)
		if err != nil {
			return nil, err
		}
		result.Layers[i] = Layer{
			Grid:      layer.Grid,
			ScreenRow: layer.ScreenRow,
			ScreenCol: layer.ScreenCol,
			Width:     grid.Width,
			Height:    grid.Height,
			Clip:      clip,
			Items:     items,
		}
	}
	return result, nil
}

func marshalJSON(list *List) ([]byte, error) {
	if list == nil {
		return nil, fmt.Errorf("drawlist: nil List")
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(list); err != nil {
		return nil, fmt.Errorf("drawlist: encode JSON: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func visibleRect(screenRow, screenCol, width, height, rootWidth, rootHeight int) (Rect, error) {
	if width < 0 || height < 0 || rootWidth < 0 || rootHeight < 0 {
		return Rect{}, fmt.Errorf("drawlist: negative grid size %dx%d within root %dx%d", width, height, rootWidth, rootHeight)
	}
	x0, y0 := screenCol, screenRow
	x1, y1 := screenCol+width, screenRow+height
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > rootWidth {
		x1 = rootWidth
	}
	if y1 > rootHeight {
		y1 = rootHeight
	}
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}
	return Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, nil
}

func buildItems(state *screen.State, grid *screen.Grid, styles map[int]resolvedStyle) ([]Item, error) {
	if grid.Width < 0 || grid.Height < 0 {
		return nil, fmt.Errorf("drawlist: grid has negative size %dx%d", grid.Width, grid.Height)
	}
	fills := make([]Item, 0)
	foreground := make([]Item, 0)
	for row := 0; row < grid.Height; row++ {
		if row >= len(grid.Cells) {
			return nil, fmt.Errorf("drawlist: grid missing row %d", row)
		}
		if len(grid.Cells[row]) < grid.Width {
			return nil, fmt.Errorf("drawlist: grid row %d has %d cells, want %d", row, len(grid.Cells[row]), grid.Width)
		}
		for column := 0; column < grid.Width; column++ {
			cell := grid.Cells[row][column]
			style, err := resolveStyle(state, cell.HL, styles)
			if err != nil {
				return nil, fmt.Errorf("drawlist: row %d column %d: %w", row, column, err)
			}
			fill := Item{
				Kind:   "fill",
				Row:    row,
				Col:    column,
				Width:  1,
				Height: 1,
				BG:     style.BG,
				SP:     style.SP,
				Blend:  style.Blend,
				Attrs:  style.Attrs,
			}
			if last := len(fills) - 1; last >= 0 && canMergeFill(fills[last], fill) {
				fills[last].Width++
			} else {
				fills = append(fills, fill)
			}

			if cell.Text != "" && cell.Text != " " {
				width := 1
				if column+1 < grid.Width && grid.Cells[row][column+1].Text == "" {
					width = 2
				}
				item := Item{
					Row:    row,
					Col:    column,
					Width:  width,
					Height: 1,
					FG:     style.FG,
					BG:     style.BG,
					SP:     style.SP,
					Blend:  style.Blend,
					Attrs:  style.Attrs,
				}
				if boxRune(cell.Text) {
					item.Kind = "box"
					item.Rune = cell.Text
				} else {
					item.Kind = "text"
					item.Text = cell.Text
				}
				foreground = append(foreground, item)
			}
		}
	}
	return append(fills, foreground...), nil
}

func canMergeFill(previous, next Item) bool {
	return previous.Row == next.Row && previous.Col+previous.Width == next.Col &&
		previous.Height == next.Height && previous.BG == next.BG && previous.SP == next.SP &&
		previous.Blend == next.Blend &&
		sameAttrs(previous.Attrs, next.Attrs)
}

func sameAttrs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func resolveStyle(state *screen.State, highlightID int, cache map[int]resolvedStyle) (resolvedStyle, error) {
	if style, found := cache[highlightID]; found {
		return style, nil
	}
	attribute := state.Attrs[highlightID]
	style := resolvedStyle{FG: state.DefaultColors.FG, BG: state.DefaultColors.BG, SP: state.DefaultColors.SP}
	keys := make([]string, 0, len(attribute))
	for key := range attribute {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := attribute[key]
		switch key {
		case "foreground", "background", "special":
			if value == nil {
				continue
			}
			color, ok := value.(int64)
			if !ok {
				return resolvedStyle{}, fmt.Errorf("attribute %q has unsupported value type %T", key, value)
			}
			switch key {
			case "foreground":
				style.FG = color
			case "background":
				style.BG = color
			case "special":
				style.SP = color
			}
		case "blend":
			if value == nil {
				continue
			}
			blend, ok := value.(int64)
			if !ok {
				return resolvedStyle{}, fmt.Errorf("attribute %q has unsupported value type %T", key, value)
			}
			if blend < 0 {
				blend = 0
			} else if blend > 100 {
				blend = 100
			}
			style.Blend = int(blend)
		case "reverse":
			continue
		default:
			if err := validateAttribute(value); err != nil {
				return resolvedStyle{}, fmt.Errorf("attribute %q: %w", key, err)
			}
			if attributeEnabled(value) {
				style.Attrs = append(style.Attrs, key)
			}
		}
	}
	reverse := attribute["reverse"]
	if reverse != nil {
		enabled, ok := reverse.(bool)
		if !ok {
			return resolvedStyle{}, fmt.Errorf("attribute %q has unsupported value type %T", "reverse", reverse)
		}
		if enabled {
			style.FG, style.BG = style.BG, style.FG
		}
	}
	cache[highlightID] = style
	return style, nil
}

func validateAttribute(value any) error {
	switch value := value.(type) {
	case nil:
		return nil
	case bool:
		return nil
	case int64, string:
		return nil
	default:
		return fmt.Errorf("has unsupported value type %T", value)
	}
}

func attributeEnabled(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case int64:
		return value != 0
	case string:
		return value != ""
	default:
		return false
	}
}

func boxRune(text string) bool {
	runeValue, size := utf8.DecodeRuneInString(text)
	if runeValue == utf8.RuneError || size != len(text) {
		return false
	}
	switch {
	case 0x2500 <= runeValue && runeValue <= 0x257f:
		return boxArms[runeValue-0x2500] != "" || 0x256d <= runeValue && runeValue <= 0x2573
	case 0x2580 <= runeValue && runeValue <= 0x259f:
		return true
	case 0x1fb00 <= runeValue && runeValue <= 0x1fb3b:
		return true
	case 0xe0b0 <= runeValue && runeValue <= 0xe0b7:
		return true
	}
	return false
}

func buildCursorItem(state *screen.State, grid *screen.Grid, styles map[int]resolvedStyle) Item {
	row, col := state.Cursor.Row, state.Cursor.Col
	width := 1
	style := resolvedStyle{
		FG: state.DefaultColors.FG,
		BG: state.DefaultColors.BG,
		SP: state.DefaultColors.SP,
	}
	var cell screen.Cell
	inBounds := row >= 0 && row < grid.Height && row < len(grid.Cells) &&
		col >= 0 && col < grid.Width && col < len(grid.Cells[row])
	if inBounds {
		cell = grid.Cells[row][col]
		if resolved, err := resolveStyle(state, cell.HL, styles); err == nil {
			style = resolved
		}
		if cell.Text != "" && cell.Text != " " &&
			col+1 < grid.Width && col+1 < len(grid.Cells[row]) && grid.Cells[row][col+1].Text == "" {
			width = 2
		}
	}

	fg, bg := cursorColors(state, style)
	item := Item{
		Kind:   "cursor",
		Row:    row,
		Col:    col,
		Width:  width,
		Height: 1,
		FG:     fg,
		BG:     bg,
		SP:     style.SP,
	}

	if shape := cursorShapeAttr(state.Mode); shape != "" {
		item.Attrs = []string{shape}
		return item
	}

	if inBounds && cell.Text != "" && cell.Text != " " {
		if boxRune(cell.Text) {
			item.Rune = cell.Text
		} else {
			item.Text = cell.Text
		}
		if len(style.Attrs) > 0 {
			item.Attrs = append([]string(nil), style.Attrs...)
		}
	}
	return item
}

func cursorShapeAttr(mode string) string {
	switch mode {
	case "insert", "cmdline_insert", "visual_select":
		return "cursor_beam"
	case "replace", "cmdline_replace", "operator":
		return "cursor_underline"
	default:
		return ""
	}
}

func cursorColors(state *screen.State, style resolvedStyle) (fg, bg int64) {
	defaultBG := normalizeRGB(state.DefaultColors.BG, 0x000000)
	defaultFG := normalizeRGB(state.DefaultColors.FG, 0xffffff)
	cellBG := normalizeRGB(style.BG, defaultBG)
	cellFG := normalizeRGB(style.FG, defaultFG)

	bg = cellFG
	fg = cellBG

	cellBGLuma := relativeLuma(cellBG)
	if absInt64(relativeLuma(bg)-cellBGLuma) < 80 {
		if absInt64(relativeLuma(defaultFG)-cellBGLuma) >= 80 {
			bg = defaultFG
		} else if cellBGLuma >= 128 {
			bg = 0x181818
		} else {
			bg = 0xf0f0f0
		}
	}

	bgLuma := relativeLuma(bg)
	if absInt64(relativeLuma(fg)-bgLuma) < 100 {
		if bgLuma >= 128 {
			fg = 0x101010
		} else {
			fg = 0xffffff
		}
	}
	return fg, bg
}

func normalizeRGB(color, fallback int64) int64 {
	if color >= 0 {
		return color & 0xffffff
	}
	return fallback
}

func relativeLuma(rgb int64) int64 {
	r := (rgb >> 16) & 0xff
	g := (rgb >> 8) & 0xff
	b := rgb & 0xff
	return (299*r + 587*g + 114*b) / 1000
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
