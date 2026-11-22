// Ticket 43: deterministic render and frame-cache P0 regression set.
// Covered (5 functions / 5 IDs): neovide#481, neovim#7479, neovim#12866, macvim#557, neovide#1312.
// Explicitly deferred (31 total across this set; registered here instead of wiki/m3-exit.md,
// which is outside this ticket's write scope):
//   - 文字渲染 (6): equalsraf/neovim-qt#166 (M4.4 连字), neovim#32539 (二期变字号 statusline),
//     vhakulinen/gnvim#85 (Linux Pango), neovide#589, neovide#1231, rohit-px2/nvui#87 (L3 人眼 A/B).
//   - macOS 应用壳 (14): neovide#1332 (M2.1), neovide#1586 (M2.2), neovide#2378 (M2),
//     macvim#312, macvim#497 (M2.4), neovim#3344, neovim#11393 (TUI 终端协议不适用),
//     macvim#525, macvim#947, macvim#1119, neovide#459, neovide#1859, neovide#2384, vimr#683
//     (历史平台崩溃无现代复现).
//   - 性能 (1): neovim#6950 (上游 jemalloc 5.0 分配器 bug，不适用).
//   - 动画 (3): neovide#850, neovim#279, neovim#8715 (全部 M4).
//   - 分发 (7): neovim-qt#771, macvim#552, macvim#952, neovide#1679, neovim#18504,
//     neovim#9630, gnvim#219 (全部 M5 签名公证/dmg/cask).
//
// 13 covered P0 IDs + 31 deferred = 44/44; no subjective or milestone-specific scenario is forced.
package render_test

import (
	"bytes"
	"fmt"
	"image"
	imagecolor "image/color"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

func priorBugResolver(t *testing.T, guifont string) *text.Resolver {
	t.Helper()
	resolver, err := text.NewResolver(text.Config{GUIFont: guifont, Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver(%q): %v", guifont, err)
	}
	return resolver
}

func priorBugPixel(rendered *image.RGBA, x, y int) imagecolor.RGBA {
	if rendered == nil || x < 0 || y < 0 || x >= rendered.Bounds().Dx() || y >= rendered.Bounds().Dy() {
		return imagecolor.RGBA{}
	}
	return rendered.RGBAAt(x, y)
}

func priorBugHasColor(rendered *image.RGBA, wanted imagecolor.RGBA) bool {
	if rendered == nil {
		return false
	}
	for y := 0; y < rendered.Bounds().Dy(); y++ {
		for x := 0; x < rendered.Bounds().Dx(); x++ {
			if priorBugNearColor(priorBugPixel(rendered, x, y), wanted) {
				return true
			}
		}
	}
	return false
}

func priorBugNearColor(left, right imagecolor.RGBA) bool {
	return priorBugAbsByte(left.R, right.R) <= 24 && priorBugAbsByte(left.G, right.G) <= 24 &&
		priorBugAbsByte(left.B, right.B) <= 24 && priorBugAbsByte(left.A, right.A) <= 24
}

func priorBugAbsByte(left, right uint8) uint8 {
	if left > right {
		return left - right
	}
	return right - left
}

func priorBugSameFloat(left, right float32) bool {
	return math.Abs(float64(left-right)) <= 0.001
}

func priorBugRender(view *render.View, width, height int) *image.RGBA {
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	rendered := tester.Image()
	if rendered == nil {
		return &image.RGBA{}
	}
	return rendered
}

func priorBugBuildList(t *testing.T, state *screen.State) *drawlist.List {
	t.Helper()
	list, err := drawlist.Build(state)
	if err != nil {
		t.Fatalf("drawlist.Build: %v", err)
	}
	return list
}

func priorBugPlannedFrame(t *testing.T, planner *render.Planner, list *drawlist.List) renderlist.Frame {
	t.Helper()
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return frame
}

func TestPriorBug_Neovide481_FontSizeScalesCellAndLineHeight(t *testing.T) {
	sizes := []string{"Menlo:h12", "Menlo:h16", "Menlo:h20"}
	cells := make([]text.CellSize, len(sizes))
	for index, guifont := range sizes {
		resolver := priorBugResolver(t, guifont)
		cells[index] = resolver.Cell()
		if cells[index].Width <= 0 || cells[index].Height <= 0 {
			t.Fatalf("Cell(%q) = %+v, want positive width and height", guifont, cells[index])
		}
	}
	for index := 1; index < len(cells); index++ {
		if cells[index].Width <= cells[index-1].Width || cells[index].Height <= cells[index-1].Height {
			t.Fatalf("font sizes %q -> %q: Cell %+v -> %+v, want strict growth", sizes[index-1], sizes[index], cells[index-1], cells[index])
		}
	}

	planner := render.NewPlanner(priorBugResolver(t, sizes[1]))
	rows := 3
	items := make([]drawlist.Item, 0, rows*2)
	for row := 0; row < rows; row++ {
		items = append(items,
			drawlist.Item{Kind: "fill", Row: row, Col: 0, Width: 2, Height: 1, BG: 0x101010},
			drawlist.Item{Kind: "text", Row: row, Col: 0, Width: 2, Height: 1, Text: "ab", FG: 0xffffff},
		)
	}
	frame, err := planner.Plan(&drawlist.List{Version: 1, Seq: 481, Layers: []drawlist.Layer{{Grid: 1, Items: items}}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	cell := cells[1]
	previousRowBottom := float32(-1)
	previousRow := -1
	textRows := 0
	for index, command := range frame.Commands {
		if command.Op != "glyphs" && command.Op != "fill" {
			continue
		}
		row := index / 2
		wantY := float32(row) * cell.Height
		if !priorBugSameFloat(command.Y, wantY) {
			t.Fatalf("command[%d].Y = %v, want %v", index, command.Y, wantY)
		}
		if row != previousRow && command.Y < previousRowBottom {
			t.Fatalf("command[%d].Y = %v overlaps previous row bottom %v", index, command.Y, previousRowBottom)
		}
		if command.H <= 0 || command.H > cell.Height {
			t.Fatalf("command[%d].H = %v, want within one cell height %v", index, command.H, cell.Height)
		}
		if row != previousRow {
			previousRowBottom = command.Y + command.H
			previousRow = row
		}
		if command.Op == "glyphs" {
			textRows++
		}
	}
	if textRows != rows {
		t.Fatalf("planned %d text rows, want %d", textRows, rows)
	}
}

func TestPriorBug_Neovim7479_StyledColoredUnderlinesAndSPFallback(t *testing.T) {
	attrs := []string{"underline", "undercurl", "underdouble", "underdotted", "underdashed", "strikethrough", "overline"}
	red := imagecolor.RGBA{R: 255, A: 255}
	green := imagecolor.RGBA{G: 255, A: 255}
	for _, attr := range attrs {
		t.Run(attr, func(t *testing.T) {
			planner := render.NewPlanner(priorBugResolver(t, "Menlo:h14"))
			makeList := func(special int64) *drawlist.List {
				return &drawlist.List{Version: 1, Seq: 7479, Layers: []drawlist.Layer{{Grid: 1, Items: []drawlist.Item{
					{Kind: "fill", Row: 0, Col: 0, Width: 12, Height: 1, BG: 0x000000},
					{Kind: "text", Row: 0, Col: 0, Width: 12, Height: 1, Text: "marked",
						FG: 0x00ff00, BG: 0x000000, SP: special, Attrs: []string{attr}},
				}}}}
			}
			frame := priorBugPlannedFrame(t, planner, makeList(0xff0000))
			view := render.NewView()
			view.SetFrame(frame)
			rendered := priorBugRender(view, 120, 30)
			if !priorBugHasColor(rendered, red) {
				t.Fatalf("%s failed to paint SP color in a headless frame", attr)
			}

			fallbackList := makeList(0)
			fallbackFrame := priorBugPlannedFrame(t, planner, fallbackList)
			fallbackView := render.NewView()
			fallbackView.SetFrame(fallbackFrame)
			fallbackRendered := priorBugRender(fallbackView, 120, 30)
			if !priorBugHasColor(fallbackRendered, green) {
				t.Fatalf("%s with SP==0 did not paint FG fallback", attr)
			}
			if priorBugHasColor(fallbackRendered, red) {
				t.Fatalf("%s with SP==0 still painted SP red", attr)
			}

			plainList := makeList(0)
			plainList.Layers[0].Items[1].Attrs = nil
			plainFrame := priorBugPlannedFrame(t, planner, plainList)
			plainView := render.NewView()
			plainView.SetFrame(plainFrame)
			plainRendered := priorBugRender(plainView, 120, 30)
			if bytes.Equal(fallbackRendered.Pix, plainRendered.Pix) {
				t.Fatalf("%s with SP==0 painted no visible FG decoration", attr)
			}
		})
	}
}

func TestPriorBug_Neovim12866_WrappedLinesAndSignColumnAlignment(t *testing.T) {
	const signWidth = 2
	const width = 12
	const height = 5
	signs := []rune{'!', '.', '>', '~', '*'}
	lines := []string{
		"one two",
		"continuation",
		"第二行文字",
		"wrap again",
		"last",
	}
	cells := make([][]screen.Cell, height)
	for row, line := range lines {
		cells[row] = make([]screen.Cell, signWidth+width)
		for column := range cells[row] {
			cells[row][column] = screen.Cell{Text: " ", HL: 0}
		}
		cells[row][0] = screen.Cell{Text: string(signs[row]), HL: 1}
		cells[row][1] = cells[row][0]
		column := signWidth
		for _, character := range line {
			if column >= len(cells[row]) {
				break
			}
			cells[row][column] = screen.Cell{Text: string(character), HL: 0}
			column++
			if character >= 0x2e80 {
				if column >= len(cells[row]) {
					break
				}
				cells[row][column] = screen.Cell{Text: "", HL: 0}
				column++
			}
		}
	}
	state := &screen.State{
		Seq:   12866,
		Grids: map[int]*screen.Grid{1: {Width: signWidth + width, Height: height, Cells: cells}},
		Attrs: map[int]screen.Attr{
			0: {},
			1: {"foreground": int64(0xffcc00)},
		},
		Placements:    map[int]screen.Placement{},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101010},
		Cursor:        screen.Cursor{Grid: 1, Row: height - 1, Col: signWidth},
		Mode:          "normal",
	}
	list := priorBugBuildList(t, state)
	resolver := priorBugResolver(t, "Menlo:h14")
	frame := priorBugPlannedFrame(t, render.NewPlanner(resolver), list)
	cell := resolver.Cell()
	for row := 0; row < height; row++ {
		signDone := false
		bodyDone := false
		previousX := -cell.Width
		for index, command := range frame.Commands {
			if command.Op != "glyphs" || !priorBugSameFloat(command.Y, float32(row)*cell.Height) {
				continue
			}
			if !utf8.ValidString(command.Text) {
				t.Fatalf("row %d glyphs[%d].Text = %q is invalid UTF-8", row, index, command.Text)
			}
			if !priorBugSameFloat(command.H, cell.Height) {
				t.Fatalf("row %d glyphs[%d].H = %v, want one cell height %v", row, index, command.H, cell.Height)
			}
			if command.X < previousX {
				t.Fatalf("row %d glyphs[%d].X = %v moved backwards after %v", row, index, command.X, previousX)
			}
			previousX = command.X
			if priorBugSameFloat(command.X, 0) && priorBugSameFloat(command.W, signWidth*cell.Width) &&
				strings.HasPrefix(command.Text, strings.Repeat(string(signs[row]), signWidth)) {
				signDone = true
			}
			if command.X >= signWidth*cell.Width && command.W > 0 {
				bodyDone = true
			}
			if row == 2 {
				if priorBugSameFloat(command.X, 3*cell.Width) {
					t.Fatalf("row %d glyphs[%d] starts inside a CJK continuation cell", row, index)
				}
				if command.Text == "第" && (!priorBugSameFloat(command.X, signWidth*cell.Width) ||
					!priorBugSameFloat(command.W, 2*cell.Width)) {
					t.Fatalf("double-width CJK glyph = %+v, want X=%v W=%v", command, signWidth*cell.Width, 2*cell.Width)
				}
			}
		}
		if !signDone {
			t.Fatalf("row %d has no sign-column glyphs at X=0", row)
		}
		if !bodyDone {
			t.Fatalf("row %d has no wrapped-body glyphs after the sign column", row)
		}
	}
}

func TestPriorBug_MacVim557_FullRepaintAfterSetFrameNotBlank(t *testing.T) {
	resolver := priorBugResolver(t, "Menlo:h14")
	cell := resolver.Cell()
	width := int(12 * cell.Width)
	height := int(3 * cell.Height)
	makeFrame := func(seq uint64, label string) renderlist.Frame {
		return renderlist.Frame{Version: 1, Seq: seq, Commands: []renderlist.Command{
			{Op: "fill", X: 0, Y: 0, W: float32(width), H: float32(height), BG: 0x101018},
			{Op: "glyphs", X: 0, Y: cell.Height, W: 12 * cell.Width, H: cell.Height,
				Text: label, Family: "Menlo", Size: 14, FG: 0xffffff},
			{Op: "cursor", X: 0, Y: 2 * cell.Height, W: cell.Width, H: cell.Height, FG: 0x101018, BG: 0xffcc00},
		}}
	}

	view := render.NewView()
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	renderWithTester := func() *image.RGBA {
		tester.Frame()
		return tester.Image()
	}
	assertFullRepaint := func(rendered *image.RGBA, label string) {
		if priorBugBlank(rendered) {
			t.Fatalf("%s is blank/black", label)
		}
		textPixels := 0
		cursorPixels := 0
		for y := 0; y < rendered.Bounds().Dy(); y++ {
			for x := 0; x < rendered.Bounds().Dx(); x++ {
				pixel := priorBugPixel(rendered, x, y)
				if y < 2*int(cell.Height) && pixel.R > 180 && pixel.G > 180 && pixel.B > 180 {
					textPixels++
				}
				if y >= 2*int(cell.Height) && pixel.R > 180 && pixel.G > 140 && pixel.B < 100 {
					cursorPixels++
				}
			}
		}
		if textPixels == 0 {
			t.Fatalf("%s is missing white text pixels", label)
		}
		if cursorPixels == 0 {
			t.Fatalf("%s is missing yellow cursor pixels", label)
		}
	}
	firstFrame := makeFrame(557, "full repaint")
	view.SetFrame(firstFrame)
	first := renderWithTester()
	assertFullRepaint(first, "initial frame")
	again := renderWithTester()
	if !bytes.Equal(first.Pix, again.Pix) {
		t.Fatal("repeated tester.Frame() changed a fully committed frame")
	}
	assertFullRepaint(again, "repeated initial frame")

	for index := 1; index <= 4; index++ {
		view.SetFrame(makeFrame(uint64(557+index), fmt.Sprintf("frame %d", index)))
		for repeat := 0; repeat < 3; repeat++ {
			rendered := renderWithTester()
			assertFullRepaint(rendered, fmt.Sprintf("frame %d repeat %d", index, repeat))
			if !priorBugHasColor(rendered, imagecolor.RGBA{R: 255, G: 204, A: 255}) {
				t.Fatalf("frame %d repeat %d lost the cursor color", index, repeat)
			}
		}
	}

	view.SetFrame(firstFrame)
	final := renderWithTester()
	if !bytes.Equal(first.Pix, final.Pix) {
		t.Fatal("restoring the original frame did not reproduce deterministic pixels")
	}
	assertFullRepaint(final, "restored initial frame")
}

func priorBugBlank(rendered *image.RGBA) bool {
	if rendered == nil || rendered.Bounds().Dx() == 0 || rendered.Bounds().Dy() == 0 {
		return true
	}
	for y := 0; y < rendered.Bounds().Dy(); y++ {
		for x := 0; x < rendered.Bounds().Dx(); x++ {
			pixel := priorBugPixel(rendered, x, y)
			if pixel.R > 24 || pixel.G > 24 || pixel.B > 24 {
				return false
			}
		}
	}
	return true
}

func TestPriorBug_Neovide1312_Large4KGridPlanBoundedBatchesAndRowCache(t *testing.T) {
	const columns = 240
	const rows = 70
	const editedRow = 3
	state := priorBugDenseState(columns, rows, 1312)
	list := priorBugBuildList(t, state)
	nonSpace := columns * rows
	resolver := priorBugResolver(t, "Menlo:h14")
	planner := render.NewPlanner(resolver)
	frame := priorBugPlannedFrame(t, planner, list)
	glyphCommands := 0
	for _, command := range frame.Commands {
		if command.Op == "glyphs" {
			glyphCommands++
		}
	}
	limit := nonSpace / 4
	if glyphCommands > limit {
		t.Fatalf("glyphs commands = %d, non-space cells = %d, want <= %d (25%%)", glyphCommands, nonSpace, limit)
	}

	view := render.NewView()
	width := int(columns * resolver.Cell().Width)
	height := 320
	view.SetFrame(frame)
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, width, height)
	first := tester.Image()
	if priorBugBlank(first) {
		t.Fatal("4K frame rendered blank")
	}
	firstPixels := append([]byte(nil), first.Pix...)
	before := view.ShapedRows()
	if before < rows/4 || before > rows+1 {
		t.Fatalf("ShapedRows() = %d after initial 4K render, want visible rows in [%d, %d]", before, rows/4, rows+1)
	}

	repeatState := priorBugDenseState(columns, rows, 1312)
	repeatFrame := priorBugPlannedFrame(t, planner, priorBugBuildList(t, repeatState))
	view.SetFrame(repeatFrame)
	tester.Frame()
	repeat := tester.Image()
	if !bytes.Equal(firstPixels, repeat.Pix) {
		t.Fatal("identical 4K frame did not reproduce deterministic pixels")
	}
	if repeatRows := view.ShapedRows(); repeatRows != before {
		t.Fatalf("ShapedRows changed from %d to %d across identical 4K frame", before, repeatRows)
	}

	edited := priorBugDenseState(columns, rows, 1313)
	edited.Grids[1].Cells[editedRow][8] = screen.Cell{Text: "Z", HL: 0}
	editedFrame := priorBugPlannedFrame(t, planner, priorBugBuildList(t, edited))
	editedRowRuns := 0
	for _, command := range editedFrame.Commands {
		if command.Op == "glyphs" && priorBugSameFloat(command.Y, float32(editedRow)*resolver.Cell().Height) {
			editedRowRuns++
		}
	}
	if editedRowRuns == 0 {
		t.Fatalf("edited row %d has zero glyphs commands", editedRow)
	}
	view.SetFrame(editedFrame)
	tester.Frame()
	editedPixels := append([]byte(nil), tester.Image().Pix...)
	if bytes.Equal(firstPixels, editedPixels) {
		t.Fatal("single-row edit did not change the 4K frame pixels")
	}
	after := view.ShapedRows()
	delta := after - before
	if delta < 1 || delta > editedRowRuns {
		t.Fatalf("ShapedRows delta %d (from %d to %d) after one-row edit, want [1, %d]", delta, before, after, editedRowRuns)
	}
	if delta*2 >= before {
		t.Fatalf("ShapedRows delta %d is not bounded against initial %d shaped rows", delta, before)
	}

	for repeat := 0; repeat < 3; repeat++ {
		view.SetFrame(editedFrame)
		tester.Frame()
		rendered := tester.Image()
		if priorBugBlank(rendered) {
			t.Fatalf("4K frame repeat %d rendered blank", repeat)
		}
		if !bytes.Equal(rendered.Pix, editedPixels) {
			t.Fatalf("4K frame repeat %d changed pixels after an identical repeated frame", repeat)
		}
	}
	if final := view.ShapedRows(); final != after {
		t.Fatalf("ShapedRows changed from %d to %d across repeated frames", after, final)
	}
}

func priorBugDenseState(columns, rows int, seq uint64) *screen.State {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	cells := make([][]screen.Cell, rows)
	for row := range cells {
		cells[row] = make([]screen.Cell, columns)
		for column := range cells[row] {
			cells[row][column] = screen.Cell{Text: string(alphabet[column%len(alphabet)]), HL: 0}
		}
	}
	return &screen.State{
		Seq:           seq,
		Grids:         map[int]*screen.Grid{1: {Width: columns, Height: rows, Cells: cells}},
		Attrs:         map[int]screen.Attr{0: {}},
		Placements:    map[int]screen.Placement{},
		DefaultColors: screen.Colors{FG: 0xffffff, BG: 0x101018},
		Cursor:        screen.Cursor{Grid: 1, Row: rows - 1, Col: 0},
		Mode:          "normal",
	}
}
