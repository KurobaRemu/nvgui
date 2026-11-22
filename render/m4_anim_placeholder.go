package render

import (
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
)

type manualClockImpl struct{}

type animatorImpl struct{}

var errM4AnimNotImplemented = errors.New("render: M4 animation not implemented")

func newManualClock(time.Time) *ManualClock                    { return &ManualClock{impl: &manualClockImpl{}} }
func manualClockNow(*ManualClock) time.Time                    { return time.Time{} }
func manualClockAdvance(*ManualClock, time.Duration) time.Time { return time.Time{} }

func defaultAnimationConfig() AnimationConfig  { return AnimationConfig{} }
func disabledAnimationConfig() AnimationConfig { return AnimationConfig{} }

func springUpdate(*Spring, float32, float32) bool { return false }
func springReset(*Spring)                         {}
func springActive(*Spring) bool                   { return false }

func newAnimator(*Planner, AnimationConfig, Clock) *Animator {
	return &Animator{impl: &animatorImpl{}}
}

func animatorConfig(*Animator) AnimationConfig { return AnimationConfig{} }
func animatorSetConfig(*Animator, AnimationConfig) {
}

func animatorSubmit(*Animator, *screen.State, *screen.FrameMeta, *drawlist.List) (renderlist.Frame, bool, error) {
	return renderlist.Frame{}, false, errM4AnimNotImplemented
}

func animatorStep(*Animator) (renderlist.Frame, bool, error) {
	return renderlist.Frame{}, false, errM4AnimNotImplemented
}

func animatorActive(*Animator) bool                    { return false }
func animatorCursorQuad(*Animator) ([4]ui.Point, bool) { return [4]ui.Point{}, false }
func animatorScrollOffset(*Animator, int) (float32, float32, bool) {
	return 0, 0, false
}
