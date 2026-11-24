package render

import (
	"bytes"
	"fmt"
	"image"
	imagecolor "image/color"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

func mustTestResolver(t *testing.T) *text.Resolver {
	t.Helper()
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func TestPlannerUsesCellCoordinatesAndStableOutput(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	list := &drawlist.List{
		Version: 1,
		Seq:     9,
		Layers: []drawlist.Layer{{
			Grid:      7,
			ScreenRow: 3,
			ScreenCol: 2,
			Items: []drawlist.Item{
				{Kind: "text", Row: 1, Col: 4, Width: 2, Height: 1, Text: "abc", FG: 0x112233, BG: 0x445566},
				{Kind: "fill", Row: 0, Col: 0, Width: 3, Height: 2, BG: 0x778899},
			},
		}},
	}

	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	again, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("second Plan: %v", err)
	}
	if !reflect.DeepEqual(frame, again) {
		t.Fatalf("Plan output differs for same input:\nfirst  %+v\nsecond %+v", frame, again)
	}
	if len(frame.Commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(frame.Commands))
	}
	want := []renderlist.Command{
		{Op: "glyphs", X: 6 * cell.Width, Y: 4 * cell.Height, W: 2 * cell.Width, H: cell.Height,
			Text: "abc", FG: 0x112233, BG: 0x445566},
		{Op: "fill", X: 2 * cell.Width, Y: 3 * cell.Height, W: 3 * cell.Width, H: 2 * cell.Height, BG: 0x778899},
	}
	for index := range want {
		got := frame.Commands[index]
		if got.Op != want[index].Op || got.X != want[index].X || got.Y != want[index].Y ||
			got.W != want[index].W || got.H != want[index].H || got.Text != want[index].Text ||
			got.FG != want[index].FG || got.BG != want[index].BG {
			t.Errorf("command %d = %+v, want geometry/colors of %+v", index, got, want[index])
		}
		if want[index].Op == "glyphs" && (got.Family == "" || got.Size <= 0) {
			t.Errorf("glyph command %d has no resolved font: %+v", index, got)
		}
	}

	if _, err := planner.Plan(nil); err == nil {
		t.Error("Plan(nil) returned nil error")
	}
}

func TestColorEncoding(t *testing.T) {
	if got := color(0x000000); got != (ui.Color{R: 0, G: 0, B: 0, A: 255}) {
		t.Fatalf("color(0x000000) = %+v, want opaque black", got)
	}
	if got := color(0x8044aa77); got != (ui.Color{R: 0x44, G: 0xaa, B: 0x77, A: 0x80}) {
		t.Fatalf("color(0x8044aa77) = %+v, want 32-bit ARGB with alpha 0x80", got)
	}
	if got := colorValue(0x8044aa77); got != 0x8044aa77 {
		t.Fatalf("colorValue(32-bit ARGB) = %#x, want full value", got)
	}
}

func TestLayerClipPlanning(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	makeList := func(clip drawlist.Rect, layerWidth int) *drawlist.List {
		return &drawlist.List{
			Version: 1,
			Seq:     12,
			Width:   10,
			Height:  10,
			Layers: []drawlist.Layer{{
				Grid:   1,
				Width:  layerWidth,
				Height: 2,
				Clip:   clip,
				Items: []drawlist.Item{
					{Kind: "fill", Col: 0, Row: 0, Width: 4, Height: 2, BG: 0x111111},
					{Kind: "text", Col: 1, Row: 0, Width: 2, Height: 1, Text: "ab", FG: 0xffffff, BG: 0x111111},
					{Kind: "box", Col: 3, Row: 1, Width: 1, Height: 1, Rune: "─", FG: 0xffffff},
					{Kind: "cursor", Col: 2, Row: 1, Width: 1, Height: 1},
				},
			}},
		}
	}

	partial, err := planner.Plan(makeList(drawlist.Rect{X: 1, Y: 0, W: 2, H: 2}, 0))
	if err != nil {
		t.Fatalf("Plan partial clip: %v", err)
	}
	if len(partial.Commands) != 3 {
		t.Fatalf("partial clip commands = %d, want 3: %+v", len(partial.Commands), partial.Commands)
	}
	fill := partial.Commands[0]
	if fill.Op != "fill" || fill.X != cell.Width || fill.W != 2*cell.Width || fill.H != 2*cell.Height {
		t.Errorf("clipped fill = %+v, want intersection x=1*cell w=2*cell h=2*cell", fill)
	}
	metas := metadataFor(partial).rows
	if len(metas) != len(partial.Commands) {
		t.Fatalf("metadata entries = %d, want %d", len(metas), len(partial.Commands))
	}
	wantClip := ui.Rect{X: cell.Width, W: 2 * cell.Width, H: 2 * cell.Height}
	for index := 1; index < len(metas); index++ {
		if metas[index].clip != wantClip {
			t.Errorf("command %d clip = %+v, want %+v", index, metas[index].clip, wantClip)
		}
	}

	outside, err := planner.Plan(makeList(drawlist.Rect{X: 20, Y: 20, W: 2, H: 2}, 0))
	if err != nil {
		t.Fatalf("Plan outside clip: %v", err)
	}
	if len(outside.Commands) != 0 {
		t.Fatalf("outside clip commands = %d, want 0: %+v", len(outside.Commands), outside.Commands)
	}

	emptyLayer, err := planner.Plan(makeList(drawlist.Rect{}, 4))
	if err != nil {
		t.Fatalf("Plan empty layer clip: %v", err)
	}
	if len(emptyLayer.Commands) != 0 {
		t.Fatalf("empty layer clip commands = %d, want 0: %+v", len(emptyLayer.Commands), emptyLayer.Commands)
	}

	for _, invalid := range []drawlist.Rect{{W: -1, H: 1}, {W: 1, H: -1}} {
		if _, err := planner.Plan(makeList(invalid, 0)); err == nil {
			t.Errorf("Plan accepted negative clip %+v", invalid)
		}
	}
}

func TestClipRenderingUsesDIPs(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	list := &drawlist.List{
		Version: 1,
		Seq:     21,
		Width:   10,
		Height:  2,
		Layers: []drawlist.Layer{{
			Grid:  1,
			Clip:  drawlist.Rect{X: 0, Y: 0, W: 1, H: 1},
			Items: []drawlist.Item{{Kind: "fill", Col: 0, Row: 0, Width: 1, Height: 1, BG: 0xff0000}},
		}},
	}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	frame.Commands = append(frame.Commands, renderlist.Command{
		Op: "fill", X: cell.Width, Y: 0, W: cell.Width, H: cell.Height, BG: 0x00ff00,
	})
	metas := append(metadataFor(frame).rows, rowMeta{})
	view := NewView()
	view.SetFrame(frame)
	view.impl.meta = frameMetadata{rows: metas}
	tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, int(2*cell.Width), int(cell.Height))
	image := tester.Image()
	if pixelAt(image, int(cell.Width/2), int(cell.Height/2)) == (imagecolor.RGBA{}) {
		t.Error("green command outside red DIPs clip was not painted")
	}
}

func TestMetadataHashRejectsFalseMatches(t *testing.T) {
	commands := []renderlist.Command{
		{Op: "glyphs", X: 1, Y: 2, Text: "same", FG: 1, BG: 2},
		{Op: "fill", X: 3, Y: 4, W: 5, H: 6, BG: 7},
	}
	firstSeq := uint64(9)
	otherSeq := uint64(10)
	storeMetadata(renderlist.Frame{Seq: firstSeq, Commands: commands}, frameMetadata{rows: []rowMeta{{grid: 1}, {attrMask: attrUnderline}}})
	storeMetadata(renderlist.Frame{Seq: otherSeq, Commands: commands}, frameMetadata{rows: []rowMeta{{grid: 2}, {attrMask: attrBold}}})
	if got := metadataFor(renderlist.Frame{Seq: firstSeq, Commands: commands}).rows; got[0].grid != 1 || got[1].attrMask != attrUnderline {
		t.Fatalf("metadata for seq %d = %+v, want first frame metadata", firstSeq, got)
	}
	if got := metadataFor(renderlist.Frame{Seq: otherSeq, Commands: commands}).rows; got[0].grid != 2 || got[1].attrMask != attrBold {
		t.Fatalf("metadata for seq %d = %+v, want second frame metadata", otherSeq, got)
	}
	changed := append([]renderlist.Command(nil), commands...)
	changed[1].BG = 99
	if got := metadataFor(renderlist.Frame{Seq: firstSeq, Commands: changed}); !reflect.DeepEqual(got, frameMetadata{}) {
		t.Fatalf("metadataFor accepted changed commands: %+v", got)
	}
}

func TestConcurrentViewsAreIsolated(t *testing.T) {
	const views = 4
	const iterations = 8
	instances := make([]*View, views)
	for index := range instances {
		instances[index] = NewView()
	}
	makeFrame := func(index, iteration int) renderlist.Frame {
		return renderlist.Frame{
			Version: 1,
			Seq:     uint64(index*100 + iteration),
			Commands: []renderlist.Command{
				{Op: "fill", X: 0, Y: 0, W: 40, H: 20, BG: uint32(0x101010 + index)},
				{Op: "glyphs", X: 2, Y: 2, W: 30, H: 18, Text: fmt.Sprintf("view %d frame %d", index, iteration),
					Family: "Menlo", Size: 14, FG: 0xffffff},
				{Op: "cursor", X: float32(index), Y: 2, W: 9, H: 18, FG: 0x101010, BG: 0xffcc00},
			},
		}
	}
	for index, view := range instances {
		view.SetFrame(makeFrame(index, 0))
	}
	var group sync.WaitGroup
	for index, view := range instances {
		group.Add(1)
		go func(index int, view *View) {
			defer group.Done()
			for iteration := 1; iteration < iterations; iteration++ {
				view.SetFrame(makeFrame(index, iteration))
				if caret := view.Caret(); caret.X != float32(index) {
					t.Errorf("view %d caret X = %v, want %d", index, caret.X, index)
				}
				tester := ui.NewTester(func(c *ui.Context) { view.Draw(c) }, 80, 40)
				if image := tester.Image(); image == nil {
					t.Errorf("view %d rendered nil image", index)
				}
			}
		}(index, view)
	}
	group.Wait()
	for index, view := range instances {
		if caret := view.Caret(); caret.X != float32(index) {
			t.Errorf("final view %d caret X = %v, want %d", index, caret.X, index)
		}
	}
}

func TestCaretClearsOnFrameWithoutCursor(t *testing.T) {
	view := NewView()
	view.SetFrame(renderlist.Frame{Version: 1, Seq: 1, Commands: []renderlist.Command{
		{Op: "cursor", X: 10, Y: 20, W: 9, H: 18, FG: 0, BG: 0xffcc00},
	}})
	if got := view.Caret(); got == (ui.Rect{}) {
		t.Fatal("cursor frame produced empty caret")
	}
	view.SetFrame(renderlist.Frame{Version: 1, Seq: 2, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 40, H: 20, BG: 0x101010},
	}})
	if got := view.Caret(); got != (ui.Rect{}) {
		t.Fatalf("Caret() after frame without cursor = %+v, want zero", got)
	}
}

func TestRowCacheKeyIgnoresFrameSeq(t *testing.T) {
	cache := newRowCache()
	command := renderlist.Command{Op: "glyphs", X: 3, Y: 36, W: 90, H: 18, Text: "same row", Family: "Menlo", Size: 14, FG: 0xffffff, BG: 0x101010}
	first := cache.glyphs(command, rowMeta{grid: 4, attrMask: attrBold})
	if len(first) == 0 {
		t.Fatal("shaping returned no glyphs")
	}
	second := cache.glyphs(command, rowMeta{grid: 4, attrMask: attrBold})
	if got := cache.shapedRows(); got != 1 {
		t.Fatalf("ShapedRows after repeated row = %d, want 1", got)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("cached glyphs changed between identical calls")
	}
	edited := command
	edited.Text = "changed row"
	_ = cache.glyphs(edited, rowMeta{grid: 4, attrMask: attrBold})
	if got := cache.shapedRows(); got != 2 {
		t.Fatalf("ShapedRows after row edit = %d, want 2", got)
	}
	_ = cache.glyphs(command, rowMeta{grid: 4, attrMask: attrItalic})
	if got := cache.shapedRows(); got != 3 {
		t.Fatalf("ShapedRows after attrs edit = %d, want 3", got)
	}
}

func TestRowCacheBoundedLRU(t *testing.T) {
	cache := newRowCache()
	cache.capacity = 3
	first := renderlist.Command{Op: "glyphs", X: 0, Y: 0, W: 20, H: 18, Text: "first", Family: "Menlo", Size: 14}
	cache.glyphs(first, rowMeta{grid: 1})
	for index := 1; index <= 2; index++ {
		command := first
		command.Text = fmt.Sprintf("row %d", index)
		cache.glyphs(command, rowMeta{grid: 1})
	}
	cache.glyphs(first, rowMeta{grid: 1})
	for index := 3; index <= 4; index++ {
		command := first
		command.Text = fmt.Sprintf("row %d", index)
		cache.glyphs(command, rowMeta{grid: 1})
	}
	if got := cache.size(); got != 3 {
		t.Fatalf("cache size = %d, want 3", got)
	}
	if got := cache.shapedRows(); got != 5 {
		t.Fatalf("shapedRows = %d, want 5", got)
	}
	recent := first
	recent.Text = "row 4"
	if got := cache.glyphs(recent, rowMeta{grid: 1}); len(got) == 0 {
		t.Fatal("LRU hit returned no glyphs")
	}
	if got := cache.shapedRows(); got != 5 {
		t.Fatalf("LRU hit reshaped: shapedRows = %d, want 5", got)
	}
}

func TestForegroundDoesNotCoverAdjacentBackground(t *testing.T) {
	frame := renderlist.Frame{Version: 1, Seq: 31, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 100, H: 20, BG: 0x0000ff},
		{Op: "glyphs", X: 40, Y: 0, W: 60, H: 20, Text: "A", Family: "Menlo", Size: 14, FG: 0xffffff, BG: 0xff0000},
		{Op: "box", X: 70, Y: 0, W: 10, H: 20, Rune: "█", FG: 0x00ff00, BG: 0xff0000},
	}}
	withoutForegroundBackground := renderImage(frame, 100, 20)
	if got := pixelAt(withoutForegroundBackground, 5, 10); got != (imagecolor.RGBA{B: 255, A: 255}) {
		t.Fatalf("left background pixel = %+v, want blue", got)
	}
	withForegroundBackground := renderImage(renderlist.Frame{Version: 1, Seq: 32, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 100, H: 20, BG: 0x0000ff},
		{Op: "fill", X: 40, Y: 0, W: 60, H: 20, BG: 0xff0000},
		{Op: "glyphs", X: 40, Y: 0, W: 60, H: 20, Text: "A", Family: "Menlo", Size: 14, FG: 0xffffff, BG: 0xff0000},
		{Op: "box", X: 70, Y: 0, W: 10, H: 20, Rune: "█", FG: 0x00ff00, BG: 0xff0000},
	}}, 100, 20)
	if got := pixelAt(withForegroundBackground, 95, 19); got != (imagecolor.RGBA{R: 255, A: 255}) {
		t.Fatalf("explicit foreground background pixel = %+v, want red", got)
	}
	for _, point := range [][2]int{{45, 19}, {75, 19}, {95, 19}} {
		if got := pixelAt(withoutForegroundBackground, point[0], point[1]); got == (imagecolor.RGBA{R: 255, A: 255}) {
			t.Errorf("foreground at %v painted its own red background: %+v", point, got)
		}
	}
}

func TestDecorationsRender(t *testing.T) {
	attrs := []string{"underline", "undercurl", "underdouble", "underdotted", "underdashed", "strikethrough", "overline"}
	for _, attr := range attrs {
		t.Run(attr, func(t *testing.T) {
			frame := renderlist.Frame{Version: 1, Seq: 40, Commands: []renderlist.Command{
				{Op: "fill", X: 0, Y: 0, W: 120, H: 30, BG: 0x000000},
				{Op: "glyphs", X: 0, Y: 0, W: 120, H: 30, Text: "marked", Family: "Menlo", Size: 14, FG: 0xffffff, SP: 0xff0000},
			}}
			view := NewView()
			view.SetFrame(frame)
			view.impl.meta = frameMetadata{rows: []rowMeta{{attrMask: attrsMask([]string{attr})}, {attrMask: attrsMask([]string{attr})}}}
			image := renderView(view, 120, 30)
			if !hasColorNear(image, imagecolor.RGBA{R: 255, A: 255}) {
				t.Errorf("%s did not paint its SP color", attr)
			}
			plain := renderImage(frame, 120, 30)
			if bytes.Equal(image.Pix, plain.Pix) {
				t.Errorf("%s produced no visible decoration", attr)
			}
		})
	}

	noAttrs := renderImage(renderlist.Frame{Version: 1, Seq: 41, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 120, H: 30, BG: 0x000000},
		{Op: "glyphs", X: 0, Y: 0, W: 120, H: 30, Text: "plain", Family: "Menlo", Size: 14, FG: 0xffffff, SP: 0xff0000},
	}}, 120, 30)
	if hasColorNear(noAttrs, imagecolor.RGBA{R: 255, A: 255}) {
		t.Error("SP color painted without an attrs trigger")
	}
}

func TestBoldItalicFontPropagation(t *testing.T) {
	resolver := mustTestResolver(t)
	planner := NewPlanner(resolver)
	list := &drawlist.List{Version: 1, Seq: 50, Layers: []drawlist.Layer{{Items: []drawlist.Item{
		{Kind: "text", Width: 1, Height: 1, Text: "style", Attrs: []string{"bold", "italic"}},
	}}}}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	boldItalic := frame.Commands[0]
	base := resolver.FontFor("style", false, false)
	if boldItalic.Weight <= base.Weight || !boldItalic.Italic {
		t.Fatalf("bold/italic command font = weight %d italic %v, base weight %d", boldItalic.Weight, boldItalic.Italic, base.Weight)
	}
	font := text.Font{Family: boldItalic.Family, Size: boldItalic.Size, Weight: boldItalic.Weight, Italic: boldItalic.Italic, Features: boldItalic.Features}.MyGo()
	if font.Weight != boldItalic.Weight || font.Italic != boldItalic.Italic || !strings.Contains(font.Features, "kern=0") {
		t.Fatalf("MyGo font = %+v, want weight/italic and macOS kern=0", font)
	}
}

func TestBoxRunesAndViewDrawMatchDraw(t *testing.T) {
	runes := []string{"─", "╭", "█", "▌", "▚", "\U0001FB00", "\uE0B0", "\uE0B7"}
	for _, runeValue := range runes {
		if !drawnBoxRune([]rune(runeValue)[0]) {
			t.Errorf("rune %q was not self-drawn", runeValue)
		}
	}
	for _, character := range []rune{'A', '-', '界'} {
		if drawnBoxRune(character) {
			t.Errorf("rune %q was self-drawn unexpectedly", character)
		}
	}

	frame := renderlist.Frame{Version: 1, Seq: 60, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 160, H: 40, BG: 0x000000},
	}}
	for index, runeValue := range runes {
		frame.Commands = append(frame.Commands, renderlist.Command{
			Op: "box", X: float32(index * 20), Y: 4, W: 20, H: 32, Rune: runeValue, FG: 0x00ff88, BG: 0xff0000,
		})
	}
	viewImage := renderImageWithView(frame, 160, 40)
	drawImage := renderImage(frame, 160, 40)
	if !bytes.Equal(viewImage.Pix, drawImage.Pix) {
		t.Error("View.Draw and package Draw rendered different pixels")
	}
	if hasColorNear(viewImage, imagecolor.RGBA{R: 255, A: 255}) {
		t.Error("box command painted its background over the fill")
	}
	for _, runeValue := range runes {
		single := renderImage(renderlist.Frame{Version: 1, Seq: 61, Commands: []renderlist.Command{
			{Op: "fill", X: 0, Y: 0, W: 20, H: 32, BG: 0x000000},
			{Op: "box", X: 0, Y: 0, W: 20, H: 32, Rune: runeValue, FG: 0x00ff88},
		}}, 20, 32)
		if !hasGreenForeground(single) {
			t.Errorf("box rune %q painted no foreground pixels", runeValue)
		}
	}
}

func TestViewDrawRespectsLayoutRect(t *testing.T) {
	frame := renderlist.Frame{Version: 1, Seq: 70, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 20, H: 20, BG: 0x00ff00},
	}}
	view := NewView()
	view.SetFrame(frame)
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Absolute().Left(30).Top(10).Size(20, 20).Fill().Children(func() {
			view.Draw(c)
		})
	}, 80, 50)
	image := tester.Image()
	if got := pixelAt(image, 35, 15); got != (imagecolor.RGBA{G: 255, A: 255}) {
		t.Fatalf("pixel inside translated view = %+v, want green", got)
	}
	if got := pixelAt(image, 5, 5); got == (imagecolor.RGBA{G: 255, A: 255}) {
		t.Fatal("view painted outside its layout rect")
	}
}

func TestDecorationColorFallsBackWhenSPZero(t *testing.T) {
	frame := renderlist.Frame{Version: 1, Seq: 42, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 120, H: 30, BG: 0x000000},
		{Op: "glyphs", X: 0, Y: 0, W: 120, H: 30, Text: "plain", Family: "Menlo", Size: 14, FG: 0x00ff00, SP: 0},
	}}
	view := NewView()
	view.SetFrame(frame)
	view.impl.meta = frameMetadata{rows: []rowMeta{{attrMask: attrUnderline}, {attrMask: attrUnderline}}}
	if image := renderView(view, 120, 30); !hasGreenForeground(image) {
		t.Error("decoration did not use FG when SP was zero")
	}
}

func TestLayerClipPartiallyClipsForeground(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	list := &drawlist.List{Version: 1, Seq: 22, Width: 4, Height: 1, Layers: []drawlist.Layer{{
		Grid: 1,
		Clip: drawlist.Rect{X: 0, Y: 0, W: 1, H: 1},
		Items: []drawlist.Item{
			{Kind: "fill", Col: 0, Row: 0, Width: 4, Height: 1, BG: 0x0000ff},
			{Kind: "box", Col: 0, Row: 0, Width: 2, Height: 1, Rune: "█", FG: 0x00ff00},
		},
	}}}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	view := NewView()
	view.SetFrame(frame)
	image := renderView(view, int(4*cell.Width), int(cell.Height))
	if got := pixelAt(image, int(cell.Width*1.5), int(cell.Height/2)); got == (imagecolor.RGBA{G: 255, A: 255}) {
		t.Fatalf("foreground painted beyond layer clip: %+v", got)
	}
}

func TestDrawnBoxRanges(t *testing.T) {
	for _, character := range []rune{'─', '╭', '█', 0x1fb00, 0xe0b0, 0xe0b7} {
		if !drawnBoxRune(character) {
			t.Errorf("rune %q was not self-drawn", character)
		}
	}
	for _, character := range []rune{'A', '-', '界'} {
		if drawnBoxRune(character) {
			t.Errorf("rune %q was self-drawn unexpectedly", character)
		}
	}
}

func TestWideCursorLightThemeAndInsertBeamRender(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)

	makeState := func(mode string) *screen.State {
		return &screen.State{
			Seq: 101,
			Grids: map[int]*screen.Grid{
				1: {
					Width:  4,
					Height: 1,
					Cells: [][]screen.Cell{{
						{Text: "本", HL: 0},
						{Text: "", HL: 0},
						{Text: "A", HL: 0},
						{Text: " ", HL: 0},
					}},
				},
			},
			Attrs:         map[int]screen.Attr{0: {}},
			DefaultColors: screen.Colors{FG: 0x24292f, BG: 0xffffff, SP: 0xff0000},
			Placements:    map[int]screen.Placement{},
			Cursor:        screen.Cursor{Grid: 1, Row: 0, Col: 0},
			Mode:          mode,
		}
	}

	normalList, err := drawlist.Build(makeState("normal"))
	if err != nil {
		t.Fatalf("drawlist.Build(normal): %v", err)
	}
	normalFrame, err := planner.Plan(normalList)
	if err != nil {
		t.Fatalf("Plan(normal): %v", err)
	}
	var cursorCmd renderlist.Command
	foundCursor := false
	for _, cmd := range normalFrame.Commands {
		if cmd.Op == "cursor" {
			cursorCmd = cmd
			foundCursor = true
		}
	}
	if !foundCursor {
		t.Fatal("missing cursor command in normal frame")
	}
	if cursorCmd.W != 2*cell.Width || cursorCmd.Text != "本" || cursorCmd.Family == "" || cursorCmd.Size <= 0 {
		t.Fatalf("wide cursor command = %+v, want width %.1f text \"本\" with resolved font", cursorCmd, 2*cell.Width)
	}
	if cursorCmd.BG != 0x24292f || cursorCmd.FG != 0xffffff {
		t.Fatalf("wide cursor colors = (fg=%#x, bg=%#x), want (0xffffff, 0x24292f)", cursorCmd.FG, cursorCmd.BG)
	}

	width := int(4 * cell.Width)
	height := int(cell.Height)
	normalImg := renderImageWithView(normalFrame, width, height)
	darkCursor := imagecolor.RGBA{R: 0x24, G: 0x29, B: 0x2f, A: 0xff}
	lightGlyph := imagecolor.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	cellW := int(cell.Width)

	leftHasDark, rightHasDark := false, false
	cursorHasLightGlyph := false
	for y := 0; y < height; y++ {
		for x := 0; x < 2*cellW && x < width; x++ {
			px := normalImg.RGBAAt(x, y)
			if nearColor(px, darkCursor) {
				if x < cellW {
					leftHasDark = true
				} else {
					rightHasDark = true
				}
			}
			if px.R > 200 && px.G > 200 && px.B > 200 {
				cursorHasLightGlyph = true
			}
		}
	}
	if !leftHasDark || !rightHasDark {
		t.Fatalf("wide cursor block did not cover both cells on light background (left=%v, right=%v)", leftHasDark, rightHasDark)
	}
	if !cursorHasLightGlyph {
		t.Fatal("wide cursor block did not redraw \"本\" in high-contrast reversed foreground color")
	}

	insertList, err := drawlist.Build(makeState("insert"))
	if err != nil {
		t.Fatalf("drawlist.Build(insert): %v", err)
	}
	insertFrame, err := planner.Plan(insertList)
	if err != nil {
		t.Fatalf("Plan(insert): %v", err)
	}
	insertImg := renderImageWithView(insertFrame, width, height)
	if got := pixelAt(insertImg, 0, height/2); !nearColor(got, darkCursor) {
		t.Fatalf("insert beam left-edge pixel = %+v, want dark cursor %+v", got, darkCursor)
	}
	// Right edge of second cell of "本" must remain light background (beam does not black out the whole block).
	if got := pixelAt(insertImg, 2*cellW-1, 0); !nearColor(got, lightGlyph) {
		t.Fatalf("insert beam unexpectedly filled whole block at (%d,0): %+v", 2*cellW-1, got)
	}
	// Beam stays crisp (1.5 DIP) so x=2 at top border remains light background, not a chunky 3px bar.
	if got := pixelAt(insertImg, 2, 0); !nearColor(got, lightGlyph) {
		t.Fatalf("insert beam too wide at (2,0): %+v, want light background %+v", got, lightGlyph)
	}
	// Wide glyph "本" is horizontally centered in its 2-cell block (non-zero positive center offset when narrower than 2 cells).
	wideFont := commandFont(cursorCmd)
	wideGlyphs := ui.Shape("本", wideFont)
	if len(wideGlyphs) > 0 && wideGlyphs[0].Advance < cursorCmd.W-0.5 {
		if off := glyphCenterOffset(wideGlyphs, cursorCmd.W); off <= 0 {
			t.Fatalf("glyphCenterOffset(wide \"本\", %.2f) = %.2f, want > 0", cursorCmd.W, off)
		}
	}
}

func renderImage(frame renderlist.Frame, width, height int) *image.RGBA {
	return captureTester(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, _ ui.Rect) { Draw(p, frame) })
	}, width, height)
}

func renderImageWithView(frame renderlist.Frame, width, height int) *image.RGBA {
	view := NewView()
	view.SetFrame(frame)
	return renderView(view, width, height)
}

func renderView(view *View, width, height int) *image.RGBA {
	return captureTester(func(c *ui.Context) { view.Draw(c) }, width, height)
}

func captureTester(view func(c *ui.Context), width, height int) *image.RGBA {
	tester := ui.NewTester(view, width, height)
	rendered := tester.Image()
	if rendered == nil {
		return &image.RGBA{}
	}
	return rendered
}

func pixelAt(rendered *image.RGBA, x, y int) imagecolor.RGBA {
	if rendered == nil || x < 0 || y < 0 || x >= rendered.Bounds().Dx() || y >= rendered.Bounds().Dy() {
		return imagecolor.RGBA{}
	}
	return rendered.RGBAAt(x, y)
}

func hasColorNear(rendered *image.RGBA, wanted imagecolor.RGBA) bool {
	if rendered == nil {
		return false
	}
	for y := 0; y < rendered.Bounds().Dy(); y++ {
		for x := 0; x < rendered.Bounds().Dx(); x++ {
			if nearColor(rendered.RGBAAt(x, y), wanted) {
				return true
			}
		}
	}
	return false
}

func nearColor(left, right imagecolor.RGBA) bool {
	return absByte(left.R, right.R) <= 24 && absByte(left.G, right.G) <= 24 &&
		absByte(left.B, right.B) <= 24 && absByte(left.A, right.A) <= 24
}

func absByte(left, right uint8) uint8 {
	if left > right {
		return left - right
	}
	return right - left
}

func hasGreenForeground(rendered *image.RGBA) bool {
	if rendered == nil {
		return false
	}
	for y := 0; y < rendered.Bounds().Dy(); y++ {
		for x := 0; x < rendered.Bounds().Dx(); x++ {
			pixel := rendered.RGBAAt(x, y)
			if pixel.G > 100 && pixel.G > pixel.R+40 && pixel.G > pixel.B+40 {
				return true
			}
		}
	}
	return false
}

func TestPlannerPreservesFallbackChainsInOneFrame(t *testing.T) {
	resolver := mustTestResolver(t)
	planner := NewPlanner(resolver)
	cases := []struct {
		text     string
		contains []string
		excludes []string
	}{
		{text: "render", contains: []string{"menlo"}, excludes: []string{"pingfang", "emoji", "nerd"}},
		{text: "世", contains: []string{"menlo", "pingfang"}, excludes: []string{"emoji", "nerd"}},
		{text: "🌍", contains: []string{"menlo", "emoji"}, excludes: []string{"pingfang", "nerd"}},
		{text: "\uE0B0", contains: []string{"menlo"}, excludes: []string{"pingfang", "emoji"}},
	}
	items := make([]drawlist.Item, 0, len(cases))
	for index, testCase := range cases {
		items = append(items, drawlist.Item{
			Kind: "text", Row: 0, Col: index, Width: 1, Height: 1,
			Text: testCase.text, FG: 0xffffff, BG: 0x101010,
		})
	}
	frame, err := planner.Plan(&drawlist.List{Version: 1, Seq: 300, Layers: []drawlist.Layer{{
		Grid: 1, Items: items,
	}}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(frame.Commands) != len(cases) {
		t.Fatalf("commands = %d, want %d: %+v", len(frame.Commands), len(cases), frame.Commands)
	}
	for index, testCase := range cases {
		want := resolver.FontFor(testCase.text, false, false)
		got := frame.Commands[index]
		if got.Text != testCase.text || got.Family != want.Family || got.Size != want.Size ||
			got.Weight != want.Weight || got.Italic != want.Italic || got.Features != want.Features {
			t.Errorf("command %d = %+v, want text %q and font %+v", index, got, testCase.text, want)
		}
		if testCase.text == "\uE0B0" && strings.Contains(strings.ToLower(want.Family), "nerd") {
			testCase.contains = append(testCase.contains, "nerd")
		}
		family := strings.ToLower(got.Family)
		for _, contains := range testCase.contains {
			if !strings.Contains(family, contains) {
				t.Errorf("%q family %q lacks fallback %q", testCase.text, got.Family, contains)
			}
		}
		for _, excludes := range testCase.excludes {
			if strings.Contains(family, excludes) {
				t.Errorf("%q family %q unexpectedly contains %q", testCase.text, got.Family, excludes)
			}
		}
	}
}

func TestPlannerASCIIFontCacheTracksResolverConfig(t *testing.T) {
	beforeResolver := mustTestResolver(t)
	afterResolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h22", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver(h22): %v", err)
	}
	planner := NewPlanner(beforeResolver)
	list := &drawlist.List{Version: 1, Seq: 301, Layers: []drawlist.Layer{{Items: []drawlist.Item{
		{Kind: "text", Width: 1, Height: 1, Text: "A", FG: 0xffffff},
	}}}}
	before, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan before config change: %v", err)
	}
	if len(before.Commands) != 1 || before.Commands[0].Size == 0 {
		t.Fatalf("before commands = %+v, want one resolved font", before.Commands)
	}

	planner.mu.Lock()
	planner.resolver = afterResolver
	planner.mu.Unlock()
	if planner.resolver.Config().GUIFont != "Menlo:h22" {
		t.Fatal("planner resolver config did not change")
	}

	after, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan after config change: %v", err)
	}
	want := afterResolver.FontFor("A", false, false)
	if len(after.Commands) != 1 || after.Commands[0].Size != want.Size || after.Commands[0].Family != want.Family {
		t.Fatalf("after commands = %+v, want fresh font %+v", after.Commands, want)
	}
	if before.Commands[0].Size == after.Commands[0].Size {
		t.Fatalf("font cache did not invalidate: size stayed %v", before.Commands[0].Size)
	}
}

func TestPlannerOtherFontCacheIsBoundedAndTracksConfig(t *testing.T) {
	beforeResolver := mustTestResolver(t)
	oldConfig := beforeResolver.Config()
	planner := NewPlanner(beforeResolver)
	planner.mu.Lock()
	planner.otherFonts = map[otherFontQuery]text.Font{
		{text: "世"}: {Family: "stale", Size: 14},
	}
	planner.config = oldConfig
	planner.ready = true
	planner.mu.Unlock()
	items := make([]drawlist.Item, 0, otherFontCacheCapacity+2)
	for index := 0; index < otherFontCacheCapacity+2; index++ {
		character := rune(0x100 + index)
		items = append(items, drawlist.Item{
			Kind: "text", Row: 0, Col: index, Width: 1, Height: 1,
			Text: string(character), FG: 0xffffff,
		})
	}
	list := &drawlist.List{Version: 1, Seq: 307, Layers: []drawlist.Layer{{Grid: 1, Items: items}}}
	before, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan before config change: %v", err)
	}
	if len(before.Commands) != len(items) {
		t.Fatalf("commands = %d, want %d standalone non-ASCII glyphs", len(before.Commands), len(items))
	}
	planner.mu.Lock()
	count := len(planner.otherFonts)
	planner.mu.Unlock()
	if count > otherFontCacheCapacity {
		t.Fatalf("otherFonts size = %d, want <= %d", count, otherFontCacheCapacity)
	}
	planner.mu.Lock()
	_, stale := planner.otherFonts[otherFontQuery{text: "世"}]
	planner.mu.Unlock()
	if stale {
		t.Fatal("capacity clearing kept a preexisting non-ASCII font")
	}

	afterResolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h22", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver(h22): %v", err)
	}
	planner.mu.Lock()
	planner.resolver = afterResolver
	planner.mu.Unlock()
	probe := "世"
	probeList := &drawlist.List{Version: 1, Seq: 308, Layers: []drawlist.Layer{{Grid: 1, Items: []drawlist.Item{
		{Kind: "text", Width: 1, Height: 1, Text: probe, FG: 0xffffff},
	}}}}
	want := afterResolver.FontFor(probe, false, false)
	oldFont := beforeResolver.FontFor(probe, false, false)
	for attempt := 0; attempt < 2; attempt++ {
		frame, err := planner.Plan(probeList)
		if err != nil {
			t.Fatalf("Plan after config change (attempt %d): %v", attempt, err)
		}
		if len(frame.Commands) != 1 {
			t.Fatalf("commands = %d, want one non-ASCII glyph: %+v", len(frame.Commands), frame.Commands)
		}
		got := frame.Commands[0]
		if got.Text != probe || got.Family != want.Family || got.Size != want.Size ||
			got.Weight != want.Weight || got.Italic != want.Italic || got.Features != want.Features {
			t.Fatalf("command = %+v, want fresh non-ASCII font %+v", got, want)
		}
	}
	if oldFont.Size == want.Size {
		t.Fatalf("config change did not alter font size: both %v", want.Size)
	}
	planner.mu.Lock()
	cached, ok := planner.otherFonts[otherFontQuery{text: probe}]
	delete(planner.otherFonts, otherFontQuery{text: probe})
	planner.resolver = beforeResolver
	planner.mu.Unlock()
	if !ok || cached != want {
		t.Fatalf("new-config non-ASCII cache = %+v (present %v), want %+v", cached, ok, want)
	}
	staleFont := planner.fontFor(drawlist.Item{Kind: "text", Text: probe}, [4]text.Font{}, oldConfig)
	planner.mu.Lock()
	planner.resolver = afterResolver
	_, polluted := planner.otherFonts[otherFontQuery{text: probe}]
	planner.mu.Unlock()
	if staleFont != oldFont {
		t.Fatalf("stale-config call returned %+v, want old font %+v", staleFont, oldFont)
	}
	if polluted {
		t.Fatal("stale config writeback polluted the new non-ASCII font cache")
	}
}

func TestPlannerKeepsFillCrossingTopOfLayerClip(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	list := &drawlist.List{Version: 1, Seq: 302, Width: 6, Height: 4, Layers: []drawlist.Layer{{
		Grid: 2, ScreenRow: 0, ScreenCol: 0, Width: 6, Height: 3,
		Clip: drawlist.Rect{X: 1, Y: 2, W: 3, H: 2},
		Items: []drawlist.Item{{
			Kind: "fill", Row: 0, Col: 0, Width: 6, Height: 3, BG: 0x123456,
		}},
	}}}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(frame.Commands) != 1 {
		t.Fatalf("commands = %d, want clipped multi-row fill: %+v", len(frame.Commands), frame.Commands)
	}
	fill := frame.Commands[0]
	want := renderlist.Command{
		Op: "fill", X: cell.Width, Y: 2 * cell.Height,
		W: 3 * cell.Width, H: cell.Height, BG: 0x123456,
	}
	if fill != want {
		t.Fatalf("clipped fill = %+v, want %+v", fill, want)
	}
}

func TestPlannerMergesOnlyVisibleColumnsInsideClip(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	const text = "ABCDEFGHIJKL"
	items := make([]drawlist.Item, 0, len(text))
	for index, character := range []byte(text) {
		items = append(items, drawlist.Item{
			Kind: "text", Row: 1, Col: index, Width: 1, Height: 1,
			Text: string(character), FG: 0xffffff, BG: 0x101010,
		})
	}
	list := &drawlist.List{Version: 1, Seq: 303, Width: 12, Height: 3, Layers: []drawlist.Layer{{
		Grid: 3, ScreenRow: 0, ScreenCol: 0, Width: 12, Height: 3,
		Clip: drawlist.Rect{X: 3, Y: 0, W: 6, H: 3}, Items: items,
	}}}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(frame.Commands) != 1 {
		t.Fatalf("commands = %d, want one visible run: %+v", len(frame.Commands), frame.Commands)
	}
	got := frame.Commands[0]
	if got.Text != text[3:9] || got.X != 3*cell.Width || got.W != 6*cell.Width {
		t.Fatalf("visible run = %+v, want Text=%q X=%v W=%v", got, text[3:9], 3*cell.Width, 6*cell.Width)
	}
	metas := metadataFor(frame).rows
	if len(metas) != 1 || metas[0].clip != (ui.Rect{X: 3 * cell.Width, W: 6 * cell.Width, H: 3 * cell.Height}) {
		t.Fatalf("run metadata = %+v, want layer clip", metas)
	}
}

func TestLongMergedUnderdashedRunUsesCellSizedDashes(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	items := make([]drawlist.Item, 0, 24)
	for column := 0; column < 24; column++ {
		items = append(items, drawlist.Item{
			Kind: "text", Row: 0, Col: column, Width: 1, Height: 1,
			Text: "x", FG: 0xffffff, BG: 0x101010, Attrs: []string{"underdashed"},
		})
	}
	frame, err := planner.Plan(&drawlist.List{Version: 1, Seq: 304, Layers: []drawlist.Layer{{
		Grid: 4, Items: items,
	}}})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(frame.Commands) != 1 || frame.Commands[0].Text != strings.Repeat("x", 24) {
		t.Fatalf("commands = %+v, want one 24-column run", frame.Commands)
	}
	command := frame.Commands[0]
	command.SP = 0xff0000
	view := NewView()
	view.SetFrame(renderlist.Frame{Version: 1, Seq: 304, Commands: []renderlist.Command{command}})
	view.impl.meta = frameMetadata{rows: []rowMeta{{attrMask: attrUnderdashed}}}
	image := renderView(view, int(24*cell.Width), int(cell.Height))
	segments := 0
	inSegment := false
	for x := 0; x < int(24*cell.Width); x++ {
		colored := false
		for y := 0; y < int(cell.Height); y++ {
			if nearColor(pixelAt(image, x, y), imagecolor.RGBA{R: 255, A: 255}) {
				colored = true
				break
			}
		}
		if colored && !inSegment {
			segments++
		}
		inSegment = colored
	}
	if segments < 6 {
		t.Fatalf("24-cell underdashed run painted %d segments, want >= 6 cell-sized dashes", segments)
	}
}

func TestDrawFrameGroupsClipAndKeepsAcrossLayerOrder(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	clip := drawlist.Rect{X: 2, Y: 1, W: 6, H: 2}
	floatItems := []drawlist.Item{
		{Kind: "fill", Row: 0, Col: 0, Width: 12, Height: 3, BG: 0x005500},
	}
	for column := 0; column < 12; column++ {
		floatItems = append(floatItems, drawlist.Item{
			Kind: "text", Row: 1, Col: column, Width: 1, Height: 1,
			Text: "Z", FG: 0x00ff00, BG: 0x005500,
		})
	}
	floatItems = append(floatItems,
		drawlist.Item{Kind: "box", Row: 1, Col: 3, Width: 1, Height: 1, Rune: "│", FG: 0x00ff00, BG: 0x005500},
		drawlist.Item{Kind: "cursor", Row: 1, Col: 4, Width: 1, Height: 1, FG: 0x005500, BG: 0x00ff00},
	)
	list := &drawlist.List{Version: 1, Seq: 305, Width: 12, Height: 4, Layers: []drawlist.Layer{
		{
			Grid: 1, Width: 12, Height: 4, Clip: drawlist.Rect{X: 0, Y: 0, W: 12, H: 4},
			Items: []drawlist.Item{{Kind: "fill", Row: 0, Col: 0, Width: 12, Height: 4, BG: 0x000010}},
		},
		{
			Grid: 2, ScreenRow: 1, ScreenCol: 2, Width: 12, Height: 3, Clip: clip, Items: floatItems,
		},
		{
			Grid: 1, Width: 12, Height: 4, Clip: drawlist.Rect{X: 0, Y: 0, W: 12, H: 4},
			Items: []drawlist.Item{{Kind: "fill", Row: 3, Col: 0, Width: 12, Height: 1, BG: 0x222222}},
		},
	}}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	metas := metadataFor(frame).rows
	if len(metas) != len(frame.Commands) {
		t.Fatalf("metadata entries = %d, commands = %d", len(metas), len(frame.Commands))
	}
	clipRect := ui.Rect{X: 2 * cell.Width, Y: cell.Height, W: 6 * cell.Width, H: 2 * cell.Height}
	grouped := 0
	clipOps := 0
	clipGrids := map[int]bool{}
	for index, command := range frame.Commands {
		if command.Op == "fill" && metas[index].clip != (ui.Rect{}) {
			t.Errorf("fill %d retained GPU clip: %+v", index, metas[index].clip)
		}
		if metas[index].clip == clipRect {
			grouped++
			clipOps++
			clipGrids[int(metas[index].grid)] = true
		}
	}
	if grouped < 3 {
		t.Fatalf("clip group has %d commands, want glyphs, box and cursor grouped", grouped)
	}
	if len(clipGrids) != 1 {
		t.Errorf("clip group merged %d grids, want one layer", len(clipGrids))
	}

	view := NewView()
	view.SetFrame(frame)
	image := renderView(view, int(12*cell.Width), int(4*cell.Height))
	root := imagecolor.RGBA{B: 0x10, A: 255}
	floatBG := imagecolor.RGBA{G: 0x55, A: 255}
	lateBG := imagecolor.RGBA{R: 0x22, G: 0x22, B: 0x22, A: 255}
	if got := pixelAt(image, int(3*cell.Width), int(2*cell.Height)); !nearColor(got, floatBG) && got.G <= got.B {
		t.Errorf("float fill did not cover root background: %+v", got)
	}
	if got := pixelAt(image, int(3*cell.Width), int(3.5*cell.Height)); !nearColor(got, lateBG) {
		t.Errorf("late root layer did not paint over lower float: %+v", got)
	}
	for _, point := range []struct {
		x, y float32
		want imagecolor.RGBA
	}{
		{x: 1.5, y: 1.5, want: root},
		{x: 8.5, y: 1.5, want: root},
		{x: 1.5, y: 3.5, want: lateBG},
	} {
		if got := pixelAt(image, int(point.x*cell.Width), int(point.y*cell.Height)); !nearColor(got, point.want) {
			t.Errorf("clip leaked at (%v,%v): got %+v, want %+v", point.x, point.y, got, point.want)
		}
	}
	if !hasColorNear(image, imagecolor.RGBA{G: 255, A: 255}) {
		t.Error("clipped float glyphs were not painted")
	}

	commandCount := len(frame.Commands)
	clipCalls := 0
	for index := 0; index < commandCount; {
		meta := metas[index]
		end := index + 1
		if meta.clip.W > 0 && meta.clip.H > 0 && frame.Commands[index].Op != "fill" {
			for end < commandCount && end < len(metas) && frame.Commands[end].Op != "fill" &&
				metas[end].clip == meta.clip && metas[end].grid == meta.grid {
				end++
			}
		}
		if meta.clip.W > 0 && meta.clip.H > 0 {
			clipCalls++
		}
		index = end
	}
	if clipCalls > 2 {
		t.Errorf("frame needs %d GPU clips, want at most two clip groups", clipCalls)
	}
}

func TestUnknownAttrsPreventMergingAcrossDifferentValues(t *testing.T) {
	resolver := mustTestResolver(t)
	cell := resolver.Cell()
	planner := NewPlanner(resolver)
	list := &drawlist.List{Version: 1, Seq: 306, Layers: []drawlist.Layer{{Items: []drawlist.Item{
		{Kind: "text", Row: 0, Col: 0, Width: 1, Height: 1, Text: "A", FG: 0xffffff, Attrs: []string{"altfont"}},
		{Kind: "text", Row: 0, Col: 1, Width: 1, Height: 1, Text: "B", FG: 0xffffff, Attrs: []string{"nocombine"}},
		{Kind: "text", Row: 0, Col: 2, Width: 1, Height: 1, Text: "C", FG: 0xffffff, Attrs: []string{"altfont"}},
	}}}}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(frame.Commands) != 3 {
		t.Fatalf("commands = %d, want 3 unmergeable unknown-attr items: %+v", len(frame.Commands), frame.Commands)
	}
	for index, want := range []string{"A", "B", "C"} {
		got := frame.Commands[index]
		if got.Text != want || got.X != float32(index)*cell.Width || got.W != cell.Width {
			t.Errorf("command %d = %+v, want single %q cell", index, got, want)
		}
	}
	metas := metadataFor(frame).rows
	for index, meta := range metas {
		if meta.attrMask&attrOther == 0 {
			t.Errorf("metadata %d lost unknown attr marker: %+v", index, meta)
		}
	}
}

func TestBlendBackdropLeavesUnderlyingContentVisible(t *testing.T) {
	base := renderImage(renderlist.Frame{Version: 1, Seq: 400, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 64, H: 32, BG: 0x2060c0},
	}}, 64, 32)
	blended := renderImage(renderlist.Frame{Version: 1, Seq: 401, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 64, H: 32, BG: 0x2060c0},
		{Op: "fill", X: 0, Y: 0, W: 64, H: 32, BG: 0x000000, Blend: 60},
	}}, 64, 32)
	transparent := renderImage(renderlist.Frame{Version: 1, Seq: 402, Commands: []renderlist.Command{
		{Op: "fill", X: 0, Y: 0, W: 64, H: 32, BG: 0x2060c0},
		{Op: "fill", X: 0, Y: 0, W: 64, H: 32, BG: 0x000000, Blend: 100},
	}}, 64, 32)

	basePixel := base.RGBAAt(16, 16)
	blendedPixel := blended.RGBAAt(16, 16)
	transparentPixel := transparent.RGBAAt(16, 16)

	if blendedPixel.B == 0 || blendedPixel.B >= basePixel.B {
		t.Fatalf("blended pixel = %+v, base = %+v, want darkened but visible blue channel", blendedPixel, basePixel)
	}
	if transparentPixel != basePixel {
		t.Fatalf("blend=100 pixel = %+v, want unchanged base %+v", transparentPixel, basePixel)
	}
}

func TestVerticalBaselineCentersGlyphWithinExpandedCell(t *testing.T) {
	tight := verticalBaseline(10, 16, 12, 16)
	expanded := verticalBaseline(10, 24, 12, 16)
	if tight != 22 {
		t.Fatalf("tight baseline = %v, want 22", tight)
	}
	if expanded != 26 {
		t.Fatalf("expanded baseline = %v, want 26 (10 + (24-16)/2 + 12)", expanded)
	}
}
