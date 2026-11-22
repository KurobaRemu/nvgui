package render

import "math"

func defaultAnimationConfig() AnimationConfig {
	return AnimationConfig{
		CursorEnabled:            true,
		CursorAnimationLength:    0.080,
		CursorShortLength:        0.030,
		CursorTrailSize:          0.25,
		CursorAnimateInsertMode:  true,
		CursorAnimateCommandLine: false,
		ScrollEnabled:            true,
		ScrollAnimationLength:    0.180,
		ScrollFarLines:           10,
		LigaturesEnabled:         true,
	}
}

func disabledAnimationConfig() AnimationConfig {
	cfg := defaultAnimationConfig()
	cfg.CursorEnabled = false
	cfg.ScrollEnabled = false
	cfg.LigaturesEnabled = false
	return cfg
}

func springUpdate(s *Spring, dt, animationLength float32) bool {
	if s == nil {
		return false
	}
	if !finiteSpringValue(s.Position) || !finiteSpringValue(s.Velocity) ||
		!finiteSpringValue(dt) || !finiteSpringValue(animationLength) {
		s.Reset()
		return false
	}
	if dt <= 0 {
		return s.Active()
	}
	if animationLength <= 0 || animationLength <= dt {
		s.Reset()
		return false
	}
	if s.Position == 0 && s.Velocity == 0 {
		s.Reset()
		return false
	}
	const zeta float32 = 1.0
	omega := 4.0 / (zeta * animationLength)
	a := s.Position
	b := s.Position*omega + s.Velocity
	c := float32(math.Exp(float64(-omega * dt)))
	s.Position = (a + b*dt) * c
	s.Velocity = c * (-a*omega - b*dt*omega + b)
	frameTime := dt
	if frameTime < 1.0/120.0 {
		frameTime = 1.0 / 120.0
	}
	if math.Abs(float64(s.Position)) < 0.01 &&
		math.Abs(float64(s.Velocity))*float64(frameTime) < 0.01 {
		s.Reset()
		return false
	}
	if !finiteSpringValue(s.Position) || !finiteSpringValue(s.Velocity) {
		s.Reset()
		return false
	}
	return true
}

func springReset(s *Spring) {
	if s == nil {
		return
	}
	s.Position = 0
	s.Velocity = 0
}

func springActive(s *Spring) bool {
	return s != nil && (s.Position != 0 || s.Velocity != 0)
}

func finiteSpringValue(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}
