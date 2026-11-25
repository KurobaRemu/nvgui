package text

import (
	"math"
	"testing"
)

func TestScaleFactorOrthogonalToDPIScaleAndClamped(t *testing.T) {
	cases := []struct {
		in       float32
		wantSize float32
	}{
		{in: 0, wantSize: 14.0},
		{in: -1, wantSize: 14.0},
		{in: float32(math.NaN()), wantSize: 14.0},
		{in: float32(math.Inf(1)), wantSize: 14.0},
		{in: 0.1, wantSize: 7.0},
		{in: 0.5, wantSize: 7.0},
		{in: 1.5, wantSize: 21.0},
		{in: 4.0, wantSize: 56.0},
		{in: 99, wantSize: 56.0},
	}
	for _, tc := range cases {
		r, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1, ScaleFactor: tc.in})
		if err != nil {
			t.Fatalf("NewResolver(ScaleFactor=%v): %v", tc.in, err)
		}
		if got := r.FontFor("hello", false, false).Size; math.Abs(float64(got-tc.wantSize)) > 1e-4 {
			t.Fatalf("FontFor.Size for ScaleFactor=%v = %v, want %v", tc.in, got, tc.wantSize)
		}
	}

	base, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1, ScaleFactor: 1.0})
	if err != nil {
		t.Fatalf("NewResolver(base): %v", err)
	}
	zoomed, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 1, ScaleFactor: 1.5})
	if err != nil {
		t.Fatalf("NewResolver(zoomed): %v", err)
	}
	zoomedHiDPI, err := NewResolver(Config{GUIFont: "Menlo:h14", Scale: 2, ScaleFactor: 1.5})
	if err != nil {
		t.Fatalf("NewResolver(zoomedHiDPI): %v", err)
	}

	if got := zoomed.FontFor("hello", false, false).Size; math.Abs(float64(got-21)) > 1e-4 {
		t.Fatalf("zoomed FontFor.Size = %v, want 21", got)
	}
	if got := zoomedHiDPI.FontFor("hello", false, false).Size; math.Abs(float64(got-21)) > 1e-4 {
		t.Fatalf("zoomedHiDPI FontFor.Size = %v, want 21 (independent of Scale=2)", got)
	}

	baseCell := base.Cell()
	zoomedCell := zoomed.Cell()
	hiDPICell := zoomedHiDPI.Cell()
	if zoomedCell.Width <= baseCell.Width*1.3 || zoomedCell.Height <= baseCell.Height*1.3 {
		t.Fatalf("1.5x ScaleFactor did not scale cell: base=%+v zoomed=%+v", baseCell, zoomedCell)
	}
	if math.Abs(float64(hiDPICell.Height-zoomedCell.Height)) > 1e-4 {
		t.Fatalf("DPI Scale=2 changed Cell.Height: 1x=%v 2x=%v", zoomedCell.Height, hiDPICell.Height)
	}
	if math.Abs(float64(hiDPICell.Width-zoomedCell.Width)) > 1.0 {
		t.Fatalf("DPI Scale=2 changed Cell.Width beyond pixel snap: 1x=%v 2x=%v", zoomedCell.Width, hiDPICell.Width)
	}

	// Changing ScaleFactor on impl invalidates shape cache.
	font := zoomed.FontFor("hello", false, false)
	_ = zoomed.Shape("hello", font)
	if zoomed.ShapeCalls() != 1 {
		t.Fatalf("ShapeCalls = %d, want 1", zoomed.ShapeCalls())
	}
	zoomed.impl.mu.Lock()
	zoomed.impl.cfg.ScaleFactor = 2.0
	zoomed.impl.mu.Unlock()
	if got := zoomed.ShapeCalls(); got != 0 {
		t.Fatalf("ShapeCalls after ScaleFactor change = %d, want 0", got)
	}
}

func TestResolverCloneForScaleEquivalenceAndCacheIsolation(t *testing.T) {
	baseCfg := Config{
		GUIFont:     "Menlo:h14",
		Scale:       1,
		ScaleFactor: 1.0,
		LineSpace:   2,
		LineHeight:  1.25,
	}
	base, err := NewResolver(baseCfg)
	if err != nil {
		t.Fatalf("NewResolver(base): %v", err)
	}
	// Warm base cache to prove clone starts with an isolated cache.
	baseFont := base.FontFor("hello", false, false)
	_ = base.Shape("hello", baseFont)
	if base.ShapeCalls() != 1 {
		t.Fatalf("base ShapeCalls = %d, want 1", base.ShapeCalls())
	}

	samples := []struct {
		text   string
		bold   bool
		italic bool
	}{
		{"hello world", false, false},
		{"bold italic", true, true},
		{"世界你好", false, false},
		{"🌍", false, false},
		{"👨‍👩‍👧‍👦", false, false},
	}

	for _, tc := range []struct {
		scale       float32
		scaleFactor float32
	}{
		{1, 1.5},
		{2, 1.5},
		{2, 0.75},
		{0, 2.0},
		{1.5, 0},
		{2, 99},
		{1, 0.1},
	} {
		cloned, err := base.CloneForScale(tc.scale, tc.scaleFactor)
		if err != nil {
			t.Fatalf("CloneForScale(%v, %v): %v", tc.scale, tc.scaleFactor, err)
		}
		freshCfg := baseCfg
		freshCfg.Scale = tc.scale
		freshCfg.ScaleFactor = tc.scaleFactor
		fresh, err := NewResolver(freshCfg)
		if err != nil {
			t.Fatalf("NewResolver(%+v): %v", freshCfg, err)
		}
		if cloned.Config() != fresh.Config() {
			t.Fatalf("CloneForScale(%v, %v).Config() = %+v, want %+v", tc.scale, tc.scaleFactor, cloned.Config(), fresh.Config())
		}
		if cloned.Cell() != fresh.Cell() {
			t.Fatalf("CloneForScale(%v, %v).Cell() = %+v, want %+v", tc.scale, tc.scaleFactor, cloned.Cell(), fresh.Cell())
		}
		for _, sample := range samples {
			gotFont := cloned.FontFor(sample.text, sample.bold, sample.italic)
			wantFont := fresh.FontFor(sample.text, sample.bold, sample.italic)
			if gotFont != wantFont {
				t.Fatalf("FontFor(%q, %v, %v) mismatch: got %+v, want %+v", sample.text, sample.bold, sample.italic, gotFont, wantFont)
			}
			gotGlyphs := cloned.Shape(sample.text, gotFont)
			wantGlyphs := fresh.Shape(sample.text, wantFont)
			if len(gotGlyphs) != len(wantGlyphs) {
				t.Fatalf("Shape(%q) len = %d, want %d", sample.text, len(gotGlyphs), len(wantGlyphs))
			}
			for i := range gotGlyphs {
				if gotGlyphs[i] != wantGlyphs[i] {
					t.Fatalf("Shape(%q)[%d] = %+v, want %+v", sample.text, i, gotGlyphs[i], wantGlyphs[i])
				}
			}
		}
	}

	// Verify cache isolation between base and cloned resolver.
	isolated, err := base.CloneForScale(1, 1.0)
	if err != nil {
		t.Fatalf("CloneForScale(1, 1.0): %v", err)
	}
	if isolated.ShapeCalls() != 0 {
		t.Fatalf("fresh clone ShapeCalls = %d, want 0", isolated.ShapeCalls())
	}
	isoFont := isolated.FontFor("hello", false, false)
	_ = isolated.Shape("hello", isoFont)
	_ = isolated.Shape("another", isoFont)
	if isolated.ShapeCalls() != 2 {
		t.Fatalf("isolated ShapeCalls = %d, want 2", isolated.ShapeCalls())
	}
	if base.ShapeCalls() != 1 {
		t.Fatalf("base ShapeCalls mutated by clone: got %d, want 1", base.ShapeCalls())
	}
	isolated.ClearCache()
	if isolated.ShapeCalls() != 0 || base.ShapeCalls() != 1 {
		t.Fatalf("ClearCache leaked across resolvers: isolated=%d base=%d", isolated.ShapeCalls(), base.ShapeCalls())
	}

	// Verify invalid scale and nil receiver return explicit errors.
	for _, bad := range []float32{-1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if _, err := base.CloneForScale(bad, 1.0); err == nil {
			t.Fatalf("CloneForScale(%v, 1.0) = nil error, want error", bad)
		}
	}
	var nilResolver *Resolver
	if _, err := nilResolver.CloneForScale(1, 1); err == nil {
		t.Fatal("nil Resolver.CloneForScale returned nil error, want error")
	}
}
