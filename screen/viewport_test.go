package screen

import (
	"bytes"
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
