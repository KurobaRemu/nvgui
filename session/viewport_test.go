package session

import (
	"testing"

	"nvgui/protocol"
	"nvgui/screen"
)

type stubSnapshotSource struct {
	state       *screen.State
	latestMeta  *screen.FrameMeta
	metaForSeq  map[uint64]*screen.FrameMeta
	latestCalls int
	metaCalls   int
}

func (s *stubSnapshotSource) Latest() *screen.State {
	s.latestCalls++
	return s.state
}

func (s *stubSnapshotSource) LatestMeta() *screen.FrameMeta {
	return s.latestMeta
}

func (s *stubSnapshotSource) MetaForSeq(seq uint64) *screen.FrameMeta {
	s.metaCalls++
	return s.metaForSeq[seq]
}

func TestLatestSnapshotUsesAlignedHistoryMeta(t *testing.T) {
	reducer := screen.NewReducer()
	states, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 4, Height: 2},
		protocol.Unknown{Name: "win_viewport", Args: []any{
			int64(1), int64(1000), int64(0), int64(2), int64(0), int64(0), int64(10), 2.5,
		}},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	session := &Session{reducer: reducer}
	state, meta := session.LatestSnapshot()
	if state != states[0] || meta == nil {
		t.Fatalf("LatestSnapshot = (%p, %+v), want committed state and meta", state, meta)
	}
	if meta.Seq != state.Seq || !meta.Viewports[1].HasScrollDelta || meta.Viewports[1].ScrollDelta != 2.5 {
		t.Fatalf("LatestSnapshot meta = %+v, want aligned scroll delta 2.5", meta)
	}
}

func TestLatestSnapshotFromNewReducerWithoutFlushReturnsEmptyMeta(t *testing.T) {
	reducer := screen.NewReducer()
	states, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 4, Height: 2},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	restored := screen.NewReducerFrom(states[0])
	session := &Session{reducer: restored}
	state, meta := session.LatestSnapshot()
	if state == nil || state.Seq != states[0].Seq || meta == nil {
		t.Fatalf("LatestSnapshot = (%+v, %+v), want state and empty meta", state, meta)
	}
	if meta.Seq != state.Seq || len(meta.Viewports) != 0 || len(meta.Margins) != 0 || meta.Busy || meta.GUIFont != "" {
		t.Fatalf("empty fallback meta = %+v, want Seq=%d and empty maps", meta, state.Seq)
	}
}

func TestLatestSnapshotFromRetriesUntilHistoryHit(t *testing.T) {
	state := &screen.State{Seq: 3}
	meta := &screen.FrameMeta{Seq: 3, GUIFont: "Menlo:h14"}
	source := &stubSnapshotSource{
		state:      state,
		latestMeta: &screen.FrameMeta{Seq: 70},
		metaForSeq: map[uint64]*screen.FrameMeta{},
	}
	gotState, gotMeta := sessionLatestSnapshotFrom(&retrySnapshotSource{delegate: source, hitOnCall: 2, meta: meta})
	if gotState != state || gotMeta != meta {
		t.Fatalf("sessionLatestSnapshotFrom retry = (%p, %+v), want (%p, %+v)", gotState, gotMeta, state, meta)
	}
	if source.latestCalls != 2 || source.metaCalls != 2 {
		t.Fatalf("retry calls = (%d, %d), want (2, 2)", source.latestCalls, source.metaCalls)
	}
}

func TestLatestSnapshotFromPersistentMissUsesFutureMetaSafely(t *testing.T) {
	state := &screen.State{Seq: 1}
	source := &stubSnapshotSource{
		state: state,
		latestMeta: &screen.FrameMeta{
			Seq: 70,
			Viewports: map[int]screen.Viewport{
				1: {TopLine: 10, ScrollDelta: 7.5, HasScrollDelta: true},
			},
		},
		metaForSeq: map[uint64]*screen.FrameMeta{},
	}
	gotState, meta := sessionLatestSnapshotFrom(source)
	if gotState != state || meta == nil {
		t.Fatalf("sessionLatestSnapshotFrom = (%p, %+v), want state and fallback meta", gotState, meta)
	}
	if meta.Seq != 1 {
		t.Fatalf("fallback meta Seq = %d, want 1", meta.Seq)
	}
	viewport := meta.Viewports[1]
	if viewport.TopLine != 10 || viewport.ScrollDelta != 0 || viewport.HasScrollDelta {
		t.Fatalf("fallback viewport = %+v, want TopLine=10 ScrollDelta=0 HasScrollDelta=false", viewport)
	}
	if source.latestMeta.Viewports[1].ScrollDelta != 7.5 || !source.latestMeta.Viewports[1].HasScrollDelta {
		t.Fatal("fallback mutated source LatestMeta")
	}
}

type retrySnapshotSource struct {
	delegate  *stubSnapshotSource
	hitOnCall int
	meta      *screen.FrameMeta
}

func (s *retrySnapshotSource) Latest() *screen.State { return s.delegate.Latest() }
func (s *retrySnapshotSource) LatestMeta() *screen.FrameMeta {
	return s.delegate.LatestMeta()
}
func (s *retrySnapshotSource) MetaForSeq(seq uint64) *screen.FrameMeta {
	meta := s.delegate.MetaForSeq(seq)
	if s.delegate.metaCalls == s.hitOnCall {
		return s.meta
	}
	return meta
}
