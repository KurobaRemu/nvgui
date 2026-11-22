package screen

import (
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
