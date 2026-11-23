package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/render"
)

func m2DoctorSyncTempInit(t *testing.T, source string) string {
	t.Helper()
	initPath := filepath.Join(t.TempDir(), "init.lua")
	if err := os.WriteFile(initPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write init.lua: %v", err)
	}
	return initPath
}

func m2DoctorSyncStartWorkspace(t *testing.T, args []string, env []string) (*Workspace, *Shell) {
	t.Helper()
	base := LaunchRequest{
		Args:       args,
		Env:        env,
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	workspace := NewWorkspace(WorkspaceOptions{BaseRequest: base, Headless: true, Clipboard: &testClipboard{}})
	t.Cleanup(func() { _ = workspace.Close() })
	_, shell, err := workspace.NewWindow(context.Background(), base)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	return workspace, shell
}

func m2DoctorSyncStartPromptShell(t *testing.T, lines int) (*Shell, *ui.Tester) {
	t.Helper()
	var source strings.Builder
	for index := 1; index <= lines; index++ {
		fmt.Fprintf(&source, "vim.api.nvim_err_writeln('hot sync blocking prompt line %02d: waiting for pager input')\n", index)
	}
	initPath := m2DoctorSyncTempInit(t, source.String())
	workingDir := filepath.Dir(initPath)
	shell, err := Start(context.Background(), LaunchRequest{
		Args:       []string{"--clean", "-u", initPath, "-n"},
		Env:        m4AnimFilteredEnv(),
		WorkingDir: workingDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}, nil, &testClipboard{})
	if err != nil {
		t.Fatalf("Start(headless more prompt): %v", err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	tester := ui.NewTester(shell.View, 800, 480)
	waitUntil(t, 5*time.Second, "rendered blocking prompt", func() bool {
		tester.Frame()
		return tester.Image() != nil
	})
	waitUntil(t, 5*time.Second, "blocking mode", func() bool {
		mode, err := shell.impl.session.Nvim().Mode()
		return err == nil && mode != nil && mode.Blocking
	})
	return shell, tester
}

func TestM2DoctorSync_CollectDoctorMissingNvimAndCLIWarning(t *testing.T) {
	workDir := t.TempDir()
	env := []string{"PATH=" + t.TempDir(), "HOME=" + t.TempDir(), "NVGUI_BUNDLE_ID=com.nvgui.cli"}
	report := CollectDoctor(context.Background(), DoctorOptions{
		Launch:         LaunchRequest{Env: env, WorkingDir: workDir},
		Source:         LaunchSourceCLI,
		BundleID:       "com.nvgui.cli",
		ExecutablePath: filepath.Join(workDir, "nvgui"),
		SquirrelConfig: []byte("app_options:\n  com.nvgui.app:\n    ascii_mode: true\n"),
	})
	if report.NvimPath != "" || report.NvimVersion != "" {
		t.Fatalf("missing nvim report = %+v, want empty path and version", report)
	}
	if !report.Squirrel.CLIMismatch || report.Squirrel.Warning == "" {
		t.Fatalf("CLI Squirrel status = %+v, want mismatch warning", report.Squirrel)
	}
	warningText := strings.Join(report.Warnings, "\n")
	if !strings.Contains(warningText, "nvim not found in PATH") {
		t.Fatalf("missing nvim warning absent from warnings %q", report.Warnings)
	}
	if !strings.Contains(warningText, report.Squirrel.Warning) {
		t.Fatalf("Squirrel CLI mismatch warning absent from warnings %q", report.Warnings)
	}
	text := report.FormatText()
	for _, needle := range []string{"nvgui:", "nvim:", "PATH:", "LaunchSource:", "BundleID:", "ActiveWindows:", "Squirrel:", "Config:", "Warnings:"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("FormatText missing %q:\n%s", needle, text)
		}
	}
	raw, err := report.FormatJSON()
	if err != nil || !json.Valid(raw) {
		t.Fatalf("FormatJSON() = (%q, %v), want valid JSON", raw, err)
	}
}

func TestM2DoctorSync_CollectDoctorProbeAndWorkspaceWindows(t *testing.T) {
	binDir := t.TempDir()
	script := filepath.Join(binDir, "nvim")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'NVIM v0.12.6-test\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	workDir := t.TempDir()
	base := LaunchRequest{Args: []string{"--clean", "-n"}, Env: []string{"PATH=/usr/bin:/bin", "HOME=" + workDir}, WorkingDir: workDir, Width: 80, Height: 24, Headless: true}
	workspace := NewWorkspace(WorkspaceOptions{BaseRequest: base, Headless: true, Clipboard: &testClipboard{}})
	t.Cleanup(func() { _ = workspace.Close() })
	for range 2 {
		if _, _, err := workspace.NewWindow(context.Background(), base); err != nil {
			t.Fatalf("NewWindow: %v", err)
		}
	}
	_, front := workspace.Front()
	if front == nil {
		t.Fatal("Workspace.Front() returned nil")
	}
	if _, err := front.SyncNvimConfig(context.Background()); err != nil {
		t.Fatalf("warm up front window sync: %v", err)
	}
	report := CollectDoctor(context.Background(), DoctorOptions{
		Launch:         base,
		Source:         LaunchSourceDock,
		Version:        "0.2.0-test",
		BundleID:       "com.nvgui.test",
		ExecutablePath: "/Applications/nvgui.app/Contents/MacOS/nvgui",
		SquirrelConfig: []byte("app_options:\n  com.nvgui.test:\n    ascii_mode: true\n"),
		Workspace:      workspace,
		ProbeLoginPATH: true,
		PathProbe: PathProbeOptions{
			NoCache: true,
			Force:   true,
			Probe: func(context.Context, string, []string) (string, error) {
				calls.Add(1)
				return binDir, nil
			},
		},
	})
	if !report.PathProbed || calls.Load() != 1 {
		t.Fatalf("login PATH probe = (probed=%v, calls=%d), want true/1", report.PathProbed, calls.Load())
	}
	if report.NvimPath != script || !strings.Contains(report.NvimVersion, "NVIM") {
		t.Fatalf("nvim discovery = (%q, %q), want %q and version", report.NvimPath, report.NvimVersion, script)
	}
	if report.ActiveWindows != 2 || report.Config.OpenIn != OpenInFront {
		t.Fatalf("workspace report = (windows=%d, config=%+v), want 2/front", report.ActiveWindows, report.Config)
	}
	if !report.Squirrel.Matched || report.Squirrel.CLIMismatch || len(report.Warnings) != 0 {
		t.Fatalf("Squirrel/warnings = (%+v, %v), want matched clean app bundle", report.Squirrel, report.Warnings)
	}
	raw, err := report.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON(): %v", err)
	}
	var decoded DoctorReport
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Config != report.Config || decoded.ActiveWindows != report.ActiveWindows {
		t.Fatalf("JSON round-trip = (%+v, %v), want %+v", decoded, err, report)
	}
}

func TestM2DoctorSync_HotUpdateInvalidValuesAndSteadyState(t *testing.T) {
	shell := m4AnimStart(t, nil, m4AnimFilteredEnv())
	initial, err := shell.SyncNvimConfig(context.Background())
	if err != nil {
		t.Fatalf("initial SyncNvimConfig: %v", err)
	}
	if !initial.CursorAnimation || !initial.Ligatures || initial.OpenIn != OpenInFront {
		t.Fatalf("initial snapshot = %+v, want defaults", initial)
	}
	before := shell.NvimConfig()
	invalid := []string{
		"let g:nvgui_cursor_animation_length = -1",
		"let g:nvgui_scroll_animation_length = 0",
		"let g:nvgui_open_in = 'sideways'",
		"let g:nvgui_cursor_animation = v:null",
	}
	for _, command := range invalid {
		if err := shell.impl.session.Nvim().Command(command); err != nil {
			t.Fatalf("Command(%s): %v", command, err)
		}
	}
	after, err := shell.SyncNvimConfig(context.Background())
	if err != nil {
		t.Fatalf("SyncNvimConfig after invalid values: %v", err)
	}
	if after != before {
		t.Fatalf("invalid values changed config: got %+v, want %+v", after, before)
	}
	impl := shell.impl
	impl.mu.Lock()
	dirty := impl.configDirty
	impl.mu.Unlock()
	if dirty {
		t.Fatal("unchanged SyncNvimConfig marked configDirty=true")
	}

	valid := "let g:nvgui_cursor_animation = 0 | let g:nvgui_scroll_animation_length = 0.125 | let g:nvgui_open_in = 'new'"
	if err := shell.impl.session.Nvim().Command(valid); err != nil {
		t.Fatalf("Command(valid hot update): %v", err)
	}
	updated, err := shell.SyncNvimConfig(context.Background())
	if err != nil {
		t.Fatalf("SyncNvimConfig after valid values: %v", err)
	}
	if updated.CursorAnimation || updated.ScrollAnimationLength != 0.125 || updated.OpenIn != OpenInNew {
		t.Fatalf("valid hot update snapshot = %+v, want cursor off/scroll 0.125/new", updated)
	}
	if shell.AnimationConfig().CursorEnabled || shell.OpenInPolicy() != OpenInNew {
		t.Fatalf("valid hot update not applied: cfg=%+v policy=%q", shell.AnimationConfig(), shell.OpenInPolicy())
	}
	workspace := NewWorkspace(WorkspaceOptions{BaseRequest: LaunchRequest{}, Headless: true})
	t.Cleanup(func() { _ = workspace.Close() })
	shell.impl.mu.Lock()
	shell.impl.workspace = workspace
	shell.impl.mu.Unlock()
	shell.SetOpenInPolicy(OpenInNew)
	if shell.OpenInPolicy() != OpenInNew || workspace.OpenInPolicy() != OpenInNew {
		t.Fatalf("SetOpenInPolicy(OpenInNew) did not update shell/workspace: %q/%q", shell.OpenInPolicy(), workspace.OpenInPolicy())
	}
	shell.SetOpenInPolicy(OpenInFront)
	if shell.OpenInPolicy() != OpenInFront || workspace.OpenInPolicy() != OpenInFront {
		t.Fatalf("SetOpenInPolicy(OpenInFront) did not update shell/workspace: %q/%q", shell.OpenInPolicy(), workspace.OpenInPolicy())
	}
	cfg := render.DefaultAnimationConfig()
	cfg.LigaturesEnabled = false
	if err := shell.ApplyAnimationConfigAndSync(context.Background(), cfg); err != nil {
		t.Fatalf("ApplyAnimationConfigAndSync: %v", err)
	}
	if shell.AnimationConfig().LigaturesEnabled || shell.NvimConfig().Ligatures {
		t.Fatalf("Go->Neovim sync did not apply config: %+v / %+v", shell.AnimationConfig(), shell.NvimConfig())
	}
	var marker int64
	if err := shell.impl.session.Nvim().Eval("get(g:, 'nvgui', -1)", &marker); err != nil || marker != 1 {
		t.Fatalf("g:nvgui = (%d, %v), want 1", marker, err)
	}
	var ligatures int64
	if err := shell.impl.session.Nvim().Eval("get(g:, 'nvgui_ligatures', -1) ? 1 : 0", &ligatures); err != nil || ligatures != 0 {
		t.Fatalf("g:nvgui_ligatures = (%d, %v), want 0", ligatures, err)
	}
}

func TestM2DoctorSync_MorePromptSkipsThenResumesSync(t *testing.T) {
	shell, tester := m2DoctorSyncStartPromptShell(t, 40)
	blockingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	if _, err := shell.SyncNvimConfig(blockingCtx); err != nil {
		t.Fatalf("SyncNvimConfig during blocking mode: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 1500*time.Millisecond {
		t.Fatalf("SyncNvimConfig during blocking mode took %v, want fast return", elapsed)
	}

	tester.Type("G")
	tester.Key(0, ui.KeyEnter)
	waitUntil(t, 5*time.Second, "normal mode", func() bool {
		mode, err := shell.impl.session.Nvim().Mode()
		return err == nil && mode != nil && !mode.Blocking
	})
	if err := shell.impl.session.Nvim().Command("let g:nvgui_ligatures = v:false"); err != nil {
		t.Fatalf("set g:nvgui_ligatures: %v", err)
	}
	waitUntil(t, 5*time.Second, "hot sync after blocking mode", func() bool {
		snapshot, err := shell.SyncNvimConfig(context.Background())
		return err == nil && !snapshot.Ligatures && !shell.AnimationConfig().LigaturesEnabled
	})
	if _, err := shell.SyncNvimConfig(context.Background()); err != nil {
		t.Fatalf("steady SyncNvimConfig after recovery: %v", err)
	}
}

func TestM2DoctorSync_WorkspaceStartupOpenInAndEnvironmentOverride(t *testing.T) {
	nvimSetup := "let g:nvgui_open_in = 'new'"
	t.Run("Neovim startup value propagates", func(t *testing.T) {
		workspace, shell := m2DoctorSyncStartWorkspace(t, []string{"--clean", "--cmd", nvimSetup, "-n"}, m4AnimFilteredEnv())
		waitUntil(t, 5*time.Second, "workspace open-in new", func() bool {
			_, err := shell.SyncNvimConfig(context.Background())
			return err == nil && shell.OpenInPolicy() == OpenInNew && workspace.OpenInPolicy() == OpenInNew
		})
	})
	t.Run("NVGUI_OPEN_IN overrides Neovim startup value", func(t *testing.T) {
		workspace, shell := m2DoctorSyncStartWorkspace(t, []string{"--clean", "--cmd", nvimSetup, "-n"}, m4AnimFilteredEnv("NVGUI_OPEN_IN=front"))
		waitUntil(t, 5*time.Second, "workspace open-in front", func() bool {
			_, err := shell.SyncNvimConfig(context.Background())
			return err == nil && shell.OpenInPolicy() == OpenInFront && workspace.OpenInPolicy() == OpenInFront
		})
		if _, err := shell.SyncNvimConfig(context.Background()); err != nil {
			t.Fatalf("steady SyncNvimConfig: %v", err)
		}
		if shell.OpenInPolicy() != OpenInFront || workspace.OpenInPolicy() != OpenInFront {
			t.Fatalf("environment override lost: shell=%q workspace=%q", shell.OpenInPolicy(), workspace.OpenInPolicy())
		}
	})
}
