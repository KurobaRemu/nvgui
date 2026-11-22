package screen_test

import (
	"bytes"
	"math"
	"testing"

	"nvgui/protocol"
	"nvgui/screen"
)

func m4WinViewport(grid, win int, top, bot, curLine, curCol, lineCount int64, delta float64) protocol.Event {
	return protocol.Unknown{
		Name: "win_viewport",
		Args: []any{
			int64(grid),
			protocol.Handle{Kind: 2, ID: int64(win)},
			top, bot, curLine, curCol, lineCount, delta,
		},
	}
}

func m4WinViewportMargins(grid, win, top, bot, left, right int) protocol.Event {
	return protocol.Unknown{
		Name: "win_viewport_margins",
		Args: []any{
			int64(grid),
			protocol.Handle{Kind: 2, ID: int64(win)},
			int64(top), int64(bot), int64(left), int64(right),
		},
	}
}

func m4OptionSet(name string, value any) protocol.Event {
	return protocol.Unknown{Name: "option_set", Args: []any{name, value}}
}

func TestM4Viewport_MetaAccumulationAndHistory(t *testing.T) {
	r := screen.NewReducer()
	states, err := r.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 80, Height: 24},
		protocol.GridResize{Grid: 2, Width: 30, Height: 10},
		protocol.WinFloatPos{Grid: 2, Win: 1002, Anchor: "NW", AnchorGrid: 1, ScreenRow: 2, ScreenCol: 4, ZIndex: 50, CompIndex: 1},
		m4WinViewport(1, 1000, 10, 34, 15, 3, 200, 2.0),
		m4WinViewport(1, 1000, 12, 36, 17, 4, 200, 1.5),
		m4WinViewportMargins(2, 1002, 1, 1, 1, 1),
		protocol.Unknown{Name: "busy_start"},
		m4OptionSet("guifont", "JetBrains Mono:h14"),
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply frame 1: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("committed %d states, want 1", len(states))
	}
	seq1 := states[0].Seq

	meta1 := r.LatestMeta()
	if meta1 == nil {
		t.Fatal("LatestMeta() returned nil after flush")
	}
	if meta1.Seq != seq1 {
		t.Fatalf("LatestMeta().Seq = %d, want %d", meta1.Seq, seq1)
	}
	vp1, ok := meta1.Viewports[1]
	if !ok {
		t.Fatalf("LatestMeta().Viewports missing grid 1: %+v", meta1.Viewports)
	}
	if !vp1.HasScrollDelta || math.Abs(vp1.ScrollDelta-3.5) > 1e-9 {
		t.Fatalf("accumulated ScrollDelta = %v (has=%v), want 3.5", vp1.ScrollDelta, vp1.HasScrollDelta)
	}
	if vp1.TopLine != 12 || vp1.BotLine != 36 || vp1.CurLine != 17 || vp1.CurCol != 4 || !vp1.HasLineCount || vp1.LineCount != 200 {
		t.Fatalf("viewport 1 fields = %+v, want latest batch values", vp1)
	}
	if got := meta1.Margins[2]; got != (screen.ViewportMargins{Top: 1, Bottom: 1, Left: 1, Right: 1}) {
		t.Fatalf("Margins[2] = %+v, want {1,1,1,1}", got)
	}
	if !meta1.Busy {
		t.Fatal("meta1.Busy = false, want true")
	}
	if meta1.GUIFont != "JetBrains Mono:h14" {
		t.Fatalf("meta1.GUIFont = %q, want %q", meta1.GUIFont, "JetBrains Mono:h14")
	}

	// Commit frame 2: busy_stop and new guifont, verify historical MetaForSeq(seq1) stays intact.
	states2, err := r.Apply([]protocol.Event{
		protocol.Unknown{Name: "busy_stop"},
		m4OptionSet("guifont", "Menlo:h15"),
		protocol.Flush{},
	})
	if err != nil || len(states2) != 1 {
		t.Fatalf("Apply frame 2: states=%d err=%v", len(states2), err)
	}
	seq2 := states2[0].Seq
	meta2 := r.MetaForSeq(seq2)
	if meta2 == nil || meta2.Seq != seq2 {
		t.Fatalf("MetaForSeq(%d) = %+v", seq2, meta2)
	}
	if meta2.Busy {
		t.Fatal("meta2.Busy = true after busy_stop, want false")
	}
	if meta2.GUIFont != "Menlo:h15" {
		t.Fatalf("meta2.GUIFont = %q, want %q", meta2.GUIFont, "Menlo:h15")
	}

	hist1 := r.MetaForSeq(seq1)
	if hist1 == nil || hist1.Seq != seq1 {
		t.Fatalf("MetaForSeq(%d) = %+v, want seq %d", seq1, hist1, seq1)
	}
	if !hist1.Busy || hist1.GUIFont != "JetBrains Mono:h14" || math.Abs(hist1.Viewports[1].ScrollDelta-3.5) > 1e-9 {
		t.Fatalf("historical MetaForSeq(%d) mutated: %+v", seq1, hist1)
	}
}

func TestM4Viewport_ScrollDeltaClearsOnNextFlush(t *testing.T) {
	r := screen.NewReducer()
	_, err := r.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 40, Height: 12},
		m4WinViewport(1, 1000, 5, 17, 6, 0, 100, 4.0),
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply frame 1: %v", err)
	}
	m1 := r.LatestMeta()
	if m1 == nil || !m1.Viewports[1].HasScrollDelta || m1.Viewports[1].ScrollDelta != 4.0 {
		t.Fatalf("frame 1 meta = %+v, want ScrollDelta=4.0", m1)
	}

	// Next flush without win_viewport must auto-clear ScrollDelta so old scroll is not replayed.
	_, err = r.Apply([]protocol.Event{
		protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "A", Repeat: 1}}},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply frame 2: %v", err)
	}
	m2 := r.LatestMeta()
	if m2 == nil {
		t.Fatal("LatestMeta() nil on frame 2")
	}
	vp2, ok := m2.Viewports[1]
	if !ok {
		t.Fatalf("frame 2 lost viewport entry for grid 1: %+v", m2.Viewports)
	}
	if vp2.HasScrollDelta || vp2.ScrollDelta != 0 {
		t.Fatalf("frame 2 ScrollDelta not cleared: %+v, want HasScrollDelta=false ScrollDelta=0", vp2)
	}
	if vp2.TopLine != 5 || vp2.BotLine != 17 {
		t.Fatalf("frame 2 viewport bounds lost: %+v, want TopLine=5 BotLine=17", vp2)
	}
}

func TestM4Viewport_UnknownGridLifecycleAndRollback(t *testing.T) {
	r := screen.NewReducer()
	setup, err := r.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 20, Height: 8},
		protocol.GridResize{Grid: 2, Width: 10, Height: 5},
		protocol.WinFloatPos{Grid: 2, Win: 1002, Anchor: "NW", AnchorGrid: 1, ScreenRow: 1, ScreenCol: 1, ZIndex: 10, CompIndex: 1},
		m4WinViewport(1, 1000, 0, 8, 0, 0, 50, 0),
		m4WinViewportMargins(2, 1002, 1, 1, 1, 1),
		m4OptionSet("guifont", "Menlo:h14"),
		protocol.Flush{},
	})
	if err != nil || len(setup) != 1 {
		t.Fatalf("setup Apply: states=%d err=%v", len(setup), err)
	}
	baseCanon, err := screen.Canonical(setup[0])
	if err != nil {
		t.Fatalf("Canonical(setup): %v", err)
	}

	// Unknown grid win_viewport must not error, must not create grid 97, must not pollute Canonical.
	unkStates, err := r.Apply([]protocol.Event{
		m4WinViewport(97, 9999, 10, 20, 12, 1, 100, 3.0),
		protocol.Flush{},
	})
	if err != nil || len(unkStates) != 1 {
		t.Fatalf("unknown grid Apply: states=%d err=%v", len(unkStates), err)
	}
	if _, exists := unkStates[0].Grids[97]; exists {
		t.Fatal("unknown grid 97 was added to state.Grids")
	}
	unkCanon, err := screen.Canonical(unkStates[0])
	if err != nil {
		t.Fatalf("Canonical(unk): %v", err)
	}
	if !bytes.Equal(unkCanon, baseCanon) {
		t.Fatalf("unknown win_viewport polluted Canonical:\n got %s\nwant %s", unkCanon, baseCanon)
	}
	if meta := r.LatestMeta(); meta == nil {
		t.Fatal("LatestMeta() nil after unknown grid flush")
	} else if _, leaked := meta.Viewports[97]; leaked {
		t.Fatalf("unknown grid 97 leaked into FrameMeta.Viewports: %+v", meta.Viewports)
	}

	// grid_resize, grid_clear, win_hide must clear pending ScrollDelta on that grid.
	for _, tc := range []struct {
		name  string
		event protocol.Event
	}{
		{"grid_resize", protocol.GridResize{Grid: 2, Width: 12, Height: 6}},
		{"grid_clear", protocol.GridClear{Grid: 2}},
		{"win_hide", protocol.WinHide{Grid: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Apply([]protocol.Event{
				m4WinViewport(2, 1002, 2, 7, 3, 0, 50, 2.5),
				tc.event,
				protocol.Flush{},
			}); err != nil {
				t.Fatalf("Apply(%s): %v", tc.name, err)
			}
			meta := r.LatestMeta()
			if meta == nil {
				t.Fatalf("%s: LatestMeta() is nil", tc.name)
			}
			if vp, exists := meta.Viewports[2]; exists && (vp.HasScrollDelta || vp.ScrollDelta != 0) {
				t.Fatalf("%s did not clear ScrollDelta: %+v", tc.name, vp)
			}
		})
	}

	// grid_destroy removes viewport and margins for grid 2.
	if _, err := r.Apply([]protocol.Event{
		m4WinViewport(2, 1002, 2, 7, 3, 0, 50, 1.0),
		protocol.GridDestroy{Grid: 2},
		protocol.Flush{},
	}); err != nil {
		t.Fatalf("Apply(grid_destroy): %v", err)
	}
	if meta := r.LatestMeta(); meta == nil {
		t.Fatal("LatestMeta() nil after grid_destroy")
	} else {
		if _, exists := meta.Viewports[2]; exists {
			t.Fatalf("grid_destroy left grid 2 in Viewports: %+v", meta.Viewports)
		}
		if _, exists := meta.Margins[2]; exists {
			t.Fatalf("grid_destroy left grid 2 in Margins: %+v", meta.Margins)
		}
	}

	// Error rollback must also roll back workingMeta (Busy, GUIFont, Viewports, Margins).
	beforeRollback := r.LatestMeta()
	if beforeRollback == nil {
		t.Fatal("LatestMeta() nil before rollback test")
	}
	_, err = r.Apply([]protocol.Event{
		protocol.Unknown{Name: "busy_start"},
		m4OptionSet("guifont", "CORRUPTED:h99"),
		m4WinViewport(1, 1000, 99, 120, 100, 0, 500, 9.0),
		m4WinViewportMargins(1, 1000, 3, 3, 3, 3),
		protocol.GridLine{Grid: 999, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "X", Repeat: 1}}},
		protocol.Flush{},
	})
	if err == nil {
		t.Fatal("expected error from invalid GridLine on non-existent grid 999")
	}
	// Commit a clean flush after the failed batch; none of the failed metadata mutations may survive.
	if _, err := r.Apply([]protocol.Event{protocol.Flush{}}); err != nil {
		t.Fatalf("clean Flush after rollback: %v", err)
	}
	afterRollback := r.LatestMeta()
	if afterRollback == nil {
		t.Fatal("LatestMeta() nil after clean Flush")
	}
	if afterRollback.Busy != beforeRollback.Busy {
		t.Fatalf("rollback leaked Busy=%v, want %v", afterRollback.Busy, beforeRollback.Busy)
	}
	if afterRollback.GUIFont != beforeRollback.GUIFont {
		t.Fatalf("rollback leaked GUIFont=%q, want %q", afterRollback.GUIFont, beforeRollback.GUIFont)
	}
	if vp := afterRollback.Viewports[1]; vp.TopLine == 99 || vp.HasScrollDelta {
		t.Fatalf("rollback leaked Viewports[1]=%+v", vp)
	}
	if m := afterRollback.Margins[1]; m != (screen.ViewportMargins{}) {
		t.Fatalf("rollback leaked Margins[1]=%+v", m)
	}
}

func TestM4Viewport_ImmutabilityAndCanonicalInvariant(t *testing.T) {
	rPlain := screen.NewReducer()
	rMeta := screen.NewReducer()

	coreEvents := []protocol.Event{
		protocol.GridResize{Grid: 1, Width: 16, Height: 4},
		protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "H", Repeat: 1}, {Text: "i", Repeat: 1}}},
		protocol.GridCursorGoto{Grid: 1, Row: 0, Col: 1},
	}
	plainStates, err := rPlain.Apply(append(append([]protocol.Event(nil), coreEvents...), protocol.Flush{}))
	if err != nil {
		t.Fatalf("rPlain.Apply: %v", err)
	}
	metaEvents := append(append([]protocol.Event(nil), coreEvents...),
		m4WinViewport(1, 1000, 0, 4, 0, 1, 20, 1.5),
		m4WinViewportMargins(1, 1000, 1, 0, 0, 0),
		protocol.Unknown{Name: "busy_start"},
		m4OptionSet("guifont", "Menlo:h14"),
		protocol.Flush{},
	)
	metaStates, err := rMeta.Apply(metaEvents)
	if err != nil {
		t.Fatalf("rMeta.Apply: %v", err)
	}

	plainJSON, err := screen.Canonical(plainStates[0])
	if err != nil {
		t.Fatalf("Canonical(plain): %v", err)
	}
	metaJSON, err := screen.Canonical(metaStates[0])
	if err != nil {
		t.Fatalf("Canonical(meta): %v", err)
	}
	if !bytes.Equal(plainJSON, metaJSON) {
		t.Fatalf("FrameMeta events altered Canonical JSON v1:\n got %s\nwant %s", metaJSON, plainJSON)
	}

	// Mutating returned *FrameMeta must not affect subsequent reads of LatestMeta or MetaForSeq.
	got := rMeta.LatestMeta()
	if got == nil {
		t.Fatal("LatestMeta() returned nil")
	}
	got.Busy = false
	got.GUIFont = "TAMPERED"
	got.Viewports[1] = screen.Viewport{Grid: 1, TopLine: 999, ScrollDelta: 999, HasScrollDelta: true}
	got.Viewports[88] = screen.Viewport{Grid: 88}
	got.Margins[1] = screen.ViewportMargins{Top: 99}

	fresh := rMeta.LatestMeta()
	bySeq := rMeta.MetaForSeq(metaStates[0].Seq)
	for label, m := range map[string]*screen.FrameMeta{"LatestMeta": fresh, "MetaForSeq": bySeq} {
		if m == nil {
			t.Fatalf("%s returned nil", label)
		}
		if !m.Busy || m.GUIFont != "Menlo:h14" {
			t.Fatalf("%s was mutated by caller: Busy=%v GUIFont=%q", label, m.Busy, m.GUIFont)
		}
		if m.Viewports[1].TopLine != 0 || math.Abs(m.Viewports[1].ScrollDelta-1.5) > 1e-9 {
			t.Fatalf("%s Viewports[1] mutated: %+v", label, m.Viewports[1])
		}
		if _, leaked := m.Viewports[88]; leaked {
			t.Fatalf("%s Viewports map mutated (grid 88 leaked)", label)
		}
		if m.Margins[1] != (screen.ViewportMargins{Top: 1}) {
			t.Fatalf("%s Margins[1] mutated: %+v", label, m.Margins[1])
		}
	}
}
