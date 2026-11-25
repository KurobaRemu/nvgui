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
		Viewports:    make(map[int]Viewport),
		Margins:      make(map[int]ViewportMargins),
		MouseEnabled: true,
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
	if len(source.scrollResets) > 0 {
		clone.scrollResets = make(map[int]bool, len(source.scrollResets))
		for gridID, reset := range source.scrollResets {
			clone.scrollResets[gridID] = reset
		}
	} else {
		clone.scrollResets = nil
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

func reducerAggregateMetaSince(r *Reducer, sinceSeq, targetSeq uint64) *FrameMeta {
	if r == nil || targetSeq == 0 {
		return nil
	}
	history := r.history()
	history.mu.Lock()
	defer history.mu.Unlock()

	target, exists := history.entries[targetSeq]
	if !exists || target == nil {
		return nil
	}
	if sinceSeq == 0 || targetSeq <= sinceSeq+1 || targetSeq-sinceSeq > frameMetaHistoryCapacity {
		return cloneFrameMeta(target)
	}
	for seq := sinceSeq + 1; seq <= targetSeq; seq++ {
		if entry := history.entries[seq]; entry == nil {
			return cloneFrameMeta(target)
		}
	}
	result := cloneFrameMeta(target)
	for gridID, targetVP := range result.Viewports {
		var sum float64
		var has bool
		for seq := sinceSeq + 1; seq <= targetSeq; seq++ {
			entry := history.entries[seq]
			if entry.scrollResets[gridID] {
				sum = 0
				has = false
			}
			vp, ok := entry.Viewports[gridID]
			if !ok {
				sum = 0
				has = false
				continue
			}
			if vp.HasScrollDelta {
				sum += vp.ScrollDelta
				has = true
			}
		}
		targetVP.ScrollDelta = sum
		targetVP.HasScrollDelta = has
		result.Viewports[gridID] = targetVP
	}
	return result
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
	case "mouse_on", "mouse_off":
		if enabled, ok := protocol.ParseMouseEvent(unknown); ok {
			meta.MouseEnabled = enabled
		}
	case "set_title":
		if parsed, ok, err := protocol.ParseSetTitle(unknown); err == nil && ok {
			meta.Title = parsed.Title
			meta.HasTitle = true
		}
	case "option_set":
		name, value, ok := protocol.ParseOptionSet(unknown)
		if !ok {
			return
		}
		switch name {
		case "guifont":
			if guifont, ok := value.(string); ok {
				meta.GUIFont = guifont
			}
		case "linespace":
			if lineSpace, ok := parseOptionInt(value); ok {
				meta.LineSpace = lineSpace
			}
		case "mousemoveevent":
			if enabled, ok := value.(bool); ok {
				meta.MouseMoveEvent = enabled
			}
		}
	}
}

func parseOptionInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case int32:
		return int(typed), true
	case int16:
		return int(typed), true
	case int8:
		return int(typed), true
	case uint64:
		return int(typed), true
	case uint32:
		return int(typed), true
	case uint16:
		return int(typed), true
	case uint8:
		return int(typed), true
	case uint:
		return int(typed), true
	default:
		return 0, false
	}
}

func (r *Reducer) clearFrameMetaScrollDelta(gridID int) {
	r.metaMu.Lock()
	defer r.metaMu.Unlock()
	meta := r.workingFrameMetaLocked()
	if meta.scrollResets == nil {
		meta.scrollResets = make(map[int]bool)
	}
	meta.scrollResets[gridID] = true
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
	if meta.scrollResets == nil {
		meta.scrollResets = make(map[int]bool)
	}
	meta.scrollResets[gridID] = true
	delete(meta.Viewports, gridID)
	delete(meta.Margins, gridID)
}
