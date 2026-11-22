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
	settledFrame renderlist.Frame
	settledMetas []rowMeta
	initialized  bool
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
	if !cfg.CursorEnabled {
		impl.cursorSpring.Reset()
	}
	if !cfg.ScrollEnabled {
		impl.scrollSpring.Reset()
	}
	if !cfg.CursorEnabled && !cfg.ScrollEnabled {
		impl.active = false
	}
	if impl.initialized && impl.planner != nil && impl.lastList != nil {
		settledFrame, err := impl.planner.Plan(impl.lastList)
		if err == nil {
			impl.settledFrame = cloneFrame(settledFrame)
			impl.settledMetas = metadataFor(impl.settledFrame)
			storeMetadata(impl.settledFrame, impl.settledMetas)
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
	_ = state
	_ = meta
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
	if !impl.cfg.CursorEnabled && !impl.cfg.ScrollEnabled {
		impl.active = false
		storeMetadata(impl.settledFrame, impl.settledMetas)
		return cloneFrame(impl.settledFrame), false, nil
	}
	impl.active = false
	storeMetadata(impl.settledFrame, impl.settledMetas)
	return cloneFrame(impl.settledFrame), false, nil
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
	if !impl.active {
		impl.lastTime = now
		storeMetadata(impl.settledFrame, impl.settledMetas)
		return cloneFrame(impl.settledFrame), false, nil
	}
	if dt <= 0 {
		storeMetadata(impl.settledFrame, impl.settledMetas)
		return cloneFrame(impl.settledFrame), true, nil
	}
	impl.lastTime = now
	impl.cursorSpring.Reset()
	impl.scrollSpring.Reset()
	impl.active = false
	storeMetadata(impl.settledFrame, impl.settledMetas)
	return cloneFrame(impl.settledFrame), false, nil
}

func animatorActive(a *Animator) bool {
	return a != nil && a.impl != nil && a.impl.active
}

func animatorCursorQuad(a *Animator) ([4]ui.Point, bool) {
	return [4]ui.Point{}, false
}

func animatorScrollOffset(a *Animator, gridID int) (float32, float32, bool) {
	return 0, 0, false
}
