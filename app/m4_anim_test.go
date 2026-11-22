package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/render"
)

func m4AnimFilteredEnv(extra ...string) []string {
	raw := os.Environ()
	env := make([]string, 0, len(raw)+len(extra))
	for _, entry := range raw {
		if !strings.HasPrefix(entry, "NVGUI_") {
			env = append(env, entry)
		}
	}
	return append(env, extra...)
}

func m4AnimStart(t *testing.T, extraArgs []string, env []string) *Shell {
	t.Helper()
	args := append([]string{"--clean", "-n"}, extraArgs...)
	req := LaunchRequest{
		Args:       args,
		Env:        env,
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	shell, err := Start(context.Background(), req, nil, &testClipboard{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	return shell
}

func TestM4Anim_EnvOverridesAndInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want render.AnimationConfig
	}{
		{
			name: "invalid boolean falls back to default",
			env:  []string{"NVGUI_CURSOR_ANIM=maybe", "NVGUI_SCROLL_ANIM=2", "NVGUI_LIGATURES=YES"},
			want: render.DefaultAnimationConfig(),
		},
		{
			name: "master switch keeps ligatures",
			env:  []string{"NVGUI_NO_ANIMATIONS=on", "NVGUI_LIGATURES=1"},
			want: func() render.AnimationConfig {
				cfg := render.DefaultAnimationConfig()
				cfg.CursorEnabled = false
				cfg.ScrollEnabled = false
				return cfg
			}(),
		},
		{
			name: "explicit values apply",
			env: []string{
				"NVGUI_CURSOR_ANIM=off",
				"NVGUI_CURSOR_ANIMATE_INSERT=false",
				"NVGUI_CURSOR_ANIMATE_COMMAND_LINE=true",
				"NVGUI_SCROLL_ANIM=0",
				"NVGUI_LIGATURES=off",
			},
			want: func() render.AnimationConfig {
				cfg := render.DefaultAnimationConfig()
				cfg.CursorEnabled = false
				cfg.CursorAnimateInsertMode = false
				cfg.CursorAnimateCommandLine = true
				cfg.ScrollEnabled = false
				cfg.LigaturesEnabled = false
				return cfg
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnimationConfigFromEnv(tc.env); got != tc.want {
				t.Fatalf("AnimationConfigFromEnv = %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := GUIFontFromEnv([]string{"NVGUI_GUIFONT=  Menlo:h14  "}); got != "Menlo:h14" {
		t.Fatalf("GUIFontFromEnv = %q, want Menlo:h14", got)
	}
}

func TestM4Anim_StartupInvalidGUIFontAndNoNvimConfigSideEffects(t *testing.T) {
	env := m4AnimFilteredEnv("NVGUI_GUIFONT=Menlo:h-1", "NVGUI_CURSOR_ANIM=0", "NVGUI_SCROLL_ANIM=0")
	shell := m4AnimStart(t, nil, env)
	impl := shell.impl
	impl.mu.Lock()
	lastGUIFont := impl.lastGUIFont
	cfg := impl.animator.Config()
	configDirty := impl.configDirty
	explicitConfig := impl.explicitConfig
	impl.mu.Unlock()
	if lastGUIFont != "" {
		t.Fatalf("lastGUIFont = %q, want fallback empty string", lastGUIFont)
	}
	if cfg.CursorEnabled || cfg.ScrollEnabled || !cfg.LigaturesEnabled {
		t.Fatalf("startup config lost env overrides: %+v", cfg)
	}
	if configDirty || explicitConfig {
		t.Fatalf("untouched vim.g.nvgui_* set configDirty=%v explicitConfig=%v", configDirty, explicitConfig)
	}
}

func TestM4Anim_NvimConfigAppliedAndEnvironmentOverridesWin(t *testing.T) {
	const nvimSetup = "let g:nvgui_cursor_animation = v:false | let g:nvgui_scroll_animation = v:false | let g:nvgui_scroll_animation_length = 0.125 | let g:nvgui_ligatures = v:false"
	t.Run("nvim config applies", func(t *testing.T) {
		shell := m4AnimStart(t, []string{"--cmd", nvimSetup}, m4AnimFilteredEnv())
		waitUntil(t, 5*time.Second, "vim.g animation config", func() bool {
			cfg := shell.AnimationConfig()
			return !cfg.CursorEnabled && !cfg.ScrollEnabled && !cfg.LigaturesEnabled && cfg.ScrollAnimationLength == 0.125
		})
	})
	t.Run("environment overrides nvim config", func(t *testing.T) {
		shell := m4AnimStart(t, []string{"--cmd", nvimSetup}, m4AnimFilteredEnv("NVGUI_NO_ANIMATIONS=0", "NVGUI_CURSOR_ANIM=1", "NVGUI_SCROLL_ANIM=1", "NVGUI_LIGATURES=1"))
		waitUntil(t, 5*time.Second, "startup config published", func() bool {
			return shell.impl.session.Latest() != nil
		})
		cfg := shell.AnimationConfig()
		if !cfg.CursorEnabled || !cfg.ScrollEnabled || !cfg.LigaturesEnabled || cfg.ScrollAnimationLength != render.DefaultAnimationConfig().ScrollAnimationLength {
			t.Fatalf("env overrides were replaced by vim.g config: %+v", cfg)
		}
	})
}

func TestM4Anim_OptionSetGUIFontHotUpdate(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	tester := ui.NewTester(shell.View, 800, 480)
	waitUntil(t, 5*time.Second, "initial frame", func() bool {
		tester.Frame()
		return shell.impl.session.Latest() != nil
	})
	if err := shell.impl.session.Nvim().Command("set guifont=Menlo:h15"); err != nil {
		t.Fatalf("set guifont: %v", err)
	}
	waitUntil(t, 5*time.Second, "option_set guifont hot update", func() bool {
		tester.Frame()
		shell.impl.mu.Lock()
		lastGUIFont := shell.impl.lastGUIFont
		shell.impl.mu.Unlock()
		return lastGUIFont == "Menlo:h15"
	})
}

func TestM4Anim_InstantSettlesActiveAnimationImmediately(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	tester := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		shell.View(c)
	}, 800, 480)
	waitUntil(t, 5*time.Second, "initial frame", func() bool {
		tester.Frame()
		return shell.impl.session.Latest() != nil
	})
	if err := shell.impl.session.Nvim().Command(`call setline(1, map(range(1, 50), '"instant_" . v:val'))`); err != nil {
		t.Fatalf("setline: %v", err)
	}
	if _, err := shell.impl.session.Nvim().Input("50G"); err != nil {
		t.Fatalf("Input 50G: %v", err)
	}
	for step := 0; step < 100; step++ {
		tester.Frame()
		tester.Advance(16 * time.Millisecond)
		shell.impl.mu.Lock()
		active := shell.impl.animator.Active()
		scrollOffset, _, scrollActive := shell.impl.animator.ScrollOffset(2)
		shell.impl.mu.Unlock()
		if !active && !scrollActive && scrollOffset == 0 {
			if tester.FramePending() {
				t.Fatal("Instant() frame left FramePending true")
			}
			return
		}
	}
	t.Fatal("Instant() did not settle all animation state")
}
