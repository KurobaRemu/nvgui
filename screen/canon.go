package screen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

type canonicalCell [2]any

type canonicalGrid struct {
	W    int               `json:"w"`
	H    int               `json:"h"`
	Rows [][]canonicalCell `json:"rows"`
}

type canonicalWinPlacement struct {
	Kind   string `json:"kind"`
	Win    int64  `json:"win"`
	Row    int    `json:"row"`
	Col    int    `json:"col"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type canonicalFloatPlacement struct {
	Kind       string  `json:"kind"`
	Win        int64   `json:"win"`
	Anchor     string  `json:"anchor"`
	AnchorGrid int     `json:"anchor_grid"`
	AnchorRow  float64 `json:"anchor_row"`
	AnchorCol  float64 `json:"anchor_col"`
	Mouse      bool    `json:"mouse"`
	ZIndex     int     `json:"zindex"`
	CompIndex  int     `json:"compindex"`
	ScreenRow  int     `json:"screen_row"`
	ScreenCol  int     `json:"screen_col"`
}

type canonicalExternalPlacement struct {
	Kind string `json:"kind"`
}

type canonicalMsg struct {
	Grid      int    `json:"grid"`
	Row       int    `json:"row"`
	Scrolled  bool   `json:"scrolled"`
	SepChar   string `json:"sep_char"`
	ZIndex    int    `json:"zindex"`
	CompIndex int    `json:"compindex"`
}

type canonicalCursor struct {
	Grid int `json:"grid"`
	Row  int `json:"row"`
	Col  int `json:"col"`
}

type canonicalColors struct {
	FG int64 `json:"fg"`
	BG int64 `json:"bg"`
	SP int64 `json:"sp"`
}

// Canonical converts a Committed Screen to Canonical JSON v1. Highlighters are
// represented by value: indices are assigned in grid id, row, then column order.
func Canonical(s *State) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("screen: nil State")
	}
	attrs := []map[string]any{{}}
	attrIndices := map[string]int{"{}": 0}
	grids := make(map[string]canonicalGrid, len(s.Grids))
	placements := make(map[string]any, len(s.Placements))

	gridIDs := sortedKeys(s.Grids)
	for _, gridID := range gridIDs {
		grid := s.Grids[gridID]
		if grid == nil {
			return nil, fmt.Errorf("screen: nil grid %d", gridID)
		}
		if grid.Width < 0 || grid.Height < 0 {
			return nil, fmt.Errorf("screen: grid %d has negative size %dx%d", gridID, grid.Width, grid.Height)
		}
		rows := make([][]canonicalCell, grid.Height)
		for row := 0; row < grid.Height; row++ {
			if row >= len(grid.Cells) {
				return nil, fmt.Errorf("screen: grid %d missing row %d", gridID, row)
			}
			canonicalRow := make([]canonicalCell, grid.Width)
			if len(grid.Cells[row]) < grid.Width {
				return nil, fmt.Errorf("screen: grid %d row %d has %d cells, want %d", gridID, row, len(grid.Cells[row]), grid.Width)
			}
			for column := 0; column < grid.Width; column++ {
				cell := grid.Cells[row][column]
				attrValue, serialization, err := canonicalAttr(cellAttr(s.Attrs, cell.HL))
				if err != nil {
					return nil, fmt.Errorf("screen: grid %d row %d column %d: %w", gridID, row, column, err)
				}
				attrIndex, found := attrIndices[serialization]
				if !found {
					attrIndex = len(attrs)
					attrs = append(attrs, attrValue)
					attrIndices[serialization] = attrIndex
				}
				canonicalRow[column] = canonicalCell{cell.Text, attrIndex}
			}
			rows[row] = canonicalRow
		}
		grids[strconv.Itoa(gridID)] = canonicalGrid{W: grid.Width, H: grid.Height, Rows: rows}

		placement, found := s.Placements[gridID]
		if !found {
			continue
		}
		key := strconv.Itoa(gridID)
		switch placement.Kind {
		case PlacedWin:
			placements[key] = canonicalWinPlacement{
				Kind: "win", Win: placement.Win,
				Row: placement.Row, Col: placement.Col,
				Width: placement.Width, Height: placement.Height,
			}
		case PlacedFloat:
			placements[key] = canonicalFloatPlacement{
				Kind: "float", Win: placement.Win,
				Anchor: placement.Anchor, AnchorGrid: placement.AnchorGrid,
				AnchorRow: placement.AnchorRow, AnchorCol: placement.AnchorCol,
				Mouse: placement.MouseEnabled, ZIndex: placement.ZIndex, CompIndex: placement.CompIndex,
				ScreenRow: placement.ScreenRow, ScreenCol: placement.ScreenCol,
			}
		case PlacedExternal:
			placements[key] = canonicalExternalPlacement{Kind: "external"}
		default:
			return nil, fmt.Errorf("screen: grid %d has unknown placement kind %d", gridID, placement.Kind)
		}
	}

	var msg *canonicalMsg
	if s.Msg != nil {
		msg = &canonicalMsg{
			Grid: s.Msg.Grid, Row: s.Msg.Row, Scrolled: s.Msg.Scrolled,
			SepChar: s.Msg.SepChar, ZIndex: s.Msg.ZIndex, CompIndex: s.Msg.CompIndex,
		}
	}
	mode := s.Mode
	if mode == "" {
		mode = "normal"
	}

	parts := []string{
		`"v":1`,
		`"default_colors":` + mustJSON(canonicalColors{FG: s.DefaultColors.FG, BG: s.DefaultColors.BG, SP: s.DefaultColors.SP}),
		`"attrs":` + mustJSON(attrs),
		`"grids":` + encodeMap(grids),
		`"placements":` + encodeMap(placements),
		`"msg":` + mustJSON(msg),
		`"cursor":` + mustJSON(canonicalCursor(s.Cursor)),
		`"mode":` + mustJSON(mode),
	}
	return []byte("{" + joinComma(parts) + "}"), nil
}

func canonicalAttr(attr Attr) (map[string]any, string, error) {
	result := make(map[string]any, len(attr))
	for key, value := range attr {
		switch value.(type) {
		case nil:
			continue
		case int64, bool, string:
			result[key] = value
		default:
			return nil, "", fmt.Errorf("attribute %q has unsupported value type %T", key, value)
		}
	}
	return result, string(mustJSON(result)), nil
}

func cellAttr(attrs map[int]Attr, highlightID int) Attr {
	if highlightID == 0 || attrs == nil {
		return Attr{}
	}
	return attrs[highlightID]
}

func mustJSON(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(fmt.Sprintf("screen: encode canonical JSON: %v", err))
	}
	return buffer.String()[:buffer.Len()-1]
}

func encodeMap[V any](values map[string]V) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, mustJSON(key)+":"+mustJSON(values[key]))
	}
	return "{" + joinComma(parts) + "}"
}

func joinComma(parts []string) string {
	result := ""
	for index, part := range parts {
		if index > 0 {
			result += ","
		}
		result += part
	}
	return result
}

func sortedKeys[V any](values map[int]V) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}
