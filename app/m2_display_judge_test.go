package app_test

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/render"
)

func m2DisplayWaitUntil(timeout time.Duration, cond func() bool) bool {
	if cond() {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			return cond()
		case <-ticker.C:
			if cond() {
				return true
			}
		}
	}
}

func m2DisplayStartShell(t *testing.T) (*app.Shell, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	shell, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}, clock, &fakeClipboard{})
	if err != nil {
		t.Fatalf("app.Start(headless): %v", err)
	}
	t.Cleanup(func() {
		_ = shell.Close()
		if exited := shell.SessionExited(); exited != nil {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-exited:
			case <-timer.C:
			}
		}
	})
	return shell, clock
}

func TestM2Display_SetScaleRebuildIdempotentAndPainterObserver(t *testing.T) {
	shell, _ := m2DisplayStartShell(t)

	customCfg := render.DefaultAnimationConfig()
	customCfg.CursorEnabled = false
	customCfg.LigaturesEnabled = false
	shell.SetAnimationConfig(customCfg)

	// Attach Tester while scale is 1.0 so initial frame matches initial scale (zero spurious rebuild).
	tester := ui.NewTester(shell.View, 800, 480)
	tester.Frame()

	if got := shell.Scale(); math.Abs(float64(got-1.0)) > 1e-4 {
		t.Fatalf("initial Shell.Scale() = %v, want 1", got)
	}

	for _, bad := range []float32{0, -1, float32(math.NaN()), float32(math.Inf(1))} {
		if err := shell.SetScale(bad); err == nil {
			t.Fatalf("SetScale(%v) returned nil error, want error", bad)
		}
	}
	if m := shell.DisplayMetrics(); m.RebuildCount != 0 {
		t.Fatalf("RebuildCount after rejected scales = %d, want 0", m.RebuildCount)
	}

	if err := shell.SetScale(2.0); err != nil {
		t.Fatalf("SetScale(2.0): %v", err)
	}
	if got := shell.Scale(); math.Abs(float64(got-2.0)) > 1e-4 {
		t.Fatalf("Scale() after SetScale(2.0) = %v, want 2.0", got)
	}
	m1 := shell.DisplayMetrics()
	if math.Abs(float64(m1.Scale-2.0)) > 1e-4 || m1.RebuildCount != 1 {
		t.Fatalf("DisplayMetrics after SetScale(2.0) = %+v, want Scale=2.0 RebuildCount=1", m1)
	}
	if m1.Cell.Width <= 0 || m1.Cell.Height <= 0 {
		t.Fatalf("DisplayMetrics.Cell after SetScale(2.0) = %+v, want positive cell size", m1.Cell)
	}
	if gotCfg := shell.AnimationConfig(); gotCfg.CursorEnabled || gotCfg.LigaturesEnabled {
		t.Fatalf("AnimationConfig after SetScale(2.0) = %+v, want custom CursorEnabled=false LigaturesEnabled=false preserved", gotCfg)
	}

	// Repeating the same scale must be idempotent (zero rebuild).
	if err := shell.SetScale(2.0); err != nil {
		t.Fatalf("repeat SetScale(2.0): %v", err)
	}
	if m2 := shell.DisplayMetrics(); m2.RebuildCount != 1 {
		t.Fatalf("RebuildCount after idempotent SetScale(2.0) = %d, want 1", m2.RebuildCount)
	}

	// Painter.Scale() observation via ui.Tester.SetScale(1.5).
	tester.SetScale(1.5)
	tester.Frame()
	if got := shell.Scale(); math.Abs(float64(got-1.5)) > 1e-4 {
		t.Fatalf("Scale() after tester.SetScale(1.5) = %v, want 1.5", got)
	}
	m3 := shell.DisplayMetrics()
	if m3.RebuildCount != 2 {
		t.Fatalf("RebuildCount after tester.SetScale(1.5) = %d, want 2", m3.RebuildCount)
	}
	if gotCfg := shell.AnimationConfig(); gotCfg.CursorEnabled || gotCfg.LigaturesEnabled {
		t.Fatalf("AnimationConfig after Painter scale rebuild = %+v, want custom settings preserved", gotCfg)
	}
}

func TestM2Display_FullscreenSpaceRecoveryAndNoBlank(t *testing.T) {
	shell, clock := m2DisplayStartShell(t)

	tester := ui.NewTester(shell.View, 800, 480)
	if !m2DisplayWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		img := tester.Image()
		return img != nil && !m4IsUniformImage(img.Pix)
	}) {
		t.Fatal("Shell never produced initial non-uniform boot frame")
	}

	tester.Type(":call setline(1, map(range(1, 20), '\"m2_display_visible_row_\" . v:val'))")
	tester.Key(0, ui.KeyEnter)
	if !m2DisplayWaitUntil(5*time.Second, func() bool {
		clock.Advance(10 * time.Millisecond)
		tester.Frame()
		dir := filepath.Join(t.TempDir(), "bundle")
		if err := shell.ExportDiag(dir); err != nil {
			return false
		}
		states, err := diag.Replay(dir)
		if err != nil || len(states) == 0 {
			return false
		}
		return m4StateHasText(states[len(states)-1], "m2_display_visible_row_1")
	}) {
		t.Fatal("visible rows never appeared in Shell screen state")
	}
	tester.Advance(1 * time.Second)
	tester.Frame()

	assertNonBlank := func(stage string) {
		t.Helper()
		img := tester.Image()
		if img == nil || m4IsUniformImage(img.Pix) {
			t.Fatalf("rendered image at stage %q is nil or uniform blank/black", stage)
		}
	}
	assertNonBlank("initial")

	prevRepaints := shell.DisplayMetrics().RepaintCount

	shell.NotifyDisplayRecovery(app.DisplayRecoveryEnterFullScreen)
	tester.Frame()
	m1 := shell.DisplayMetrics()
	if !m1.FullScreen || m1.RepaintCount <= prevRepaints {
		t.Fatalf("after EnterFullScreen: metrics=%+v (prevRepaints=%d), want FullScreen=true and RepaintCount > prev", m1, prevRepaints)
	}
	assertNonBlank("enter-fullscreen")
	prevRepaints = m1.RepaintCount

	shell.NotifyDisplayRecovery(app.DisplayRecoverySpaceReturn)
	tester.Frame()
	m2 := shell.DisplayMetrics()
	if !m2.FullScreen || m2.RepaintCount <= prevRepaints {
		t.Fatalf("after SpaceReturn: metrics=%+v (prevRepaints=%d), want FullScreen=true and RepaintCount > prev", m2, prevRepaints)
	}
	assertNonBlank("space-return")
	prevRepaints = m2.RepaintCount

	shell.NotifyDisplayRecovery(app.DisplayRecoveryLeaveFullScreen)
	tester.Frame()
	m3 := shell.DisplayMetrics()
	if m3.FullScreen || m3.RepaintCount <= prevRepaints {
		t.Fatalf("after LeaveFullScreen: metrics=%+v (prevRepaints=%d), want FullScreen=false and RepaintCount > prev", m3, prevRepaints)
	}
	assertNonBlank("leave-fullscreen")
	prevRepaints = m3.RepaintCount

	// WindowHidden true -> false transition must trigger automatic SpaceReturn recovery.
	tester.SetWindowHidden(true)
	tester.Frame()
	tester.SetWindowHidden(false)
	tester.Frame()
	m4 := shell.DisplayMetrics()
	if m4.RepaintCount <= prevRepaints {
		t.Fatalf("after SetWindowHidden(true->false): RepaintCount=%d <= prev=%d, want increment", m4.RepaintCount, prevRepaints)
	}
	assertNonBlank("window-unhidden")

	// Steady state after recovery must not spin.
	tester.Advance(1 * time.Second)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("steady-state Shell has FramePending() == true after display recovery")
	}
}
