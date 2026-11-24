package app

import "nvgui/render"

func AnimationConfigFromEnv(env []string) render.AnimationConfig {
	return animationConfigFromEnv(env)
}

func GUIFontFromEnv(env []string) string {
	return guiFontFromEnv(env)
}

func LineHeightFromEnv(env []string) float32 {
	return lineHeightFromEnv(env)
}

func (s *Shell) AnimationConfig() render.AnimationConfig {
	return shellAnimationConfig(s)
}

func (s *Shell) SetAnimationConfig(cfg render.AnimationConfig) {
	shellSetAnimationConfig(s, cfg)
}
