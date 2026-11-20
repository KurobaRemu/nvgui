package render_test

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/renderlist"
	"nvgui/text"
)

func mustResolver(t *testing.T) *text.Resolver {
	t.Helper()
	r, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r == nil {
		t.Fatal("NewResolver returned nil")
	}
	return r
}

func approxEqual(a, b, tol float32) bool {
	return float32(math.Abs(float64(a-b))) <= tol
}

func TestRenderPlannerDeterminismAndCommands(t *testing.T) {
	resolver := mustResolver(t)
	cell := resolver.Cell()
	if cell.Width <= 0 || cell.Height <= 0 {
		t.Fatalf("Cell() = %+v, want positive cell size", cell)
	}

	planner := render.NewPlanner(resolver)
	if planner == nil {
		t.Fatal("NewPlanner returned nil")
	}
	if _, err := planner.Plan(nil); err == nil {
		t.Fatal("Plan(nil) returned nil error, want error")
	}

	list := &drawlist.List{
		Version: 1,
		Seq:     42,
		Width:   40,
		Height:  10,
		Layers: []drawlist.Layer{
			{
				Grid:      1,
				ScreenRow: 0,
				ScreenCol: 0,
				Width:     40,
				Height:    10,
				Clip:      drawlist.Rect{X: 0, Y: 0, W: 40, H: 10},
				Items: []drawlist.Item{
					{Kind: "fill", Row: 0, Col: 0, Width: 40, Height: 10, BG: 0x1e1e2e},
					{Kind: "text", Row: 0, Col: 0, Width: 5, Height: 1, Text: "hello", FG: 0xcdd6f4, BG: 0x1e1e2e},
					{Kind: "box", Row: 1, Col: 2, Width: 1, Height: 1, Rune: "─", FG: 0x89b4fa, BG: 0x1e1e2e},
					{Kind: "box", Row: 1, Col: 3, Width: 1, Height: 1, Rune: "█", FG: 0xa6e3a1, BG: 0x1e1e2e},
					{Kind: "box", Row: 1, Col: 4, Width: 1, Height: 1, Rune: "\uE0B0", FG: 0xf9e2af, BG: 0x1e1e2e},
					{Kind: "cursor", Row: 2, Col: 5, Width: 1, Height: 1, FG: 0x1e1e2e, BG: 0xf5e0dc},
				},
			},
			{
				Grid:      2,
				ScreenRow: 2,
				ScreenCol: 4,
				Width:     10,
				Height:    3,
				Clip:      drawlist.Rect{X: 4, Y: 2, W: 10, H: 3},
				Items: []drawlist.Item{
					{Kind: "fill", Row: 0, Col: 0, Width: 10, Height: 3, BG: 0x313244},
					{Kind: "box", Row: 0, Col: 0, Width: 1, Height: 1, Rune: "╭", FG: 0xcba6f7, BG: 0x313244},
					{Kind: "text", Row: 1, Col: 1, Width: 4, Height: 1, Text: "float", FG: 0xcdd6f4, BG: 0x313244},
				},
			},
		},
	}

	f1, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	f2, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("second Plan: %v", err)
	}
	if !reflect.DeepEqual(f1, f2) {
		t.Fatal("Plan is not deterministic for identical *drawlist.List")
	}
	if f1.Version != 1 || f1.Seq != 42 {
		t.Errorf("Frame header = (v=%d, seq=%d), want (1, 42)", f1.Version, f1.Seq)
	}
	if len(f1.Commands) == 0 {
		t.Fatal("Plan produced zero commands")
	}

	foundBoxRunes := map[string]bool{}
	foundCursor := false
	foundFloatGlyph := false
	for _, cmd := range f1.Commands {
		switch cmd.Op {
		case "box":
			foundBoxRunes[cmd.Rune] = true
		case "cursor":
			foundCursor = true
			wantX := 5 * cell.Width
			wantY := 2 * cell.Height
			if !approxEqual(cmd.X, wantX, 0.5) || !approxEqual(cmd.Y, wantY, 0.5) {
				t.Errorf("cursor command pos = (%v, %v), want (%v, %v)", cmd.X, cmd.Y, wantX, wantY)
			}
		case "glyphs":
			if cmd.Text == "float" {
				foundFloatGlyph = true
				// Layer 2 at (ScreenRow=2, ScreenCol=4) + Item (Row=1, Col=1) => (Row=3, Col=5)
				wantX := 5 * cell.Width
				wantY := 3 * cell.Height
				if !approxEqual(cmd.X, wantX, 0.5) || !approxEqual(cmd.Y, wantY, 0.5) {
					t.Errorf("float glyph pos = (%v, %v), want (%v, %v)", cmd.X, cmd.Y, wantX, wantY)
				}
			}
		}
	}
	for _, wantRune := range []string{"─", "█", "\uE0B0", "╭"} {
		if !foundBoxRunes[wantRune] {
			t.Errorf("missing Op=\"box\" command for rune %q", wantRune)
		}
	}
	if !foundCursor {
		t.Error("missing Op=\"cursor\" command")
	}
	if !foundFloatGlyph {
		t.Error("missing Op=\"glyphs\" command for float text")
	}
}

func TestRenderViewHeadlessGoldenAndCaret(t *testing.T) {
	v := render.NewView()
	if v == nil {
		t.Fatal("NewView returned nil")
	}

	frame := renderlist.Frame{
		Version: 1,
		Seq:     1,
		Commands: []renderlist.Command{
			{Op: "fill", X: 0, Y: 0, W: 400, H: 200, BG: 0x101820},
			{Op: "glyphs", X: 10, Y: 10, W: 80, H: 18, Text: "nvgui 世界", Family: "Menlo", Size: 14, FG: 0xffffff, BG: 0x101820},
			{Op: "box", X: 10, Y: 40, W: 10, H: 18, Rune: "╭", FG: 0x00ff88, BG: 0x101820},
			{Op: "box", X: 20, Y: 40, W: 10, H: 18, Rune: "─", FG: 0x00ff88, BG: 0x101820},
			{Op: "box", X: 30, Y: 40, W: 10, H: 18, Rune: "█", FG: 0x00ff88, BG: 0x101820},
			{Op: "box", X: 40, Y: 40, W: 10, H: 18, Rune: "\uE0B0", FG: 0x00ff88, BG: 0x101820},
			{Op: "cursor", X: 50, Y: 10, W: 9, H: 18, FG: 0x101820, BG: 0xffcc00},
		},
	}
	v.SetFrame(frame)

	caret := v.Caret()
	if !approxEqual(caret.X, 50, 0.5) || !approxEqual(caret.Y, 10, 0.5) || !approxEqual(caret.W, 9, 0.5) || !approxEqual(caret.H, 18, 0.5) {
		t.Errorf("Caret() = %+v, want {X:50 Y:10 W:9 H:18}", caret)
	}

	tester := ui.NewTester(func(c *ui.Context) {
		v.Draw(c)
	}, 400, 200)
	img1 := tester.Image()
	if img1 == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	if img1.Bounds().Dx() < 400 || img1.Bounds().Dy() < 200 {
		t.Fatalf("Image bounds = %v, want at least 400x200", img1.Bounds())
	}
	if isUniformImage(img1.Pix) {
		t.Fatal("rendered 400x200 image is completely uniform (nothing drawn)")
	}
	pix1 := append([]byte(nil), img1.Pix...)

	tester.Frame()
	img2 := tester.Image()
	if img2 == nil || !bytes.Equal(pix1, img2.Pix) {
		t.Fatal("headless golden rendering is not byte-deterministic across repeated frames")
	}
}

func TestRenderBoxDrawsPixels(t *testing.T) {
	v := render.NewView()
	if v == nil {
		t.Fatal("NewView returned nil")
	}
	v.SetFrame(renderlist.Frame{
		Version: 1,
		Seq:     1,
		Commands: []renderlist.Command{
			{Op: "fill", X: 0, Y: 0, W: 100, H: 40, BG: 0x000000},
			{Op: "box", X: 10, Y: 10, W: 12, H: 20, Rune: "█", FG: 0xffffff, BG: 0x000000},
			{Op: "box", X: 22, Y: 10, W: 12, H: 20, Rune: "─", FG: 0xffffff, BG: 0x000000},
		},
	})
	tester := ui.NewTester(func(c *ui.Context) {
		v.Draw(c)
	}, 100, 40)
	img := tester.Image()
	if img == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	if isUniformImage(img.Pix) {
		t.Fatal("box-only frame produced uniform pixels; Op=\"box\" did not paint")
	}
}

func TestRenderPackageDrawFunction(t *testing.T) {
	frame := renderlist.Frame{
		Version: 1,
		Seq:     1,
		Commands: []renderlist.Command{
			{Op: "fill", X: 0, Y: 0, W: 120, H: 40, BG: 0x101820},
			{Op: "box", X: 8, Y: 8, W: 12, H: 20, Rune: "█", FG: 0x00ff88, BG: 0x101820},
			{Op: "cursor", X: 28, Y: 8, W: 10, H: 20, FG: 0x101820, BG: 0xffcc00},
		},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, _ ui.Rect) {
			render.Draw(p, frame)
		})
	}, 120, 40)
	img := tester.Image()
	if img == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	if isUniformImage(img.Pix) {
		t.Fatal("render.Draw produced uniform pixels; package-level Draw did not paint")
	}
}

func TestRenderViewRowCacheSingleLineEdit(t *testing.T) {
	v := render.NewView()
	if v == nil {
		t.Fatal("NewView returned nil")
	}
	makeFrame := func(seq uint64, line2 string) renderlist.Frame {
		lines := []string{"row 0 alpha", "row 1 beta", line2, "row 3 delta", "row 4 epsilon"}
		cmds := []renderlist.Command{
			{Op: "fill", X: 0, Y: 0, W: 400, H: 200, BG: 0x101010},
		}
		for i, line := range lines {
			cmds = append(cmds, renderlist.Command{
				Op:     "glyphs",
				X:      0,
				Y:      float32(i) * 18,
				W:      200,
				H:      18,
				Text:   line,
				Family: "Menlo",
				Size:   14,
				FG:     0xffffff,
				BG:     0x101010,
			})
		}
		return renderlist.Frame{Version: 1, Seq: seq, Commands: cmds}
	}

	v.SetFrame(makeFrame(1, "row 2 gamma"))
	tester := ui.NewTester(func(c *ui.Context) {
		v.Draw(c)
	}, 400, 200)
	_ = tester.Image()
	s1 := v.ShapedRows()
	if s1 < 5 {
		t.Fatalf("ShapedRows after initial 5-row render = %d, want >= 5", s1)
	}

	// Identical content on next frame must not reshape any row.
	v.SetFrame(makeFrame(2, "row 2 gamma"))
	tester.Frame()
	_ = tester.Image()
	s2 := v.ShapedRows()
	if s2 != s1 {
		t.Fatalf("ShapedRows after identical repeat frame = %d, want %d (no reshape)", s2, s1)
	}

	// Editing only 1 row must reshape at most 1 row (never reset the whole table).
	v.SetFrame(makeFrame(3, "row 2 GAMMA EDITED"))
	tester.Frame()
	_ = tester.Image()
	s3 := v.ShapedRows()
	delta := s3 - s2
	if delta < 1 || delta > 1 {
		t.Fatalf("ShapedRows delta after single-row edit = %d (from %d to %d), want 1", delta, s2, s3)
	}
}

func isUniformImage(pix []byte) bool {
	if len(pix) < 8 {
		return true
	}
	r0, g0, b0, a0 := pix[0], pix[1], pix[2], pix[3]
	for i := 4; i+3 < len(pix); i += 4 {
		if pix[i] != r0 || pix[i+1] != g0 || pix[i+2] != b0 || pix[i+3] != a0 {
			return false
		}
	}
	return true
}
