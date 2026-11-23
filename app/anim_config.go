package app

import (
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"nvgui/render"
)

func animationConfigFromEnv(env []string) render.AnimationConfig {
	cfg := render.DefaultAnimationConfig()
	if value, ok := envBool(env, "NVGUI_CURSOR_ANIM"); ok {
		cfg.CursorEnabled = value
	}
	if value, ok := envBool(env, "NVGUI_CURSOR_ANIMATE_COMMAND_LINE"); ok {
		cfg.CursorAnimateCommandLine = value
	}
	if value, ok := envBool(env, "NVGUI_CURSOR_ANIMATE_INSERT"); ok {
		cfg.CursorAnimateInsertMode = value
	}
	if value, ok := envBool(env, "NVGUI_SCROLL_ANIM"); ok {
		cfg.ScrollEnabled = value
	}
	if value, ok := envBool(env, "NVGUI_LIGATURES"); ok {
		cfg.LigaturesEnabled = value
	}
	if value, ok := envBool(env, "NVGUI_NO_ANIMATIONS"); ok && value {
		cfg.CursorEnabled = false
		cfg.ScrollEnabled = false
	}
	return cfg
}

func guiFontFromEnv(env []string) string {
	return strings.TrimSpace(envValue(env, "NVGUI_GUIFONT"))
}

func envBool(env []string, key string) (bool, bool) {
	value, ok := envValueOK(env, key)
	if !ok {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "on":
		return true, true
	case "0", "false", "off":
		return false, true
	default:
		return false, false
	}
}

func envValueOK(env []string, key string) (string, bool) {
	prefix := key + "="
	for index := len(env) - 1; index >= 0; index-- {
		if value, ok := strings.CutPrefix(env[index], prefix); ok {
			return value, true
		}
	}
	return "", false
}

func envConfigKeys(env []string) map[string]bool {
	keys := make(map[string]bool, len(animationEnvKeys))
	for _, key := range animationEnvKeys {
		if _, ok := envBool(env, key); ok {
			keys[key] = true
		}
	}
	return keys
}

func shellAnimationConfig(shell *Shell) render.AnimationConfig {
	if shell == nil || shell.impl == nil {
		return render.DefaultAnimationConfig()
	}
	impl := shell.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if impl.animator == nil {
		return render.DefaultAnimationConfig()
	}
	return impl.animator.Config()
}

func shellSetAnimationConfig(shell *Shell, cfg render.AnimationConfig) {
	if shell == nil || shell.impl == nil {
		return
	}
	impl := shell.impl
	impl.mu.Lock()
	valid := validAnimationConfig(cfg)
	impl.explicitConfig = true
	if impl.explicitKeys == nil {
		impl.explicitKeys = make(map[string]bool)
	}
	clear(impl.explicitKeys)
	for _, key := range animationEnvKeys {
		impl.explicitKeys[key] = true
	}
	next := nvimConfigSnapshotFrom(valid, impl.nvimConfig.snapshot.OpenIn).normalized()
	snapshotUnchanged := impl.nvimConfig.snapshot.normalized() == next
	impl.nvimConfig.snapshot = next
	if !snapshotUnchanged {
		impl.markSyncPendingLocked()
	}
	if impl.animator == nil || impl.planner == nil {
		impl.mu.Unlock()
		return
	}
	animatorUnchanged := impl.animator.Config() == valid
	if snapshotUnchanged && animatorUnchanged {
		impl.mu.Unlock()
		return
	}
	impl.configDirty = true
	impl.configGeneration++
	impl.animator.SetConfig(valid)
	impl.planner.SetLigaturesEnabled(valid.LigaturesEnabled)
	services := impl.services
	window := impl.window
	closed := impl.closed
	impl.mu.Unlock()
	if !closed {
		invalidate(services, window)
	}
}

func validAnimationConfig(cfg render.AnimationConfig) render.AnimationConfig {
	defaults := render.DefaultAnimationConfig()
	if cfg.CursorAnimationLength <= 0 || math.IsNaN(float64(cfg.CursorAnimationLength)) || math.IsInf(float64(cfg.CursorAnimationLength), 0) {
		cfg.CursorAnimationLength = defaults.CursorAnimationLength
	}
	if cfg.ScrollAnimationLength <= 0 || math.IsNaN(float64(cfg.ScrollAnimationLength)) || math.IsInf(float64(cfg.ScrollAnimationLength), 0) {
		cfg.ScrollAnimationLength = defaults.ScrollAnimationLength
	}
	return cfg
}

func invalidate(services ui.Services, window *mygo.Window) {
	if window != nil {
		window.Invalidate()
		return
	}
	services.Invalidate()
}

func valueToBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case int64:
		return typed != 0, true
	case int:
		return typed != 0, true
	case uint64:
		return typed != 0, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return false, false
		}
		return typed != 0, true
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return false, false
		}
		return typed != 0, true
	case string:
		return envBool([]string{"value=" + typed}, "value")
	default:
		return false, false
	}
}

func valueToFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float64:
		return finiteFloat(typed)
	case float32:
		return finiteFloat(float64(typed))
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return finiteFloat(parsed)
	default:
		return 0, false
	}
}

func finiteFloat(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}
