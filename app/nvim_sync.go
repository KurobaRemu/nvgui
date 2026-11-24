package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/neovim/go-client/nvim"

	"nvgui/render"
)

const (
	nvimConfigPollInterval = 250 * time.Millisecond
	nvimSyncExpression     = `{'nvgui': get(g:, 'nvgui', v:null), ` +
		`'nvgui_cursor_animation': get(g:, 'nvgui_cursor_animation', v:null), ` +
		`'nvgui_cursor_animation_length': get(g:, 'nvgui_cursor_animation_length', v:null), ` +
		`'nvgui_cursor_animate_command_line': get(g:, 'nvgui_cursor_animate_command_line', v:null), ` +
		`'nvgui_cursor_animate_insert_mode': get(g:, 'nvgui_cursor_animate_insert_mode', v:null), ` +
		`'nvgui_scroll_animation': get(g:, 'nvgui_scroll_animation', v:null), ` +
		`'nvgui_scroll_animation_length': get(g:, 'nvgui_scroll_animation_length', v:null), ` +
		`'nvgui_ligatures': get(g:, 'nvgui_ligatures', v:null), ` +
		`'nvgui_open_in': get(g:, 'nvgui_open_in', v:null), ` +
		`'nvgui_scale_factor': get(g:, 'nvgui_scale_factor', v:null)}`
)

type nvimConfigState struct {
	snapshot        NvimConfigSnapshot
	lastSynced      NvimConfigSnapshot
	initialSyncDone bool
	syncPending     bool
}

func nvimConfigSnapshotFrom(cfg render.AnimationConfig, policy OpenInPolicy) NvimConfigSnapshot {
	if policy == "" {
		policy = OpenInFront
	}
	return NvimConfigSnapshot{
		CursorAnimation:          cfg.CursorEnabled,
		CursorAnimationLength:    cfg.CursorAnimationLength,
		CursorAnimateCommandLine: cfg.CursorAnimateCommandLine,
		CursorAnimateInsertMode:  cfg.CursorAnimateInsertMode,
		ScrollAnimation:          cfg.ScrollEnabled,
		ScrollAnimationLength:    cfg.ScrollAnimationLength,
		Ligatures:                cfg.LigaturesEnabled,
		OpenIn:                   policy,
		ScaleFactor:              defaultScaleFactor,
	}
}

func (snapshot NvimConfigSnapshot) withValidDefaults() NvimConfigSnapshot {
	if snapshot.CursorAnimationLength <= 0 || math.IsNaN(float64(snapshot.CursorAnimationLength)) || math.IsInf(float64(snapshot.CursorAnimationLength), 0) {
		snapshot.CursorAnimationLength = render.DefaultAnimationConfig().CursorAnimationLength
	}
	if snapshot.ScrollAnimationLength <= 0 || math.IsNaN(float64(snapshot.ScrollAnimationLength)) || math.IsInf(float64(snapshot.ScrollAnimationLength), 0) {
		snapshot.ScrollAnimationLength = render.DefaultAnimationConfig().ScrollAnimationLength
	}
	if _, ok := parseOpenInPolicy(string(snapshot.OpenIn)); !ok {
		snapshot.OpenIn = OpenInFront
	}
	snapshot.ScaleFactor = normalizeUserScaleFactor(snapshot.ScaleFactor)
	return snapshot
}

func shellNvimConfig(shell *Shell) NvimConfigSnapshot {
	if shell == nil || shell.impl == nil {
		return nvimConfigSnapshotFrom(render.DefaultAnimationConfig(), OpenInFront).normalized()
	}
	impl := shell.impl
	impl.mu.Lock()
	snapshot := impl.nvimConfig.snapshot
	impl.mu.Unlock()
	return snapshot.normalized()
}

func (snapshot NvimConfigSnapshot) normalized() NvimConfigSnapshot {
	snapshot.CursorAnimationLength = float32(math.Round(float64(snapshot.CursorAnimationLength)*1_000_000) / 1_000_000)
	snapshot.ScrollAnimationLength = float32(math.Round(float64(snapshot.ScrollAnimationLength)*1_000_000) / 1_000_000)
	snapshot.ScaleFactor = normalizeUserScaleFactor(snapshot.ScaleFactor)
	snapshot = snapshot.withValidDefaults()
	return snapshot
}

func shellOpenInPolicy(shell *Shell) OpenInPolicy {
	return shellNvimConfig(shell).OpenIn
}

func shellSetOpenInPolicy(shell *Shell, policy OpenInPolicy) {
	if shell == nil || shell.impl == nil {
		return
	}
	parsed, ok := parseOpenInPolicy(string(policy))
	if !ok {
		return
	}
	impl := shell.impl
	impl.mu.Lock()
	if impl.nvimConfig.snapshot.OpenIn == parsed {
		impl.explicitOpenIn = true
	} else {
		impl.nvimConfig.snapshot.OpenIn = parsed
		impl.explicitOpenIn = true
		impl.markSyncPendingLocked()
	}
	workspace, closed := impl.workspace, impl.closed
	impl.mu.Unlock()
	if !closed && workspace != nil {
		workspace.SetOpenInPolicy(parsed)
	}
}

func (impl *shellImpl) markSyncPendingLocked() {
	impl.nvimConfig.syncPending = true
	select {
	case impl.syncCh <- struct{}{}:
	default:
	}
}

func shellApplyAnimationConfigAndSync(shell *Shell, ctx context.Context, cfg render.AnimationConfig) error {
	shell.SetAnimationConfig(cfg)
	if shell != nil && shell.impl != nil {
		impl := shell.impl
		impl.mu.Lock()
		impl.nvimConfig.syncPending = true
		impl.mu.Unlock()
	}
	_, err := shellSyncNvimConfig(shell, ctx)
	return err
}

func shellSyncNvimConfig(shell *Shell, ctx context.Context) (NvimConfigSnapshot, error) {
	if shell == nil || shell.impl == nil {
		return nvimConfigSnapshotFrom(render.DefaultAnimationConfig(), OpenInFront).normalized(), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	impl := shell.impl
	impl.mu.Lock()
	if !impl.started || impl.closed || impl.session == nil {
		snapshot := impl.nvimConfig.snapshot.normalized()
		impl.mu.Unlock()
		return snapshot, nil
	}
	session := impl.session
	impl.mu.Unlock()
	select {
	case <-impl.syncSem:
	case <-ctx.Done():
		return shellNvimConfig(shell), ctx.Err()
	case <-impl.closing:
		return shellNvimConfig(shell), nil
	case <-session.Exited():
		return shellNvimConfig(shell), nil
	}
	defer func() { impl.syncSem <- struct{}{} }()
	if !syncCanUseRPC(ctx, impl, session) {
		return shellNvimConfig(shell), nil
	}
	remote, err := evalNvimConfig(ctx, impl, session)
	if err != nil {
		return shellNvimConfig(shell), err
	}
	initial := false
	initialChanged := false
	shouldSyncWorkspace := false
	scaleFactorChanged := false
	impl.mu.Lock()
	services, window := impl.services, impl.window
	before := impl.nvimConfig.snapshot.normalized()
	if !impl.nvimConfig.initialSyncDone {
		initial = true
		merged := initialNvimConfig(impl, remote, before).normalized()
		initialChanged = merged != before
		shouldSyncWorkspace = initialChanged && merged.OpenIn != before.OpenIn && impl.workspace != nil
		scaleFactorChanged = initialChanged && scaleChanged(merged.ScaleFactor, impl.scaleFactor)
		impl.nvimConfig.snapshot = merged
		impl.nvimConfig.initialSyncDone = true
		impl.nvimConfig.syncPending = true
		if initialChanged {
			impl.applySnapshotToShellLocked(merged)
			if scaleFactorChanged {
				impl.scaleFactor = merged.ScaleFactor
			}
			impl.configDirty = true
			impl.configGeneration++
		}
	}
	local := impl.nvimConfig.snapshot.normalized()
	pending := impl.nvimConfig.syncPending
	workspace := impl.workspace
	impl.mu.Unlock()
	if shouldSyncWorkspace {
		workspace.SetOpenInPolicy(local.OpenIn)
	}
	if scaleFactorChanged {
		_ = impl.rebuildDisplayResolver(shell)
	}
	if initialChanged {
		invalidate(services, window)
	}
	if initial || pending {
		if err := writeNvimConfig(ctx, impl, session, local); err != nil {
			return local, err
		}
		impl.mu.Lock()
		if impl.nvimConfig.snapshot.normalized() == local {
			impl.nvimConfig.lastSynced = local
			impl.nvimConfig.syncPending = false
		}
		impl.mu.Unlock()
		return local, nil
	}
	if err := applyRemoteConfig(shell, remote, local); err != nil {
		return shellNvimConfig(shell), err
	}
	return shellNvimConfig(shell), nil
}

func syncCanUseRPC(ctx context.Context, impl *shellImpl, sess nvimConfigSession) bool {
	select {
	case <-ctx.Done():
		return false
	case <-impl.closing:
		return false
	case <-sess.Exited():
		return false
	default:
	}
	result := make(chan struct {
		blocking bool
		err      error
	}, 1)
	go func() {
		mode, err := sess.Nvim().Mode()
		result <- struct {
			blocking bool
			err      error
		}{blocking: mode != nil && mode.Blocking, err: err}
	}()
	select {
	case probe := <-result:
		return probe.err == nil && !probe.blocking
	case <-ctx.Done():
		return false
	case <-impl.closing:
		return false
	case <-sess.Exited():
		return false
	}
}

func evalNvimConfig(ctx context.Context, impl *shellImpl, sess nvimConfigSession) (map[string]any, error) {
	result := make(chan struct {
		values map[string]any
		err    error
	}, 1)
	go func() {
		values := make(map[string]any)
		err := sess.Nvim().Eval(nvimSyncExpression, &values)
		result <- struct {
			values map[string]any
			err    error
		}{values: values, err: err}
	}()
	select {
	case value := <-result:
		return value.values, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-impl.closing:
		return nil, errShellClosed
	case <-sess.Exited():
		return nil, errShellClosed
	}
}

type nvimConfigSession interface {
	Nvim() *nvim.Nvim
	Exited() <-chan struct{}
}

func initialNvimConfig(impl *shellImpl, remote map[string]any, before NvimConfigSnapshot) NvimConfigSnapshot {
	cfg := impl.animator.Config()
	local := nvimConfigSnapshotFrom(cfg, before.OpenIn)
	local.ScaleFactor = before.ScaleFactor
	envKeys := impl.envConfigKeys
	explicitKeys := impl.explicitKeys
	masterValue, masterSet := envBool(impl.req.Env, "NVGUI_NO_ANIMATIONS")
	noAnimations := masterSet && masterValue
	overridden := func(envKey string) bool {
		return impl.explicitConfig || explicitKeys[envKey] || envKeys[envKey] ||
			(noAnimations && (envKey == "NVGUI_CURSOR_ANIM" || envKey == "NVGUI_SCROLL_ANIM"))
	}
	fromRemote := func(key, envKey string, fallback any) any {
		if overridden(envKey) {
			return fallback
		}
		if value, ok := remote[key]; ok && value != nil {
			return value
		}
		return fallback
	}
	merged := local
	if value, ok := valueToBool(fromRemote("nvgui_cursor_animation", "NVGUI_CURSOR_ANIM", local.CursorAnimation)); ok {
		merged.CursorAnimation = value
	}
	if value, ok := valueToBool(fromRemote("nvgui_cursor_animate_command_line", "NVGUI_CURSOR_ANIMATE_COMMAND_LINE", local.CursorAnimateCommandLine)); ok {
		merged.CursorAnimateCommandLine = value
	}
	if value, ok := valueToBool(fromRemote("nvgui_cursor_animate_insert_mode", "NVGUI_CURSOR_ANIMATE_INSERT", local.CursorAnimateInsertMode)); ok {
		merged.CursorAnimateInsertMode = value
	}
	if value, ok := valueToBool(fromRemote("nvgui_scroll_animation", "NVGUI_SCROLL_ANIM", local.ScrollAnimation)); ok {
		merged.ScrollAnimation = value
	}
	if value, ok := valueToBool(fromRemote("nvgui_ligatures", "NVGUI_LIGATURES", local.Ligatures)); ok {
		merged.Ligatures = value
	}
	if !impl.explicitConfig {
		if value, ok := valueToFloat(fromRemote("nvgui_cursor_animation_length", "NVGUI_CURSOR_ANIM", local.CursorAnimationLength)); ok && value > 0 {
			merged.CursorAnimationLength = float32(value)
		}
		if value, ok := valueToFloat(fromRemote("nvgui_scroll_animation_length", "NVGUI_SCROLL_ANIM", local.ScrollAnimationLength)); ok && value > 0 {
			merged.ScrollAnimationLength = float32(value)
		}
	}
	_, envOpenInSet := parseOpenInPolicy(envValue(impl.req.Env, "NVGUI_OPEN_IN"))
	if !envOpenInSet && !impl.explicitOpenIn && !impl.explicitConfig {
		if value, ok := remote["nvgui_open_in"]; ok && value != nil {
			if policy, ok := parseOpenInPolicy(toString(value)); ok {
				merged.OpenIn = policy
			}
		}
	}
	_, envScaleSet := scaleFactorFromEnv(impl.req.Env)
	if !envScaleSet && !impl.explicitScaleFactor && !impl.explicitConfig {
		if value, ok := remote["nvgui_scale_factor"]; ok && value != nil {
			if parsed, ok := valueToFloat(value); ok && parsed > 0 {
				merged.ScaleFactor = normalizeUserScaleFactor(float32(parsed))
			}
		}
	}
	return merged.normalized()
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(value)
	}
}

func applyRemoteConfig(shell *Shell, remote map[string]any, local NvimConfigSnapshot) error {
	local = local.normalized()
	impl := shell.impl
	impl.mu.Lock()
	if impl.closed || impl.nvimConfig.syncPending || impl.nvimConfig.snapshot.normalized() != local {
		impl.mu.Unlock()
		return nil
	}
	baseline := impl.nvimConfig.lastSynced.normalized()
	if baseline == (NvimConfigSnapshot{}) {
		baseline = local
	}
	synced := baseline
	next := local
	changed := false
	remoteChanged := false
	setBool := func(key string, target *bool) {
		value, ok := remote[key]
		if !ok || value == nil {
			return
		}
		parsed, ok := valueToBool(value)
		if !ok {
			return
		}
		var base bool
		switch key {
		case "nvgui_cursor_animation":
			base = baseline.CursorAnimation
			synced.CursorAnimation = parsed
		case "nvgui_cursor_animate_command_line":
			base = baseline.CursorAnimateCommandLine
			synced.CursorAnimateCommandLine = parsed
		case "nvgui_cursor_animate_insert_mode":
			base = baseline.CursorAnimateInsertMode
			synced.CursorAnimateInsertMode = parsed
		case "nvgui_scroll_animation":
			base = baseline.ScrollAnimation
			synced.ScrollAnimation = parsed
		case "nvgui_ligatures":
			base = baseline.Ligatures
			synced.Ligatures = parsed
		default:
			return
		}
		remoteChanged = true
		if base != parsed {
			*target = parsed
			changed = true
		}
	}
	setBool("nvgui_cursor_animation", &next.CursorAnimation)
	setBool("nvgui_cursor_animate_command_line", &next.CursorAnimateCommandLine)
	setBool("nvgui_cursor_animate_insert_mode", &next.CursorAnimateInsertMode)
	setBool("nvgui_scroll_animation", &next.ScrollAnimation)
	setBool("nvgui_ligatures", &next.Ligatures)
	setLength := func(key string, target *float32) {
		value, ok := remote[key]
		if !ok || value == nil {
			return
		}
		parsed, ok := valueToFloat(value)
		if !ok || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return
		}
		rounded := float32(math.Round(parsed*1_000_000) / 1_000_000)
		if rounded <= 0 {
			return
		}
		var base float32
		switch key {
		case "nvgui_cursor_animation_length":
			base = baseline.CursorAnimationLength
			synced.CursorAnimationLength = rounded
		case "nvgui_scroll_animation_length":
			base = baseline.ScrollAnimationLength
			synced.ScrollAnimationLength = rounded
		default:
			return
		}
		remoteChanged = true
		if base != rounded {
			*target = rounded
			changed = true
		}
	}
	setLength("nvgui_cursor_animation_length", &next.CursorAnimationLength)
	setLength("nvgui_scroll_animation_length", &next.ScrollAnimationLength)
	if value, ok := remote["nvgui_open_in"]; ok && value != nil {
		if policy, ok := parseOpenInPolicy(toString(value)); ok {
			synced.OpenIn = policy
			remoteChanged = true
			if baseline.OpenIn != policy {
				next.OpenIn = policy
				changed = true
			}
		}
	}
	if value, ok := remote["nvgui_scale_factor"]; ok && value != nil {
		if parsed, ok := valueToFloat(value); ok && parsed > 0 {
			clamped := normalizeUserScaleFactor(float32(parsed))
			synced.ScaleFactor = clamped
			remoteChanged = true
			if scaleChanged(baseline.ScaleFactor, clamped) {
				next.ScaleFactor = clamped
				changed = true
			}
		}
	}
	next = next.normalized()
	synced = synced.normalized()
	if !remoteChanged {
		impl.mu.Unlock()
		return nil
	}
	impl.nvimConfig.lastSynced = synced
	if !changed || next == local {
		impl.mu.Unlock()
		return nil
	}
	scaleFactorChanged := scaleChanged(next.ScaleFactor, impl.scaleFactor)
	impl.nvimConfig.snapshot = next
	impl.applySnapshotToShellLocked(next)
	if scaleFactorChanged {
		impl.scaleFactor = next.ScaleFactor
	}
	impl.configDirty = true
	impl.configGeneration++
	services, window := impl.services, impl.window
	workspace := impl.workspace
	closed := impl.closed
	impl.mu.Unlock()
	if closed {
		return nil
	}
	if workspace != nil && next.OpenIn != local.OpenIn {
		workspace.SetOpenInPolicy(next.OpenIn)
	}
	if scaleFactorChanged {
		_ = impl.rebuildDisplayResolver(shell)
	}
	invalidate(services, window)
	return nil
}

func (impl *shellImpl) applySnapshotToShellLocked(snapshot NvimConfigSnapshot) {
	if impl.animator != nil {
		cfg := impl.animator.Config()
		cfg.CursorEnabled = snapshot.CursorAnimation
		cfg.CursorAnimationLength = snapshot.CursorAnimationLength
		cfg.CursorAnimateCommandLine = snapshot.CursorAnimateCommandLine
		cfg.CursorAnimateInsertMode = snapshot.CursorAnimateInsertMode
		cfg.ScrollEnabled = snapshot.ScrollAnimation
		cfg.ScrollAnimationLength = snapshot.ScrollAnimationLength
		cfg.LigaturesEnabled = snapshot.Ligatures
		impl.animator.SetConfig(cfg)
		if impl.planner != nil {
			impl.planner.SetLigaturesEnabled(cfg.LigaturesEnabled)
		}
	}
}

func writeNvimConfig(ctx context.Context, impl *shellImpl, sess nvimConfigSession, snapshot NvimConfigSnapshot) error {
	payload, err := json.Marshal(snapshot.normalized())
	if err != nil {
		return fmt.Errorf("app: encode nvim config: %w", err)
	}
	const code = `local cfg = vim.json.decode(...)
vim.g.nvgui = 1
for key, value in pairs(cfg) do
  vim.g[key] = value
end`
	result := make(chan error, 1)
	go func() { result <- sess.Nvim().ExecLua(code, nil, string(payload)) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-impl.closing:
		return errShellClosed
	case <-sess.Exited():
		return errShellClosed
	}
}

func (impl *shellImpl) watchNvimConfig() {
	ticker := time.NewTicker(nvimConfigPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-impl.closing:
			return
		case <-impl.ctx.Done():
			return
		case <-impl.session.Exited():
			return
		case <-impl.syncCh:
		case <-ticker.C:
		}
		if impl.isClosing() {
			return
		}
		if impl.session.Latest() == nil {
			continue
		}
		_, _ = shellSyncNvimConfig(&Shell{impl: impl}, impl.ctx)
	}
}
