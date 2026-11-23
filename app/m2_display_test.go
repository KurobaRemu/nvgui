package app_test

import (
	"context"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/drawlist"
	"nvgui/render"
	"nvgui/text"
)

func m2DisplayInsertRows(t *testing.T, shell *app.Shell, clock *fakeClock, tester *ui.Tester, token string) {
	t.Helper()
	tester.Type(`:call setline(1, map(range(1, 20), '"m2_display_lru_row_" . v:val'))`)
	tester.Key(0, ui.KeyEnter)
	if !m2DisplayWaitUntil(5*time.Second, func() bool {
		clock.Advance(10 * time.Millisecond)
		tester.Frame()
		dir := filepath.Join(t.TempDir(), "bundle")
		if err := shell.ExportDiag(dir); err != nil {
			return false
		}
		states, err := diag.Replay(dir)
		return err == nil && len(states) > 0 && m4StateHasText(states[len(states)-1], token)
	}) {
		t.Fatalf("visible rows %q never appeared", token)
	}
}

func m2DisplayAssertNonBlank(t *testing.T, tester *ui.Tester, stage string) {
	t.Helper()
	img := tester.Image()
	if img == nil || m4IsUniformImage(img.Pix) {
		t.Fatalf("rendered image at %q is nil or uniform blank", stage)
	}
}

func m2DisplayFlushMetadataLRU(t *testing.T) {
	t.Helper()
	resolver, err := text.NewResolver(text.Config{Scale: 1})
	if err != nil {
		t.Fatalf("create noise resolver: %v", err)
	}
	planner := render.NewPlanner(resolver)
	for frame := 0; frame < 76; frame++ {
		noise := &drawlist.List{
			Version: 1,
			Seq:     uint64(10_000 + frame),
			Width:   10,
			Height:  2,
			Layers: []drawlist.Layer{{
				Grid:   1,
				Width:  10,
				Height: 2,
				Clip:   drawlist.Rect{X: 0, Y: 0, W: 10, H: 2},
				Items: []drawlist.Item{
					{Kind: "fill", Row: 0, Col: 0, Width: 10, Height: 2, BG: int64(0x101010 + frame)},
					{Kind: "text", Row: 0, Col: frame % 8, Width: 1, Height: 1, Text: "Z", FG: 0xffffff, BG: 0x101010},
				},
			}},
		}
		if _, err := planner.Plan(noise); err != nil {
			t.Fatalf("flush metadata LRU frame %d: %v", frame, err)
		}
	}
}

func TestM2Display_InvalidScaleRejectedAndClosedShellSafe(t *testing.T) {
	shell, _ := m2DisplayStartShell(t)
	for _, bad := range []float32{0, -1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if err := shell.SetScale(bad); err == nil {
			t.Fatalf("SetScale(%v) returned nil, want explicit error", bad)
		}
	}
	if got := shell.DisplayMetrics().RebuildCount; got != 0 {
		t.Fatalf("RebuildCount after rejected scales = %d, want 0", got)
	}

	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	closed, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}, clock, &fakeClipboard{})
	if err != nil {
		t.Fatalf("start closed shell: %v", err)
	}
	if err := closed.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := closed.SetScale(2); err == nil {
		t.Fatal("SetScale after Close returned nil, want errShellClosed")
	}
	if got := closed.Scale(); got != 1 {
		t.Fatalf("closed Scale() = %v, want 1", got)
	}
	metrics := closed.DisplayMetrics()
	if metrics.Scale != 1 || metrics.RebuildCount != 0 || metrics.RepaintCount != 0 {
		t.Fatalf("closed DisplayMetrics = %+v, want Scale=1 and zero counters", metrics)
	}
	closed.NotifyDisplayRecovery(app.DisplayRecoveryEnterFullScreen)
	if got := closed.DisplayMetrics().RepaintCount; got != 0 {
		t.Fatalf("NotifyDisplayRecovery after Close incremented RepaintCount to %d", got)
	}
}

func TestM2Display_GUIFontHotUpdateKeepsCurrentScale(t *testing.T) {
	shell, clock := m2DisplayStartShell(t)
	tester := ui.NewTester(shell.View, 800, 480)
	tester.SetScale(2)
	tester.Frame()
	if got := shell.Scale(); math.Abs(float64(got-2)) > 1e-4 {
		t.Fatalf("Scale after 2x painter observation = %v, want 2", got)
	}
	if err := shell.SetScale(2); err != nil {
		t.Fatalf("SetScale(2): %v", err)
	}
	before := shell.DisplayMetrics()
	if before.RebuildCount != 1 {
		t.Fatalf("RebuildCount on 2x tester = %d, want 1", before.RebuildCount)
	}
	tester.Type(":set guifont=Menlo:h15")
	tester.Key(0, ui.KeyEnter)
	wantResolver, err := text.NewResolver(text.Config{GUIFont: "Menlo:h15", Scale: 2})
	if err != nil {
		t.Fatalf("create expected 2x guifont resolver: %v", err)
	}
	wantCell := wantResolver.Cell()
	if !m2DisplayWaitUntil(5*time.Second, func() bool {
		clock.Advance(10 * time.Millisecond)
		tester.Frame()
		got := shell.DisplayMetrics()
		return got.Cell == wantCell && got.Cell != before.Cell
	}) {
		t.Fatal("guifont hot update did not change the font cell")
	}
	for frame := 0; frame < 4; frame++ {
		tester.Frame()
	}
	after := shell.DisplayMetrics()
	if math.Abs(float64(after.Scale-2)) > 1e-4 {
		t.Fatalf("Scale after guifont hot update = %v, want 2", after.Scale)
	}
	if after.Cell != wantCell {
		t.Fatalf("Cell after guifont hot update = %+v, want 2x Menlo cell %+v", after.Cell, wantCell)
	}
	if math.Abs(float64(before.Scale-2)) > 1e-4 {
		t.Fatalf("Scale before guifont hot update = %v, want 2", before.Scale)
	}
	if after.RebuildCount < 1 {
		t.Fatalf("RebuildCount after guifont hot update = %d, want >= 1", after.RebuildCount)
	}
}

func TestM2Display_ConcurrentSetScaleViewCloseRace(t *testing.T) {
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
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tester := ui.NewTester(shell.View, 800, 480)
			for step := 0; step < 12; step++ {
				scale := []float32{1, 1.25, 1.5, 2}[step%4]
				_ = shell.SetScale(scale)
				tester.SetScale(scale)
				tester.Frame()
				_ = shell.DisplayMetrics()
				shell.NotifyDisplayRecovery(app.DisplayRecoveryGeometryJump)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = shell.Close()
	}()
	wg.Wait()
	if err := shell.Close(); err != nil {
		t.Fatalf("idempotent Close: %v", err)
	}
}

func TestM2Display_MetadataLRUFlushThenRecoveryRepaints(t *testing.T) {
	shell, clock := m2DisplayStartShell(t)
	customCfg := render.DefaultAnimationConfig()
	customCfg.CursorEnabled = false
	customCfg.ScrollEnabled = false
	shell.SetAnimationConfig(customCfg)
	tester := ui.NewTester(shell.View, 800, 480)
	m2DisplayInsertRows(t, shell, clock, tester, "m2_display_lru_row_1")
	tester.Advance(time.Second)
	tester.Frame()
	m2DisplayAssertNonBlank(t, tester, "before-LRU-flush")

	m2DisplayFlushMetadataLRU(t)
	before := shell.DisplayMetrics()
	shell.NotifyDisplayRecovery(app.DisplayRecoverySpaceReturn)
	tester.Frame()
	after := shell.DisplayMetrics()
	if after.RepaintCount <= before.RepaintCount {
		t.Fatalf("display recovery RepaintCount = %d, want > %d", after.RepaintCount, before.RepaintCount)
	}
	m2DisplayAssertNonBlank(t, tester, "after-LRU-flush-recovery")
	tester.Advance(time.Second)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("display recovery left FramePending() == true")
	}
}
