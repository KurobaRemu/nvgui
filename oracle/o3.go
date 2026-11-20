package oracle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"nvgui/screen"
)

// O3Cell represents one sampled screen cell inspected by nvim__inspect_cell or extracted from a committed screen.
type O3Cell struct {
	Row   int            `json:"row"`
	Col   int            `json:"col"`
	Text  string         `json:"text"`
	Width int            `json:"width"`
	HL    map[string]any `json:"hl,omitempty"`
}

// O3Sample is a versioned, deterministic collection of sampled grid cells.
type O3Sample struct {
	Version     int      `json:"v"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	NvimVersion string   `json:"nvim_version,omitempty"`
	Cells       []O3Cell `json:"cells"`
}

// ParseO3Sample decodes and validates a JSON O3 cell sample.
func ParseO3Sample(data []byte) (*O3Sample, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("oracle o3: empty sample")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var sample O3Sample
	if err := dec.Decode(&sample); err != nil {
		return nil, fmt.Errorf("oracle o3: decode json: %w", err)
	}
	if sample.Version != 1 {
		return nil, fmt.Errorf("oracle o3: unsupported version %d, want 1", sample.Version)
	}
	if sample.Width <= 0 || sample.Height <= 0 {
		return nil, fmt.Errorf("oracle o3: invalid dimensions %dx%d", sample.Width, sample.Height)
	}
	seen := make(map[[2]int]struct{}, len(sample.Cells))
	for i := range sample.Cells {
		cell := &sample.Cells[i]
		if cell.Row < 0 || cell.Row >= sample.Height || cell.Col < 0 || cell.Col >= sample.Width {
			return nil, fmt.Errorf("oracle o3: cell[%d] (%d,%d) out of bounds %dx%d", i, cell.Row, cell.Col, sample.Width, sample.Height)
		}
		if cell.Width < 0 || cell.Width > 2 {
			return nil, fmt.Errorf("oracle o3: cell (%d,%d) invalid width %d", cell.Row, cell.Col, cell.Width)
		}
		key := [2]int{cell.Row, cell.Col}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("oracle o3: duplicate cell (%d,%d)", cell.Row, cell.Col)
		}
		seen[key] = struct{}{}
		cell.HL = normalizeHLMap(cell.HL)
	}
	return &sample, nil
}

// CompareO3 compares two O3 samples cell-by-cell in canonical (row, col) order.
// It returns ("", true) when identical, or (firstDiffPath, false) at the first mismatch.
func CompareO3(actual, expected *O3Sample) (string, bool) {
	if actual == nil || expected == nil {
		if actual == expected {
			return "", true
		}
		return "sample: nil mismatch", false
	}
	if actual.Version != expected.Version {
		return fmt.Sprintf("v: got %d, want %d", actual.Version, expected.Version), false
	}
	if actual.Width != expected.Width || actual.Height != expected.Height {
		return fmt.Sprintf("size: got %dx%d, want %dx%d", actual.Width, actual.Height, expected.Width, expected.Height), false
	}

	actMap := make(map[[2]int]O3Cell, len(actual.Cells))
	for _, c := range actual.Cells {
		actMap[[2]int{c.Row, c.Col}] = c
	}
	expMap := make(map[[2]int]O3Cell, len(expected.Cells))
	for _, c := range expected.Cells {
		expMap[[2]int{c.Row, c.Col}] = c
	}

	coords := make([][2]int, 0, len(actMap)+len(expMap))
	seen := make(map[[2]int]struct{}, len(actMap)+len(expMap))
	for k := range actMap {
		seen[k] = struct{}{}
		coords = append(coords, k)
	}
	for k := range expMap {
		if _, ok := seen[k]; !ok {
			coords = append(coords, k)
		}
	}
	sort.Slice(coords, func(i, j int) bool {
		if coords[i][0] != coords[j][0] {
			return coords[i][0] < coords[j][0]
		}
		return coords[i][1] < coords[j][1]
	})

	for _, pos := range coords {
		ac, aok := actMap[pos]
		ec, eok := expMap[pos]
		if !aok {
			return fmt.Sprintf("cell[%d,%d]: missing in actual", pos[0], pos[1]), false
		}
		if !eok {
			return fmt.Sprintf("cell[%d,%d]: unexpected in actual", pos[0], pos[1]), false
		}
		if ac.Text != ec.Text {
			return fmt.Sprintf("cell[%d,%d].text: got %q, want %q", pos[0], pos[1], ac.Text, ec.Text), false
		}
		if ac.Width != ec.Width {
			return fmt.Sprintf("cell[%d,%d].width: got %d, want %d", pos[0], pos[1], ac.Width, ec.Width), false
		}
		if diff := compareHLMap(pos[0], pos[1], normalizeHLMap(ac.HL), normalizeHLMap(ec.HL)); diff != "" {
			return diff, false
		}
	}
	return "", true
}

// ExtractO3FromState extracts sampled root-grid cells from a committed screen.State.
func ExtractO3FromState(state *screen.State, coords [][2]int) (*O3Sample, error) {
	if state == nil {
		return nil, fmt.Errorf("oracle o3: nil state")
	}
	grid := state.Grids[1]
	if grid == nil {
		return nil, fmt.Errorf("oracle o3: state missing root grid 1")
	}
	if grid.Width <= 0 || grid.Height <= 0 {
		return nil, fmt.Errorf("oracle o3: invalid root grid dimensions %dx%d", grid.Width, grid.Height)
	}
	layers := screen.Layers(state)
	seen := make(map[[2]int]struct{}, len(coords))
	cells := make([]O3Cell, 0, len(coords))
	for _, pos := range coords {
		row, col := pos[0], pos[1]
		if row < 0 || row >= grid.Height || col < 0 || col >= grid.Width {
			return nil, fmt.Errorf("oracle o3: coord (%d,%d) out of bounds %dx%d", row, col, grid.Width, grid.Height)
		}
		if _, dup := seen[pos]; dup {
			return nil, fmt.Errorf("oracle o3: duplicate coord (%d,%d)", row, col)
		}
		seen[pos] = struct{}{}
		targetGrid := grid
		localRow, localCol := row, col
		for idx := len(layers) - 1; idx >= 0; idx-- {
			layer := layers[idx]
			lg := state.Grids[layer.Grid]
			if lg == nil {
				continue
			}
			lr := row - layer.ScreenRow
			lc := col - layer.ScreenCol
			if lr >= 0 && lr < lg.Height && lc >= 0 && lc < lg.Width {
				targetGrid = lg
				localRow, localCol = lr, lc
				break
			}
		}
		c := targetGrid.Cells[localRow][localCol]
		width := 1
		if c.Text == "" {
			width = 0
		} else if localCol+1 < targetGrid.Width && targetGrid.Cells[localRow][localCol+1].Text == "" {
			width = 2
		}
		var hl map[string]any
		if attr, ok := state.Attrs[c.HL]; ok && len(attr) > 0 {
			hl = normalizeHLMap(attr)
		}
		cells = append(cells, O3Cell{
			Row:   row,
			Col:   col,
			Text:  c.Text,
			Width: width,
			HL:    hl,
		})
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Row != cells[j].Row {
			return cells[i].Row < cells[j].Row
		}
		return cells[i].Col < cells[j].Col
	})
	return &O3Sample{
		Version: 1,
		Width:   grid.Width,
		Height:  grid.Height,
		Cells:   cells,
	}, nil
}

func normalizeHLMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch tv := v.(type) {
		case json.Number:
			if i, err := tv.Int64(); err == nil {
				out[k] = i
			} else if f, err := tv.Float64(); err == nil {
				out[k] = f
			} else {
				out[k] = tv.String()
			}
		case int:
			out[k] = int64(tv)
		case int32:
			out[k] = int64(tv)
		case uint64:
			out[k] = int64(tv)
		case float64:
			if float64(int64(tv)) == tv {
				out[k] = int64(tv)
			} else {
				out[k] = tv
			}
		default:
			out[k] = v
		}
	}
	return out
}

func compareHLMap(row, col int, actual, expected map[string]any) string {
	keys := make([]string, 0, len(actual)+len(expected))
	seen := make(map[string]struct{}, len(actual)+len(expected))
	for k := range actual {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range expected {
		if _, ok := seen[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		av, aok := actual[k]
		ev, eok := expected[k]
		if !aok {
			return fmt.Sprintf("cell[%d,%d].hl.%s: missing in actual", row, col, k)
		}
		if !eok {
			return fmt.Sprintf("cell[%d,%d].hl.%s: unexpected in actual", row, col, k)
		}
		if fmt.Sprintf("%T:%v", av, av) != fmt.Sprintf("%T:%v", ev, ev) {
			return fmt.Sprintf("cell[%d,%d].hl.%s: got %v, want %v", row, col, k, av, ev)
		}
	}
	return ""
}
