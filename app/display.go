package app

import (
	"errors"
	"fmt"
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"nvgui/render"
	"nvgui/text"
)

const (
	scaleEpsilon       = 1e-4
	minUserScaleFactor = float32(0.5)
	maxUserScaleFactor = float32(4.0)
	defaultScaleFactor = float32(1.0)
	zoomStepDelta      = 0.1
)

var (
	errInvalidScale       = errors.New("app: scale must be finite and > 0")
	errInvalidScaleFactor = errors.New("app: scale factor must be finite and > 0")
)

func finitePositiveFloat32(value float32) bool {
	return value > 0 && !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func clampUserScaleFactorFloat64(factor float64) float64 {
	if math.IsNaN(factor) || math.IsInf(factor, 0) || factor <= 0 {
		return float64(defaultScaleFactor)
	}
	if factor < float64(minUserScaleFactor) {
		return float64(minUserScaleFactor)
	}
	if factor > float64(maxUserScaleFactor) {
		return float64(maxUserScaleFactor)
	}
	return factor
}

func clampUserScaleFactor(factor float32) float32 {
	return float32(clampUserScaleFactorFloat64(float64(factor)))
}

func roundScaleFactorFloat64(factor float64) float32 {
	return float32(math.Round(factor*1_000_000) / 1_000_000)
}

func roundScaleFactor(factor float32) float32 {
	return roundScaleFactorFloat64(float64(factor))
}

func normalizeUserScaleFactor(factor float32) float32 {
	return roundScaleFactorFloat64(clampUserScaleFactorFloat64(float64(factor)))
}

func (impl *shellImpl) currentAnimConfigLocked() render.AnimationConfig {
	if impl.snappingAnim {
		return impl.preSnapAnimConfig
	}
	return impl.animator.Config()
}

func scaleChanged(current, next float32) bool {
	return math.Abs(float64(current-next)) >= scaleEpsilon
}

func shellScale(shell *Shell) float32 {
	if shell == nil || shell.impl == nil {
		return 1
	}
	impl := shell.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if !finitePositiveFloat32(impl.scale) {
		return 1
	}
	return impl.scale
}

func shellScaleFactor(shell *Shell) float32 {
	if shell == nil || shell.impl == nil {
		return defaultScaleFactor
	}
	impl := shell.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if !finitePositiveFloat32(impl.scaleFactor) {
		return defaultScaleFactor
	}
	return normalizeUserScaleFactor(impl.scaleFactor)
}

func shellSetScale(shell *Shell, scale float32) error {
	if !finitePositiveFloat32(scale) {
		return fmt.Errorf("%w: %v", errInvalidScale, scale)
	}
	if shell == nil || shell.impl == nil {
		return errShellClosed
	}
	impl := shell.impl
	impl.mu.Lock()
	if impl.closed || !impl.started {
		impl.mu.Unlock()
		return errShellClosed
	}
	if !scaleChanged(impl.scale, scale) && !scaleChanged(impl.builtScale, scale) && !scaleChanged(impl.builtScaleFactor, impl.scaleFactor) {
		impl.mu.Unlock()
		return nil
	}
	impl.scale = scale
	impl.mu.Unlock()
	return impl.rebuildDisplayResolver(shell)
}

func shellSetScaleFactor(shell *Shell, factor float32) error {
	if !finitePositiveFloat32(factor) {
		return fmt.Errorf("%w: %v", errInvalidScaleFactor, factor)
	}
	clamped := normalizeUserScaleFactor(factor)
	return updateScaleFactor(shell, func(float32) float32 { return clamped })
}

func shellZoomIn(shell *Shell) error {
	return shellZoomDelta(shell, zoomStepDelta)
}

func shellZoomOut(shell *Shell) error {
	return shellZoomDelta(shell, -zoomStepDelta)
}

func shellZoomReset(shell *Shell) error {
	return shellSetScaleFactor(shell, defaultScaleFactor)
}

func shellZoomDelta(shell *Shell, delta float64) error {
	return updateScaleFactor(shell, func(current float32) float32 {
		return roundScaleFactorFloat64(clampUserScaleFactorFloat64(float64(current) + delta))
	})
}

func shellZoomBy(shell *Shell, multiplier float32) error {
	if !finitePositiveFloat32(multiplier) {
		return fmt.Errorf("%w: %v", errInvalidScaleFactor, multiplier)
	}
	return updateScaleFactor(shell, func(current float32) float32 {
		return roundScaleFactorFloat64(clampUserScaleFactorFloat64(float64(current) * float64(multiplier)))
	})
}

func updateScaleFactor(shell *Shell, nextFn func(current float32) float32) error {
	if shell == nil || shell.impl == nil {
		return errShellClosed
	}
	impl := shell.impl
	impl.mu.Lock()
	if impl.closed || !impl.started {
		impl.mu.Unlock()
		return errShellClosed
	}
	current := impl.scaleFactor
	if !finitePositiveFloat32(current) {
		current = defaultScaleFactor
	}
	next := nextFn(current)
	impl.explicitScaleFactor = true
	if scaleChanged(impl.scaleFactor, next) {
		impl.scaleFactor = next
		impl.nvimConfig.snapshot.ScaleFactor = next
		impl.markSyncPendingLocked()
	} else if !scaleChanged(impl.builtScaleFactor, impl.scaleFactor) && !scaleChanged(impl.builtScale, impl.scale) {
		impl.mu.Unlock()
		return nil
	}
	impl.mu.Unlock()
	return impl.rebuildDisplayResolver(shell)
}

func (impl *shellImpl) handleZoomInput(ev ui.InputEvent) bool {
	if impl == nil || impl.checkOpen() != nil {
		return false
	}
	switch ev.Kind {
	case ui.InputKeyDown:
		switch {
		case ev.Key == ui.KeyEqual && (ev.Mods == ui.Cmd || ev.Mods == ui.Cmd|ui.Shift):
			_ = shellZoomIn(&Shell{impl: impl})
			return true
		case ev.Key == ui.KeyMinus && ev.Mods == ui.Cmd:
			_ = shellZoomOut(&Shell{impl: impl})
			return true
		case ev.Key == ui.Key0 && ev.Mods == ui.Cmd:
			_ = shellZoomReset(&Shell{impl: impl})
			return true
		}
	case ui.InputZoom:
		if finitePositiveFloat32(ev.Scale) {
			if ev.Scale != 1 {
				_ = shellZoomBy(&Shell{impl: impl}, ev.Scale)
			}
			return true
		}
	}
	return false
}

func (impl *shellImpl) rebuildDisplayResolver(shell *Shell) error {
	for {
		impl.mu.Lock()
		if impl.closed || !impl.started {
			impl.mu.Unlock()
			return errShellClosed
		}
		wantScale := impl.scale
		if !finitePositiveFloat32(wantScale) {
			wantScale = 1
		}
		wantFactor := normalizeUserScaleFactor(impl.scaleFactor)
		if !scaleChanged(impl.builtScale, wantScale) && !scaleChanged(impl.builtScaleFactor, wantFactor) {
			impl.mu.Unlock()
			return nil
		}
		guiFont := impl.lastGUIFont
		lineSpace := impl.lastLineSpace
		lineHeight := impl.lineHeight
		impl.mu.Unlock()

		newResolver, err := text.NewResolver(text.Config{
			GUIFont:     guiFont,
			Scale:       wantScale,
			ScaleFactor: wantFactor,
			LineSpace:   float32(lineSpace),
			LineHeight:  lineHeight,
		})
		if err != nil {
			if guiFont == "" {
				return fmt.Errorf("app: create font resolver for scale %v/%v: %w", wantScale, wantFactor, err)
			}
			newResolver, err = text.NewResolver(text.Config{
				Scale:       wantScale,
				ScaleFactor: wantFactor,
				LineSpace:   float32(lineSpace),
				LineHeight:  lineHeight,
			})
			if err != nil {
				return fmt.Errorf("app: create font resolver for scale %v/%v: %w", wantScale, wantFactor, err)
			}
		}

		impl.animSnapMu.Lock()
		impl.mu.Lock()
		if impl.closed {
			impl.mu.Unlock()
			impl.animSnapMu.Unlock()
			return errShellClosed
		}
		if scaleChanged(impl.scale, wantScale) || scaleChanged(impl.scaleFactor, wantFactor) || impl.lastGUIFont != guiFont || impl.lastLineSpace != lineSpace {
			if impl.lastGUIFont != guiFont || impl.lastLineSpace != lineSpace {
				impl.forceFullRepaint = true
				impl.configDirty = true
			}
			impl.mu.Unlock()
			impl.animSnapMu.Unlock()
			continue
		}
		if !scaleChanged(impl.builtScale, wantScale) && !scaleChanged(impl.builtScaleFactor, wantFactor) {
			impl.mu.Unlock()
			impl.animSnapMu.Unlock()
			return nil
		}
		if impl.animator == nil || impl.planner == nil {
			impl.mu.Unlock()
			impl.animSnapMu.Unlock()
			return errShellClosed
		}
		savedCfg := impl.currentAnimConfigLocked()
		nextPlanner := render.NewPlanner(newResolver)
		nextPlanner.SetLigaturesEnabled(savedCfg.LigaturesEnabled)
		nextAnimator := render.NewAnimator(nextPlanner, savedCfg, impl.animClock)
		impl.resolver = newResolver
		impl.planner = nextPlanner
		impl.animator = nextAnimator
		impl.builtScale = wantScale
		impl.builtScaleFactor = wantFactor
		impl.scaleRebuildCount++
		impl.forceFullRepaint = true
		impl.snapNextFrame = true
		impl.configDirty = true
		impl.configGeneration++
		impl.lastColumns, impl.lastRows = -1, -1
		services, window, contentW, contentH := impl.services, impl.window, impl.lastContentWidth, impl.lastContentHeight
		impl.mu.Unlock()
		impl.animSnapMu.Unlock()

		queueDisplayResize(shell, window, contentW, contentH)
		invalidate(services, window)
		return nil
	}
}

func shellDisplayMetrics(shell *Shell) DisplayMetrics {
	if shell == nil || shell.impl == nil {
		return DisplayMetrics{Scale: 1, ScaleFactor: defaultScaleFactor}
	}
	impl := shell.impl
	impl.mu.Lock()
	metrics := DisplayMetrics{
		Scale:        impl.scale,
		ScaleFactor:  impl.scaleFactor,
		FullScreen:   impl.fullScreen,
		RebuildCount: impl.scaleRebuildCount,
		RepaintCount: impl.repaintCount,
	}
	resolver := impl.resolver
	impl.mu.Unlock()
	if !finitePositiveFloat32(metrics.Scale) {
		metrics.Scale = 1
	}
	if !finitePositiveFloat32(metrics.ScaleFactor) {
		metrics.ScaleFactor = defaultScaleFactor
	} else {
		metrics.ScaleFactor = normalizeUserScaleFactor(metrics.ScaleFactor)
	}
	if resolver != nil {
		metrics.Cell = resolver.Cell()
	}
	return metrics
}

func shellNotifyDisplayRecovery(shell *Shell, reason DisplayRecoveryReason) {
	notifyDisplayRecovery(shell, reason, false)
}

func notifyDisplayRecovery(shell *Shell, reason DisplayRecoveryReason, skipInvalidate bool) {
	if shell == nil || shell.impl == nil {
		return
	}
	impl := shell.impl
	impl.mu.Lock()
	if impl.closed || !impl.started {
		impl.mu.Unlock()
		return
	}
	switch reason {
	case DisplayRecoveryEnterFullScreen:
		impl.fullScreen = true
	case DisplayRecoveryLeaveFullScreen:
		impl.fullScreen = false
	}
	impl.forceFullRepaint = true
	impl.configDirty = true
	impl.configGeneration++
	impl.repaintCount++
	if reason == DisplayRecoveryEnterFullScreen || reason == DisplayRecoveryLeaveFullScreen || reason == DisplayRecoveryGeometryJump {
		impl.snapNextFrame = true
	}
	services, window, contentW, contentH := impl.services, impl.window, impl.lastContentWidth, impl.lastContentHeight
	impl.mu.Unlock()

	if !skipInvalidate {
		queueDisplayResize(shell, window, contentW, contentH)
		invalidate(services, window)
	}
}

func queueDisplayResize(shell *Shell, window *mygo.Window, contentW, contentH int) {
	width, height := 0, 0
	if window != nil && !window.IsDestroyed() {
		width, height = window.ContentSize()
	} else {
		width, height = contentW, contentH
	}
	if width <= 0 || height <= 0 {
		return
	}
	shell.impl.asyncTask(func() { _ = shell.Resize(width, height) })
}

func observeWindowState(shell *Shell, c *ui.Context) {
	if shell == nil || shell.impl == nil || c == nil {
		return
	}
	width, height := c.Size()
	hidden, active := c.WindowHidden(), c.WindowActive()
	impl := shell.impl
	impl.mu.Lock()
	if width > 0 && height > 0 {
		impl.recordResize(int(width), int(height))
	}
	spaceReturn := impl.sawWindowState && impl.lastWindowHidden && !hidden
	reactivated := impl.sawWindowState && !impl.lastWindowActive && active
	impl.sawWindowState = true
	impl.lastWindowHidden, impl.lastWindowActive = hidden, active
	impl.mu.Unlock()
	if spaceReturn {
		notifyDisplayRecovery(shell, DisplayRecoverySpaceReturn, true)
	}
	if reactivated {
		notifyDisplayRecovery(shell, DisplayRecoveryWindowReactivated, true)
	}
}

func observePainterScale(shell *Shell, p *ui.Painter) {
	if shell == nil || shell.impl == nil || p == nil {
		return
	}
	scale := p.Scale()
	if scale <= 0 || math.IsNaN(float64(scale)) || math.IsInf(float64(scale), 0) {
		return
	}
	impl := shell.impl
	impl.mu.Lock()
	changed := scaleChanged(impl.scale, scale)
	impl.mu.Unlock()
	if changed {
		_ = shell.SetScale(scale)
	}
}

func attachDisplayWindow(shell *Shell, window *mygo.Window, onFocus func()) []func() {
	if shell == nil || shell.impl == nil || window == nil {
		return nil
	}
	updateScale := func() {
		if window.IsDestroyed() {
			return
		}
		display := mygo.Screen.DisplayMatching(window.Bounds())
		if display.ScaleFactor > 0 {
			_ = shell.SetScale(float32(display.ScaleFactor))
		}
	}
	offs := []func(){
		window.OnEnterFullScreen(func() { shell.NotifyDisplayRecovery(DisplayRecoveryEnterFullScreen) }),
		window.OnLeaveFullScreen(func() { shell.NotifyDisplayRecovery(DisplayRecoveryLeaveFullScreen) }),
		window.OnShow(func() { shell.NotifyDisplayRecovery(DisplayRecoverySpaceReturn) }),
		window.OnFocus(func() {
			shell.NotifyDisplayRecovery(DisplayRecoveryWindowReactivated)
			if onFocus != nil {
				onFocus()
			}
		}),
		window.OnMove(updateScale),
		window.OnResize(func() {
			if window.IsDestroyed() {
				return
			}
			width, height := window.ContentSize()
			_ = shell.Resize(width, height)
			updateScale()
		}),
		mygo.Screen.OnDisplaysChanged(updateScale),
	}
	return offs
}

func registerDisplayCleanup(shell *Shell, offs []func()) {
	if shell == nil || shell.impl == nil || len(offs) == 0 {
		return
	}
	impl := shell.impl
	impl.mu.Lock()
	if impl.closed {
		impl.mu.Unlock()
		for _, off := range offs {
			off()
		}
		return
	}
	impl.displayOffs = append(impl.displayOffs, offs...)
	impl.mu.Unlock()
}

func detachDisplayCleanup(impl *shellImpl) []func() {
	offs := impl.displayOffs
	impl.displayOffs = nil
	return offs
}

func (impl *shellImpl) recordResize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	oldWidth, oldHeight := impl.lastContentWidth, impl.lastContentHeight
	impl.lastContentWidth, impl.lastContentHeight = width, height
	if oldWidth <= 0 || oldHeight <= 0 {
		return
	}
	oldArea := float64(oldWidth) * float64(oldHeight)
	newArea := float64(width) * float64(height)
	if oldArea <= 0 {
		return
	}
	areaChange := math.Abs(newArea-oldArea) / oldArea
	widthChange := math.Abs(float64(width-oldWidth)) / float64(oldWidth)
	heightChange := math.Abs(float64(height-oldHeight)) / float64(oldHeight)
	if areaChange >= 0.4 || widthChange >= 0.35 || heightChange >= 0.35 {
		impl.snapNextFrame = true
		impl.forceFullRepaint = true
	}
}

func (impl *shellImpl) consumeDisplayFlags() (forceFullRepaint, snapNextFrame bool) {
	forceFullRepaint, snapNextFrame = impl.forceFullRepaint, impl.snapNextFrame
	impl.forceFullRepaint, impl.snapNextFrame = false, false
	return forceFullRepaint, snapNextFrame
}
