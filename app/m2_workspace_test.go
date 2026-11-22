package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/screen"
)

func TestM2Workspace_NilReceiverAndConcurrentLifecycle(t *testing.T) {
	var nilWorkspace *app.Workspace
	if nilWorkspace.WindowCount() != 0 || nilWorkspace.Windows() != nil ||
		nilWorkspace.Shell(1) != nil || nilWorkspace.AllClosed() != nil ||
		nilWorkspace.BuildMenu() == nil || nilWorkspace.FocusWindow(1) ||
		nilWorkspace.HandleShortcut(nil, 1) {
		t.Fatal("nil receiver exposed non-zero state")
	}
	if id, shell := nilWorkspace.Front(); id != 0 || shell != nil {
		t.Fatal("nil receiver Front() returned a window")
	}
	if err := nilWorkspace.CloseWindow(1); err != nil {
		t.Fatalf("nil receiver CloseWindow returned %v", err)
	}
	if err := nilWorkspace.Close(); err != nil {
		t.Fatalf("nil receiver Close returned %v", err)
	}
	if _, _, err := nilWorkspace.NewWindow(context.Background(), app.LaunchRequest{}); err == nil {
		t.Fatal("nil receiver NewWindow succeeded")
	}

	req := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	ws := app.NewWorkspace(app.WorkspaceOptions{BaseRequest: req, Headless: true})
	t.Cleanup(func() { _ = ws.Close() })

	const workers = 6
	var wg sync.WaitGroup
	ids := make(chan uint64, workers)
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := ws.NewWindow(context.Background(), app.LaunchRequest{})
			if err != nil {
				t.Errorf("concurrent NewWindow: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	created := make([]uint64, 0, workers)
	for id := range ids {
		created = append(created, id)
		if !ws.FocusWindow(id) {
			t.Errorf("FocusWindow(%d) returned false", id)
		}
	}
	if len(created) != workers || ws.WindowCount() != workers {
		t.Fatalf("created %d windows, WindowCount=%d, want %d", len(created), ws.WindowCount(), workers)
	}
	for _, id := range created {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			if err := ws.CloseWindow(id); err != nil {
				t.Errorf("concurrent CloseWindow(%d): %v", id, err)
			}
			if err := ws.CloseWindow(id); err != nil {
				t.Errorf("idempotent CloseWindow(%d): %v", id, err)
			}
		}(id)
	}
	wg.Wait()
	if got := ws.WindowCount(); got != 0 {
		t.Fatalf("WindowCount after concurrent close = %d, want 0", got)
	}
}

func TestM2Workspace_DirectStartRemainsSingleShell(t *testing.T) {
	req := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	shell, err := app.Start(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("app.Start: %v", err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	if shell.SessionExited() == nil {
		t.Fatal("app.Start returned a shell without a session")
	}
	if shell.Window() != nil {
		t.Fatal("headless app.Start created a GUI window")
	}
	if err := shell.Close(); err != nil {
		t.Fatalf("app.Start shell Close: %v", err)
	}
	select {
	case <-shell.SessionExited():
	default:
		t.Fatal("app.Start session did not exit after Close")
	}
}

func TestM2Workspace_CmdNUsesCleanEnvironmentWithoutOldFileArgs(t *testing.T) {
	workDir := t.TempDir()
	oldFile := filepath.Join(workDir, "old_file_marker.txt")
	optionFile := filepath.Join(workDir, "option_marker.txt")
	listenAddress := filepath.Join(workDir, "listen.sock")
	if err := os.WriteFile(oldFile, []byte("workspace_old_file_marker"), 0o644); err != nil {
		t.Fatalf("write launch file: %v", err)
	}
	req := app.LaunchRequest{
		Args: []string{
			"--clean", "-n", "--listen", listenAddress, "-c",
			"call writefile(['workspace_option_marker'], $NVGUI_WORKSPACE_OPTION_FILE)",
			oldFile,
		},
		Env:        append(m4FilteredEnv(), "NVGUI_WORKSPACE_OPTION_FILE="+optionFile),
		WorkingDir: workDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	ws := app.NewWorkspace(app.WorkspaceOptions{BaseRequest: req, Headless: true})
	t.Cleanup(func() { _ = ws.Close() })
	if _, _, err := ws.NewWindow(context.Background(), req); err != nil {
		t.Fatalf("initial NewWindow: %v", err)
	}
	if !m2WorkspaceWaitUntil(5*time.Second, func() bool {
		_, err := os.Stat(optionFile)
		return err == nil
	}) {
		t.Fatal("initial window did not execute the inherited nvim option")
	}
	if _, err := os.Stat(listenAddress); err != nil {
		t.Fatalf("initial --listen socket was not created: %v", err)
	}
	if err := os.Remove(optionFile); err != nil {
		t.Fatalf("remove option marker: %v", err)
	}

	handled := false
	tester := ui.NewTester(func(c *ui.Context) {
		if ws.HandleShortcut(c, 1) {
			handled = true
		}
		ui.Box(c).Fill()
	}, 200, 100)
	tester.Key(ui.Cmd, ui.KeyN)
	if !handled {
		t.Fatal("Cmd+N was not handled by Workspace")
	}
	if !m2WorkspaceWaitUntil(5*time.Second, func() bool { return ws.WindowCount() == 2 }) {
		t.Fatalf("WindowCount after Cmd+N = %d, want 2", ws.WindowCount())
	}
	newID, newShell := ws.Front()
	if newID == 0 || newShell == nil {
		t.Fatal("Cmd+N did not create a front window")
	}
	states := m2WorkspaceState(t, newShell)
	if m4StateHasText(states, "workspace_old_file_marker") {
		t.Fatal("Cmd+N inherited the previous window's file contents")
	}
	if !m2WorkspaceWaitUntil(5*time.Second, func() bool {
		data, err := os.ReadFile(optionFile)
		return err == nil && strings.Contains(string(data), "workspace_option_marker")
	}) {
		t.Fatal("Cmd+N did not inherit the nvim option and its value")
	}
	if count := len(ws.Windows()); count != 2 {
		t.Fatalf("WindowCount after Cmd+N = %d, want 2", count)
	}
	for _, info := range ws.Windows() {
		if info.WorkingDir != workDir {
			t.Fatalf("Workspace window WorkingDir = %q, want %q", info.WorkingDir, workDir)
		}
	}
}

func TestM2Workspace_CloseWindowStateIsImmediatelyConsistent(t *testing.T) {
	req := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	ws := app.NewWorkspace(app.WorkspaceOptions{BaseRequest: req, Headless: true})
	t.Cleanup(func() { _ = ws.Close() })
	firstID, _, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #1: %v", err)
	}
	secondID, secondShell, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #2: %v", err)
	}
	if !ws.FocusWindow(firstID) {
		t.Fatal("FocusWindow(first) returned false")
	}
	if err := ws.CloseWindow(firstID); err != nil {
		t.Fatalf("CloseWindow(front): %v", err)
	}
	if got := ws.WindowCount(); got != 1 {
		t.Fatalf("WindowCount after CloseWindow = %d, want 1", got)
	}
	if got := ws.Shell(firstID); got != nil {
		t.Fatalf("Shell(%d) after CloseWindow = %p, want nil", firstID, got)
	}
	if id, shell := ws.Front(); id != secondID || shell != secondShell {
		t.Fatalf("Front() after CloseWindow = (%d, %p), want (%d, %p)", id, shell, secondID, secondShell)
	}
	select {
	case <-ws.AllClosed():
		t.Fatal("AllClosed fired while another window is alive")
	default:
	}
}

func m2WorkspaceState(t *testing.T, shell *app.Shell) *screen.State {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bundle")
	var state *screen.State
	if !m2WorkspaceWaitUntil(5*time.Second, func() bool {
		if err := shell.ExportDiag(dir); err != nil {
			return false
		}
		states, err := diag.Replay(dir)
		if err != nil || len(states) == 0 {
			return false
		}
		state = states[len(states)-1]
		return true
	}) {
		t.Fatal("diagnostic state did not become available")
	}
	return state
}
