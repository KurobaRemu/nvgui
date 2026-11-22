package screen

import (
	"sync"

	"nvgui/protocol"
)

const frameMetaHistoryCapacity = 64

type metaHistory struct {
	mu      sync.Mutex
	entries map[uint64]*FrameMeta
	slots   [frameMetaHistoryCapacity]uint64
	cursor  int
}

func (r *Reducer) workingFrameMetaLocked() *FrameMeta {
	if r.workingMeta == nil {
		r.workingMeta = newFrameMeta()
	}
	if r.workingMeta.Viewports == nil {
		r.workingMeta.Viewports = make(map[int]Viewport)
	}
	if r.workingMeta.Margins == nil {
		r.workingMeta.Margins = make(map[int]ViewportMargins)
	}
	return r.workingMeta
}

func (r *Reducer) history() *metaHistory {
	r.metaMu.Lock()
	defer r.metaMu.Unlock()
	return r.historyLocked()
}

func (r *Reducer) historyLocked() *metaHistory {
	if r.metasBySeq == nil {
		r.metasBySeq = &metaHistory{
			entries: make(map[uint64]*FrameMeta, frameMetaHistoryCapacity),
		}
	}
	if r.metasBySeq.entries == nil {
		r.metasBySeq.entries = make(map[uint64]*FrameMeta, frameMetaHistoryCapacity)
	}
	return r.metasBySeq
}

func newFrameMeta() *FrameMeta {
	return &FrameMeta{
		Viewports: make(map[int]Viewport),
		Margins:   make(map[int]ViewportMargins),
	}
}

func cloneFrameMeta(source *FrameMeta) *FrameMeta {
	if source == nil {
		return newFrameMeta()
	}
	clone := *source
	clone.Viewports = make(map[int]Viewport, len(source.Viewports))
	for gridID, viewport := range source.Viewports {
		clone.Viewports[gridID] = viewport
	}
	clone.Margins = make(map[int]ViewportMargins, len(source.Margins))
	for gridID, margins := range source.Margins {
		clone.Margins[gridID] = margins
	}
	return &clone
}

func (r *Reducer) publishMetaLocked(meta *FrameMeta) {
	published := cloneFrameMeta(meta)
	r.latestMeta.Store(published)
	history := r.historyLocked()
	history.mu.Lock()
	defer history.mu.Unlock()
	if _, exists := history.entries[published.Seq]; !exists {
		if len(history.entries) >= frameMetaHistoryCapacity {
			evicted := history.slots[history.cursor]
			delete(history.entries, evicted)
		}
		history.slots[history.cursor] = published.Seq
		history.cursor = (history.cursor + 1) % frameMetaHistoryCapacity
	}
	history.entries[published.Seq] = published
}

func reducerLatestMeta(r *Reducer) *FrameMeta {
	if r == nil {
		return nil
	}
	latest := r.latestMeta.Load()
	if latest == nil {
		return nil
	}
	return cloneFrameMeta(latest)
}

func reducerMetaForSeq(r *Reducer, seq uint64) *FrameMeta {
	if r == nil {
		return nil
	}
	history := r.history()
	history.mu.Lock()
	entry, exists := history.entries[seq]
	history.mu.Unlock()
	if !exists || entry == nil {
		return nil
	}
	return cloneFrameMeta(entry)
}

func (r *Reducer) applyFrameMetaEvent(state *State, event protocol.Event) {
	unknown, ok := event.(protocol.Unknown)
	if !ok {
		return
	}
	r.metaMu.Lock()
	defer r.metaMu.Unlock()
	meta := r.workingFrameMetaLocked()
	switch unknown.Name {
	case "win_viewport":
		parsed, ok, err := protocol.ParseWinViewport(unknown)
		if err != nil || !ok {
			return
		}
		if _, exists := state.Grids[parsed.Grid]; !exists {
			return
		}
		previous := meta.Viewports[parsed.Grid]
		viewport := Viewport{
			Grid:           parsed.Grid,
			Win:            int64(parsed.Win),
			TopLine:        parsed.TopLine,
			BotLine:        parsed.BotLine,
			CurLine:        parsed.CurLine,
			CurCol:         parsed.CurCol,
			LineCount:      parsed.LineCount,
			ScrollDelta:    previous.ScrollDelta + parsed.ScrollDelta,
			HasLineCount:   parsed.HasLineCount,
			HasScrollDelta: previous.HasScrollDelta || parsed.HasScrollDelta,
		}
		meta.Viewports[parsed.Grid] = viewport
	case "win_viewport_margins":
		margins, ok, err := protocol.ParseWinViewportMargins(unknown)
		if err != nil || !ok {
			return
		}
		if _, exists := state.Grids[margins.Grid]; !exists {
			return
		}
		meta.Margins[margins.Grid] = ViewportMargins{
			Top:    max(margins.Top, 0),
			Bottom: max(margins.Bottom, 0),
			Left:   max(margins.Left, 0),
			Right:  max(margins.Right, 0),
		}
	case "busy_start", "busy_stop":
		if busy, ok := protocol.ParseBusyEvent(unknown); ok {
			meta.Busy = busy
		}
	case "option_set":
		name, value, ok := protocol.ParseOptionSet(unknown)
		if !ok || name != "guifont" {
			return
		}
		guifont, ok := value.(string)
		if !ok {
			return
		}
		meta.GUIFont = guifont
	}
}

func (r *Reducer) clearFrameMetaScrollDelta(gridID int) {
	r.metaMu.Lock()
	defer r.metaMu.Unlock()
	meta := r.workingFrameMetaLocked()
	viewport, exists := meta.Viewports[gridID]
	if !exists {
		return
	}
	viewport.ScrollDelta = 0
	viewport.HasScrollDelta = false
	meta.Viewports[gridID] = viewport
}

func (r *Reducer) clearFrameMetaGrid(gridID int) {
	r.metaMu.Lock()
	defer r.metaMu.Unlock()
	meta := r.workingFrameMetaLocked()
	delete(meta.Viewports, gridID)
	delete(meta.Margins, gridID)
}
