package screen

import (
	"bytes"
	"math"
	"testing"

	"nvgui/protocol"
)

func TestNewReducerFromLazilyInitializesFrameMeta(t *testing.T) {
	original := NewReducer()
	states, err := original.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 4, Height: 2},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	state := states[0]
	reducer := NewReducerFrom(state)
	if reducer.workingMeta != nil || reducer.metasBySeq != nil || reducer.latestMeta.Load() != nil {
		t.Fatal("NewReducerFrom eagerly initialized FrameMeta storage")
	}
	states, err = reducer.Apply([]protocol.Event{
		protocol.Unknown{Name: "busy_start"},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply after restore: %v", err)
	}
	meta := reducer.LatestMeta()
	if meta == nil || meta.Seq != states[0].Seq || !meta.Busy {
		t.Fatalf("LatestMeta after restore = %+v, want Seq=%d Busy=true", meta, states[0].Seq)
	}
	if reducer.MetaForSeq(states[0].Seq) == nil {
		t.Fatal("MetaForSeq after restore returned nil")
	}
}

func TestOptionSetLineSpaceUpdatesFrameMeta(t *testing.T) {
	reducer := NewReducer()
	states, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 4, Height: 2},
		protocol.Unknown{Name: "option_set", Args: []any{"linespace", int64(6)}},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	meta := reducer.MetaForSeq(states[0].Seq)
	if meta == nil || meta.LineSpace != 6 {
		t.Fatalf("MetaForSeq.LineSpace = %+v, want 6", meta)
	}
}

func TestMouseFrameMetaLifecycleRollbackAndCanonicalInvariant(t *testing.T) {
	plain := NewReducer()
	withMouse := NewReducer()

	baseEvents := []protocol.Event{
		protocol.GridResize{Grid: 1, Width: 20, Height: 6},
		protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "H", Repeat: 1}, {Text: "i", Repeat: 1}}},
		protocol.GridCursorGoto{Grid: 1, Row: 0, Col: 1},
	}

	plainStates, err := plain.Apply(append(append([]protocol.Event(nil), baseEvents...), protocol.Flush{}))
	if err != nil {
		t.Fatalf("plain.Apply: %v", err)
	}
	mouseStates1, err := withMouse.Apply(append(append([]protocol.Event(nil), baseEvents...), protocol.Flush{}))
	if err != nil {
		t.Fatalf("withMouse.Apply frame 1: %v", err)
	}
	meta1 := withMouse.LatestMeta()
	if meta1 == nil || !meta1.MouseEnabled || meta1.MouseMoveEvent {
		t.Fatalf("default FrameMeta = %+v, want MouseEnabled=true MouseMoveEvent=false", meta1)
	}

	// Apply mouse_off + option_set("mousemoveevent", true) and verify Canonical JSON v1 stays unchanged.
	mouseStates2, err := withMouse.Apply([]protocol.Event{
		protocol.Unknown{Name: "mouse_off"},
		protocol.Unknown{Name: "option_set", Args: []any{"mousemoveevent", true}},
		protocol.Flush{},
	})
	if err != nil || len(mouseStates2) != 1 {
		t.Fatalf("withMouse.Apply frame 2: states=%d err=%v", len(mouseStates2), err)
	}
	meta2 := withMouse.LatestMeta()
	if meta2 == nil || meta2.MouseEnabled || !meta2.MouseMoveEvent {
		t.Fatalf("frame 2 FrameMeta = %+v, want MouseEnabled=false MouseMoveEvent=true", meta2)
	}
	hist1 := withMouse.MetaForSeq(mouseStates1[0].Seq)
	if hist1 == nil || !hist1.MouseEnabled || hist1.MouseMoveEvent {
		t.Fatalf("historical frame 1 mutated: %+v", hist1)
	}

	plainJSON, err := Canonical(plainStates[0])
	if err != nil {
		t.Fatalf("Canonical(plain): %v", err)
	}
	mouseJSON, err := Canonical(mouseStates2[0])
	if err != nil {
		t.Fatalf("Canonical(withMouse): %v", err)
	}
	if !bytes.Equal(plainJSON, mouseJSON) {
		t.Fatalf("mouse FrameMeta events altered Committed Screen Canonical JSON:\n got %s\nwant %s", mouseJSON, plainJSON)
	}

	// Failed batch must roll back mouse_on / option_set("mousemoveevent", false) mutations.
	_, err = withMouse.Apply([]protocol.Event{
		protocol.Unknown{Name: "mouse_on"},
		protocol.Unknown{Name: "option_set", Args: []any{"mousemoveevent", false}},
		protocol.GridLine{Grid: 999, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "X", Repeat: 1}}},
		protocol.Flush{},
	})
	if err == nil {
		t.Fatal("expected error for GridLine on unknown grid 999")
	}
	if _, err := withMouse.Apply([]protocol.Event{protocol.Flush{}}); err != nil {
		t.Fatalf("clean Flush after rollback: %v", err)
	}
	afterRollback := withMouse.LatestMeta()
	if afterRollback == nil || afterRollback.MouseEnabled || !afterRollback.MouseMoveEvent {
		t.Fatalf("rollback leaked mouse metadata: %+v, want MouseEnabled=false MouseMoveEvent=true", afterRollback)
	}

	// Valid mouse_on + mousemoveevent=false restores state.
	if _, err := withMouse.Apply([]protocol.Event{
		protocol.Unknown{Name: "mouse_on"},
		protocol.Unknown{Name: "option_set", Args: []any{"mousemoveevent", false}},
		protocol.Flush{},
	}); err != nil {
		t.Fatalf("withMouse.Apply restore: %v", err)
	}
	restored := withMouse.LatestMeta()
	if restored == nil || !restored.MouseEnabled || restored.MouseMoveEvent {
		t.Fatalf("restored FrameMeta = %+v, want MouseEnabled=true MouseMoveEvent=false", restored)
	}
}

func TestSetTitleFrameMetaLifecycleRollbackAndCanonicalInvariant(t *testing.T) {
	plain := NewReducer()
	withTitle := NewReducer()

	base := []protocol.Event{
		protocol.GridResize{Grid: 1, Width: 20, Height: 6},
		protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "A", Repeat: 1}}},
		protocol.GridCursorGoto{Grid: 1, Row: 0, Col: 0},
	}
	plainStates, err := plain.Apply(append(append([]protocol.Event(nil), base...), protocol.Flush{}))
	if err != nil {
		t.Fatalf("plain.Apply: %v", err)
	}
	titleStates1, err := withTitle.Apply(append(append([]protocol.Event(nil), base...), protocol.Flush{}))
	if err != nil {
		t.Fatalf("withTitle.Apply frame 1: %v", err)
	}
	meta1 := withTitle.LatestMeta()
	if meta1 == nil || meta1.HasTitle || meta1.Title != "" {
		t.Fatalf("initial FrameMeta = %+v, want HasTitle=false Title=\"\"", meta1)
	}

	titleStates2, err := withTitle.Apply([]protocol.Event{
		protocol.Unknown{Name: "set_title", Args: []any{"hello-nvgui"}},
		protocol.Flush{},
	})
	if err != nil || len(titleStates2) != 1 {
		t.Fatalf("withTitle.Apply frame 2: states=%d err=%v", len(titleStates2), err)
	}
	meta2 := withTitle.LatestMeta()
	if meta2 == nil || !meta2.HasTitle || meta2.Title != "hello-nvgui" {
		t.Fatalf("frame 2 FrameMeta = %+v, want HasTitle=true Title=\"hello-nvgui\"", meta2)
	}
	if hist1 := withTitle.MetaForSeq(titleStates1[0].Seq); hist1 == nil || hist1.HasTitle {
		t.Fatalf("historical frame 1 mutated: %+v", hist1)
	}

	plainJSON, err := Canonical(plainStates[0])
	if err != nil {
		t.Fatalf("Canonical(plain): %v", err)
	}
	titleJSON, err := Canonical(titleStates2[0])
	if err != nil {
		t.Fatalf("Canonical(withTitle): %v", err)
	}
	if !bytes.Equal(plainJSON, titleJSON) {
		t.Fatalf("set_title altered Committed Screen Canonical JSON:\n got %s\nwant %s", titleJSON, plainJSON)
	}

	// Failed batch must roll back set_title.
	_, err = withTitle.Apply([]protocol.Event{
		protocol.Unknown{Name: "set_title", Args: []any{"rolled-back"}},
		protocol.GridLine{Grid: 999, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "X", Repeat: 1}}},
		protocol.Flush{},
	})
	if err == nil {
		t.Fatal("expected error for unknown grid 999")
	}
	if _, err := withTitle.Apply([]protocol.Event{protocol.Flush{}}); err != nil {
		t.Fatalf("clean Flush after rollback: %v", err)
	}
	afterRollback := withTitle.LatestMeta()
	if afterRollback == nil || !afterRollback.HasTitle || afterRollback.Title != "hello-nvgui" {
		t.Fatalf("rollback leaked title metadata: %+v", afterRollback)
	}

	// Empty title preserves HasTitle=true so app can fall back to "nvgui".
	if _, err := withTitle.Apply([]protocol.Event{
		protocol.Unknown{Name: "set_title", Args: []any{""}},
		protocol.Flush{},
	}); err != nil {
		t.Fatalf("withTitle.Apply empty title: %v", err)
	}
	cleared := withTitle.LatestMeta()
	if cleared == nil || !cleared.HasTitle || cleared.Title != "" {
		t.Fatalf("cleared FrameMeta = %+v, want HasTitle=true Title=\"\"", cleared)
	}
}

func testWinViewportEvent(grid, win int, top, bot, curLine, curCol, lineCount int64, delta float64) protocol.Event {
	return protocol.Unknown{
		Name: "win_viewport",
		Args: []any{
			int64(grid),
			protocol.Handle{Kind: 2, ID: int64(win)},
			top, bot, curLine, curCol, lineCount, delta,
		},
	}
}

func TestAggregateMetaSince_MultiFlushCoalesceAndResets(t *testing.T) {
	t.Run("scroll flush followed by float update preserves main grid ScrollDelta", func(t *testing.T) {
		r := NewReducer()
		s1, err := r.Apply([]protocol.Event{
			protocol.GridResize{Grid: 1, Width: 80, Height: 24},
			protocol.GridResize{Grid: 2, Width: 80, Height: 24},
			protocol.WinPos{Grid: 2, Win: 1000, StartRow: 0, StartCol: 0, Width: 80, Height: 24},
			testWinViewportEvent(2, 1000, 0, 24, 0, 0, 200, 0),
			protocol.Flush{},
		})
		if err != nil || len(s1) != 1 {
			t.Fatalf("frame 1: states=%d err=%v", len(s1), err)
		}
		seq1 := s1[0].Seq

		// Flush N: main window scrolls +3.
		s2, err := r.Apply([]protocol.Event{
			testWinViewportEvent(2, 1000, 3, 27, 3, 0, 200, 3.0),
			protocol.Flush{},
		})
		if err != nil || len(s2) != 1 {
			t.Fatalf("frame 2: states=%d err=%v", len(s2), err)
		}
		// Flush N+1: treesitter-context floating window opens/updates; main grid emits no win_viewport.
		s3, err := r.Apply([]protocol.Event{
			protocol.GridResize{Grid: 3, Width: 80, Height: 1},
			protocol.WinFloatPos{Grid: 3, Win: 1005, Anchor: "NW", AnchorGrid: 2, ScreenRow: 0, ScreenCol: 0, ZIndex: 20, CompIndex: 2},
			protocol.GridLine{Grid: 3, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "f", Repeat: 1}}},
			protocol.Flush{},
		})
		if err != nil || len(s3) != 1 {
			t.Fatalf("frame 3: states=%d err=%v", len(s3), err)
		}
		seq3 := s3[0].Seq

		// Single-frame LatestMeta has cleared ScrollDelta=0 on grid 2.
		latest := r.LatestMeta()
		if latest.Viewports[2].HasScrollDelta || latest.Viewports[2].ScrollDelta != 0 {
			t.Fatalf("LatestMeta grid 2 = %+v, want cleared delta", latest.Viewports[2])
		}

		agg := r.AggregateMetaSince(seq1, seq3)
		if agg == nil || agg.Seq != seq3 {
			t.Fatalf("AggregateMetaSince(%d, %d) = %+v", seq1, seq3, agg)
		}
		vp := agg.Viewports[2]
		if !vp.HasScrollDelta || math.Abs(vp.ScrollDelta-3.0) > 1e-9 {
			t.Fatalf("aggregated grid 2 = %+v, want ScrollDelta=3 HasScrollDelta=true", vp)
		}
		if vp.TopLine != 3 || vp.BotLine != 27 {
			t.Fatalf("aggregated grid 2 bounds = %+v, want TopLine=3 BotLine=27", vp)
		}

		// Canonical snapshot must remain completely unaffected.
		plain := NewReducer()
		plainStates, err := plain.Apply([]protocol.Event{
			protocol.GridResize{Grid: 1, Width: 80, Height: 24},
			protocol.GridResize{Grid: 2, Width: 80, Height: 24},
			protocol.WinPos{Grid: 2, Win: 1000, StartRow: 0, StartCol: 0, Width: 80, Height: 24},
			protocol.Flush{},
			protocol.Flush{},
			protocol.GridResize{Grid: 3, Width: 80, Height: 1},
			protocol.WinFloatPos{Grid: 3, Win: 1005, Anchor: "NW", AnchorGrid: 2, ScreenRow: 0, ScreenCol: 0, ZIndex: 20, CompIndex: 2},
			protocol.GridLine{Grid: 3, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "f", Repeat: 1}}},
			protocol.Flush{},
		})
		if err != nil {
			t.Fatalf("plain.Apply: %v", err)
		}
		c1, err := Canonical(s3[0])
		if err != nil {
			t.Fatalf("Canonical(s3): %v", err)
		}
		c2, err := Canonical(plainStates[len(plainStates)-1])
		if err != nil {
			t.Fatalf("Canonical(plain): %v", err)
		}
		if !bytes.Equal(c1, c2) {
			t.Fatalf("Canonical mismatch:\n got %s\nwant %s", c1, c2)
		}
	})

	t.Run("multiple scroll flushes sum deltas across range", func(t *testing.T) {
		r := NewReducer()
		s1, err := r.Apply([]protocol.Event{
			protocol.GridResize{Grid: 1, Width: 40, Height: 12},
			testWinViewportEvent(1, 1000, 0, 12, 0, 0, 200, 0),
			protocol.Flush{},
		})
		if err != nil {
			t.Fatalf("frame 1: %v", err)
		}
		sMore, err := r.Apply([]protocol.Event{
			testWinViewportEvent(1, 1000, 3, 15, 3, 0, 200, 3.0),
			protocol.Flush{},
			testWinViewportEvent(1, 1000, 5, 17, 5, 0, 200, 2.0),
			protocol.Flush{},
		})
		if err != nil || len(sMore) != 2 {
			t.Fatalf("frames 2..3: len=%d err=%v", len(sMore), err)
		}
		agg := r.AggregateMetaSince(s1[0].Seq, sMore[1].Seq)
		if agg == nil {
			t.Fatal("AggregateMetaSince returned nil")
		}
		vp := agg.Viewports[1]
		if !vp.HasScrollDelta || math.Abs(vp.ScrollDelta-5.0) > 1e-9 {
			t.Fatalf("summed viewport = %+v, want ScrollDelta=5 HasScrollDelta=true", vp)
		}
		if vp.TopLine != 5 || vp.BotLine != 17 {
			t.Fatalf("summed viewport bounds = %+v, want TopLine=5 BotLine=17", vp)
		}
	})

	t.Run("grid_resize and grid_clear reset historical delta", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			event protocol.Event
		}{
			{"grid_resize", protocol.GridResize{Grid: 2, Width: 50, Height: 14}},
			{"grid_clear", protocol.GridClear{Grid: 2}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := NewReducer()
				s1, err := r.Apply([]protocol.Event{
					protocol.GridResize{Grid: 1, Width: 80, Height: 24},
					protocol.GridResize{Grid: 2, Width: 40, Height: 12},
					testWinViewportEvent(2, 1001, 0, 12, 0, 0, 200, 0),
					protocol.Flush{},
				})
				if err != nil {
					t.Fatalf("setup: %v", err)
				}
				sRest, err := r.Apply([]protocol.Event{
					testWinViewportEvent(2, 1001, 3, 15, 3, 0, 200, 3.0),
					protocol.Flush{},
					tc.event,
					protocol.Flush{},
					testWinViewportEvent(2, 1001, 4, 16, 4, 0, 200, 1.0),
					protocol.Flush{},
				})
				if err != nil || len(sRest) != 3 {
					t.Fatalf("apply %s: len=%d err=%v", tc.name, len(sRest), err)
				}
				agg := r.AggregateMetaSince(s1[0].Seq, sRest[2].Seq)
				vp := agg.Viewports[2]
				if !vp.HasScrollDelta || math.Abs(vp.ScrollDelta-1.0) > 1e-9 {
					t.Fatalf("%s aggregated = %+v, want ScrollDelta=1 HasScrollDelta=true", tc.name, vp)
				}
			})
		}
	})

	t.Run("same-frame resize before viewport drops prior flush delta but keeps post-resize delta", func(t *testing.T) {
		r := NewReducer()
		s1, err := r.Apply([]protocol.Event{
			protocol.GridResize{Grid: 2, Width: 40, Height: 12},
			testWinViewportEvent(2, 1001, 0, 12, 0, 0, 200, 0),
			protocol.Flush{},
		})
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		sRest, err := r.Apply([]protocol.Event{
			testWinViewportEvent(2, 1001, 3, 15, 3, 0, 200, 3.0),
			protocol.Flush{},
			protocol.GridResize{Grid: 2, Width: 50, Height: 12},
			testWinViewportEvent(2, 1001, 4, 16, 4, 0, 200, 1.0),
			protocol.Flush{},
		})
		if err != nil || len(sRest) != 2 {
			t.Fatalf("apply: len=%d err=%v", len(sRest), err)
		}
		agg := r.AggregateMetaSince(s1[0].Seq, sRest[1].Seq)
		vp := agg.Viewports[2]
		if !vp.HasScrollDelta || math.Abs(vp.ScrollDelta-1.0) > 1e-9 {
			t.Fatalf("same-frame resize+viewport = %+v, want ScrollDelta=1", vp)
		}
	})

	t.Run("win_hide, win_close and grid_destroy suppress stale delta", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			resetEvt []protocol.Event
			afterEvt []protocol.Event
		}{
			{
				name:     "win_hide",
				resetEvt: []protocol.Event{protocol.WinHide{Grid: 2}},
				afterEvt: []protocol.Event{protocol.WinPos{Grid: 2, Win: 1001, StartRow: 0, StartCol: 0, Width: 40, Height: 12}},
			},
			{
				name:     "win_close",
				resetEvt: []protocol.Event{protocol.WinClose{Grid: 2}},
				afterEvt: []protocol.Event{protocol.WinPos{Grid: 2, Win: 1001, StartRow: 0, StartCol: 0, Width: 40, Height: 12}},
			},
			{
				name:     "grid_destroy",
				resetEvt: []protocol.Event{protocol.GridDestroy{Grid: 2}},
				afterEvt: []protocol.Event{
					protocol.GridResize{Grid: 2, Width: 40, Height: 12},
					testWinViewportEvent(2, 1002, 0, 12, 0, 0, 50, 0),
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := NewReducer()
				s1, err := r.Apply([]protocol.Event{
					protocol.GridResize{Grid: 1, Width: 80, Height: 24},
					protocol.GridResize{Grid: 2, Width: 40, Height: 12},
					protocol.WinPos{Grid: 2, Win: 1001, StartRow: 0, StartCol: 0, Width: 40, Height: 12},
					testWinViewportEvent(2, 1001, 0, 12, 0, 0, 200, 0),
					protocol.Flush{},
				})
				if err != nil {
					t.Fatalf("setup: %v", err)
				}
				batch := []protocol.Event{
					testWinViewportEvent(2, 1001, 3, 15, 3, 0, 200, 3.0),
					protocol.Flush{},
				}
				batch = append(batch, tc.resetEvt...)
				batch = append(batch, protocol.Flush{})
				batch = append(batch, tc.afterEvt...)
				batch = append(batch, protocol.Flush{})
				sRest, err := r.Apply(batch)
				if err != nil || len(sRest) != 3 {
					t.Fatalf("apply %s: len=%d err=%v", tc.name, len(sRest), err)
				}
				agg := r.AggregateMetaSince(s1[0].Seq, sRest[2].Seq)
				vp := agg.Viewports[2]
				if vp.ScrollDelta != 0 {
					t.Fatalf("%s leaked stale ScrollDelta: %+v", tc.name, vp)
				}
				if tc.name != "grid_destroy" && vp.HasScrollDelta {
					t.Fatalf("%s leaked HasScrollDelta=true: %+v", tc.name, vp)
				}
			})
		}
	})

	t.Run("history overflow safely falls back to target frame meta", func(t *testing.T) {
		r := NewReducer()
		s1, err := r.Apply([]protocol.Event{
			protocol.GridResize{Grid: 1, Width: 40, Height: 12},
			testWinViewportEvent(1, 1000, 0, 12, 0, 0, 500, 0),
			protocol.Flush{},
		})
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		var lastSeq uint64
		for step := 1; step <= frameMetaHistoryCapacity+8; step++ {
			states, err := r.Apply([]protocol.Event{
				testWinViewportEvent(1, 1000, int64(step), int64(step+12), int64(step), 0, 500, 1.0),
				protocol.Flush{},
			})
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			lastSeq = states[0].Seq
		}
		agg := r.AggregateMetaSince(s1[0].Seq, lastSeq)
		if agg == nil || agg.Seq != lastSeq {
			t.Fatalf("overflow AggregateMetaSince = %+v", agg)
		}
		if math.Abs(agg.Viewports[1].ScrollDelta-1.0) > 1e-9 {
			t.Fatalf("overflow ScrollDelta = %v, want single-frame 1.0", agg.Viewports[1].ScrollDelta)
		}
	})

	t.Run("rollback does not leak scrollResets into next valid flush", func(t *testing.T) {
		r := NewReducer()
		s1, err := r.Apply([]protocol.Event{
			protocol.GridResize{Grid: 1, Width: 40, Height: 12},
			testWinViewportEvent(1, 1000, 0, 12, 0, 0, 200, 0),
			protocol.Flush{},
		})
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		if _, err := r.Apply([]protocol.Event{
			testWinViewportEvent(1, 1000, 3, 15, 3, 0, 200, 3.0),
			protocol.Flush{},
		}); err != nil {
			t.Fatalf("scroll flush: %v", err)
		}
		// Failed batch calls grid_clear(1) before failing on unknown grid 999.
		if _, err := r.Apply([]protocol.Event{
			protocol.GridClear{Grid: 1},
			protocol.GridLine{Grid: 999, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "X", Repeat: 1}}},
			protocol.Flush{},
		}); err == nil {
			t.Fatal("expected error for unknown grid 999")
		}
		sClean, err := r.Apply([]protocol.Event{
			protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "A", Repeat: 1}}},
			protocol.Flush{},
		})
		if err != nil {
			t.Fatalf("clean flush: %v", err)
		}
		agg := r.AggregateMetaSince(s1[0].Seq, sClean[0].Seq)
		if !agg.Viewports[1].HasScrollDelta || math.Abs(agg.Viewports[1].ScrollDelta-3.0) > 1e-9 {
			t.Fatalf("rollback leaked scroll reset: %+v, want ScrollDelta=3.0", agg.Viewports[1])
		}
	})
}
