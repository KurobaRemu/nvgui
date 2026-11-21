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
	metas := metadataFor(partial)
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
	metas := append(metadataFor(frame), rowMeta{})
	view := NewView()
	view.SetFrame(frame)
	view.impl.metas = metas
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
	storeMetadata(renderlist.Frame{Seq: firstSeq, Commands: commands}, []rowMeta{{grid: 1}, {attrs: "underline"}})
	storeMetadata(renderlist.Frame{Seq: otherSeq, Commands: commands}, []rowMeta{{grid: 2}, {attrs: "bold"}})
	if got := metadataFor(renderlist.Frame{Seq: firstSeq, Commands: commands}); got[0].grid != 1 || got[1].attrs != "underline" {
		t.Fatalf("metadata for seq %d = %+v, want first frame metadata", firstSeq, got)
	}
	if got := metadataFor(renderlist.Frame{Seq: otherSeq, Commands: commands}); got[0].grid != 2 || got[1].attrs != "bold" {
		t.Fatalf("metadata for seq %d = %+v, want second frame metadata", otherSeq, got)
	}
	changed := append([]renderlist.Command(nil), commands...)
	changed[1].BG = 99
	if got := metadataFor(renderlist.Frame{Seq: firstSeq, Commands: changed}); got != nil {
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
	first := cache.glyphs(command, rowMeta{grid: 4, attrs: "bold"})
	if len(first) == 0 {
		t.Fatal("shaping returned no glyphs")
	}
	second := cache.glyphs(command, rowMeta{grid: 4, attrs: "bold"})
	if got := cache.shapedRows(); got != 1 {
		t.Fatalf("ShapedRows after repeated row = %d, want 1", got)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("cached glyphs changed between identical calls")
	}
	edited := command
	edited.Text = "changed row"
	_ = cache.glyphs(edited, rowMeta{grid: 4, attrs: "bold"})
	if got := cache.shapedRows(); got != 2 {
		t.Fatalf("ShapedRows after row edit = %d, want 2", got)
	}
	_ = cache.glyphs(command, rowMeta{grid: 4, attrs: "italic"})
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
			view.impl.metas = []rowMeta{{attrs: attr}, {attrs: attr}}
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
	view.impl.metas = []rowMeta{{attrs: "underline"}, {attrs: "underline"}}
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
