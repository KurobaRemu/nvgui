package drawlist_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"nvgui/drawlist"
	"nvgui/oracle"
	"nvgui/protocol"
	"nvgui/screen"
)

func replayGoldenStates(t *testing.T, golden oracle.Golden) []*screen.State {
	t.Helper()
	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("%s: ReadMessages: %v", golden.Name, err)
	}
	reducer := screen.NewReducer()
	var states []*screen.State
	for _, msg := range messages {
		if msg.Type != 2 || msg.Method != "redraw" {
			continue
		}
		events, err := protocol.DecodeRedraw(msg.Args)
		if err != nil {
			t.Fatalf("%s: DecodeRedraw: %v", golden.Name, err)
		}
		committed, err := reducer.Apply(events)
		if err != nil {
			t.Fatalf("%s: Apply: %v", golden.Name, err)
		}
		states = append(states, committed...)
	}
	if len(states) == 0 {
		t.Fatalf("%s: no committed states", golden.Name)
	}
	return states
}

func expectedClip(screenRow, screenCol, w, h, rootW, rootH int) drawlist.Rect {
	x0 := screenCol
	y0 := screenRow
	x1 := screenCol + w
	y1 := screenRow + h
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > rootW {
		x1 = rootW
	}
	if y1 > rootH {
		y1 = rootH
	}
	cw := x1 - x0
	ch := y1 - y0
	if cw < 0 {
		cw = 0
	}
	if ch < 0 {
		ch = 0
	}
	return drawlist.Rect{X: x0, Y: y0, W: cw, H: ch}
}

func assertStateDrawListInvariants(t *testing.T, label string, state *screen.State) *drawlist.List {
	t.Helper()
	beforeCanon, err := screen.Canonical(state)
	if err != nil {
		t.Fatalf("%s: Canonical before Build: %v", label, err)
	}
	list1, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("%s: Build: %v", label, err)
	}
	if list1 == nil {
		t.Fatalf("%s: Build returned nil list with nil error", label)
	}
	afterCanon, err := screen.Canonical(state)
	if err != nil {
		t.Fatalf("%s: Canonical after Build: %v", label, err)
	}
	if !bytes.Equal(beforeCanon, afterCanon) {
		t.Fatalf("%s: Build mutated input *screen.State", label)
	}

	root := state.Grids[1]
	if root == nil {
		t.Fatalf("%s: missing root grid 1", label)
	}
	if list1.Version != 1 {
		t.Errorf("%s: Version = %d, want 1", label, list1.Version)
	}
	if list1.Seq != state.Seq {
		t.Errorf("%s: Seq = %d, want %d", label, list1.Seq, state.Seq)
	}
	if list1.Width != root.Width || list1.Height != root.Height {
		t.Errorf("%s: Size = %dx%d, want %dx%d", label, list1.Width, list1.Height, root.Width, root.Height)
	}

	wantLayers := screen.Layers(state)
	if len(list1.Layers) != len(wantLayers) {
		t.Fatalf("%s: len(Layers) = %d, want %d", label, len(list1.Layers), len(wantLayers))
	}

	cursorCount := 0
	cursorLayerVisible := false
	for i, wl := range wantLayers {
		gl := list1.Layers[i]
		grid := state.Grids[wl.Grid]
		if grid == nil {
			t.Fatalf("%s: layer %d references missing grid %d", label, i, wl.Grid)
		}
		if gl.Grid != wl.Grid || gl.ScreenRow != wl.ScreenRow || gl.ScreenCol != wl.ScreenCol {
			t.Errorf("%s: layer[%d] = (grid=%d row=%d col=%d), want (grid=%d row=%d col=%d)",
				label, i, gl.Grid, gl.ScreenRow, gl.ScreenCol, wl.Grid, wl.ScreenRow, wl.ScreenCol)
		}
		if gl.Width != grid.Width || gl.Height != grid.Height {
			t.Errorf("%s: layer[%d] size = %dx%d, want %dx%d", label, i, gl.Width, gl.Height, grid.Width, grid.Height)
		}
		wantClip := expectedClip(wl.ScreenRow, wl.ScreenCol, grid.Width, grid.Height, root.Width, root.Height)
		if gl.Clip != wantClip {
			t.Errorf("%s: layer[%d] Clip = %+v, want %+v", label, i, gl.Clip, wantClip)
		}
		if wl.Grid == state.Cursor.Grid {
			cursorLayerVisible = true
		}

		for _, item := range gl.Items {
			switch item.Kind {
			case "fill":
				if item.Width <= 0 || item.Height <= 0 {
					t.Errorf("%s: layer[%d] fill has non-positive size %dx%d", label, i, item.Width, item.Height)
				}
			case "text":
				if item.Text == "" || item.Text == " " {
					t.Errorf("%s: layer[%d] text item at (%d,%d) has empty/space text %q", label, i, item.Row, item.Col, item.Text)
				}
				if item.Row >= 0 && item.Row < grid.Height && item.Col >= 0 && item.Col < grid.Width {
					if grid.Cells[item.Row][item.Col].Text == "" {
						t.Errorf("%s: layer[%d] emitted text item on double-width continuation cell (%d,%d)", label, i, item.Row, item.Col)
					}
				}
			case "box":
				if item.Rune == "" {
					t.Errorf("%s: layer[%d] box item at (%d,%d) has empty Rune", label, i, item.Row, item.Col)
				}
			case "cursor":
				cursorCount++
				if gl.Grid != state.Cursor.Grid || item.Row != state.Cursor.Row || item.Col != state.Cursor.Col {
					t.Errorf("%s: cursor at grid=%d (%d,%d), want grid=%d (%d,%d)",
						label, gl.Grid, item.Row, item.Col, state.Cursor.Grid, state.Cursor.Row, state.Cursor.Col)
				}
			default:
				t.Errorf("%s: layer[%d] unknown item kind %q", label, i, item.Kind)
			}
			if len(item.Attrs) > 1 && !sort.StringsAreSorted(item.Attrs) {
				t.Errorf("%s: layer[%d] item Attrs not sorted: %v", label, i, item.Attrs)
			}
		}
	}
	if cursorLayerVisible && cursorCount != 1 {
		t.Errorf("%s: cursorCount = %d, want 1", label, cursorCount)
	}

	// Idempotency and JSON stability.
	list2, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("%s: second Build: %v", label, err)
	}
	raw1, err := drawlist.JSON(list1)
	if err != nil {
		t.Fatalf("%s: JSON(list1): %v", label, err)
	}
	raw2, err := drawlist.JSON(list2)
	if err != nil {
		t.Fatalf("%s: JSON(list2): %v", label, err)
	}
	if len(raw1) == 0 || !json.Valid(raw1) {
		t.Fatalf("%s: JSON returned empty or invalid JSON", label)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Fatalf("%s: Build/JSON is not deterministic across repeated calls", label)
	}
	var decoded drawlist.List
	if err := json.Unmarshal(raw1, &decoded); err != nil {
		t.Fatalf("%s: unmarshal JSON output: %v", label, err)
	}
	if !reflect.DeepEqual(normalizeDrawList(&decoded), normalizeDrawList(list1)) {
		t.Fatalf("%s: JSON round-trip mismatch", label)
	}
	reencoded, err := drawlist.JSON(&decoded)
	if err != nil {
		t.Fatalf("%s: JSON(&decoded): %v", label, err)
	}
	if !bytes.Equal(reencoded, raw1) {
		t.Fatalf("%s: JSON re-encode mismatch after round-trip", label)
	}
	return list1
}

func normalizeDrawList(in *drawlist.List) *drawlist.List {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.Layers) == 0 {
		out.Layers = nil
		return &out
	}
	out.Layers = make([]drawlist.Layer, len(in.Layers))
	for i, l := range in.Layers {
		nl := l
		if len(l.Items) == 0 {
			nl.Items = nil
		} else {
			nl.Items = make([]drawlist.Item, len(l.Items))
			for j, it := range l.Items {
				ni := it
				if len(it.Attrs) == 0 {
					ni.Attrs = nil
				} else {
					ni.Attrs = append([]string(nil), it.Attrs...)
				}
				nl.Items[j] = ni
			}
		}
		out.Layers[i] = nl
	}
	return &out
}

func TestDrawListNilInputs(t *testing.T) {
	if _, err := drawlist.Build(nil); err == nil {
		t.Fatal("Build(nil) returned nil error, want error")
	}
	if _, err := drawlist.JSON(nil); err == nil {
		t.Fatal("JSON(nil) returned nil error, want error")
	}
}

func TestDrawListGoldens(t *testing.T) {
	goldens := oracle.List()
	if len(goldens) != 10 {
		t.Fatalf("oracle.List() returned %d goldens, want 10", len(goldens))
	}
	sawFloatBorderBox := false
	sawWideCharText := false
	for _, golden := range goldens {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			states := replayGoldenStates(t, golden)
			// Always check the final frame of every golden; also check all frames for
			// float-border, wide-chars, and highlight scenarios so intermediate floats/attrs are verified.
			indices := []int{len(states) - 1}
			if golden.Name == "s04-float-border" || golden.Name == "s05-wide-chars" || golden.Name == "s06-highlight" {
				indices = make([]int, len(states))
				for i := range states {
					indices[i] = i
				}
			}
			for _, idx := range indices {
				list := assertStateDrawListInvariants(t, golden.Name, states[idx])
				for _, layer := range list.Layers {
					for _, item := range layer.Items {
						if golden.Name == "s04-float-border" && len(list.Layers) > 1 && item.Kind == "box" {
							sawFloatBorderBox = true
						}
						if golden.Name == "s05-wide-chars" && item.Kind == "text" && item.Text == "世" {
							sawWideCharText = true
						}
					}
				}
			}
		})
	}
	if !sawFloatBorderBox {
		t.Error("expected at least one multi-layer box border item across s04-float-border frames")
	}
	if !sawWideCharText {
		t.Error("expected CJK wide character text item \"世\" across s05-wide-chars frames")
	}
}

func TestDrawListClipOutOfBoundsFloat(t *testing.T) {
	makeBlankGrid := func(w, h int) *screen.Grid {
		cells := make([][]screen.Cell, h)
		for r := range cells {
			cells[r] = make([]screen.Cell, w)
			for c := range cells[r] {
				cells[r][c] = screen.Cell{Text: " ", HL: 0}
			}
		}
		return &screen.Grid{Width: w, Height: h, Cells: cells}
	}
	state := &screen.State{
		Seq: 7,
		Grids: map[int]*screen.Grid{
			1: makeBlankGrid(20, 10),
			2: makeBlankGrid(8, 4),
			3: makeBlankGrid(6, 5),
		},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x000000, SP: 0xff0000},
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedFloat, Win: 1001, Width: 8, Height: 4, ScreenRow: 8, ScreenCol: 16, ZIndex: 50, CompIndex: 1},
			3: {Kind: screen.PlacedFloat, Win: 1002, Width: 6, Height: 5, ScreenRow: -2, ScreenCol: -3, ZIndex: 60, CompIndex: 2},
		},
		Cursor: screen.Cursor{Grid: 1, Row: 0, Col: 0},
		Mode:   "normal",
	}

	list := assertStateDrawListInvariants(t, "clip-oob", state)
	if len(list.Layers) != 3 {
		t.Fatalf("len(Layers) = %d, want 3 (out-of-bounds floats must be clipped, not dropped)", len(list.Layers))
	}
	if got, want := list.Layers[1].Clip, (drawlist.Rect{X: 16, Y: 8, W: 4, H: 2}); got != want {
		t.Errorf("layer 2 Clip = %+v, want %+v", got, want)
	}
	if got, want := list.Layers[2].Clip, (drawlist.Rect{X: 0, Y: 0, W: 3, H: 3}); got != want {
		t.Errorf("layer 3 Clip = %+v, want %+v", got, want)
	}
}

func TestDrawListFillMergeBoxWideCharsAndUTF8JSON(t *testing.T) {
	row0 := []screen.Cell{
		{Text: " ", HL: 1}, {Text: " ", HL: 1}, {Text: " ", HL: 1}, {Text: " ", HL: 1}, {Text: " ", HL: 1},
		{Text: " ", HL: 2}, {Text: " ", HL: 2}, {Text: " ", HL: 2},
		{Text: "世", HL: 0}, {Text: "", HL: 0},
		{Text: "🌍", HL: 0}, {Text: "", HL: 0},
		{Text: "A", HL: 0}, {Text: " ", HL: 0},
	}
	row1 := []screen.Cell{
		{Text: "─", HL: 0}, {Text: "╭", HL: 0}, {Text: "█", HL: 0},
		{Text: "\U0001FB00", HL: 0}, {Text: "\uE0B0", HL: 0},
		{Text: " ", HL: 0}, {Text: " ", HL: 0}, {Text: " ", HL: 0},
		{Text: " ", HL: 0}, {Text: " ", HL: 0}, {Text: " ", HL: 0},
		{Text: " ", HL: 0}, {Text: " ", HL: 0}, {Text: " ", HL: 0},
	}
	state := &screen.State{
		Seq: 12,
		Grids: map[int]*screen.Grid{
			1: {Width: len(row0), Height: 2, Cells: [][]screen.Cell{row0, row1}},
		},
		Attrs: map[int]screen.Attr{
			0: {},
			1: {"background": int64(0x112233)},
			2: {"background": int64(0x445566)},
		},
		DefaultColors: screen.Colors{FG: 0xeeeeee, BG: 0x101010, SP: 0x00ff00},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: 12},
		Mode:          "normal",
	}

	list := assertStateDrawListInvariants(t, "fill-box-wide", state)
	items := list.Layers[0].Items

	// Check adjacent fill merging on row 0: cols 0..4 (bg 0x112233) must merge into Width=5; cols 5..7 (bg 0x445566) into Width=3.
	foundRun1, foundRun2 := false, false
	textByCol := map[int]drawlist.Item{}
	boxByCol := map[int]drawlist.Item{}
	for _, it := range items {
		if it.Kind == "fill" && it.Row == 0 {
			if it.Col == 0 && it.Width == 5 && it.BG == 0x112233 {
				foundRun1 = true
			}
			if it.Col == 5 && it.Width == 3 && it.BG == 0x445566 {
				foundRun2 = true
			}
		}
		if it.Kind == "text" && it.Row == 0 {
			textByCol[it.Col] = it
		}
		if it.Kind == "box" && it.Row == 1 {
			boxByCol[it.Col] = it
		}
	}
	if !foundRun1 {
		t.Errorf("expected merged fill at row=0 col=0 width=5 bg=0x112233")
	}
	if !foundRun2 {
		t.Errorf("expected merged fill at row=0 col=5 width=3 bg=0x445566")
	}

	// Wide chars: "世" at col 8 (col 9 continuation omitted), "🌍" at col 10 (col 11 omitted), "A" at col 12.
	if it, ok := textByCol[8]; !ok || it.Text != "世" {
		t.Errorf("expected text \"世\" at col 8, got %+v (ok=%v)", it, ok)
	}
	if _, bad := textByCol[9]; bad {
		t.Error("continuation cell col 9 must not emit a text item")
	}
	if it, ok := textByCol[10]; !ok || it.Text != "🌍" {
		t.Errorf("expected text \"🌍\" at col 10, got %+v (ok=%v)", it, ok)
	}
	if _, bad := textByCol[11]; bad {
		t.Error("continuation cell col 11 must not emit a text item")
	}
	if it, ok := textByCol[12]; !ok || it.Text != "A" {
		t.Errorf("expected text \"A\" at col 12, got %+v (ok=%v)", it, ok)
	}

	// Box/Block/Sextant/Powerline runes on row 1 cols 0..4.
	wantBoxes := []string{"─", "╭", "█", "\U0001FB00", "\uE0B0"}
	for col, wantRune := range wantBoxes {
		it, ok := boxByCol[col]
		if !ok || it.Rune != wantRune {
			t.Errorf("row 1 col %d: expected box rune %q, got %+v (ok=%v)", col, wantRune, it, ok)
		}
	}

	// Raw UTF-8 Chinese in JSON without \uXXXX escaping.
	raw, err := drawlist.JSON(list)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !bytes.Contains(raw, []byte("世")) || bytes.Contains(raw, []byte(`\u4e16`)) {
		t.Errorf("JSON must emit raw UTF-8 Chinese without \\uXXXX escaping: %s", string(raw))
	}
}

func TestDrawListHighlightColorsReverseAndAttrs(t *testing.T) {
	row := []screen.Cell{
		{Text: "a", HL: 0},
		{Text: "b", HL: 1},
		{Text: "c", HL: 2},
		{Text: "d", HL: 3},
		{Text: "e", HL: 4},
	}
	state := &screen.State{
		Seq: 3,
		Grids: map[int]*screen.Grid{
			1: {Width: len(row), Height: 1, Cells: [][]screen.Cell{row}},
		},
		Attrs: map[int]screen.Attr{
			0: {},
			1: {
				"foreground": int64(0x00ff00),
				"special":    int64(0x0000ff),
				"undercurl":  true,
				"italic":     true,
				"bold":       true,
			},
			2: {
				"foreground": int64(0x123456),
				"background": int64(0x654321),
				"reverse":    true,
			},
			3: {
				"reverse": true,
			},
			4: {
				"foreground":          int64(0xaabbcc),
				"future_unknown_attr": true,
			},
		},
		DefaultColors: screen.Colors{FG: 0xeeeeee, BG: 0x111111, SP: 0xff0000},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: 0},
		Mode:          "normal",
	}

	list := assertStateDrawListInvariants(t, "hl-attrs", state)
	textByCol := map[int]drawlist.Item{}
	for _, it := range list.Layers[0].Items {
		if it.Kind == "text" {
			textByCol[it.Col] = it
		}
	}

	// Col 0: default colors.
	if it := textByCol[0]; it.FG != 0xeeeeee || it.BG != 0x111111 || it.SP != 0xff0000 {
		t.Errorf("col 0 colors = (%#x, %#x, %#x), want (0xeeeeee, 0x111111, 0xff0000)", it.FG, it.BG, it.SP)
	}
	// Col 1: explicit fg/sp, default bg, sorted attrs.
	if it := textByCol[1]; it.FG != 0x00ff00 || it.BG != 0x111111 || it.SP != 0x0000ff {
		t.Errorf("col 1 colors = (%#x, %#x, %#x), want (0x00ff00, 0x111111, 0x0000ff)", it.FG, it.BG, it.SP)
	} else if want := []string{"bold", "italic", "undercurl"}; !reflect.DeepEqual(it.Attrs, want) {
		t.Errorf("col 1 Attrs = %v, want %v", it.Attrs, want)
	}
	// Col 2: reverse swaps explicit fg & bg.
	if it := textByCol[2]; it.FG != 0x654321 || it.BG != 0x123456 {
		t.Errorf("col 2 reverse colors = (%#x, %#x), want (0x654321, 0x123456)", it.FG, it.BG)
	}
	// Col 3: reverse swaps default fg & bg.
	if it := textByCol[3]; it.FG != 0x111111 || it.BG != 0xeeeeee {
		t.Errorf("col 3 default reverse colors = (%#x, %#x), want (0x111111, 0xeeeeee)", it.FG, it.BG)
	}
	// Col 4: unknown attr preserved in Attrs without breaking colors.
	if it := textByCol[4]; it.FG != 0xaabbcc || it.BG != 0x111111 {
		t.Errorf("col 4 unknown attr colors = (%#x, %#x), want (0xaabbcc, 0x111111)", it.FG, it.BG)
	} else {
		foundUnknown := false
		for _, a := range it.Attrs {
			if a == "future_unknown_attr" {
				foundUnknown = true
				break
			}
		}
		if !foundUnknown {
			t.Errorf("col 4 Attrs = %v, want \"future_unknown_attr\" preserved", it.Attrs)
		}
	}
}
