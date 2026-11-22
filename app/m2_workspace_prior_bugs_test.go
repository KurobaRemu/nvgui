package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/screen"
)

func priorBugWorkspaceWait(t *testing.T, ws *app.Workspace, count int) {
	t.Helper()
	if !m2WorkspaceWaitUntil(5*time.Second, func() bool { return ws.WindowCount() == count }) {
		t.Fatalf("WindowCount = %d, want %d", ws.WindowCount(), count)
	}
}

func priorBugWorkspaceText(t *testing.T, shell *app.Shell, needle string) *screen.State {
	t.Helper()
	dir := t.TempDir()
	var matched *screen.State
	if !m2WorkspaceWaitUntil(5*time.Second, func() bool {
		if err := shell.ExportDiag(dir); err != nil {
			return false
		}
		states, err := diag.Replay(dir)
		if err != nil || len(states) == 0 {
			return false
		}
		state := states[len(states)-1]
		if !m4StateHasText(state, needle) {
			return false
		}
		matched = state
		return true
	}) {
		t.Fatalf("timed out waiting for %q", needle)
	}
	return matched
}

func priorBugWorkspaceClosed(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	if ch == nil {
		t.Fatalf("%s channel is nil", label)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not close", label)
	}
}

func TestPriorBug_Neovide1332_MultiWindowIndependentSessionsAndIsolation(t *testing.T) {
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
	_, first, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #1: %v", err)
	}
	_, second, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #2: %v", err)
	}
	if first == second || first.SessionExited() == second.SessionExited() {
		t.Fatal("windows share a shell or session")
	}
	firstTester := ui.NewTester(first.View, 800, 480)
	secondTester := ui.NewTester(second.View, 800, 480)
	firstTester.Type("iprior_bug_left")
	firstTester.Key(0, ui.KeyEscape)
	secondTester.Type("iprior_bug_right")
	secondTester.Key(0, ui.KeyEscape)
	left := priorBugWorkspaceText(t, first, "prior_bug_left")
	if m4StateHasText(left, "prior_bug_right") {
		t.Fatal("first window saw text from the second window")
	}
	right := priorBugWorkspaceText(t, second, "prior_bug_right")
	if m4StateHasText(right, "prior_bug_left") {
		t.Fatal("second window saw text from the first window")
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("Workspace.Close: %v", err)
	}
	priorBugWorkspaceClosed(t, first.SessionExited(), "first window")
	priorBugWorkspaceClosed(t, second.SessionExited(), "second window")
	priorBugWorkspaceClosed(t, ws.AllClosed(), "Workspace.AllClosed")
}

func TestPriorBug_Neovide1332_WindowMRUFocusAndCloseReassignsFront(t *testing.T) {
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
	firstID, first, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #1: %v", err)
	}
	secondID, second, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #2: %v", err)
	}
	thirdID, third, err := ws.NewWindow(context.Background(), req)
	if err != nil {
		t.Fatalf("NewWindow #3: %v", err)
	}
	priorBugWorkspaceWait(t, ws, 3)
	if id, shell := ws.Front(); id != thirdID || shell != third {
		t.Fatalf("initial Front() = (%d, %p), want (%d, %p)", id, shell, thirdID, third)
	}
	if !ws.FocusWindow(firstID) {
		t.Fatal("FocusWindow(first) returned false")
	}
	infos := ws.Windows()
	if len(infos) != 3 || !infos[0].Focused || infos[0].ID != firstID ||
		infos[1].Focused || infos[2].Focused {
		t.Fatalf("Windows() after focus = %+v", infos)
	}
	if err := ws.CloseWindow(firstID); err != nil {
		t.Fatalf("CloseWindow(front): %v", err)
	}
	priorBugWorkspaceClosed(t, first.SessionExited(), "first window")
	priorBugWorkspaceWait(t, ws, 2)
	if id, shell := ws.Front(); id != thirdID || shell != third {
		t.Fatalf("Front() after closing focused window = (%d, %p), want (%d, %p)", id, shell, thirdID, third)
	}
	if ws.Shell(firstID) != nil || ws.Shell(secondID) != second || ws.Shell(thirdID) != third {
		t.Fatal("CloseWindow(front) affected another window")
	}
	if err := ws.CloseWindow(999_999); err != nil {
		t.Fatalf("CloseWindow(unknown): %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("Workspace.Close: %v", err)
	}
	priorBugWorkspaceClosed(t, ws.AllClosed(), "Workspace.AllClosed")
}
