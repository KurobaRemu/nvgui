package render

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type animatorImpl struct {
	planner      *Planner
	cfg          AnimationConfig
	clock        Clock
	lastList     *drawlist.List
	lastTime     time.Time
	active       bool
	cursorSpring Spring
	scrollSpring Spring
	cursor       *cursorAnimation
	settledFrame renderlist.Frame
	settledMetas []rowMeta
	initialized  bool
	latestFrame  renderlist.Frame
	latestMetas  []rowMeta
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
		lastTime: clock.Now(),
	}}
}

func animatorConfig(a *Animator) AnimationConfig {
	if a == nil || a.impl == nil {
		return AnimationConfig{}
	}
	return a.impl.cfg
}

func animatorSetConfig(a *Animator, cfg AnimationConfig) {
	if a == nil || a.impl == nil {
		return
	}
	impl := a.impl
	impl.cfg = cfg
	if impl.planner != nil {
		impl.planner.SetLigaturesEnabled(cfg.LigaturesEnabled)
	}
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
	if !cfg.ScrollEnabled {
		impl.scrollSpring.Reset()
	}
	if !impl.scrollSpring.Active() && (cursorDisabled || impl.cursor == nil || !impl.cursor.active) {
		impl.active = false
	}
	if impl.initialized && impl.planner != nil && impl.lastList != nil {
		settledFrame, err := impl.planner.Plan(impl.lastList)
		if err == nil {
			impl.settledFrame = cloneFrame(settledFrame)
			impl.settledMetas = metadataFor(impl.settledFrame)
			storeMetadata(impl.settledFrame, impl.settledMetas)
			frame, frameMetas := cursorAnimationFrame(impl.cursor, impl.settledFrame, impl.settledMetas)
			storeMetadata(frame, frameMetas)
			impl.latestFrame = cloneFrame(frame)
			impl.latestMetas = append([]rowMeta(nil), frameMetas...)
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

func animatorSubmit(a *Animator, state *screen.State, meta *screen.FrameMeta, list *drawlist.List) (renderlist.Frame, bool, error) {
	if a == nil || a.impl == nil || a.impl.planner == nil {
		return renderlist.Frame{}, false, fmt.Errorf("render: nil Animator or Planner")
	}
	if list == nil {
		return renderlist.Frame{}, false, fmt.Errorf("render: nil Draw List")
	}
	impl := a.impl
	now := impl.clock.Now()
	cachedList := cloneDrawList(list)
	settledFrame, err := impl.planner.Plan(cachedList)
	if err != nil {
		return renderlist.Frame{}, false, err
	}
	impl.lastList = cachedList
	impl.settledFrame = cloneFrame(settledFrame)
	impl.settledMetas = metadataFor(impl.settledFrame)
	impl.initialized = true
	if !impl.active {
		impl.lastTime = now
	}
	cell := impl.planner.resolver.Cell()
	input := cursorInput{state: state, meta: meta, list: cachedList, frame: impl.settledFrame, metas: impl.settledMetas, cfg: impl.cfg, cell: cell}
	busy := meta != nil && meta.Busy
	index := cursorCommandIndex(impl.settledFrame)
	if busy || index < 0 {
		impl.cursor.active = false
		impl.cursor.suppressed = true
		impl.cursor.visible = false
		impl.active = false
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
	frame, frameMetas := cursorAnimationFrame(impl.cursor, impl.settledFrame, impl.settledMetas)
	storeMetadata(frame, frameMetas)
	impl.latestFrame = cloneFrame(frame)
	impl.latestMetas = append([]rowMeta(nil), frameMetas...)
	return cloneFrame(frame), impl.active, nil
}

func animatorStep(a *Animator) (renderlist.Frame, bool, error) {
	if a == nil || a.impl == nil || a.impl.planner == nil {
		return renderlist.Frame{}, false, fmt.Errorf("render: nil Animator or Planner")
	}
	impl := a.impl
	now := impl.clock.Now()
	dt := now.Sub(impl.lastTime)
	if !impl.initialized {
		return renderlist.Frame{}, false, nil
	}
	if !impl.active && !impl.cursor.active {
		impl.lastTime = now
		frame, frameMetas := cursorAnimationFrame(impl.cursor, impl.settledFrame, impl.settledMetas)
		storeMetadata(frame, frameMetas)
		impl.latestFrame = cloneFrame(frame)
		impl.latestMetas = append([]rowMeta(nil), frameMetas...)
		impl.active = false
		return cloneFrame(frame), false, nil
	}
	if dt <= 0 {
		frame := impl.latestFrame
		frameMetas := impl.latestMetas
		if len(frame.Commands) == 0 {
			frame, frameMetas = cursorAnimationFrame(impl.cursor, impl.settledFrame, impl.settledMetas)
		}
		storeMetadata(frame, frameMetas)
		return cloneFrame(frame), true, nil
	}
	impl.lastTime = now
	impl.cursor.update(float32(dt.Seconds()))
	impl.active = impl.cursor.active
	frame, frameMetas := cursorAnimationFrame(impl.cursor, impl.settledFrame, impl.settledMetas)
	storeMetadata(frame, frameMetas)
	impl.latestFrame = cloneFrame(frame)
	impl.latestMetas = append([]rowMeta(nil), frameMetas...)
	return cloneFrame(frame), impl.active, nil
}

func animatorActive(a *Animator) bool {
	return a != nil && a.impl != nil && a.impl.active
}

func animatorCursorQuad(a *Animator) ([4]ui.Point, bool) {
	if a != nil && a.impl != nil && a.impl.cursor != nil {
		return a.impl.cursor.quad()
	}
	return [4]ui.Point{}, false
}

func animatorScrollOffset(a *Animator, gridID int) (float32, float32, bool) {
	return 0, 0, false
}
