package render_test

import (
	"bytes"
	"fmt"
	imagecolor "image/color"
	"math"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/screen"
)

func putRunes(cells []screen.Cell, startCol int, text string, hl int) int {
	col := startCol
	for _, r := range text {
		if col >= len(cells) {
			break
		}
		w := 1
		if isTestWideRune(r) {
			w = 2
		}
		if col+w > len(cells) {
			break
		}
		cells[col] = screen.Cell{Text: string(r), HL: hl}
		if w == 2 {
			cells[col+1] = screen.Cell{Text: "", HL: hl}
		}
		col += w
	}
	return col
}

func isTestWideRune(r rune) bool {
	return (r >= 0x4e00 && r <= 0x9fff) || r == '🌍'
}

func newBlankGrid(width, height, hl int) *screen.Grid {
	cells := make([][]screen.Cell, height)
	for r := 0; r < height; r++ {
		row := make([]screen.Cell, width)
		for c := 0; c < width; c++ {
			row[c] = screen.Cell{Text: " ", HL: hl}
		}
		cells[r] = row
	}
	return &screen.Grid{Width: width, Height: height, Cells: cells}
}

func buildDenseMixedCodeState(seq uint64, editedRow2 bool) *screen.State {
	const (
		rootW = 200
		rootH = 60
	)
	root := newBlankGrid(rootW, rootH, 0)
	for r := 0; r < rootH; r++ {
		fnName := fmt.Sprintf("renderDenseRow%02d(ctx,frame,state)", r)
		if r == 2 && editedRow2 {
			fnName = "editedDenseRow02(ctx,frame,state)"
		}
		col := 0
		col = putRunes(root.Cells[r], col, "func", 1)
		col++ // space
		col = putRunes(root.Cells[r], col, fnName, 0)
		col++ // space
		col = putRunes(root.Cells[r], col, "//comment_payload_0123456789_abcdefghijklmnopqrstuvwxyz", 2)
		col++ // space
		col = putRunes(root.Cells[r], col, fmt.Sprintf("underlined_metric_token_%02d_abcdef", r), 3)
		col++ // space
		col = putRunes(root.Cells[r], col, "final_ascii_segment_xyz_1234567890", 0)
		if r == 2 || r == 15 {
			col = 165
			col = putRunes(root.Cells[r], col, "A世B界C🌍D", 0)
			col++
			putRunes(root.Cells[r], col, "┌─┐", 1)
		}
	}

	floatGrid := newBlankGrid(28, 6, 4)
	putRunes(floatGrid.Cells[0], 0, "┌──────────────────────────┐", 1)
	putRunes(floatGrid.Cells[1], 0, "│", 1)
	putRunes(floatGrid.Cells[1], 2, "float_status_window_ok", 4)
	putRunes(floatGrid.Cells[1], 27, "│", 1)
	putRunes(floatGrid.Cells[5], 0, "└──────────────────────────┘", 1)

	return &screen.State{
		Seq: seq,
		Grids: map[int]*screen.Grid{
			1: root,
			2: floatGrid,
		},
		Attrs: map[int]screen.Attr{
			0: {},
			1: {"foreground": int64(0xcba6f7), "bold": true},
			2: {"foreground": int64(0xa6e3a1), "italic": true},
			3: {"foreground": int64(0x89b4fa), "background": int64(0x25273a), "underline": true, "special": int64(0xf38ba8)},
			4: {"foreground": int64(0xcdd6f4), "background": int64(0x313244)},
		},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e, SP: 0xf38ba8},
		Placements: map[int]screen.Placement{
			2: {
				Kind:      screen.PlacedFloat,
				Win:       1002,
				Anchor:    "NW",
				ZIndex:    50,
				CompIndex: 10,
				ScreenRow: 45,
				ScreenCol: 165,
			},
		},
		Cursor: screen.Cursor{Grid: 1, Row: 5, Col: 5},
		Mode:   "normal",
	}
}

func TestM3RenderDenseRunMergeAndRowCache(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	state := buildDenseMixedCodeState(1, false)
	dl, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("drawlist.Build: %v", err)
	}

	// 1. Verify drawlist.Build contract is untouched (every non-space cell remains an individual text/box item).
	textItems := 0
	boxItems := 0
	cursorItems := 0
	for _, layer := range dl.Layers {
		for _, item := range layer.Items {
			switch item.Kind {
			case "text":
				textItems++
				if utf8.RuneCountInString(item.Text) != 1 {
					t.Fatalf("drawlist.Build contract broken: text item %q has %d runes, want 1", item.Text, utf8.RuneCountInString(item.Text))
				}
			case "box":
				boxItems++
			case "cursor":
				cursorItems++
			}
		}
	}
	if textItems < 5000 {
		t.Fatalf("dense 200x60 fixture has only %d text items, want >= 5000", textItems)
	}
	if boxItems == 0 || cursorItems != 1 {
		t.Fatalf("dense fixture items: box=%d cursor=%d, want box>0 and cursor=1", boxItems, cursorItems)
	}

	// 2. Plan determinism and <= 25% glyphs command ratio via ASCII run merging.
	frame1, err := planner.Plan(dl)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	frame2, err := planner.Plan(dl)
	if err != nil {
		t.Fatalf("second Plan: %v", err)
	}
	if !reflect.DeepEqual(frame1, frame2) {
		t.Fatal("Planner.Plan is not deterministic on identical dense 200x60 drawlist")
	}

	glyphCmds := 0
	boxCmds := 0
	cursorCmds := 0
	wideSeen := map[string]int{}
	for _, cmd := range frame1.Commands {
		switch cmd.Op {
		case "glyphs":
			glyphCmds++
			if !approxEqual(cmd.H, cell.Height, 0.5) {
				t.Errorf("glyphs cmd %q has H=%v, want %v (must stay single-row)", cmd.Text, cmd.H, cell.Height)
			}
			if cmd.Text == "世" || cmd.Text == "界" || cmd.Text == "🌍" {
				wideSeen[cmd.Text]++
				if !approxEqual(cmd.W, 2*cell.Width, 0.5) {
					t.Errorf("wide glyph %q has W=%v, want 2*cell.Width (%v)", cmd.Text, cmd.W, 2*cell.Width)
				}
				continue
			}
			if len(cmd.Text) > 1 {
				for i := 0; i < len(cmd.Text); i++ {
					b := cmd.Text[i]
					if b < 0x21 || b > 0x7e {
						t.Errorf("merged glyphs run %q contains non-printable-ASCII byte 0x%02x (wide/non-ASCII/space must not merge)", cmd.Text, b)
						break
					}
				}
				wantW := float32(len(cmd.Text)) * cell.Width
				if !approxEqual(cmd.W, wantW, 0.5) {
					t.Errorf("merged ASCII run %q has W=%v, want %v", cmd.Text, cmd.W, wantW)
				}
			}
		case "box":
			boxCmds++
		case "cursor":
			cursorCmds++
		}
	}

	if glyphCmds*4 > textItems {
		t.Fatalf("run merging ineffective: Plan emitted %d glyphs commands for %d non-space text items (%.1f%%), want <= 25%%",
			glyphCmds, textItems, float64(glyphCmds)*100/float64(textItems))
	}
	for _, wide := range []string{"世", "界", "🌍"} {
		if wideSeen[wide] < 2 {
			t.Errorf("wide glyph %q standalone count = %d, want >= 2 (must not merge with neighbors)", wide, wideSeen[wide])
		}
	}
	if boxCmds != boxItems {
		t.Errorf("box commands = %d, want %d (box items must not be dropped)", boxCmds, boxItems)
	}
	if cursorCmds != 1 {
		t.Errorf("cursor commands = %d, want 1", cursorCmds)
	}

	// 3. View.Draw determinism, non-uniform output, and bounded row-cache delta on single-row edit.
	view := render.NewView()
	view.SetFrame(frame1)
	tester := ui.NewTester(func(c *ui.Context) {
		view.Draw(c)
	}, 480, 240)
	img1 := tester.Image()
	if img1 == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	if isUniformImage(img1.Pix) {
		t.Fatal("rendered dense frame is completely uniform")
	}
	pix1 := append([]byte(nil), img1.Pix...)
	s1 := view.ShapedRows()
	if s1 <= 0 {
		t.Fatalf("initial ShapedRows() = %d, want > 0", s1)
	}

	// Identical repeat frame (planned cleanly so metadataFor resolves the same Seq/attrs/clip)
	// must produce byte-identical pixels and zero additional shaping.
	repeatState := buildDenseMixedCodeState(2, false)
	repeatDL, err := drawlist.Build(repeatState)
	if err != nil {
		t.Fatalf("drawlist.Build(repeat): %v", err)
	}
	repeatFrame, err := planner.Plan(repeatDL)
	if err != nil {
		t.Fatalf("Plan(repeat): %v", err)
	}
	view.SetFrame(repeatFrame)
	tester.Frame()
	img2 := tester.Image()
	if img2 == nil || !bytes.Equal(pix1, img2.Pix) {
		t.Fatal("View.Draw is not pixel-deterministic across identical frames")
	}
	s2 := view.ShapedRows()
	if s2 != s1 {
		t.Fatalf("ShapedRows() increased on identical frame: %d -> %d", s1, s2)
	}

	// Edit a single row (row 2): ShapedRows delta must stay within [1, editedRowRuns] and far below full screen.
	editedState := buildDenseMixedCodeState(3, true)
	editedDL, err := drawlist.Build(editedState)
	if err != nil {
		t.Fatalf("drawlist.Build(edited): %v", err)
	}
	editedFrame, err := planner.Plan(editedDL)
	if err != nil {
		t.Fatalf("Plan(edited): %v", err)
	}
	editedRowRuns := 0
	for _, cmd := range editedFrame.Commands {
		if cmd.Op == "glyphs" && approxEqual(cmd.Y, 2*cell.Height, 0.5) {
			editedRowRuns++
		}
	}
	if editedRowRuns == 0 {
		t.Fatal("edited row 2 has zero glyphs commands")
	}

	view.SetFrame(editedFrame)
	tester.Frame()
	_ = tester.Image()
	s3 := view.ShapedRows()
	delta := s3 - s2
	if delta < 1 || delta > editedRowRuns {
		t.Fatalf("single-row edit ShapedRows delta = %d (from %d to %d), want in [1, %d]", delta, s2, s3, editedRowRuns)
	}
	if delta*2 >= s1 {
		t.Fatalf("single-row edit ShapedRows delta = %d is not much smaller than initial full-screen ShapedRows = %d", delta, s1)
	}
}

func TestM3RenderRunMergeStyleAndWideBoundaries(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	const (
		w = 40
		h = 6
	)
	grid := newBlankGrid(w, h, 0)
	// Row 0: two same-style ASCII runs separated by a space.
	putRunes(grid.Cells[0], 0, "alpha_run", 0)
	putRunes(grid.Cells[0], 10, "beta_run", 0)
	// Row 1: adjacent style changes with zero spaces between them.
	col := 0
	col = putRunes(grid.Cells[1], col, "norm", 0)
	col = putRunes(grid.Cells[1], col, "bold", 1)
	col = putRunes(grid.Cells[1], col, "ital", 2)
	col = putRunes(grid.Cells[1], col, "fgch", 3)
	col = putRunes(grid.Cells[1], col, "bgch", 4)
	col = putRunes(grid.Cells[1], col, "undl", 5)
	putRunes(grid.Cells[1], col, "tail", 0)
	// Row 2: wide CJK and emoji immediately adjacent to ASCII with identical HL.
	putRunes(grid.Cells[2], 0, "A世B界C🌍D", 0)
	// Row 3: box drawing immediately adjacent to ASCII.
	putRunes(grid.Cells[3], 0, "┌─┐after_box", 0)
	// Row 4 end + Row 5 start: must never merge across rows.
	putRunes(grid.Cells[4], w-8, "row4_end", 0)
	putRunes(grid.Cells[5], 0, "row5_start", 0)

	state := &screen.State{
		Seq:   10,
		Grids: map[int]*screen.Grid{1: grid},
		Attrs: map[int]screen.Attr{
			0: {},
			1: {"bold": true},
			2: {"italic": true},
			3: {"foreground": int64(0xf38ba8)},
			4: {"background": int64(0x313244)},
			5: {"underline": true},
		},
		DefaultColors: screen.Colors{FG: 0xcdd6f4, BG: 0x1e1e2e, SP: 0xf38ba8},
		Placements:    map[int]screen.Placement{},
		Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: 0},
		Mode:          "normal",
	}

	dl, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("drawlist.Build: %v", err)
	}
	frame, err := planner.Plan(dl)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	type cellCoord struct{ row, col int }
	expectedText := make(map[cellCoord]drawlist.Item)
	for _, item := range dl.Layers[0].Items {
		if item.Kind == "text" {
			expectedText[cellCoord{item.Row, item.Col}] = item
		}
	}

	covered := make(map[cellCoord]bool, len(expectedText))
	row0Runs := 0
	row1Runs := 0
	boxRunes := map[string]bool{}
	wideStandalone := map[string]bool{}
	foundCursor := false

	for _, cmd := range frame.Commands {
		switch cmd.Op {
		case "box":
			boxRunes[cmd.Rune] = true
		case "cursor":
			foundCursor = true
		case "glyphs":
			row := int(math.Round(float64(cmd.Y / cell.Height)))
			startCol := int(math.Round(float64(cmd.X / cell.Width)))
			if row == 0 {
				row0Runs++
			}
			if row == 1 {
				row1Runs++
			}
			if !approxEqual(cmd.H, cell.Height, 0.5) {
				t.Errorf("glyphs %q has H=%v, want %v", cmd.Text, cmd.H, cell.Height)
			}

			if cmd.Text == "世" || cmd.Text == "界" || cmd.Text == "🌍" {
				wideStandalone[cmd.Text] = true
				if !approxEqual(cmd.W, 2*cell.Width, 0.5) {
					t.Errorf("wide glyph %q W=%v, want %v", cmd.Text, cmd.W, 2*cell.Width)
				}
				coord := cellCoord{row, startCol}
				item, ok := expectedText[coord]
				if !ok || item.Text != cmd.Text {
					t.Errorf("wide glyph %q at (%d,%d) does not match source item %+v", cmd.Text, row, startCol, item)
				}
				if covered[coord] {
					t.Errorf("duplicate coverage for cell (%d,%d)", row, startCol)
				}
				covered[coord] = true
				continue
			}

			// Single-cell or merged ASCII run: verify every cell belongs to the same row & style.
			if len(cmd.Text) > 1 {
				for i := 0; i < len(cmd.Text); i++ {
					if cmd.Text[i] < 0x21 || cmd.Text[i] > 0x7e {
						t.Fatalf("merged run %q contains non-printable-ASCII byte 0x%02x", cmd.Text, cmd.Text[i])
					}
				}
				if !approxEqual(cmd.W, float32(len(cmd.Text))*cell.Width, 0.5) {
					t.Errorf("merged run %q W=%v, want %v", cmd.Text, cmd.W, float32(len(cmd.Text))*cell.Width)
				}
				firstItem, ok := expectedText[cellCoord{row, startCol}]
				if !ok {
					t.Fatalf("merged run %q starts at unexpected cell (%d,%d)", cmd.Text, row, startCol)
				}
				for offset := 0; offset < len(cmd.Text); offset++ {
					coord := cellCoord{row, startCol + offset}
					item, ok := expectedText[coord]
					if !ok {
						t.Fatalf("merged run %q references missing cell (%d,%d)", cmd.Text, coord.row, coord.col)
					}
					if item.Text != cmd.Text[offset:offset+1] {
						t.Errorf("merged run %q offset %d: cell (%d,%d) has %q, want %q", cmd.Text, offset, coord.row, coord.col, item.Text, cmd.Text[offset:offset+1])
					}
					if item.FG != firstItem.FG || item.BG != firstItem.BG || item.SP != firstItem.SP || !reflect.DeepEqual(item.Attrs, firstItem.Attrs) {
						t.Errorf("merged run %q crossed style boundary at (%d,%d): %+v vs %+v", cmd.Text, coord.row, coord.col, item, firstItem)
					}
					if covered[coord] {
						t.Errorf("duplicate coverage for cell (%d,%d)", coord.row, coord.col)
					}
					covered[coord] = true
				}
			} else {
				coord := cellCoord{row, startCol}
				item, ok := expectedText[coord]
				if !ok || item.Text != cmd.Text {
					t.Errorf("single glyph %q at (%d,%d) does not match source item %+v", cmd.Text, row, startCol, item)
				}
				if covered[coord] {
					t.Errorf("duplicate coverage for cell (%d,%d)", coord.row, coord.col)
				}
				covered[coord] = true
			}
		}
	}

	if len(covered) != len(expectedText) {
		t.Fatalf("covered %d text cells, want %d (dropped or duplicated cells)", len(covered), len(expectedText))
	}
	if row0Runs != 2 {
		t.Errorf("row 0 (\"alpha_run beta_run\") produced %d glyphs commands, want 2 merged runs", row0Runs)
	}
	if row1Runs != 7 {
		t.Errorf("row 1 (7 adjacent style segments) produced %d glyphs commands, want 7", row1Runs)
	}
	for _, wide := range []string{"世", "界", "🌍"} {
		if !wideStandalone[wide] {
			t.Errorf("wide glyph %q did not stay standalone", wide)
		}
	}
	for _, wantBox := range []string{"┌", "─", "┐"} {
		if !boxRunes[wantBox] {
			t.Errorf("missing box command for %q", wantBox)
		}
	}
	if !foundCursor {
		t.Error("missing cursor command")
	}
}

func TestM3RenderClipGroupNoLeak(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	planner := render.NewPlanner(resolver)

	const (
		rootW = 24
		rootH = 5
	)
	floatItems := []drawlist.Item{
		{Kind: "fill", Row: 0, Col: 0, Width: 18, Height: 3, BG: 0x202020},
	}
	// Multiple adjacent text items sharing the same clip, extending beyond clip right & bottom edges.
	for c := 0; c < 15; c++ {
		floatItems = append(floatItems, drawlist.Item{
			Kind: "text", Row: 0, Col: c, Width: 1, Height: 1, Text: "W", FG: 0x00ff00, BG: 0x202020,
		})
	}
	for c := 0; c < 5; c++ {
		floatItems = append(floatItems, drawlist.Item{
			Kind: "text", Row: 1, Col: c, Width: 1, Height: 1, Text: "M", FG: 0x00ff00, BG: 0x202020,
		})
	}
	// Box straddling the clip right boundary (screen col 7 inside, col 8 outside).
	floatItems = append(floatItems, drawlist.Item{
		Kind: "box", Row: 1, Col: 5, Width: 2, Height: 1, Rune: "█", FG: 0x00ff00, BG: 0x202020,
	})
	// Row 2 is strictly below clip (Y=1, H=2 => visible screen rows [1, 3), row 2 -> screen row 3).
	for c := 0; c < 6; c++ {
		floatItems = append(floatItems, drawlist.Item{
			Kind: "text", Row: 2, Col: c, Width: 1, Height: 1, Text: "X", FG: 0x00ff00, BG: 0x202020,
		})
	}

	dl := &drawlist.List{
		Version: 1,
		Seq:     20,
		Width:   rootW,
		Height:  rootH,
		Layers: []drawlist.Layer{
			{
				Grid:      1,
				ScreenRow: 0,
				ScreenCol: 0,
				Width:     rootW,
				Height:    rootH,
				Clip:      drawlist.Rect{X: 0, Y: 0, W: rootW, H: rootH},
				Items: []drawlist.Item{
					{Kind: "fill", Row: 0, Col: 0, Width: rootW, Height: rootH, BG: 0x0a1020},
				},
			},
			{
				Grid:      2,
				ScreenRow: 1,
				ScreenCol: 2,
				Width:     18,
				Height:    3,
				Clip:      drawlist.Rect{X: 2, Y: 1, W: 6, H: 2},
				Items:     floatItems,
			},
		},
	}

	frame, err := planner.Plan(dl)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// Verify the merged float row 0 stays within clip visible columns [2, 8) (at most 6 chars wide).
	for _, cmd := range frame.Commands {
		if cmd.Op == "glyphs" && approxEqual(cmd.Y, 1*cell.Height, 0.5) {
			if cmd.X+cmd.W > float32(8)*cell.Width+0.5 {
				t.Errorf("float row 0 glyphs run %q extends past clip right edge: X+W=%v > %v", cmd.Text, cmd.X+cmd.W, float32(8)*cell.Width)
			}
		}
	}

	imgW := int(math.Floor(float64(rootW) * float64(cell.Width)))
	imgH := int(math.Floor(float64(rootH) * float64(cell.Height)))
	view := render.NewView()
	view.SetFrame(frame)
	tester := ui.NewTester(func(c *ui.Context) {
		view.Draw(c)
	}, imgW, imgH)
	img := tester.Image()
	if img == nil {
		t.Fatal("Tester.Image() returned nil")
	}

	clipX0 := int(math.Floor(float64(2 * cell.Width)))
	clipY0 := int(math.Floor(float64(1 * cell.Height)))
	clipX1 := int(math.Ceil(float64(8 * cell.Width)))
	clipY1 := int(math.Ceil(float64(3 * cell.Height)))

	insideGreen := false
	for y := clipY0; y < clipY1 && y < imgH; y++ {
		for x := clipX0; x < clipX1 && x < imgW; x++ {
			px := img.RGBAAt(x, y)
			if px.G > 180 && px.G > px.R+100 && px.G > px.B+100 {
				insideGreen = true
				break
			}
		}
		if insideGreen {
			break
		}
	}
	if !insideGreen {
		t.Fatal("no green foreground rendered inside float clip region")
	}

	rootBG := imagecolor.RGBA{R: 0x0a, G: 0x10, B: 0x20, A: 0xff}
	checkNoLeak := func(name string, x0, y0, x1, y1 int) {
		t.Helper()
		for y := y0; y < y1 && y < imgH; y++ {
			for x := x0; x < x1 && x < imgW; x++ {
				if px := img.RGBAAt(x, y); px != rootBG {
					t.Fatalf("%s leaked pixel at (%d,%d): got %+v, want root background %+v", name, x, y, px, rootBG)
				}
			}
		}
	}
	checkNoLeak("right of float clip", clipX1+2, clipY0, imgW, clipY1)
	checkNoLeak("below float clip", clipX0, clipY1+2, imgW, imgH)
}
