package app

import "nvgui/render"

func animationConfigFromEnv([]string) render.AnimationConfig { return render.AnimationConfig{} }
func guiFontFromEnv([]string) string                         { return "" }
func shellAnimationConfig(*Shell) render.AnimationConfig     { return render.AnimationConfig{} }
func shellSetAnimationConfig(*Shell, render.AnimationConfig) {}
