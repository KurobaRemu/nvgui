package render

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type animatorImpl struct {
	mu           sync.Mutex
	planner      *Planner
	cfg          AnimationConfig
	clock        Clock
	lastList     *drawlist.List
	lastState    *screen.State
	lastMeta     *screen.FrameMeta
	lastTime     time.Time
	active       bool
	cursorSpring Spring
	scroll       map[int]*gridScrollState
	cursor       *cursorAnimation
	settledFrame renderlist.Frame
	settledMetas []rowMeta
	initialized  bool
	latestFrame  renderlist.Frame
	latestMeta   frameMetadata
}

type previousScrollFrame struct {
	frame  renderlist.Frame
	metas  []rowMeta
	layers map[int]drawlist.Layer
}

func hasNonZeroScrollDelta(meta *screen.FrameMeta) bool {
	if meta == nil {
		return false
	}
	for _, viewport := range meta.Viewports {
		if viewport.HasScrollDelta && viewport.ScrollDelta != 0 &&
			!math.IsNaN(viewport.ScrollDelta) && !math.IsInf(viewport.ScrollDelta, 0) {
			return true
		}
	}
	return false
}

func newAnimator(planner *Planner, cfg AnimationConfig, clock Clock) *Animator {
	if clock == nil {
		clock = systemClock{}
	}
	if planner != nil {
		planner.SetLigaturesEnabled(cfg.LigaturesEnabled)
	}
	return &Animator{impl: &animatorImpl{
		planner:  planner,
		cfg:      cfg,
		clock:    clock,
		cursor:   newCursorAnimation(),
		scroll:   make(map[int]*gridScrollState),
		lastTime: clock.Now(),
	}}
}

func animatorConfig(a *Animator) AnimationConfig {
	if a == nil || a.impl == nil {
		return AnimationConfig{}
	}
	impl := a.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return impl.cfg
}

func animatorSetConfig(a *Animator, cfg AnimationConfig) {
	if a == nil || a.impl == nil {
		return
	}
	impl := a.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	beforeLigatures := impl.planner != nil && impl.planner.LigaturesEnabled()
	baselineExpired := len(impl.settledFrame.Commands) == 0
	impl.cfg = cfg
	if impl.planner != nil {
		impl.planner.SetLigaturesEnabled(cfg.LigaturesEnabled)
	}
	ligaturesChanged := beforeLigatures != (impl.planner != nil && impl.planner.LigaturesEnabled())
	cursorDisabled := !cfg.CursorEnabled || cfg.CursorAnimationLength <= 0
	if cursorDisabled {
		impl.cursorSpring.Reset()
		if impl.cursor != nil {
			if impl.cursor.hasTarget && !impl.cursor.suppressed {
				impl.cursor.snap()
			}
			impl.cursor.active = false
		}
	}
	if !cfg.ScrollEnabled || cfg.ScrollAnimationLength <= 0 {
		for _, state := range impl.scroll {
			state.spring.Reset()
		}
		impl.scroll = make(map[int]*gridScrollState)
	}
	if !impl.scrollActive() && (cursorDisabled || impl.cursor == nil || !impl.cursor.active) {
		impl.active = false
	}
	animationsDisabled := !impl.active && !impl.scrollActive() && (impl.cursor == nil || !impl.cursor.active)
	if impl.initialized && impl.planner != nil && impl.lastList != nil {
		settledFrame, err := impl.planner.Plan(impl.lastList)
		if err == nil {
			refreshBaseline := ligaturesChanged || baselineExpired || animationsDisabled
			if refreshBaseline {
				impl.settledFrame = cloneFrame(settledFrame)
				impl.settledMetas = metadataFor(impl.settledFrame).rows
			}
			storeMetadata(impl.settledFrame, frameMetadata{rows: impl.settledMetas})
			settled, settledMetas := impl.composedFrame(impl.settledFrame, impl.settledMetas)
			frame, frameMetas := cursorAnimationFrame(impl.cursor, settled, settledMetas)
			storeMetadata(frame, frameMetas)
			if refreshBaseline {
				impl.latestFrame = cloneFrame(frame)
				impl.latestMeta = cloneFrameMetadata(frameMetas)
			}
		}
	}
	impl.lastTime = impl.clock.Now()
}

func cloneDrawList(source *drawlist.List) *drawlist.List {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Layers = make([]drawlist.Layer, len(source.Layers))
	for layerIndex, layer := range source.Layers {
		layerClone := layer
		layerClone.Items = make([]drawlist.Item, len(layer.Items))
		for itemIndex, item := range layer.Items {
			itemClone := item
			if item.Attrs != nil {
				itemClone.Attrs = append([]string(nil), item.Attrs...)
			}
			layerClone.Items[itemIndex] = itemClone
		}
		clone.Layers[layerIndex] = layerClone
	}
	return &clone
}

func cloneFrameMeta(meta *screen.FrameMeta) *screen.FrameMeta {
	if meta == nil {
		return nil
	}
	clone := *meta
	clone.Viewports = make(map[int]screen.Viewport, len(meta.Viewports))
	for gridID, viewport := range meta.Viewports {
		clone.Viewports[gridID] = viewport
	}
	clone.Margins = make(map[int]screen.ViewportMargins, len(meta.Margins))
	for gridID, margins := range meta.Margins {
		clone.Margins[gridID] = margins
	}
	return &clone
}

func (impl *animatorImpl) scrollActive() bool {
	for _, state := range impl.scroll {
		if state.spring.Active() {
			return true
		}
	}
	return false
}

func (impl *animatorImpl) scrollDelta(cfg AnimationConfig, state *screen.State, meta *screen.FrameMeta, list *drawlist.List, previous *previousScrollFrame) {
	if !cfg.ScrollEnabled || cfg.ScrollAnimationLength <= 0 || meta == nil {
		return
	}
	cell := impl.planner.resolver.Cell()
	layers := make(map[int]drawlist.Layer, len(list.Layers))
	for _, layer := range list.Layers {
		layers[layer.Grid] = layer
	}
	for gridID, previous := range impl.scroll {
		layer, exists := layers[gridID]
		margins := scrollMarginsFor(state, meta, layer)
		if !exists || previous.width != layer.Width || previous.height != layer.Height ||
			previous.margins != margins || cell.Width <= 0 || cell.Height <= 0 {
			previous.spring.Reset()
			delete(impl.scroll, gridID)
			continue
		}
		previous.margins = margins
		previous.viewport = meta.Viewports[gridID]
	}
	for gridID, viewport := range meta.Viewports {
		layer, exists := layers[gridID]
		if !exists || !viewport.HasScrollDelta || viewport.ScrollDelta == 0 {
			continue
		}
		delta := float32(viewport.ScrollDelta)
		if math.IsNaN(float64(delta)) || math.IsInf(float64(delta), 0) || delta == 0 {
			continue
		}
		margins := scrollMarginsFor(state, meta, layer)
		innerHeight := layer.Height - margins.Top - margins.Bottom
		if innerHeight <= 0 || cell.Height <= 0 {
			continue
		}
		gridState := impl.scroll[gridID]
		if gridState == nil {
			gridState = &gridScrollState{}
			impl.scroll[gridID] = gridState
		}
		gridState.width, gridState.height = layer.Width, layer.Height
		gridState.margins = margins
		gridState.viewport = viewport
		far := max(1, cfg.ScrollFarLines)
		maxClamp := float32(max(innerHeight, far))
		injected := delta
		if math.Abs(float64(delta)) > float64(innerHeight) {
			farLimit := min(float32(innerHeight), float32(far))
			injected = float32(math.Copysign(float64(farLimit), float64(delta)))
		}
		for index := range gridState.snapshots {
			gridState.snapshots[index].lineOffset -= injected
		}
		if previous != nil {
			prevLayer, ok := previous.layers[gridID]
			if ok && prevLayer.Width == layer.Width && prevLayer.Height == layer.Height &&
				prevLayer.ScreenRow == layer.ScreenRow && prevLayer.ScreenCol == layer.ScreenCol {
				prevCommands, prevMetas := scrollPreviousInner(previous.frame, previous.metas, prevLayer, gridID, margins, cell)
				if len(prevCommands) > 0 {
					gridState.snapshots = append(gridState.snapshots, scrollSnapshot{
						commands:   prevCommands,
						metas:      prevMetas,
						lineOffset: -injected,
					})
				}
			}
		}
		previousPosition := gridState.spring.Position
		wasActive := gridState.spring.Active()
		position := float64(previousPosition + injected)
		gridState.spring.Position = float32(math.Copysign(math.Min(math.Abs(position), float64(maxClamp)), position))
		if !wasActive || previousPosition == 0 || (previousPosition > 0) != (injected > 0) {
			gridState.spring.Velocity = 0
		}
		trimScrollSnapshots(gridState, gridState.spring.Position, innerHeight)
	}
}

func (impl *animatorImpl) updateScroll(dt float32) {
	for _, state := range impl.scroll {
		if state.spring.Update(dt, impl.cfg.ScrollAnimationLength) {
			continue
		}
		state.spring.Reset()
		state.snapshots = nil
	}
}

func (impl *animatorImpl) composedFrame(frame renderlist.Frame, metas []rowMeta) (renderlist.Frame, []rowMeta) {
	if impl.lastList == nil || impl.lastState == nil || len(impl.scroll) == 0 || !impl.cfg.ScrollEnabled || impl.cfg.ScrollAnimationLength <= 0 {
		return frame, metas
	}
	cell := impl.planner.resolver.Cell()
	return scrollAnimationFrame(frame, metas, impl.lastState, impl.lastMeta, impl.lastList, impl.scroll, cell.Width, cell.Height)
}

func animatorSubmit(a *Animator, state *screen.State, meta *screen.FrameMeta, list *drawlist.List) (renderlist.Frame, bool, error) {
	if a == nil || a.impl == nil || a.impl.planner == nil {
		return renderlist.Frame{}, false, fmt.Errorf("render: nil Animator or Planner")
	}
	if list == nil {
		return renderlist.Frame{}, false, fmt.Errorf("render: nil Draw List")
	}
	impl := a.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	now := impl.clock.Now()
	cachedList := cloneDrawList(list)
	var previous *previousScrollFrame
	if impl.initialized && impl.lastList != nil && len(impl.settledFrame.Commands) > 0 &&
		impl.cfg.ScrollEnabled && impl.cfg.ScrollAnimationLength > 0 && hasNonZeroScrollDelta(meta) {
		previous = &previousScrollFrame{
			frame:  cloneFrame(impl.settledFrame),
			metas:  append([]rowMeta(nil), impl.settledMetas...),
			layers: make(map[int]drawlist.Layer, len(impl.lastList.Layers)),
		}
		for _, layer := range impl.lastList.Layers {
			previous.layers[layer.Grid] = layer
		}
	}
	if !impl.active && !impl.scrollActive() {
		impl.lastTime = now
	}
	impl.initialized = true
	settledFrame, err := impl.planner.Plan(cachedList)
	if err != nil {
		return renderlist.Frame{}, false, err
	}
	impl.lastList = cachedList
	impl.lastState = state
	impl.lastMeta = cloneFrameMeta(meta)
	impl.settledFrame = cloneFrame(settledFrame)
	impl.settledMetas = metadataFor(impl.settledFrame).rows
	impl.scrollDelta(impl.cfg, state, impl.lastMeta, cachedList, previous)
	cell := impl.planner.resolver.Cell()
	busy := meta != nil && meta.Busy
	index := cursorCommandIndex(impl.settledFrame)
	cursorGrid := int32(0)
	if state != nil {
		cursorGrid = int32(state.Cursor.Grid)
	}
	if index >= 0 && index < len(impl.settledMetas) {
		cursorGrid = impl.settledMetas[index].grid
	}
	scrolling := false
	if meta != nil {
		if viewport, ok := meta.Viewports[int(cursorGrid)]; ok && viewport.HasScrollDelta && viewport.ScrollDelta != 0 {
			scrolling = true
		}
	}
	if gridState := impl.scroll[int(cursorGrid)]; gridState != nil && gridState.spring.Active() {
		scrolling = true
	}
	input := cursorInput{state: state, meta: meta, list: cachedList, frame: impl.settledFrame, metas: impl.settledMetas, cfg: impl.cfg, cell: cell, now: now, scrolling: scrolling}
	if busy || index < 0 {
		impl.cursor.active = false
		impl.cursor.suppressed = true
		impl.cursor.visible = false
		impl.active = false
		impl.cursor.lastMoveTime = time.Time{}
		impl.cursor.lastMoveStep = false
		if busy {
			impl.cursor.prevBusy = true
			impl.cursor.hasPrev = true
		}
	} else {
		previousTarget := impl.cursor.targetState()
		wasSuppressed := impl.cursor.suppressed || !impl.cursor.visible
		impl.cursor.suppressed = false
		impl.cursor.setShapeFromInput(input, index)
		if !impl.cursor.setTarget(impl.settledFrame, index) {
			impl.cursor.active = false
			impl.cursor.suppressed = true
			impl.cursor.visible = false
			impl.active = false
		} else {
			immediate := impl.cursor.immediate(input, index, wasSuppressed)
			jumped := impl.cursor.targetChanged(previousTarget)
			switch {
			case immediate:
				impl.cursor.snap()
			case jumped:
				impl.cursor.jump(impl.cfg)
				impl.cursor.active = true
			}
			impl.cursor.remember(input, index, false)
			impl.active = impl.cursor.active
		}
	}
	settled, settledMetas := impl.composedFrame(impl.settledFrame, impl.settledMetas)
	frame, frameMetas := cursorAnimationFrame(impl.cursor, settled, settledMetas)
	storeMetadata(frame, frameMetas)
	impl.latestFrame = cloneFrame(frame)
	impl.latestMeta = cloneFrameMetadata(frameMetas)
	return cloneFrame(frame), impl.active || impl.scrollActive(), nil
}

func stoppedFrame(impl *animatorImpl) (renderlist.Frame, bool, error) {
	impl.lastTime = impl.clock.Now()
	impl.active = false
	if impl.cursor != nil {
		impl.cursor.active = false
		impl.cursor.snap()
	}
	impl.cursorSpring.Reset()
	for _, state := range impl.scroll {
		state.spring.Reset()
		state.snapshots = nil
	}
	frame, frameMetas := cursorAnimationFrame(impl.cursor, impl.settledFrame, impl.settledMetas)
	storeMetadata(impl.settledFrame, frameMetadata{rows: impl.settledMetas})
	storeMetadata(frame, frameMetas)
	impl.latestFrame = cloneFrame(frame)
	impl.latestMeta = cloneFrameMetadata(frameMetas)
	return cloneFrame(frame), false, nil
}

func animatorStep(a *Animator) (renderlist.Frame, bool, error) {
	if a == nil || a.impl == nil || a.impl.planner == nil {
		return renderlist.Frame{}, false, fmt.Errorf("render: nil Animator or Planner")
	}
	impl := a.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	now := impl.clock.Now()
	dt := now.Sub(impl.lastTime)
	if !impl.initialized {
		return renderlist.Frame{}, false, nil
	}
	if !impl.active && !impl.cursor.active && !impl.scrollActive() {
		return stoppedFrame(impl)
	}
	if dt <= 0 {
		frame := impl.latestFrame
		frameMeta := impl.latestMeta
		if len(frame.Commands) == 0 {
			settled, settledMetas := impl.composedFrame(impl.settledFrame, impl.settledMetas)
			frame, frameMeta = cursorAnimationFrame(impl.cursor, settled, settledMetas)
		}
		storeMetadata(frame, frameMeta)
		return cloneFrame(frame), impl.active || impl.cursor.active || impl.scrollActive(), nil
	}
	impl.lastTime = now
	impl.cursor.update(float32(dt.Seconds()))
	impl.updateScroll(float32(dt.Seconds()))
	if !impl.active && !impl.cursor.active && !impl.scrollActive() {
		return stoppedFrame(impl)
	}
	impl.active = impl.cursor.active
	settled, settledMetas := impl.composedFrame(impl.settledFrame, impl.settledMetas)
	frame, frameMetas := cursorAnimationFrame(impl.cursor, settled, settledMetas)
	storeMetadata(frame, frameMetas)
	impl.latestFrame = cloneFrame(frame)
	impl.latestMeta = cloneFrameMetadata(frameMetas)
	return cloneFrame(frame), impl.active || impl.scrollActive(), nil
}

func animatorActive(a *Animator) bool {
	if a == nil || a.impl == nil {
		return false
	}
	impl := a.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return impl.active || impl.cursor.active || impl.scrollActive()
}

func animatorCursorQuad(a *Animator) ([4]ui.Point, bool) {
	if a != nil && a.impl != nil {
		impl := a.impl
		impl.mu.Lock()
		defer impl.mu.Unlock()
		if impl.cursor != nil {
			return impl.cursor.quad()
		}
	}
	return [4]ui.Point{}, false
}

func animatorScrollOffset(a *Animator, gridID int) (float32, float32, bool) {
	if a == nil || a.impl == nil {
		return 0, 0, false
	}
	impl := a.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	state := impl.scroll[gridID]
	if state == nil || !state.spring.Active() {
		return 0, 0, false
	}
	cell := impl.planner.resolver.Cell()
	return state.spring.Position, state.spring.Position * cell.Height, true
}
