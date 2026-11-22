package render

import (
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/drawlist"
	"nvgui/renderlist"
	"nvgui/screen"
)

type Clock interface {
	Now() time.Time
}

type ManualClock struct {
	impl *manualClockImpl
}

func NewManualClock(start time.Time) *ManualClock        { return newManualClock(start) }
func (c *ManualClock) Now() time.Time                    { return manualClockNow(c) }
func (c *ManualClock) Advance(d time.Duration) time.Time { return manualClockAdvance(c, d) }

type AnimationConfig struct {
	CursorEnabled            bool    `json:"cursor_enabled"`
	CursorAnimationLength    float32 `json:"cursor_animation_length"`
	CursorShortLength        float32 `json:"cursor_short_length"`
	CursorTrailSize          float32 `json:"cursor_trail_size"`
	CursorAnimateInsertMode  bool    `json:"cursor_animate_insert_mode"`
	CursorAnimateCommandLine bool    `json:"cursor_animate_command_line"`

	ScrollEnabled         bool    `json:"scroll_enabled"`
	ScrollAnimationLength float32 `json:"scroll_animation_length"`
	ScrollFarLines        int     `json:"scroll_far_lines"`

	LigaturesEnabled bool `json:"ligatures_enabled"`
}

func DefaultAnimationConfig() AnimationConfig  { return defaultAnimationConfig() }
func DisabledAnimationConfig() AnimationConfig { return disabledAnimationConfig() }

type Spring struct {
	Position float32
	Velocity float32
}

func (s *Spring) Update(dt, animationLength float32) bool {
	return springUpdate(s, dt, animationLength)
}
func (s *Spring) Reset()       { springReset(s) }
func (s *Spring) Active() bool { return springActive(s) }

type Animator struct {
	impl *animatorImpl
}

func NewAnimator(planner *Planner, cfg AnimationConfig, clock Clock) *Animator {
	return newAnimator(planner, cfg, clock)
}
func (a *Animator) Config() AnimationConfig       { return animatorConfig(a) }
func (a *Animator) SetConfig(cfg AnimationConfig) { animatorSetConfig(a, cfg) }
func (a *Animator) Submit(state *screen.State, meta *screen.FrameMeta, list *drawlist.List) (renderlist.Frame, bool, error) {
	return animatorSubmit(a, state, meta, list)
}
func (a *Animator) Step() (renderlist.Frame, bool, error) { return animatorStep(a) }
func (a *Animator) Active() bool                          { return animatorActive(a) }

// CursorQuad 返回当前光标四边形；bool 表示是否存在有效光标四边形（已初始化的动画帧与停止帧均为 true，未初始化、离屏或 Busy 抑制时为 false）。
func (a *Animator) CursorQuad() ([4]ui.Point, bool) { return animatorCursorQuad(a) }
func (a *Animator) ScrollOffset(gridID int) (offsetLines float32, offsetPixels float32, active bool) {
	return animatorScrollOffset(a, gridID)
}

func (p *Planner) SetLigaturesEnabled(enabled bool) { plannerSetLigaturesEnabled(p, enabled) }
func (p *Planner) LigaturesEnabled() bool           { return plannerLigaturesEnabled(p) }
