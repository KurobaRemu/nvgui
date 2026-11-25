package session

import (
	"math"
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

func sessionWinViewport(grid, win int, top, bot, curLine, curCol, lineCount int64, delta float64) protocol.Event {
	return protocol.Unknown{
		Name: "win_viewport",
		Args: []any{
			int64(grid),
			protocol.Handle{Kind: 2, ID: int64(win)},
			top, bot, curLine, curCol, lineCount, delta,
		},
	}
}

func TestLatestSnapshotSinceAggregatesMultiFlushScrollDelta(t *testing.T) {
	reducer := screen.NewReducer()
	s1, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 80, Height: 24},
		protocol.GridResize{Grid: 2, Width: 80, Height: 24},
		protocol.WinPos{Grid: 2, Win: 1000, StartRow: 0, StartCol: 0, Width: 80, Height: 24},
		sessionWinViewport(2, 1000, 0, 24, 0, 0, 300, 0),
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	sess := &Session{reducer: reducer}
	seq1 := s1[0].Seq

	// Flush 2 (+3) + Flush 3 (float update, main grid delta cleared) -> LatestSnapshotSince(seq1) keeps +3.
	if _, err := reducer.Apply([]protocol.Event{
		sessionWinViewport(2, 1000, 3, 27, 3, 0, 300, 3.0),
		protocol.Flush{},
		protocol.GridResize{Grid: 3, Width: 80, Height: 1},
		protocol.WinFloatPos{Grid: 3, Win: 1008, Anchor: "NW", AnchorGrid: 2, ScreenRow: 0, ScreenCol: 0, ZIndex: 20, CompIndex: 2},
		protocol.Flush{},
	}); err != nil {
		t.Fatalf("frames 2..3: %v", err)
	}
	state, meta := sess.LatestSnapshotSince(seq1)
	if state == nil || meta == nil || state.Seq != meta.Seq {
		t.Fatalf("LatestSnapshotSince(%d) = (%+v, %+v)", seq1, state, meta)
	}
	if vp := meta.Viewports[2]; !vp.HasScrollDelta || math.Abs(vp.ScrollDelta-3.0) > 1e-9 {
		t.Fatalf("float-interleaved ScrollDelta = %+v, want 3.0", vp)
	}

	// Single-frame sinceSeq == state.Seq-1 or sinceSeq == 0 matches LatestSnapshot semantics.
	singleState, singleMeta := sess.LatestSnapshotSince(state.Seq - 1)
	if singleState != state || singleMeta == nil || singleMeta.Viewports[2].HasScrollDelta {
		t.Fatalf("single-frame LatestSnapshotSince = (%p, %+v), want cleared delta on latest frame", singleState, singleMeta)
	}
	zeroState, zeroMeta := sess.LatestSnapshotSince(0)
	if zeroState != state || zeroMeta == nil || zeroMeta.Viewports[2].HasScrollDelta {
		t.Fatalf("sinceSeq=0 LatestSnapshotSince = (%p, %+v), want LatestSnapshot semantics", zeroState, zeroMeta)
	}

	// Flush 4 (+3) + Flush 5 (+2) -> aggregates to +5.
	seq3 := state.Seq
	if _, err := reducer.Apply([]protocol.Event{
		sessionWinViewport(2, 1000, 6, 30, 6, 0, 300, 3.0),
		protocol.Flush{},
		sessionWinViewport(2, 1000, 8, 32, 8, 0, 300, 2.0),
		protocol.Flush{},
	}); err != nil {
		t.Fatalf("frames 4..5: %v", err)
	}
	state5, meta5 := sess.LatestSnapshotSince(seq3)
	if vp := meta5.Viewports[2]; !vp.HasScrollDelta || math.Abs(vp.ScrollDelta-5.0) > 1e-9 {
		t.Fatalf("summed ScrollDelta = %+v, want 5.0", vp)
	}

	// Flush 6 (+3) + Flush 7 (grid_resize) + Flush 8 (+1) -> reset takes effect, returns +1.
	seq5 := state5.Seq
	if _, err := reducer.Apply([]protocol.Event{
		sessionWinViewport(2, 1000, 11, 35, 11, 0, 300, 3.0),
		protocol.Flush{},
		protocol.GridResize{Grid: 2, Width: 70, Height: 20},
		protocol.Flush{},
		sessionWinViewport(2, 1000, 12, 32, 12, 0, 300, 1.0),
		protocol.Flush{},
	}); err != nil {
		t.Fatalf("frames 6..8: %v", err)
	}
	_, meta8 := sess.LatestSnapshotSince(seq5)
	if vp := meta8.Viewports[2]; !vp.HasScrollDelta || math.Abs(vp.ScrollDelta-1.0) > 1e-9 {
		t.Fatalf("post-resize ScrollDelta = %+v, want 1.0", vp)
	}

	// Non-aggregating snapshotSource safely falls back to LatestSnapshotFrom.
	stub := &stubSnapshotSource{
		state: &screen.State{Seq: 5},
		metaForSeq: map[uint64]*screen.FrameMeta{
			5: {Seq: 5, Viewports: map[int]screen.Viewport{1: {Grid: 1, TopLine: 2}}},
		},
	}
	gotState, gotMeta := sessionLatestSnapshotSinceFrom(stub, 1)
	if gotState != stub.state || gotMeta == nil || gotMeta.Seq != 5 {
		t.Fatalf("fallback LatestSnapshotSinceFrom = (%p, %+v)", gotState, gotMeta)
	}
}
