package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
)

func m2DoctorWaitUntil(timeout time.Duration, cond func() bool) bool {
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

func m2DoctorVimSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func TestM2DoctorSync_CollectDoctorTextAndJSON(t *testing.T) {
	workDir := t.TempDir()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	baseReq := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: workDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}

	ws := app.NewWorkspace(app.WorkspaceOptions{
		BaseRequest: baseReq,
		Clock:       clock,
		Clipboard:   &fakeClipboard{},
		Headless:    true,
	})
	if ws == nil {
		t.Fatal("NewWorkspace returned nil")
	}
	t.Cleanup(func() { _ = ws.Close() })

	if _, _, err := ws.NewWindow(context.Background(), baseReq); err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	squirrelYAML := []byte("app_options:\n  com.nvgui.test:\n    ascii_mode: true\n    vim_mode: true\n")
	opts := app.DoctorOptions{
		Launch:         baseReq,
		Source:         app.LaunchSourceCLI,
		Version:        "0.2.0-judge",
		BundleID:       "com.nvgui.test",
		ExecutablePath: "/Applications/nvgui.app/Contents/MacOS/nvgui",
		SquirrelConfig: squirrelYAML,
		Workspace:      ws,
		SocketPath:     "/tmp/nvgui-doctor-judge.sock",
	}

	report := app.CollectDoctor(context.Background(), opts)
	if report.Version != "0.2.0-judge" {
		t.Fatalf("DoctorReport.Version = %q, want 0.2.0-judge", report.Version)
	}
	if report.NvimPath == "" {
		t.Fatal("DoctorReport.NvimPath is empty")
	}
	if !strings.Contains(report.NvimVersion, "NVIM") {
		t.Fatalf("DoctorReport.NvimVersion = %q, want substring \"NVIM\"", report.NvimVersion)
	}
	if report.PATH == "" {
		t.Fatal("DoctorReport.PATH is empty")
	}
	if report.WorkingDir != workDir {
		t.Fatalf("DoctorReport.WorkingDir = %q, want %q", report.WorkingDir, workDir)
	}
	if report.LaunchSource != app.LaunchSourceCLI {
		t.Fatalf("DoctorReport.LaunchSource = %q, want %q", report.LaunchSource, app.LaunchSourceCLI)
	}
	if report.BundleID != "com.nvgui.test" {
		t.Fatalf("DoctorReport.BundleID = %q, want com.nvgui.test", report.BundleID)
	}
	if report.ExecutablePath != "/Applications/nvgui.app/Contents/MacOS/nvgui" {
		t.Fatalf("DoctorReport.ExecutablePath = %q, want /Applications/nvgui.app/Contents/MacOS/nvgui", report.ExecutablePath)
	}
	if report.SocketPath != "/tmp/nvgui-doctor-judge.sock" {
		t.Fatalf("DoctorReport.SocketPath = %q, want /tmp/nvgui-doctor-judge.sock", report.SocketPath)
	}
	if report.ActiveWindows != 1 {
		t.Fatalf("DoctorReport.ActiveWindows = %d, want 1", report.ActiveWindows)
	}
	if !report.Squirrel.Matched || report.Squirrel.BundleID != "com.nvgui.test" || report.Squirrel.CLIMismatch {
		t.Fatalf("DoctorReport.Squirrel = %+v, want Matched=true BundleID=com.nvgui.test CLIMismatch=false", report.Squirrel)
	}
	if report.Config.OpenIn != app.OpenInFront {
		t.Fatalf("DoctorReport.Config.OpenIn = %q, want %q", report.Config.OpenIn, app.OpenInFront)
	}
	if report.Config.CursorAnimationLength <= 0 {
		t.Fatalf("DoctorReport.Config.CursorAnimationLength = %v, want > 0", report.Config.CursorAnimationLength)
	}

	textOut := report.FormatText()
	if textOut == "" {
		t.Fatal("DoctorReport.FormatText() returned empty string")
	}
	for _, needle := range []string{"0.2.0-judge", report.NvimPath, workDir, "com.nvgui.test"} {
		if !strings.Contains(textOut, needle) {
			t.Fatalf("FormatText() missing %q:\n%s", needle, textOut)
		}
	}

	rawJSON, err := report.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON(): %v", err)
	}
	if !json.Valid(rawJSON) {
		t.Fatalf("FormatJSON() returned invalid JSON: %s", string(rawJSON))
	}
	var decoded app.DoctorReport
	if err := json.Unmarshal(rawJSON, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(FormatJSON): %v", err)
	}
	if decoded.Version != report.Version ||
		decoded.NvimPath != report.NvimPath ||
		decoded.NvimVersion != report.NvimVersion ||
		decoded.WorkingDir != report.WorkingDir ||
		decoded.LaunchSource != report.LaunchSource ||
		decoded.BundleID != report.BundleID ||
		decoded.ActiveWindows != report.ActiveWindows ||
		decoded.Squirrel.Matched != report.Squirrel.Matched ||
		decoded.Config != report.Config {
		t.Fatalf("JSON round-trip mismatch:\ngot:  %+v\nwant: %+v", decoded, report)
	}
}

func TestM2DoctorSync_BidirectionalHotUpdateAndMorePromptSafe(t *testing.T) {
	workDir := t.TempDir()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	shell, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: workDir,
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

	tester := ui.NewTester(shell.View, 800, 480)
	if !m2DoctorWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		img := tester.Image()
		return img != nil && !m4IsUniformImage(img.Pix)
	}) {
		t.Fatal("Shell never rendered initial boot frame")
	}

	snap0, err := shell.SyncNvimConfig(context.Background())
	if err != nil {
		t.Fatalf("initial SyncNvimConfig: %v", err)
	}
	if !snap0.CursorAnimation || !snap0.Ligatures || snap0.OpenIn != app.OpenInFront {
		t.Fatalf("initial NvimConfigSnapshot = %+v, want CursorAnimation=true Ligatures=true OpenIn=front", snap0)
	}

	// Verify Neovim side received g:nvgui = 1 and synced g:nvgui_* defaults.
	probe1 := filepath.Join(workDir, "probe_init.txt")
	tester.Key(0, ui.KeyEscape)
	tester.Type(":call writefile([string(get(g:,'nvgui',-1)), get(g:,'nvgui_cursor_animation',0)?'1':'0', get(g:,'nvgui_ligatures',0)?'1':'0', printf('%s', get(g:,'nvgui_open_in',''))], " + m2DoctorVimSingleQuote(probe1) + ")")
	tester.Key(0, ui.KeyEnter)
	if !m2DoctorWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		_, statErr := os.Stat(probe1)
		return statErr == nil
	}) {
		t.Fatal("timed out waiting for initial g:nvgui_* probe file")
	}
	raw1, err := os.ReadFile(probe1)
	if err != nil {
		t.Fatalf("read probe1: %v", err)
	}
	lines1 := strings.Split(strings.TrimSpace(string(raw1)), "\n")
	if len(lines1) < 4 || lines1[0] != "1" || lines1[1] != "1" || lines1[2] != "1" || lines1[3] != "front" {
		t.Fatalf("initial Neovim g:nvgui_* = %q, want [1 1 1 front]", lines1)
	}

	// Neovim -> nvgui hot update.
	tester.Key(0, ui.KeyEscape)
	tester.Type(":let g:nvgui_cursor_animation = 0 | let g:nvgui_ligatures = 0 | let g:nvgui_open_in = 'new'")
	tester.Key(0, ui.KeyEnter)

	var snap1 app.NvimConfigSnapshot
	if !m2DoctorWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		s, syncErr := shell.SyncNvimConfig(context.Background())
		if syncErr != nil {
			return false
		}
		snap1 = s
		return !s.CursorAnimation && !s.Ligatures && s.OpenIn == app.OpenInNew
	}) {
		t.Fatalf("Neovim->nvgui hot update did not apply; last snapshot = %+v", snap1)
	}
	if cfg := shell.AnimationConfig(); cfg.CursorEnabled || cfg.LigaturesEnabled {
		t.Fatalf("Shell.AnimationConfig() after Neovim hot update = %+v, want CursorEnabled=false LigaturesEnabled=false", cfg)
	}
	if got := shell.OpenInPolicy(); got != app.OpenInNew {
		t.Fatalf("Shell.OpenInPolicy() = %q, want %q", got, app.OpenInNew)
	}
	if got := shell.NvimConfig(); got.CursorAnimation || got.Ligatures || got.OpenIn != app.OpenInNew {
		t.Fatalf("Shell.NvimConfig() = %+v, want CursorAnimation=false Ligatures=false OpenIn=new", got)
	}

	// nvgui -> Neovim write-back sync.
	updatedCfg := shell.AnimationConfig()
	updatedCfg.CursorEnabled = true
	if err := shell.ApplyAnimationConfigAndSync(context.Background(), updatedCfg); err != nil {
		t.Fatalf("ApplyAnimationConfigAndSync: %v", err)
	}
	if !shell.AnimationConfig().CursorEnabled || !shell.NvimConfig().CursorAnimation {
		t.Fatalf("after ApplyAnimationConfigAndSync: anim=%+v snap=%+v, want cursor enabled", shell.AnimationConfig(), shell.NvimConfig())
	}

	probe2 := filepath.Join(workDir, "probe_after_writeback.txt")
	tester.Key(0, ui.KeyEscape)
	tester.Type(":call writefile([get(g:,'nvgui_cursor_animation',0)?'1':'0'], " + m2DoctorVimSingleQuote(probe2) + ")")
	tester.Key(0, ui.KeyEnter)
	if !m2DoctorWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		_, statErr := os.Stat(probe2)
		return statErr == nil
	}) {
		t.Fatal("timed out waiting for write-back g:nvgui_cursor_animation probe file")
	}
	raw2, err := os.ReadFile(probe2)
	if err != nil {
		t.Fatalf("read probe2: %v", err)
	}
	if strings.TrimSpace(string(raw2)) != "1" {
		t.Fatalf("Neovim g:nvgui_cursor_animation after ApplyAnimationConfigAndSync = %q, want \"1\"", strings.TrimSpace(string(raw2)))
	}

	// Steady-state unchanged sync must neither trigger Invalidate() nor leave FramePending() == true.
	if _, err := shell.SyncNvimConfig(context.Background()); err != nil {
		t.Fatalf("pre-steady-state SyncNvimConfig fence: %v", err)
	}
	tester.Frame()
	tester.Advance(1 * time.Second)
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("Shell did not settle to FramePending() == false before unchanged SyncNvimConfig")
	}
	if _, err := shell.SyncNvimConfig(context.Background()); err != nil {
		t.Fatalf("steady-state SyncNvimConfig: %v", err)
	}
	if tester.FramePending() {
		t.Fatal("unchanged SyncNvimConfig triggered spurious Invalidate() (FramePending() == true immediately)")
	}
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("steady-state Shell has FramePending() == true after unchanged SyncNvimConfig")
	}

	// Part 2: -- More -- blocking prompt must NOT hang SyncNvimConfig.
	moreDir := t.TempDir()
	moreInit := filepath.Join(moreDir, "init.lua")
	var initBuf strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&initBuf, "vim.api.nvim_err_writeln('more prompt blocking test line %02d: diagnostic warning output to trigger pager')\n", i)
	}
	if err := os.WriteFile(moreInit, []byte(initBuf.String()), 0o644); err != nil {
		t.Fatalf("write more init.lua: %v", err)
	}

	shell2, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-u", moreInit, "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: moreDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}, &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}, &fakeClipboard{})
	if err != nil {
		t.Fatalf("app.Start(-- More --): %v", err)
	}
	t.Cleanup(func() {
		_ = shell2.Close()
		if exited := shell2.SessionExited(); exited != nil {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-exited:
			case <-timer.C:
			}
		}
	})

	tester2 := ui.NewTester(shell2.View, 800, 480)
	if !m2DoctorWaitUntil(5*time.Second, func() bool {
		tester2.Frame()
		img := tester2.Image()
		return img != nil && !m4IsUniformImage(img.Pix)
	}) {
		t.Fatal("-- More -- shell never rendered initial frame")
	}

	syncCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := shell2.SyncNvimConfig(syncCtx); err != nil {
		t.Fatalf("SyncNvimConfig during -- More -- prompt returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 1500*time.Millisecond {
		t.Fatalf("SyncNvimConfig during -- More -- prompt took %v, want fast non-blocking return", elapsed)
	}
}
