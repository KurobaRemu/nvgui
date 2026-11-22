package app_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/connection"
	"nvgui/render"
	"nvgui/screen"
	"nvgui/session"
)

func TestM4App_EnvConfigAndGUIFont(t *testing.T) {
	def := render.DefaultAnimationConfig()
	if !def.CursorEnabled || !def.ScrollEnabled || !def.LigaturesEnabled {
		t.Fatalf("DefaultAnimationConfig() not initialized: %+v", def)
	}

	if got := app.AnimationConfigFromEnv(nil); got != def {
		t.Fatalf("AnimationConfigFromEnv(nil) = %+v, want default %+v", got, def)
	}
	if got := app.GUIFontFromEnv(nil); got != "" {
		t.Fatalf("GUIFontFromEnv(nil) = %q, want empty", got)
	}

	cases := []struct {
		name  string
		env   []string
		check func(t *testing.T, cfg render.AnimationConfig)
	}{
		{
			name: "disable cursor anim",
			env:  []string{"NVGUI_CURSOR_ANIM=0"},
			check: func(t *testing.T, cfg render.AnimationConfig) {
				if cfg.CursorEnabled || !cfg.ScrollEnabled || !cfg.LigaturesEnabled {
					t.Fatalf("got %+v, want CursorEnabled=false others default", cfg)
				}
			},
		},
		{
			name: "enable cmdline cursor anim",
			env:  []string{"NVGUI_CURSOR_ANIMATE_COMMAND_LINE=1"},
			check: func(t *testing.T, cfg render.AnimationConfig) {
				if !cfg.CursorAnimateCommandLine {
					t.Fatalf("got %+v, want CursorAnimateCommandLine=true", cfg)
				}
			},
		},
		{
			name: "disable scroll anim",
			env:  []string{"NVGUI_SCROLL_ANIM=false"},
			check: func(t *testing.T, cfg render.AnimationConfig) {
				if cfg.ScrollEnabled || !cfg.CursorEnabled {
					t.Fatalf("got %+v, want ScrollEnabled=false CursorEnabled=true", cfg)
				}
			},
		},
		{
			name: "disable ligatures",
			env:  []string{"NVGUI_LIGATURES=0"},
			check: func(t *testing.T, cfg render.AnimationConfig) {
				if cfg.LigaturesEnabled || !cfg.CursorEnabled || !cfg.ScrollEnabled {
					t.Fatalf("got %+v, want LigaturesEnabled=false anims true", cfg)
				}
			},
		},
		{
			name: "no animations master switch",
			env:  []string{"NVGUI_NO_ANIMATIONS=1"},
			check: func(t *testing.T, cfg render.AnimationConfig) {
				if cfg.CursorEnabled || cfg.ScrollEnabled {
					t.Fatalf("NVGUI_NO_ANIMATIONS=1 got %+v, want CursorEnabled=false ScrollEnabled=false", cfg)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := app.AnimationConfigFromEnv(tc.env)
			tc.check(t, cfg)
		})
	}

	if got := app.GUIFontFromEnv([]string{"PATH=/usr/bin", "NVGUI_GUIFONT=JetBrains Mono:h14"}); got != "JetBrains Mono:h14" {
		t.Fatalf("GUIFontFromEnv = %q, want %q", got, "JetBrains Mono:h14")
	}
}

func TestM4App_SessionLatestSnapshotAndScrollDelta(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	sess, err := session.Start(context.Background(), connection.Options{
		Args:   []string{"--clean", "-n"},
		Width:  80,
		Height: 24,
	}, clock)
	if err != nil {
		t.Fatalf("session.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Close()
		select {
		case <-sess.Exited():
		case <-time.After(5 * time.Second):
		}
	})

	// Wait for initial nvim flush.
	bootDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(bootDeadline) {
		if sess.Latest() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sess.Latest() == nil {
		t.Fatal("nvim never produced initial screen.State")
	}

	state0, meta0 := sess.LatestSnapshot()
	if state0 == nil || meta0 == nil {
		t.Fatalf("LatestSnapshot() = (%v, %v), want both non-nil", state0, meta0)
	}
	if state0.Seq == 0 || state0.Seq != meta0.Seq {
		t.Fatalf("LatestSnapshot() seq mismatch: state.Seq=%d meta.Seq=%d", state0.Seq, meta0.Seq)
	}
	if lm := sess.LatestMeta(); lm == nil || lm.Seq < meta0.Seq {
		t.Fatalf("LatestMeta() = %+v, want seq >= %d", lm, meta0.Seq)
	}

	// Fill 60 lines, jump to top, then scroll down so Neovim emits non-zero win_viewport scroll_delta.
	if err := sess.Nvim().Command(`call setline(1, map(range(1, 60), '"m4_scroll_line_" . v:val'))`); err != nil {
		t.Fatalf("setline: %v", err)
	}
	if _, err := sess.Nvim().Input("gg"); err != nil {
		t.Fatalf("Input gg: %v", err)
	}
	prepDeadline := time.Now().Add(5 * time.Second)
	var beforeScrollSeq uint64
	for time.Now().Before(prepDeadline) {
		st, _ := sess.LatestSnapshot()
		if st != nil && m4StateHasText(st, "m4_scroll_line_1") {
			beforeScrollSeq = st.Seq
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if beforeScrollSeq == 0 {
		t.Fatal("buffer lines never appeared in screen.State")
	}

	if _, err := sess.Nvim().Input("<C-d>"); err != nil {
		t.Fatalf("Input <C-d>: %v", err)
	}

	foundScrollDelta := false
	scrollDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(scrollDeadline) {
		st, meta := sess.LatestSnapshot()
		if st != nil && meta != nil {
			if st.Seq != meta.Seq {
				t.Fatalf("LatestSnapshot() returned mismatched seqs: state=%d meta=%d", st.Seq, meta.Seq)
			}
			if st.Seq > beforeScrollSeq {
				for _, vp := range meta.Viewports {
					if vp.HasScrollDelta && vp.ScrollDelta != 0 {
						foundScrollDelta = true
						break
					}
				}
				if foundScrollDelta {
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !foundScrollDelta {
		t.Fatal("LatestSnapshot() never captured non-zero win_viewport ScrollDelta after <C-d>")
	}
}

func m4StateHasText(state *screen.State, needle string) bool {
	if state == nil {
		return false
	}
	for _, g := range state.Grids {
		for _, row := range g.Cells {
			buf := make([]byte, 0, len(row))
			for _, cell := range row {
				buf = append(buf, cell.Text...)
			}
			if len(buf) >= len(needle) {
				s := string(buf)
				for i := 0; i+len(needle) <= len(s); i++ {
					if s[i:i+len(needle)] == needle {
						return true
					}
				}
			}
		}
	}
	return false
}

func m4FilteredEnv() []string {
	raw := os.Environ()
	filtered := make([]string, 0, len(raw))
	for _, entry := range raw {
		if strings.HasPrefix(entry, "NVGUI_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func m4IsUniformImage(pix []byte) bool {
	if len(pix) < 8 {
		return true
	}
	r0, g0, b0, a0 := pix[0], pix[1], pix[2], pix[3]
	for i := 4; i+3 < len(pix); i += 4 {
		if pix[i] != r0 || pix[i+1] != g0 || pix[i+2] != b0 || pix[i+3] != a0 {
			return false
		}
	}
	return true
}

func TestM4App_ShellTesterSteadyStateAdvanceAndHotToggle(t *testing.T) {
	clip := &fakeClipboard{}
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	req := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	shell, err := app.Start(context.Background(), req, clock, clip)
	if err != nil {
		t.Fatalf("app.Start(headless): %v", err)
	}
	if shell == nil {
		t.Fatal("app.Start returned nil shell")
	}
	t.Cleanup(func() {
		_ = shell.Close()
		if exited := shell.SessionExited(); exited != nil {
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
			}
		}
	})

	cfg := shell.AnimationConfig()
	if !cfg.CursorEnabled || !cfg.ScrollEnabled {
		t.Fatalf("initial Shell.AnimationConfig() = %+v, want CursorEnabled=true ScrollEnabled=true", cfg)
	}

	tester := ui.NewTester(shell.View, 800, 480)

	// Wait for the initial non-uniform boot frame to render and settle, then snapshot bootPix.
	var bootPix []byte
	bootDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(bootDeadline) {
		tester.Frame()
		tester.Advance(16 * time.Millisecond)
		if img := tester.Image(); img != nil && !m4IsUniformImage(img.Pix) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tester.Advance(1 * time.Second)
	tester.Frame()
	if img := tester.Image(); img == nil || m4IsUniformImage(img.Pix) {
		t.Fatal("Shell never rendered initial non-uniform boot frame")
	} else {
		bootPix = append([]byte(nil), img.Pix...)
	}

	// Populate 50 lines via nvim and poll until buffer pixels (top half, excluding cmdline) differ from bootPix, then settle.
	tester.Type(":call setline(1, map(range(1, 50), '\"m4_shell_\" . v:val'))")
	tester.Key(0, ui.KeyEnter)
	renderedLines := false
	topBytes := len(bootPix) / 2
	setlineDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(setlineDeadline) {
		tester.Frame()
		if img := tester.Image(); img != nil && len(img.Pix) >= topBytes && !bytes.Equal(img.Pix[:topBytes], bootPix[:topBytes]) {
			renderedLines = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !renderedLines {
		t.Fatal("setline(1..50) pixels never differed from initial boot frame")
	}
	tester.Advance(1 * time.Second)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("idle steady-state Shell has FramePending() == true (idle spin)")
	}

	// Trigger a large jump/scroll inside nvim (50G) and wait for nvim's redraw to initiate animation.
	tester.Type("50G")
	animStarted := false
	startDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(startDeadline) {
		tester.Frame()
		if tester.FramePending() {
			animStarted = true
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !animStarted {
		t.Fatal("jumping to line 50 (50G) did not trigger pending animation frame in Shell")
	}

	// Deterministically advance virtual clock until animation converges (zero time.Sleep).
	steps := 0
	for steps < 200 && tester.FramePending() {
		tester.Advance(16 * time.Millisecond)
		steps++
	}
	if steps == 0 {
		t.Fatal("expected at least 1 deterministic Advance step during animation")
	}
	if tester.FramePending() {
		t.Fatalf("Shell animation did not settle after %d x 16ms virtual steps (FramePending still true)", steps)
	}

	// Hot-toggle SetAnimationConfig(DisabledAnimationConfig()): triggers stop immediately.
	tester.Type("1G")
	hotDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(hotDeadline) {
		tester.Frame()
		if tester.FramePending() {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	shell.SetAnimationConfig(render.DisabledAnimationConfig())
	if got := shell.AnimationConfig(); got != render.DisabledAnimationConfig() {
		t.Fatalf("Shell.AnimationConfig() after SetAnimationConfig(Disabled) = %+v, want disabled", got)
	}
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("Shell still has FramePending() == true after SetAnimationConfig(DisabledAnimationConfig())")
	}
}
