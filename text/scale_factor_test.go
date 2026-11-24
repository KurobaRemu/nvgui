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
