package render

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/text"
)

func BenchmarkRenderPlan4K(b *testing.B) {
	list := benchmarkPlanList(240, 70)
	planner := NewPlanner(mustBenchResolver(b))
	batches := 0.0
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		frame, err := planner.Plan(list)
		if err != nil {
			b.Fatalf("Plan: %v", err)
		}
		batches += float64(len(frame.Commands))
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(batches/float64(b.N), "batches/op")
	}
}

func BenchmarkRenderDraw4K(b *testing.B) {
	frame := benchmarkDrawFrame(b, 160, 50)
	view := NewView()
	cell := mustBenchResolver(b).Cell()
	tester := ui.NewTester(func(c *ui.Context) {
		view.Draw(c)
	}, int(math.Ceil(float64(160*cell.Width))), int(math.Ceil(float64(50*cell.Height))))
	view.SetFrame(frame)
	batches := 0.0
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		tester.Frame()
		batches += float64(len(frame.Commands))
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(batches/float64(b.N), "batches/op")
	}
}

func mustBenchResolver(b *testing.B) *text.Resolver {
	b.Helper()
	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		b.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func benchmarkPlanList(width, height int) *drawlist.List {
	rootItems := []drawlist.Item{
		{Kind: "fill", Row: 0, Col: 0, Width: width, Height: height, BG: 0x1e1e2e},
	}
	code := []byte("func_renderFrame4K_ctx_Frame_error_return_drawGlyphRuns_ctx_4096_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			if column%13 == 0 {
				rootItems = append(rootItems, drawlist.Item{
					Kind: "text", Row: row, Col: column, Width: 1, Height: 1,
					Text: "\uE0B0", FG: 0xa6e3a1, BG: 0x1e1e2e,
				})
				continue
			}
			if column%17 == 0 {
				rootItems = append(rootItems, drawlist.Item{
					Kind: "text", Row: row, Col: column, Width: 2, Height: 1,
					Text: "世", FG: 0xcdd6f4, BG: 0x1e1e2e,
				})
				column++
				continue
			}
			if column%29 == 0 {
				rootItems = append(rootItems, drawlist.Item{
					Kind: "text", Row: row, Col: column, Width: 2, Height: 1,
					Text: "🌍", FG: 0xcdd6f4, BG: 0x1e1e2e,
				})
				column++
				continue
			}
			if column%23 == 0 {
				rootItems = append(rootItems, drawlist.Item{
					Kind: "box", Row: row, Col: column, Width: 1, Height: 1,
					Rune: "─", FG: 0x89b4fa, BG: 0x1e1e2e,
				})
				continue
			}
			character := code[(row*7+column)%len(code)]
			rootItems = append(rootItems, drawlist.Item{
				Kind: "text", Row: row, Col: column, Width: 1, Height: 1,
				Text: string(character), FG: int64(0xcba6f7 + tokenIndex(row, column)%7), BG: 0x1e1e2e,
				Attrs: benchmarkAttrs(row, tokenIndex(row, column)),
			})
		}
	}
	rootItems = append(rootItems, drawlist.Item{
		Kind: "cursor", Row: 5, Col: 5, Width: 1, Height: 1,
		Text: "r", FG: 0x1e1e2e, BG: 0xf5e0dc,
	})

	floatItems := benchmarkFloatItems(28, 6)

	return &drawlist.List{
		Version: 1,
		Seq:     1,
		Width:   width,
		Height:  height,
		Layers: []drawlist.Layer{
			{
				Grid: 1, Width: width, Height: height,
				Clip:  drawlist.Rect{X: 0, Y: 0, W: width, H: height},
				Items: rootItems,
			},
			{
				Grid: 2, ScreenRow: 45, ScreenCol: width - 28, Width: 28, Height: 6,
				Clip:  drawlist.Rect{X: width - 28, Y: 45, W: 28, H: 6},
				Items: floatItems,
			},
		},
	}
}

func tokenIndex(row, column int) int {
	span := 4 + (row*7)%21
	return column/span + row/3
}

func benchmarkAttrs(row, token int) []string {
	switch token % 5 {
	case 1:
		return []string{"bold"}
	case 2:
		return []string{"italic"}
	case 3:
		return []string{"underline"}
	case 4:
		return []string{"underdashed"}
	default:
		return nil
	}
}

func benchmarkDrawFrame(b *testing.B, width, height int) renderlist.Frame {
	b.Helper()
	planner := NewPlanner(mustBenchResolver(b))
	list := benchmarkPlanList(width, height)
	list.Seq = 2
	list.Layers = []drawlist.Layer{
		list.Layers[0],
		{
			Grid: 2, ScreenRow: height - 6, ScreenCol: width - 28, Width: 28, Height: 6,
			Clip:  drawlist.Rect{X: width - 28, Y: height - 6, W: 28, H: 6},
			Items: benchmarkFloatItems(28, 6),
		},
	}
	frame, err := planner.Plan(list)
	if err != nil {
		b.Fatalf("Plan benchmark draw frame: %v", err)
	}
	return frame
}

func benchmarkFloatItems(width, height int) []drawlist.Item {
	items := []drawlist.Item{{Kind: "fill", Row: 0, Col: 0, Width: width, Height: height, BG: 0x313244}}
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			if row == 0 || row == height-1 || column == 0 || column == width-1 {
				runeValue := "─"
				if column == 0 || column == width-1 {
					runeValue = "│"
				}
				items = append(items, drawlist.Item{
					Kind: "box", Row: row, Col: column, Width: 1, Height: 1,
					Rune: runeValue, FG: 0xcba6f7, BG: 0x313244,
				})
				continue
			}
			items = append(items, drawlist.Item{
				Kind: "text", Row: row, Col: column, Width: 1, Height: 1,
				Text: string(rune('a' + (row+column)%26)), FG: 0xcdd6f4, BG: 0x313244,
				Attrs: benchmarkAttrs(row, column/8),
			})
		}
	}
	return items
}
