package drawlist

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"nvgui/screen"
)

const (
	testFG int64 = 0xeeeeee
	testBG int64 = 0x111111
	testSP int64 = 0xff0000
)

func makeTestCell(text string, highlight int) screen.Cell {
	return screen.Cell{Text: text, HL: highlight}
}

func makeTestRow(cells ...screen.Cell) []screen.Cell {
	return cells
}

func makePaddedTestRow(width int, cells ...screen.Cell) []screen.Cell {
	row := make([]screen.Cell, width)
	for index := range row {
		row[index] = makeTestCell(" ", 0)
	}
	copy(row, cells)
	return row
}

func makeBlankTestGrid(width, height int) *screen.Grid {
	cells := make([][]screen.Cell, height)
	for rowIndex := range cells {
		cells[rowIndex] = make([]screen.Cell, width)
		for columnIndex := range cells[rowIndex] {
			cells[rowIndex][columnIndex] = makeTestCell(" ", 0)
		}
	}
	return &screen.Grid{Width: width, Height: height, Cells: cells}
}

func makeTestState(grid *screen.Grid) *screen.State {
	return &screen.State{
		Seq:           1,
		Grids:         map[int]*screen.Grid{1: grid},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: testFG, BG: testBG, SP: testSP},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1},
		Mode:          "normal",
	}
}

func stateForTestRow(attributes map[int]screen.Attr, cells ...screen.Cell) *screen.State {
	width := len(cells)
	grid := &screen.Grid{Width: width, Height: 1, Cells: [][]screen.Cell{makeTestRow(cells...)}}
	state := makeTestState(grid)
	if attributes != nil {
		state.Attrs = attributes
	}
	state.Cursor = screen.Cursor{Grid: 2}
	return state
}

func fillsForRow(items []Item, row int) []Item {
	result := make([]Item, 0)
	for _, item := range items {
		if item.Kind == "fill" && item.Row == row {
			result = append(result, item)
		}
	}
	return result
}

func findItem(items []Item, kind string, row, column int) (Item, bool) {
	for _, item := range items {
		if item.Kind == kind && item.Row == row && item.Col == column {
			return item, true
		}
	}
	return Item{}, false
}

func countKind(items []Item, kind string) int {
	count := 0
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

func assertFill(test *testing.T, fill Item, row, column, width int, background, special int64, attributes []string) {
	test.Helper()
	if fill.Kind != "fill" || fill.Row != row || fill.Col != column || fill.Width != width || fill.Height != 1 {
		test.Fatalf("fill = %+v, want row=%d col=%d width=%d height=1", fill, row, column, width)
	}
	if fill.BG != background || fill.SP != special || fill.FG != 0 {
		test.Fatalf("fill colors = (%#x, %#x, %#x), want FG=0 BG=%#x SP=%#x", fill.FG, fill.BG, fill.SP, background, special)
	}
	if fill.Text != "" || fill.Rune != "" || fill.Font != "" {
		test.Fatalf("fill has glyph fields: %+v", fill)
	}
	if !reflect.DeepEqual(fill.Attrs, attributes) {
		test.Fatalf("fill Attrs = %v, want %v", fill.Attrs, attributes)
	}
}

func assertFillCoverage(test *testing.T, items []Item, row, width int) {
	test.Helper()
	nextColumn := 0
	for _, item := range items {
		if item.Kind != "fill" || item.Row != row {
			continue
		}
		if item.Col != nextColumn {
			test.Fatalf("row %d fill starts at col %d, want %d", row, item.Col, nextColumn)
		}
		if item.Width <= 0 || item.Height != 1 {
			test.Fatalf("row %d fill has invalid size %dx%d", row, item.Width, item.Height)
		}
		nextColumn += item.Width
	}
	if nextColumn != width {
		test.Fatalf("row %d fill coverage ends at %d, want %d", row, nextColumn, width)
	}
}

func assertOrdering(test *testing.T, items []Item) {
	test.Helper()
	lastFillIndex := -1
	firstForegroundIndex := -1
	lastForegroundIndex := -1
	cursorIndex := -1
	for index, item := range items {
		switch item.Kind {
		case "fill":
			lastFillIndex = index
		case "text", "box":
			if firstForegroundIndex == -1 {
				firstForegroundIndex = index
			}
			lastForegroundIndex = index
		case "cursor":
			cursorIndex = index
		}
	}
	if firstForegroundIndex >= 0 && lastFillIndex >= firstForegroundIndex {
		test.Fatalf("last fill index %d must precede first foreground index %d", lastFillIndex, firstForegroundIndex)
	}
	if cursorIndex >= 0 && lastForegroundIndex >= 0 && cursorIndex < lastForegroundIndex {
		test.Fatalf("cursor index %d must follow last foreground index %d", cursorIndex, lastForegroundIndex)
	}
}

func assertNoForeground(test *testing.T, items []Item) {
	test.Helper()
	foregroundCount := countKind(items, "text") + countKind(items, "box")
	if foregroundCount != 0 {
		test.Fatalf("foreground item count = %d, want 0", foregroundCount)
	}
}

func TestBuildItemsFillOrderAndMerging(test *testing.T) {
	row0 := makePaddedTestRow(10,
		makeTestCell("a", 0), makeTestCell("b", 0), makeTestCell("─", 0), makeTestCell("╭", 0),
		makeTestCell("世", 0), makeTestCell("", 0), makeTestCell("🌍", 0), makeTestCell("", 0),
		makeTestCell(" ", 0), makeTestCell("A", 0),
	)
	row1 := makePaddedTestRow(10,
		makeTestCell(" ", 0), makeTestCell("", 0), makeTestCell("x", 0),
		makeTestCell(" ", 0), makeTestCell("y", 0),
	)
	row2 := makePaddedTestRow(10,
		makeTestCell("┄", 0), makeTestCell("╭", 0), makeTestCell("╱", 0), makeTestCell("█", 0),
		makeTestCell("\U0001FB00", 0), makeTestCell("\ue0b0", 0), makeTestCell("─\u0301", 0),
		makeTestCell("世", 0), makeTestCell("", 0), makeTestCell("A", 0),
	)
	grid := &screen.Grid{Width: 10, Height: 3, Cells: [][]screen.Cell{row0, row1, row2}}
	state := makeTestState(grid)
	state.Cursor = screen.Cursor{Grid: 1, Row: 1, Col: 2}

	list, err := Build(state)
	if err != nil {
		test.Fatalf("Build: %v", err)
	}
	if len(list.Layers) != 1 {
		test.Fatalf("len(Layers) = %d, want 1", len(list.Layers))
	}
	items := list.Layers[0].Items
	assertOrdering(test, items)
	assertFillCoverage(test, items, 0, 10)
	assertFillCoverage(test, items, 1, 10)
	assertFillCoverage(test, items, 2, 10)

	fills0 := fillsForRow(items, 0)
	fills1 := fillsForRow(items, 1)
	fills2 := fillsForRow(items, 2)
	if len(fills0) != 1 || len(fills1) != 1 || len(fills2) != 1 {
		test.Fatalf("fill counts = (%d, %d, %d), want (1, 1, 1)", len(fills0), len(fills1), len(fills2))
	}
	assertFill(test, fills0[0], 0, 0, 10, testBG, testSP, nil)
	assertFill(test, fills1[0], 1, 0, 10, testBG, testSP, nil)
	assertFill(test, fills2[0], 2, 0, 10, testBG, testSP, nil)

	if countKind(items, "text") != 11 || countKind(items, "box") != 7 {
		test.Fatalf("foreground counts = text %d box %d, want text 11 box 7", countKind(items, "text"), countKind(items, "box"))
	}
	textItem, found := findItem(items, "text", 0, 4)
	if !found || textItem.Text != "世" || textItem.Width != 2 {
		test.Fatalf("wide CJK item = %+v (found=%v), want text 世 width 2", textItem, found)
	}
	textItem, found = findItem(items, "text", 0, 6)
	if !found || textItem.Text != "🌍" || textItem.Width != 2 {
		test.Fatalf("wide emoji item = %+v (found=%v), want text 🌍 width 2", textItem, found)
	}
	if _, found := findItem(items, "text", 0, 5); found {
		test.Fatal("continuation cell col 5 emitted text")
	}
	if _, found := findItem(items, "text", 0, 7); found {
		test.Fatal("continuation cell col 7 emitted text")
	}
	if _, found := findItem(items, "text", 1, 1); found {
		test.Fatal("continuation cell row 1 col 1 emitted text")
	}
	if _, found := findItem(items, "text", 2, 8); found {
		test.Fatal("continuation cell row 2 col 8 emitted text")
	}

	boxItem, found := findItem(items, "box", 0, 2)
	if !found || boxItem.Rune != "─" || boxItem.Width != 1 {
		test.Fatalf("box item = %+v (found=%v), want rune ─ width 1", boxItem, found)
	}
	boxItem, found = findItem(items, "box", 2, 4)
	if !found || boxItem.Rune != "\U0001FB00" || boxItem.Width != 1 {
		test.Fatalf("sextant item = %+v (found=%v), want sextant rune width 1", boxItem, found)
	}
	textItem, found = findItem(items, "text", 2, 0)
	if !found || textItem.Text != "┄" || textItem.Width != 1 {
		test.Fatalf("dashed box item = %+v (found=%v), want text ┄ width 1", textItem, found)
	}
	textItem, found = findItem(items, "text", 2, 6)
	if !found || textItem.Text != "─\u0301" || textItem.Width != 1 {
		test.Fatalf("grapheme item = %+v (found=%v), want multi-rune text width 1", textItem, found)
	}
	boxItem, found = findItem(items, "box", 2, 5)
	if !found || boxItem.Rune != "\ue0b0" || boxItem.Width != 1 {
		test.Fatalf("powerline item = %+v (found=%v), want powerline rune width 1", boxItem, found)
	}

	if countKind(items, "cursor") != 1 {
		test.Fatalf("cursor count = %d, want 1", countKind(items, "cursor"))
	}
	cursor := items[len(items)-1]
	if cursor.Kind != "cursor" || cursor.Row != 1 || cursor.Col != 2 || cursor.Width != 1 || cursor.Height != 1 {
		test.Fatalf("cursor = %+v, want row 1 col 2 size 1x1", cursor)
	}
}

func TestBuildItemsFillSplitBoundaries(test *testing.T) {
	attributes := map[int]screen.Attr{
		0: {},
		1: {"background": int64(0x222222)},
		2: {"special": int64(0x333333)},
		3: {"bold": true},
		4: {"foreground": int64(0xabcdef), "background": int64(0x123456), "reverse": true},
		5: {"background": int64(0xabcdef)},
	}
	row := makeTestRow(
		makeTestCell(" ", 0), makeTestCell(" ", 1), makeTestCell(" ", 2),
		makeTestCell(" ", 3), makeTestCell(" ", 4), makeTestCell(" ", 5),
	)
	grid := &screen.Grid{Width: 6, Height: 1, Cells: [][]screen.Cell{row}}
	state := makeTestState(grid)
	state.Attrs = attributes
	state.Cursor = screen.Cursor{Grid: 2}

	list, err := Build(state)
	if err != nil {
		test.Fatalf("Build: %v", err)
	}
	items := list.Layers[0].Items
	assertOrdering(test, items)
	assertNoForeground(test, items)
	assertFillCoverage(test, items, 0, 6)
	fills := fillsForRow(items, 0)
	if len(fills) != 5 {
		test.Fatalf("fill count = %d, want 5", len(fills))
	}
	assertFill(test, fills[0], 0, 0, 1, testBG, testSP, nil)
	assertFill(test, fills[1], 0, 1, 1, 0x222222, testSP, nil)
	assertFill(test, fills[2], 0, 2, 1, testBG, 0x333333, nil)
	assertFill(test, fills[3], 0, 3, 1, testBG, testSP, []string{"bold"})
	assertFill(test, fills[4], 0, 4, 2, 0xabcdef, testSP, nil)
}

func TestBuildItemsContinuationCells(test *testing.T) {
	test.Run("normal wide character", func(subtest *testing.T) {
		state := stateForTestRow(nil,
			makeTestCell("世", 0), makeTestCell("", 0),
			makeTestCell("A", 0), makeTestCell(" ", 0),
		)
		list, err := Build(state)
		if err != nil {
			subtest.Fatalf("Build: %v", err)
		}
		items := list.Layers[0].Items
		assertFillCoverage(subtest, items, 0, 4)
		fills := fillsForRow(items, 0)
		if len(fills) != 1 {
			subtest.Fatalf("fill count = %d, want 1", len(fills))
		}
		assertFill(subtest, fills[0], 0, 0, 4, testBG, testSP, nil)
		textItem, found := findItem(items, "text", 0, 0)
		if !found || textItem.Text != "世" || textItem.Width != 2 {
			subtest.Fatalf("wide item = %+v (found=%v), want text 世 width 2", textItem, found)
		}
		textItem, found = findItem(items, "text", 0, 2)
		if !found || textItem.Text != "A" || textItem.Width != 1 {
			subtest.Fatalf("A item = %+v (found=%v), want text A width 1", textItem, found)
		}
		if _, found := findItem(items, "text", 0, 1); found {
			subtest.Fatal("continuation cell col 1 emitted text")
		}
		assertNoForegroundExcept(subtest, items, 2)
	})

	test.Run("wide continuation with different highlight", func(subtest *testing.T) {
		attributes := map[int]screen.Attr{
			0: {},
			1: {"background": int64(0x222222)},
		}
		state := stateForTestRow(attributes, makeTestCell("世", 0), makeTestCell("", 1))
		list, err := Build(state)
		if err != nil {
			subtest.Fatalf("Build: %v", err)
		}
		items := list.Layers[0].Items
		assertFillCoverage(subtest, items, 0, 2)
		fills := fillsForRow(items, 0)
		if len(fills) != 2 {
			subtest.Fatalf("fill count = %d, want 2", len(fills))
		}
		assertFill(subtest, fills[0], 0, 0, 1, testBG, testSP, nil)
		assertFill(subtest, fills[1], 0, 1, 1, 0x222222, testSP, nil)
		textItem, found := findItem(items, "text", 0, 0)
		if !found || textItem.Text != "世" || textItem.Width != 2 {
			subtest.Fatalf("wide item = %+v (found=%v), want text 世 width 2", textItem, found)
		}
		if _, found := findItem(items, "text", 0, 1); found {
			subtest.Fatal("continuation cell col 1 emitted text")
		}
		assertNoForegroundExcept(subtest, items, 1)
	})

	test.Run("wide continuation with invalid attribute", func(subtest *testing.T) {
		attributes := map[int]screen.Attr{
			0: {},
			1: {"bold": struct{}{}},
		}
		state := stateForTestRow(attributes, makeTestCell("世", 0), makeTestCell("", 1))
		_, err := Build(state)
		if err == nil {
			subtest.Fatal("Build returned nil error for invalid continuation highlight")
		}
		if !strings.Contains(err.Error(), "unsupported value type") {
			subtest.Fatalf("Build error = %q, want unsupported value type", err)
		}
	})

	test.Run("space followed by orphan continuation", func(subtest *testing.T) {
		attributes := map[int]screen.Attr{
			0: {},
			1: {"background": int64(0x222222)},
		}
		state := stateForTestRow(attributes,
			makeTestCell(" ", 0), makeTestCell("", 1), makeTestCell(" ", 0),
		)
		list, err := Build(state)
		if err != nil {
			subtest.Fatalf("Build: %v", err)
		}
		items := list.Layers[0].Items
		assertFillCoverage(subtest, items, 0, 3)
		assertNoForeground(subtest, items)
		fills := fillsForRow(items, 0)
		if len(fills) != 3 {
			subtest.Fatalf("fill count = %d, want 3", len(fills))
		}
		assertFill(subtest, fills[0], 0, 0, 1, testBG, testSP, nil)
		assertFill(subtest, fills[1], 0, 1, 1, 0x222222, testSP, nil)
		assertFill(subtest, fills[2], 0, 2, 1, testBG, testSP, nil)
	})

	test.Run("row starts with orphan continuation", func(subtest *testing.T) {
		attributes := map[int]screen.Attr{
			0: {},
			1: {"background": int64(0x222222)},
		}
		state := stateForTestRow(attributes,
			makeTestCell("", 1), makeTestCell(" ", 0), makeTestCell("A", 0),
		)
		list, err := Build(state)
		if err != nil {
			subtest.Fatalf("Build: %v", err)
		}
		items := list.Layers[0].Items
		assertFillCoverage(subtest, items, 0, 3)
		fills := fillsForRow(items, 0)
		if len(fills) != 2 {
			subtest.Fatalf("fill count = %d, want 2", len(fills))
		}
		assertFill(subtest, fills[0], 0, 0, 1, 0x222222, testSP, nil)
		assertFill(subtest, fills[1], 0, 1, 2, testBG, testSP, nil)
		textItem, found := findItem(items, "text", 0, 2)
		if !found || textItem.Text != "A" || textItem.Width != 1 {
			subtest.Fatalf("A item = %+v (found=%v), want text A width 1", textItem, found)
		}
		if _, found := findItem(items, "text", 0, 0); found {
			subtest.Fatal("orphan continuation col 0 emitted text")
		}
		assertNoForegroundExcept(subtest, items, 1)
	})
}

func assertNoForegroundExcept(test *testing.T, items []Item, want int) {
	test.Helper()
	foregroundCount := countKind(items, "text") + countKind(items, "box")
	if foregroundCount != want {
		test.Fatalf("foreground item count = %d, want %d", foregroundCount, want)
	}
}

func TestBoxRuneBoundaries(test *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{name: "solid box", text: "─", want: true},
		{name: "dashed box", text: "┄", want: false},
		{name: "arc", text: "╭", want: true},
		{name: "diagonal", text: "╱", want: true},
		{name: "block", text: "█", want: true},
		{name: "sextant", text: "\U0001FB00", want: true},
		{name: "powerline", text: "\ue0b0", want: true},
		{name: "multi rune grapheme", text: "─\u0301", want: false},
		{name: "plain text", text: "世", want: false},
	}
	for _, testCase := range cases {
		testCase := testCase
		test.Run(testCase.name, func(subtest *testing.T) {
			if got := boxRune(testCase.text); got != testCase.want {
				subtest.Fatalf("boxRune(%q) = %v, want %v", testCase.text, got, testCase.want)
			}
		})
	}
}

func TestBuildErrorsAndBoundaryInputs(test *testing.T) {
	test.Run("missing root grid", func(subtest *testing.T) {
		state := &screen.State{
			Grids:      map[int]*screen.Grid{2: makeBlankTestGrid(2, 1)},
			Placements: map[int]screen.Placement{},
			Cursor:     screen.Cursor{Grid: 2},
		}
		if _, err := Build(state); err == nil {
			subtest.Fatal("Build returned nil error")
		}
	})

	test.Run("nil root grid pointer", func(subtest *testing.T) {
		state := &screen.State{
			Grids:      map[int]*screen.Grid{1: nil},
			Placements: map[int]screen.Placement{},
		}
		if _, err := Build(state); err == nil {
			subtest.Fatal("Build returned nil error")
		}
	})

	test.Run("nil layer grid pointer", func(subtest *testing.T) {
		state := makeTestState(makeBlankTestGrid(2, 1))
		state.Grids[2] = nil
		state.Placements[2] = screen.Placement{Kind: screen.PlacedFloat, ScreenRow: 0, ScreenCol: 0, CompIndex: 1}
		if _, err := Build(state); err == nil {
			subtest.Fatal("Build returned nil error")
		}
	})

	test.Run("negative root size", func(subtest *testing.T) {
		state := makeTestState(&screen.Grid{Width: -1, Height: 1, Cells: [][]screen.Cell{{}}})
		if _, err := Build(state); err == nil {
			subtest.Fatal("Build returned nil error")
		}
	})

	test.Run("negative layer size", func(subtest *testing.T) {
		state := makeTestState(makeBlankTestGrid(2, 1))
		state.Grids[2] = &screen.Grid{Width: -1, Height: 1, Cells: [][]screen.Cell{{}}}
		state.Placements[2] = screen.Placement{Kind: screen.PlacedFloat, ScreenRow: 0, ScreenCol: 0, CompIndex: 1}
		if _, err := Build(state); err == nil {
			subtest.Fatal("Build returned nil error")
		}
	})

	test.Run("truncated cell rows", func(subtest *testing.T) {
		state := makeTestState(&screen.Grid{
			Width:  3,
			Height: 2,
			Cells:  [][]screen.Cell{makePaddedTestRow(3, makeTestCell("a", 0))},
		})
		if _, err := Build(state); err == nil {
			subtest.Fatal("Build returned nil error")
		}
	})

	test.Run("truncated cell row", func(subtest *testing.T) {
		state := makeTestState(&screen.Grid{
			Width:  3,
			Height: 1,
			Cells:  [][]screen.Cell{makeTestRow(makeTestCell("a", 0), makeTestCell("b", 0))},
		})
		if _, err := Build(state); err == nil {
			subtest.Fatal("Build returned nil error")
		}
	})

	invalidAttributes := []struct {
		name      string
		attribute screen.Attr
	}{
		{name: "invalid foreground", attribute: screen.Attr{"foreground": float64(1)}},
		{name: "invalid background", attribute: screen.Attr{"background": "not-a-color"}},
		{name: "invalid reverse", attribute: screen.Attr{"reverse": "yes"}},
		{name: "invalid unknown", attribute: screen.Attr{"unknown": []int{1}}},
	}
	for _, testCase := range invalidAttributes {
		testCase := testCase
		test.Run(testCase.name, func(subtest *testing.T) {
			attributes := map[int]screen.Attr{0: {}, 1: testCase.attribute}
			state := stateForTestRow(attributes, makeTestCell("a", 1))
			_, err := Build(state)
			if err == nil {
				subtest.Fatal("Build returned nil error")
			}
			if !strings.Contains(err.Error(), "unsupported value type") {
				subtest.Fatalf("Build error = %q, want unsupported value type", err)
			}
		})
	}

	test.Run("nil foreground", func(subtest *testing.T) {
		attributes := map[int]screen.Attr{
			0: {},
			1: {"foreground": nil, "background": nil, "special": nil},
		}
		state := stateForTestRow(attributes, makeTestCell("a", 1))
		list, err := Build(state)
		if err != nil {
			subtest.Fatalf("Build: %v", err)
		}
		items := list.Layers[0].Items
		assertFill(subtest, fillsForRow(items, 0)[0], 0, 0, 1, testBG, testSP, nil)
		textItem, found := findItem(items, "text", 0, 0)
		if !found {
			subtest.Fatal("missing text item")
		}
		if textItem.FG != testFG || textItem.BG != testBG || textItem.SP != testSP {
			subtest.Fatalf("text colors = (%#x, %#x, %#x), want defaults", textItem.FG, textItem.BG, textItem.SP)
		}
	})
}

func TestBuildHiddenLayerDoesNotEmitCursor(test *testing.T) {
	state := makeTestState(makeBlankTestGrid(3, 2))
	state.Grids[2] = makeBlankTestGrid(2, 1)
	state.Cursor = screen.Cursor{Grid: 2, Row: 0, Col: 1}

	list, err := Build(state)
	if err != nil {
		test.Fatalf("Build: %v", err)
	}
	if len(list.Layers) != 1 {
		test.Fatalf("len(Layers) = %d, want 1", len(list.Layers))
	}
	if countKind(list.Layers[0].Items, "cursor") != 0 {
		test.Fatal("hidden layer emitted cursor")
	}
}

func TestBuildClipForOutOfBoundsAndZeroSizedFloats(test *testing.T) {
	rootGrid := makeBlankTestGrid(10, 10)
	positiveGrid := makeBlankTestGrid(3, 2)
	zeroGrid := makeBlankTestGrid(0, 0)
	negativeGrid := makeBlankTestGrid(2, 2)
	state := &screen.State{
		Seq: 1,
		Grids: map[int]*screen.Grid{
			1: rootGrid,
			2: positiveGrid,
			3: zeroGrid,
			4: negativeGrid,
		},
		Attrs:         map[int]screen.Attr{0: {}},
		DefaultColors: screen.Colors{FG: testFG, BG: testBG, SP: testSP},
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedFloat, ScreenRow: 20, ScreenCol: 20, ZIndex: 1, CompIndex: 1},
			3: {Kind: screen.PlacedFloat, ScreenRow: 3, ScreenCol: 4, ZIndex: 2, CompIndex: 2},
			4: {Kind: screen.PlacedFloat, ScreenRow: -5, ScreenCol: -5, ZIndex: 3, CompIndex: 3},
		},
		Cursor: screen.Cursor{Grid: 1},
	}

	list, err := Build(state)
	if err != nil {
		test.Fatalf("Build: %v", err)
	}
	wantOrder := []int{1, 2, 3, 4}
	wantClips := map[int]Rect{
		1: {X: 0, Y: 0, W: 10, H: 10},
		2: {X: 20, Y: 20, W: 0, H: 0},
		3: {X: 4, Y: 3, W: 0, H: 0},
		4: {X: 0, Y: 0, W: 0, H: 0},
	}
	if len(list.Layers) != len(wantOrder) {
		test.Fatalf("len(Layers) = %d, want %d", len(list.Layers), len(wantOrder))
	}
	allItems := make([]Item, 0)
	for index, layer := range list.Layers {
		if layer.Grid != wantOrder[index] {
			test.Fatalf("layer[%d].Grid = %d, want %d", index, layer.Grid, wantOrder[index])
		}
		expectedClip, found := wantClips[layer.Grid]
		if !found {
			test.Fatalf("unexpected layer grid %d", layer.Grid)
		}
		if layer.Clip != expectedClip {
			test.Fatalf("layer %d Clip = %+v, want %+v", layer.Grid, layer.Clip, expectedClip)
		}
		grid := state.Grids[layer.Grid]
		if layer.Width != grid.Width || layer.Height != grid.Height {
			test.Fatalf("layer %d size = %dx%d, want %dx%d", layer.Grid, layer.Width, layer.Height, grid.Width, grid.Height)
		}
		allItems = append(allItems, layer.Items...)
	}
	if countKind(allItems, "cursor") != 1 {
		test.Fatalf("cursor count = %d, want 1", countKind(allItems, "cursor"))
	}
}

func TestDrawListJSONKeepsUTF8AndHTMLCharacters(test *testing.T) {
	list := &List{
		Version: 1,
		Seq:     1,
		Width:   1,
		Height:  1,
		Layers: []Layer{{
			Grid:   1,
			Width:  1,
			Height: 1,
			Items: []Item{{
				Kind:   "text",
				Row:    0,
				Col:    0,
				Width:  1,
				Height: 1,
				Text:   "<>&中文",
			}},
		}},
	}
	raw, err := JSON(list)
	if err != nil {
		test.Fatalf("JSON: %v", err)
	}
	if !json.Valid(raw) {
		test.Fatalf("JSON returned invalid bytes: %s", raw)
	}
	if !bytes.Contains(raw, []byte("<>&中文")) {
		test.Fatalf("JSON escaped or dropped raw text: %s", raw)
	}
	for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`, `\u4e16`} {
		if bytes.Contains(raw, []byte(escaped)) {
			test.Fatalf("JSON contains escaped sequence %s: %s", escaped, raw)
		}
	}
}

func TestBuildCursorWideCharHighContrastAndModes(test *testing.T) {
	row := makeTestRow(
		makeTestCell("本", 0), makeTestCell("", 0),
		makeTestCell("x", 1),
		makeTestCell("╭", 0),
		makeTestCell(" ", 0),
	)
	makeState := func(defFG, defBG int64, mode string, col int) *screen.State {
		grid := &screen.Grid{Width: len(row), Height: 1, Cells: [][]screen.Cell{append([]screen.Cell(nil), row...)}}
		return &screen.State{
			Seq:   1,
			Grids: map[int]*screen.Grid{1: grid},
			Attrs: map[int]screen.Attr{
				0: {"bold": true},
				1: {"foreground": int64(0xd8d8d8)}, // low contrast on light theme
			},
			DefaultColors: screen.Colors{FG: defFG, BG: defBG, SP: 0xff0000},
			Placements:    map[int]screen.Placement{},
			Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: col},
			Mode:          mode,
		}
	}

	test.Run("light theme wide CJK block cursor", func(t *testing.T) {
		list, err := Build(makeState(0x24292f, 0xffffff, "normal", 0))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		cursor, found := findItem(list.Layers[0].Items, "cursor", 0, 0)
		if !found {
			t.Fatal("missing cursor item")
		}
		if cursor.Width != 2 || cursor.Height != 1 {
			t.Fatalf("cursor size = %dx%d, want 2x1", cursor.Width, cursor.Height)
		}
		if cursor.Text != "本" || cursor.Rune != "" {
			t.Fatalf("cursor glyph = (text=%q, rune=%q), want text=\"本\"", cursor.Text, cursor.Rune)
		}
		if cursor.BG != 0x24292f || cursor.FG != 0xffffff {
			t.Fatalf("cursor colors = (fg=%#x, bg=%#x), want (0xffffff, 0x24292f)", cursor.FG, cursor.BG)
		}
		if !reflect.DeepEqual(cursor.Attrs, []string{"bold"}) {
			t.Fatalf("cursor Attrs = %v, want [bold]", cursor.Attrs)
		}
	})

	test.Run("dark theme wide CJK block cursor", func(t *testing.T) {
		list, err := Build(makeState(0xcdd6f4, 0x1e1e2e, "normal", 0))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		cursor, found := findItem(list.Layers[0].Items, "cursor", 0, 0)
		if !found {
			t.Fatal("missing cursor item")
		}
		if cursor.Width != 2 || cursor.Text != "本" || cursor.BG != 0xcdd6f4 || cursor.FG != 0x1e1e2e {
			t.Fatalf("dark wide cursor = %+v", cursor)
		}
	})

	test.Run("light theme low-contrast cell boosts cursor contrast", func(t *testing.T) {
		list, err := Build(makeState(0x24292f, 0xffffff, "normal", 2))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		cursor, found := findItem(list.Layers[0].Items, "cursor", 0, 2)
		if !found {
			t.Fatal("missing cursor item")
		}
		if cursor.Width != 1 || cursor.Text != "x" {
			t.Fatalf("cursor = %+v, want width 1 text \"x\"", cursor)
		}
		if cursor.BG != 0x24292f || cursor.FG != 0xffffff {
			t.Fatalf("boosted cursor colors = (fg=%#x, bg=%#x), want (0xffffff, 0x24292f)", cursor.FG, cursor.BG)
		}
	})

	test.Run("unset default colors never produce zero-zero cursor", func(t *testing.T) {
		list, err := Build(makeState(-1, -1, "normal", 4))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		cursor, found := findItem(list.Layers[0].Items, "cursor", 0, 4)
		if !found {
			t.Fatal("missing cursor item")
		}
		if cursor.FG == 0 && cursor.BG == 0 {
			t.Fatalf("cursor colors both zero: %+v", cursor)
		}
		if absInt64(relativeLuma(cursor.BG)-relativeLuma(cursor.FG)) < 100 {
			t.Fatalf("cursor contrast too low: fg=%#x bg=%#x", cursor.FG, cursor.BG)
		}
	})

	test.Run("box rune under block cursor uses Rune field", func(t *testing.T) {
		list, err := Build(makeState(0x24292f, 0xffffff, "normal", 3))
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		cursor, found := findItem(list.Layers[0].Items, "cursor", 0, 3)
		if !found {
			t.Fatal("missing cursor item")
		}
		if cursor.Width != 1 || cursor.Rune != "╭" || cursor.Text != "" {
			t.Fatalf("box cursor = %+v, want Rune=\"╭\"", cursor)
		}
	})

	test.Run("insert and replace modes tag cursor shape without covering text", func(t *testing.T) {
		insertList, err := Build(makeState(0x24292f, 0xffffff, "insert", 0))
		if err != nil {
			t.Fatalf("Build insert: %v", err)
		}
		insertCursor, found := findItem(insertList.Layers[0].Items, "cursor", 0, 0)
		if !found {
			t.Fatal("missing insert cursor")
		}
		if insertCursor.Width != 2 || insertCursor.Text != "" || insertCursor.Rune != "" || !reflect.DeepEqual(insertCursor.Attrs, []string{"cursor_beam"}) {
			t.Fatalf("insert cursor = %+v, want width 2 beam without text", insertCursor)
		}

		replaceList, err := Build(makeState(0x24292f, 0xffffff, "replace", 0))
		if err != nil {
			t.Fatalf("Build replace: %v", err)
		}
		replaceCursor, found := findItem(replaceList.Layers[0].Items, "cursor", 0, 0)
		if !found {
			t.Fatal("missing replace cursor")
		}
		if replaceCursor.Width != 2 || replaceCursor.Text != "" || !reflect.DeepEqual(replaceCursor.Attrs, []string{"cursor_underline"}) {
			t.Fatalf("replace cursor = %+v, want width 2 underline without text", replaceCursor)
		}
	})
}
