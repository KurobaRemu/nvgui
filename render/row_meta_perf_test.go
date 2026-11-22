package render

import (
	imagecolor "image/color"
	"math"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
	"nvgui/text"
)

func TestRowMetaSizeAndFrameCursorQuadMetadata(t *testing.T) {
	if got := unsafe.Sizeof(rowMeta{}); got > 32 {
		t.Fatalf("unsafe.Sizeof(rowMeta{}) = %d, want <= 32", got)
	}

	resolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h14", Scale: 1})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	cell := resolver.Cell()
	list := &drawlist.List{
		Version: 1,
		Seq:     5901,
		Width:   8,
		Height:  2,
		Layers: []drawlist.Layer{{
			Grid: 7, ScreenRow: 3, Width: 8, Height: 4,
			Clip: drawlist.Rect{X: 0, Y: 3, W: 8, H: 4},
			Items: []drawlist.Item{
				{Kind: "fill", Row: 0, Col: 0, Width: 8, Height: 2, BG: 0x101820},
				{Kind: "cursor", Row: 1, Col: 2, Width: 1, Height: 1, BG: 0xffcc00},
			},
		}},
	}
	planner := NewPlanner(resolver)
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	meta := metadataFor(frame)
	if len(meta.rows) != len(frame.Commands) {
		t.Fatalf("metadata rows = %d, want %d", len(meta.rows), len(frame.Commands))
	}
	if len(meta.rows) != 2 {
		t.Fatalf("metadata rows = %d, want 2", len(meta.rows))
	}
	for index, row := range meta.rows {
		wantRow := int32(list.Layers[0].ScreenRow + list.Layers[0].Items[index].Row)
		if !row.hasRow || row.row != wantRow || row.grid != int32(list.Layers[0].Grid) {
			t.Fatalf("metadata row %d = %+v, want grid=%d row=%d hasRow=true", index, row, list.Layers[0].Grid, wantRow)
		}
	}
	if meta.hasCursorQuad || meta.cursorIndex != 0 || meta.cursorQuad != ([4]ui.Point{}) {
		t.Fatalf("static frame metadata = %+v, want no frame cursor quad", meta)
	}

	originalBG := frame.Commands[0].BG
	frame.Commands[0].BG = 0x222222
	if got := metadataFor(frame); !reflect.DeepEqual(got, frameMetadata{}) {
		t.Fatalf("mutated frame commands still matched cached metadata: %+v", got)
	}
	baseline := renderlist.Frame{Version: 1, Seq: frame.Seq, Commands: append([]renderlist.Command(nil), frame.Commands...)}
	baseline.Commands[0].BG = originalBG
	frame.Commands[0].BG = originalBG
	if got := metadataFor(baseline); !reflect.DeepEqual(got, meta) {
		t.Fatalf("restored frame metadata = %+v, want %+v", got, meta)
	}

	clock := NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	animator := NewAnimator(planner, DefaultAnimationConfig(), clock)
	startList := cursorAnimList(5902, cursorAnimItem(2, 2))
	if _, _, err := animator.Submit(cursorAnimState(5902, 2, 2, "normal"), &screen.FrameMeta{Seq: 5902}, startList); err != nil {
		t.Fatalf("Submit start: %v", err)
	}
	targetList := cursorAnimList(5903, cursorAnimItem(12, 30))
	if _, active, err := animator.Submit(cursorAnimState(5903, 12, 30, "normal"), &screen.FrameMeta{Seq: 5903}, targetList); err != nil || !active {
		t.Fatalf("Submit long jump = (active=%v, err=%v), want active", active, err)
	}
	clock.Advance(16 * time.Millisecond)
	mid, _, err := animator.Step()
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	wantQuad, ok := animator.CursorQuad()
	if !ok {
		t.Fatal("CursorQuad ok=false in intermediate frame")
	}
	midMeta := metadataFor(mid)
	if !midMeta.hasCursorQuad || midMeta.cursorIndex != cursorCommandIndex(mid) || midMeta.cursorQuad != wantQuad {
		t.Fatalf("intermediate frame metadata = %+v, want quad %+v at cursor index", midMeta, wantQuad)
	}
	if !frameCursorQuadRasterized(t, mid, wantQuad, int(math.Ceil(float64(40*cell.Width))), int(math.Ceil(float64(20*cell.Height)))) {
		t.Fatal("View.Draw did not rasterize the frame-level cursor quad")
	}

	static, err := planner.Plan(targetList)
	if err != nil {
		t.Fatalf("Plan static target: %v", err)
	}
	for step := 0; step < 120; step++ {
		clock.Advance(16 * time.Millisecond)
		stopped, active, err := animator.Step()
		if err != nil {
			t.Fatalf("Step converge: %v", err)
		}
		if active {
			continue
		}
		if !reflect.DeepEqual(stopped, static) {
			t.Fatalf("stopped frame differs from static frame")
		}
		stoppedMeta := metadataFor(stopped)
		if stoppedMeta.hasCursorQuad || stoppedMeta.cursorIndex != 0 || stoppedMeta.cursorQuad != ([4]ui.Point{}) {
			t.Fatalf("stopped frame metadata = %+v, want no frame cursor quad", stoppedMeta)
		}
		if !reflect.DeepEqual(stoppedMeta, metadataFor(static)) {
			t.Fatalf("stopped metadata = %+v, want static metadata %+v", stoppedMeta, metadataFor(static))
		}
		return
	}
	t.Fatal("cursor animation did not converge")
}

func frameCursorQuadRasterized(t *testing.T, frame renderlist.Frame, quad [4]ui.Point, width, height int) bool {
	t.Helper()
	view := NewView()
	view.SetFrame(frame)
	if width <= 0 {
		width = 800
	}
	if height <= 0 {
		height = 480
	}
	rendered := renderView(view, width, height)
	if rendered == nil {
		return false
	}
	cursorIndex := cursorCommandIndex(frame)
	if cursorIndex < 0 {
		return false
	}
	wanted := color(frame.Commands[cursorIndex].BG)
	want := imagecolor.RGBA{R: wanted.R, G: wanted.G, B: wanted.B, A: wanted.A}
	minX, minY, maxX, maxY := quadBounds(quad)
	for x := int(math.Ceil(float64(minX))); x <= int(math.Floor(float64(maxX))); x++ {
		for y := int(math.Ceil(float64(minY))); y <= int(math.Floor(float64(maxY))); y++ {
			if nearColor(pixelAt(rendered, x, y), want) {
				return true
			}
		}
	}
	return false
}
