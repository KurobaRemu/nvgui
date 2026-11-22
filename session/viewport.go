package session

import "nvgui/screen"

type snapshotSource interface {
	Latest() *screen.State
	LatestMeta() *screen.FrameMeta
	MetaForSeq(uint64) *screen.FrameMeta
}

func sessionLatestMeta(s *Session) *screen.FrameMeta {
	if s == nil || s.reducer == nil {
		return nil
	}
	return s.reducer.LatestMeta()
}

func sessionLatestSnapshot(s *Session) (*screen.State, *screen.FrameMeta) {
	if s == nil || s.reducer == nil {
		return nil, nil
	}
	return sessionLatestSnapshotFrom(s.reducer)
}

func sessionLatestSnapshotFrom(src snapshotSource) (*screen.State, *screen.FrameMeta) {
	if src == nil {
		return nil, nil
	}
	for attempt := 0; attempt < 4; attempt++ {
		state := src.Latest()
		if state == nil {
			return nil, nil
		}
		meta := src.MetaForSeq(state.Seq)
		if meta != nil && meta.Seq == state.Seq {
			return state, meta
		}
	}
	state := src.Latest()
	if state == nil {
		return nil, nil
	}
	latest := src.LatestMeta()
	if latest == nil {
		return state, &screen.FrameMeta{
			Seq:       state.Seq,
			Viewports: make(map[int]screen.Viewport),
			Margins:   make(map[int]screen.ViewportMargins),
		}
	}
	if latest.Seq == state.Seq {
		return state, latest
	}
	aligned := *latest
	aligned.Seq = state.Seq
	aligned.Viewports = make(map[int]screen.Viewport, len(latest.Viewports))
	for gridID, viewport := range latest.Viewports {
		viewport.ScrollDelta = 0
		viewport.HasScrollDelta = false
		aligned.Viewports[gridID] = viewport
	}
	aligned.Margins = make(map[int]screen.ViewportMargins, len(latest.Margins))
	for gridID, margins := range latest.Margins {
		aligned.Margins[gridID] = margins
	}
	return state, &aligned
}
