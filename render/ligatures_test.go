package render

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
)

func TestLigaturePlannerToggleInvalidatesFontSnapshot(t *testing.T) {
	resolver := mustTestResolver(t)
	planner := NewPlanner(resolver)
	if !planner.LigaturesEnabled() {
		t.Fatal("NewPlanner ligatures disabled by default")
	}
	original := resolver.FontFor("A", false, false)
	fonts, _, enabled := planner.refreshCache()
	if !enabled || fonts[0].Features != original.Features {
		t.Fatalf("default cache = (%q, %v), want resolver features %q and enabled", fonts[0].Features, enabled, original.Features)
	}
	planner.mu.Lock()
	if !planner.ready {
		t.Fatal("refreshCache did not mark Planner ready")
	}
	planner.mu.Unlock()

	planner.SetLigaturesEnabled(false)
	disabledFonts, _, enabled := planner.refreshCache()
	if enabled || disabledFonts[0].Features != "calt=0,liga=0,clig=0,dlig=0" {
		t.Fatalf("disabled cache = (%q, %v), want explicit disable features", disabledFonts[0].Features, enabled)
	}
	planner.SetLigaturesEnabled(true)
	restoredFonts, _, enabled := planner.refreshCache()
	if !enabled || restoredFonts[0].Features != original.Features {
		t.Fatalf("restored cache = (%q, %v), want resolver features %q and enabled", restoredFonts[0].Features, enabled, original.Features)
	}
}

func TestLigaturePlannerAppliesFeaturesWithoutMutatingOtherFonts(t *testing.T) {
	planner := NewPlanner(mustTestResolver(t))
	planner.SetLigaturesEnabled(false)
	const probe = "世"
	list := &drawlist.List{
		Version: 1,
		Seq:     1,
		Layers: []drawlist.Layer{{Grid: 1, Items: []drawlist.Item{
			{Kind: "text", Row: 0, Col: 0, Width: 2, Height: 1, Text: probe, FG: 0xffffff},
		}}},
	}
	frame, err := planner.Plan(list)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var got renderlist.Command
	for _, command := range frame.Commands {
		if command.Op == "glyphs" && command.Text == probe {
			got = command
		}
	}
	if got.Features != "calt=0,liga=0,clig=0,dlig=0" {
		t.Fatalf("command.Features = %q, want disabled ligature tags", got.Features)
	}
	planner.mu.Lock()
	cached := planner.otherFonts[otherFontQuery{text: probe}]
	planner.mu.Unlock()
	if want := mustTestResolver(t).FontFor(probe, false, false); cached != want {
		t.Fatalf("otherFonts cache = %+v, want resolver original %+v", cached, want)
	}
}

func TestLigatureClusterAnchoringIsCachedAndPreservesOffsets(t *testing.T) {
	glyphs := []ui.Glyph{
		{Cluster: 0, X: 0.1, Advance: 9},
		{Cluster: 0, X: 0.4, Advance: 9},
		{Cluster: 1, X: 9.2, Advance: 9},
		{Cluster: 2, X: 18.3, Advance: 9},
		{Cluster: 2, X: 18.8, Advance: 9},
	}
	want := append([]ui.Glyph(nil), glyphs...)
	const cellWidth float32 = 9.25
	for index := range want {
		clusterX := float32(want[index].Cluster) * cellWidth
		groupStart := index
		for groupStart > 0 && glyphs[groupStart-1].Cluster == glyphs[index].Cluster {
			groupStart--
		}
		want[index].X = clusterX + (glyphs[index].X - glyphs[groupStart].X)
	}
	for _, testCase := range []struct {
		text string
		meta rowMeta
	}{
		{text: "abc", meta: rowMeta{grid: 1, cellWidth: cellWidth}},
		{text: "abc", meta: rowMeta{grid: 1, cellWidth: cellWidth + 1}},
	} {
		if rowCacheKey(renderlist.Command{Text: testCase.text}, testCase.meta).cellWidth != testCase.meta.cellWidth {
			t.Fatalf("rowCacheKey(%q) lost cellWidth %v", testCase.text, testCase.meta.cellWidth)
		}
	}
	anchorGlyphClustersToCells(glyphs, cellWidth)
	if !reflect.DeepEqual(glyphs, want) {
		t.Fatalf("anchored glyphs = %+v, want %+v", glyphs, want)
	}

	cache := newRowCache()
	command := renderlist.Command{
		Op: "glyphs", X: 0, Y: 0, W: 3 * cellWidth, H: 18,
		Text: "abc", Family: "Menlo", Size: 14, Weight: 400, FG: 0xffffff,
	}
	meta := rowMeta{grid: 1, cellWidth: cellWidth}
	first, ascent := cache.glyphRun(command, meta)
	if len(first) == 0 || ascent <= 0 {
		t.Fatalf("glyphRun = (%d glyphs, ascent %v), want shaped run", len(first), ascent)
	}
	second, _ := cache.glyphRun(command, meta)
	if !reflect.DeepEqual(first, second) || cache.shapedRows() != 1 {
		t.Fatalf("rowCache did not reuse anchored glyphs: shapedRows=%d", cache.shapedRows())
	}
	for index, glyph := range first {
		if glyph.Cluster >= 0 && glyph.Cluster < 3 {
			wantX := float32(glyph.Cluster) * cellWidth
			if glyph.X < wantX-1 || glyph.X > wantX+1 {
				t.Errorf("glyph %d X = %v, want anchored near %v", index, glyph.X, wantX)
			}
		}
	}
	if rowCacheKey(command, meta).cellWidth != cellWidth {
		t.Fatal("rowCacheKey lost cellWidth")
	}
}

func TestLigatureSingleAndWideGlyphsKeepCenterOffset(t *testing.T) {
	single := renderlist.Command{Text: "X", Family: "Menlo", Size: 14, Weight: 400}
	singleGlyphs := ui.Shape(single.Text, commandFont(single))
	if len(singleGlyphs) == 0 {
		t.Fatal("Shape returned no single glyph")
	}
	before := append([]ui.Glyph(nil), singleGlyphs...)
	if got := glyphCenterOffset(singleGlyphs, 9); got == 0 || !reflect.DeepEqual(singleGlyphs, before) {
		t.Fatalf("single glyph center offset = %v for %+v, want unchanged nonzero centering", got, singleGlyphs)
	}
	wide := []ui.Glyph{{Cluster: 0, X: 4, Advance: 8}}
	if got := glyphCenterOffset(wide, 18); got <= 0 {
		t.Fatalf("glyphCenterOffset(wide) = %v, want positive centering", got)
	}
}
