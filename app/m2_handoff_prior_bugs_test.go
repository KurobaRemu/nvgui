package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/screen"
)

func m2PriorShellShowsText(t *testing.T, shell *app.Shell, needle string) bool {
	t.Helper()
	dir := t.TempDir()
	if err := shell.ExportDiag(dir); err != nil {
		return false
	}
	states, err := diag.Replay(dir)
	if err != nil || len(states) == 0 {
		return false
	}
	return m2PriorStateHasText(states[len(states)-1], needle)
}

func m2PriorStateHasText(state *screen.State, needle string) bool {
	if state == nil {
		return false
	}
	for _, grid := range state.Grids {
		for _, row := range grid.Cells {
			var text strings.Builder
			for _, cell := range row {
				text.WriteString(cell.Text)
			}
			if strings.Contains(text.String(), needle) {
				return true
			}
		}
	}
	return false
}

func m2PriorSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "nvgui-s-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "i.sock")
}

func m2PriorWorkspace(t *testing.T) (*app.Workspace, app.LaunchRequest, *fakeClock) {
	t.Helper()
	workDir := t.TempDir()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	request := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: workDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	workspace := app.NewWorkspace(app.WorkspaceOptions{
		BaseRequest: request,
		Clock:       clock,
		Clipboard:   &fakeClipboard{},
		Headless:    true,
	})
	t.Cleanup(func() { _ = workspace.Close() })
	return workspace, request, clock
}

func m2PriorWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestPriorBug_Neovide1586_SingleInstanceCLIReusesFrontOrOpensNewWindow(t *testing.T) {
	sockPath := m2PriorSocketPath(t)
	workspace, request, clock := m2PriorWorkspace(t)
	frontID, _, err := workspace.NewWindow(context.Background(), request)
	if err != nil {
		t.Fatalf("NewWindow(front): %v", err)
	}
	server, err := app.ListenInstance(sockPath, workspace.HandleHandoff)
	if err != nil {
		t.Fatalf("ListenInstance: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	frontFile := filepath.Join(request.WorkingDir, "reuse front window.txt")
	m2PriorWriteFile(t, frontFile, "prior_bug_front_token\n")
	frontResult, forwarded, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
		Files:      []string{frontFile},
		WorkingDir: request.WorkingDir,
		Source:     app.LaunchSourceSingleInstance,
	})
	if err != nil || !forwarded {
		t.Fatalf("ForwardInstance(front) = (%+v, %v, %v), want forwarded=true", frontResult, forwarded, err)
	}
	if frontResult.NewWindow || frontResult.WindowID != frontID || frontResult.Opened != 1 {
		t.Fatalf("ForwardInstance(front) result = %+v, want reuse WindowID=%d", frontResult, frontID)
	}
	if got := workspace.WindowCount(); got != 1 {
		t.Fatalf("WindowCount after front handoff = %d, want 1", got)
	}
	frontShell := workspace.Shell(frontID)
	if frontShell == nil {
		t.Fatalf("Shell(%d) is nil", frontID)
	}
	frontTester := ui.NewTester(frontShell.View, 800, 480)
	if !m2HandoffWaitUntil(5*time.Second, func() bool {
		clock.Advance(10 * time.Millisecond)
		frontTester.Frame()
		return m2PriorShellShowsText(t, frontShell, "prior_bug_front_token")
	}) {
		t.Fatal("front window did not show front handoff file content")
	}

	newFile := filepath.Join(request.WorkingDir, "force new window.txt")
	m2PriorWriteFile(t, newFile, "prior_bug_new_window_token\n")
	newResult, forwarded, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
		Files:      []string{newFile},
		WorkingDir: request.WorkingDir,
		NewWindow:  true,
		Source:     app.LaunchSourceSingleInstance,
	})
	if err != nil || !forwarded {
		t.Fatalf("ForwardInstance(--new-window) = (%+v, %v, %v), want forwarded=true", newResult, forwarded, err)
	}
	if !newResult.NewWindow || newResult.WindowID == frontID {
		t.Fatalf("ForwardInstance(--new-window) result = %+v, want distinct new window", newResult)
	}
	if got := workspace.WindowCount(); got != 2 {
		t.Fatalf("WindowCount after --new-window handoff = %d, want 2", got)
	}
	newShell := workspace.Shell(newResult.WindowID)
	if newShell == nil {
		t.Fatalf("Shell(%d) is nil", newResult.WindowID)
	}
	newTester := ui.NewTester(newShell.View, 800, 480)
	if !m2HandoffWaitUntil(5*time.Second, func() bool {
		clock.Advance(10 * time.Millisecond)
		newTester.Frame()
		return m2PriorShellShowsText(t, newShell, "prior_bug_new_window_token")
	}) {
		t.Fatal("new window did not show --new-window handoff file content")
	}
}

func TestPriorBug_Neovide1259_OpenWithAndFileDropEscapePathsWithoutEmptyBuffer(t *testing.T) {
	workDir := t.TempDir()
	marker := filepath.Join(workDir, "injected.marker")
	initLua := filepath.Join(workDir, "init.lua")
	initSource := "vim.cmd([[\nfunction! g:nvgui_prior_injected()\n  call writefile(['INJECTED'], " +
		m2HandoffVimSingleQuote(marker) + ")\nendfunction\n]])\n"
	m2PriorWriteFile(t, initLua, initSource)

	evilName := `open with |call g:nvgui_prior_injected() "a'b%#$ special.txt`
	evilPath := filepath.Join(workDir, evilName)
	m2PriorWriteFile(t, evilPath, "prior_bug_drop_token_42\n")

	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	shell, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-u", initLua, "-n"},
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
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
			}
		}
	})
	tester := ui.NewTester(shell.View, 800, 480)
	if !m2HandoffWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		img := tester.Image()
		return img != nil && !m4IsUniformImage(img.Pix)
	}) {
		t.Fatal("Shell never produced initial boot frame")
	}

	if err := shell.DropFiles(context.Background(), []string{"bad\npath"}); err == nil {
		t.Fatal("DropFiles(newline path) returned nil error")
	}
	if err := shell.DropFiles(context.Background(), []string{evilPath}); err != nil {
		t.Fatalf("DropFiles(special path): %v", err)
	}
	m2HandoffWaitForText(t, shell, clock, tester, "prior_bug_drop_token_42")

	if ok := tester.DropFiles(120, 120, evilPath); !ok {
		t.Fatal("ui.Tester.DropFiles did not reach Shell input element")
	}
	m2HandoffWaitForText(t, shell, clock, tester, "prior_bug_drop_token_42")

	probeOut := filepath.Join(workDir, "drop_probe.txt")
	probe := ":call writefile([string(len(getbufinfo({'buflisted':1}))), bufname('%')], " +
		m2HandoffVimSingleQuote(probeOut) + ")"
	tester.Key(0, ui.KeyEscape)
	tester.Type(probe)
	tester.Key(0, ui.KeyEnter)
	if !m2HandoffWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		_, statErr := os.Stat(probeOut)
		return statErr == nil
	}) {
		t.Fatal("timed out waiting for buffer probe")
	}
	raw, err := os.ReadFile(probeOut)
	if err != nil {
		t.Fatalf("read buffer probe: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "1" {
		t.Fatalf("listed buffer state = %q, want exactly 1 buffer", string(raw))
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), evilName) {
		t.Fatalf("current bufname = %q, want suffix %q", lines[1], evilName)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Ex command injection marker exists")
	}

	command, err := app.FormatDropCommand([]string{evilPath})
	if err != nil {
		t.Fatalf("FormatDropCommand(special path): %v", err)
	}
	if !strings.HasPrefix(command, "drop ") {
		t.Fatalf("FormatDropCommand = %q, want drop prefix", command)
	}
	for _, target := range []byte{' ', '"', '\'', '%', '#', '$', '|', '(', ')'} {
		if m2HandoffHasUnescapedChar(strings.TrimPrefix(command, "drop "), target) {
			t.Fatalf("FormatDropCommand left unescaped %q: %s", target, command)
		}
	}
}

func TestPriorBug_Neovide3641_NonTerminalLaunchProbesLoginPATHWithoutBlocking(t *testing.T) {
	sockPath := m2PriorSocketPath(t)
	dockEnv := []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=/tmp/nvgui-prior-dock-home",
		fmt.Sprintf("NVGUI_SOCKET=%s", sockPath),
	}
	cli, err := app.ParseCLILaunch(nil, dockEnv, t.TempDir())
	if err != nil {
		t.Fatalf("ParseCLILaunch(dock): %v", err)
	}
	if cli.Source != app.LaunchSourceDock || cli.Request.Headless {
		t.Fatalf("dock launch = %+v, want Source=dock Headless=false", cli)
	}

	var calls atomic.Int64
	merged, probed := app.ResolveLoginPATH(context.Background(), dockEnv, app.PathProbeOptions{
		NoCache: true,
		Force:   true,
		Shell:   "/bin/zsh",
		Probe: func(context.Context, string, []string) (string, error) {
			calls.Add(1)
			return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin", nil
		},
	})
	if !probed || calls.Load() != 1 {
		t.Fatalf("ResolveLoginPATH(dock) = (probed=%v, calls=%d), want one successful probe", probed, calls.Load())
	}
	path := m2HandoffEnvValue(merged, "PATH")
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		if !strings.Contains(path, want) {
			t.Fatalf("merged PATH = %q, want %q", path, want)
		}
	}

	start := time.Now()
	fallback, probed := app.ResolveLoginPATH(context.Background(), dockEnv, app.PathProbeOptions{
		NoCache: true,
		Force:   true,
		Timeout: 100,
		Probe: func(ctx context.Context, _ string, _ []string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	elapsed := time.Since(start)
	if probed || elapsed >= time.Second {
		t.Fatalf("hanging probe = (probed=%v, elapsed=%v), want fast fallback", probed, elapsed)
	}
	if got := m2HandoffEnvValue(fallback, "PATH"); got != "/usr/bin:/bin:/usr/sbin:/sbin" {
		t.Fatalf("fallback PATH = %q, want original launchd PATH", got)
	}
}
