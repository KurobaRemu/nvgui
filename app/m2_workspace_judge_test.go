package app_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/screen"
)

func m2WorkspaceWaitUntil(timeout time.Duration, cond func() bool) bool {
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

func m2WorkspaceReplayLatest(t *testing.T, shell *app.Shell) *screen.State {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := shell.ExportDiag(dir); err != nil {
		return nil
	}
	states, err := diag.Replay(dir)
	if err != nil || len(states) == 0 {
		return nil
	}
	return states[len(states)-1]
}

func m2WorkspaceWaitForText(t *testing.T, shell *app.Shell, clock *fakeClock, tester *ui.Tester, needle string) *screen.State {
	t.Helper()
	var matched *screen.State
	ok := m2WorkspaceWaitUntil(5*time.Second, func() bool {
		if clock != nil {
			clock.Advance(10 * time.Millisecond)
		}
		if tester != nil {
			tester.Frame()
		}
		st := m2WorkspaceReplayLatest(t, shell)
		if st != nil && m4StateHasText(st, needle) {
			matched = st
			return true
		}
		return false
	})
	if !ok {
		t.Fatalf("timed out waiting for %q in shell screen state", needle)
	}
	return matched
}

func m2WorkspaceWaitChanClosed(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	if ch == nil {
		t.Fatalf("%s channel is nil", label)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
		t.Fatalf("%s did not close within 5s", label)
	}
}

func TestM2Workspace_MultiWindowIndependentSessionsAndLifecycle(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	workDir := t.TempDir()
	baseReq := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: workDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}

	var allClosedCount atomic.Int32
	var addedCount atomic.Int32
	ws := app.NewWorkspace(app.WorkspaceOptions{
		BaseRequest: baseReq,
		Clock:       clock,
		Clipboard:   &fakeClipboard{},
		Headless:    true,
		OnAllClosed: func() {
			allClosedCount.Add(1)
		},
		OnWindowAdded: func(id uint64, sh *app.Shell) {
			if id > 0 && sh != nil {
				addedCount.Add(1)
			}
		},
	})
	if ws == nil {
		t.Fatal("NewWorkspace returned nil")
	}
	t.Cleanup(func() {
		_ = ws.Close()
	})

	allClosed := ws.AllClosed()
	if allClosed == nil {
		t.Fatal("AllClosed() returned nil channel")
	}

	id1, sh1, err := ws.NewWindow(context.Background(), baseReq)
	if err != nil {
		t.Fatalf("NewWindow #1: %v", err)
	}
	if id1 == 0 || sh1 == nil {
		t.Fatalf("NewWindow #1 returned id=%d shell=%v, want non-zero id and non-nil shell", id1, sh1)
	}
	if got := ws.WindowCount(); got != 1 {
		t.Fatalf("WindowCount after #1 = %d, want 1", got)
	}

	id2, sh2, err := ws.NewWindow(context.Background(), baseReq)
	if err != nil {
		t.Fatalf("NewWindow #2: %v", err)
	}
	if id2 <= id1 || sh2 == nil || sh2 == sh1 {
		t.Fatalf("NewWindow #2 returned id=%d (id1=%d) shell=%p (sh1=%p), want strictly increasing id and distinct shell", id2, id1, sh2, sh1)
	}
	if got := ws.WindowCount(); got != 2 {
		t.Fatalf("WindowCount after #2 = %d, want 2", got)
	}
	if got := addedCount.Load(); got != 2 {
		t.Fatalf("OnWindowAdded count = %d, want 2", got)
	}
	if ws.Shell(id1) != sh1 || ws.Shell(id2) != sh2 {
		t.Fatal("Workspace.Shell(id) did not return matching shells")
	}

	select {
	case <-allClosed:
		t.Fatal("AllClosed() closed prematurely while 2 windows are open")
	default:
	}

	t1 := ui.NewTester(sh1.View, 800, 480)
	t2 := ui.NewTester(sh2.View, 800, 480)

	t1.Type("iws_win_one_alpha")
	t1.Key(0, ui.KeyEscape)
	t2.Type("iws_win_two_beta")
	t2.Key(0, ui.KeyEscape)

	st1 := m2WorkspaceWaitForText(t, sh1, clock, t1, "ws_win_one_alpha")
	if m4StateHasText(st1, "ws_win_two_beta") {
		t.Fatal("window 1 leaked text from window 2")
	}
	st2 := m2WorkspaceWaitForText(t, sh2, clock, t2, "ws_win_two_beta")
	if m4StateHasText(st2, "ws_win_one_alpha") {
		t.Fatal("window 2 leaked text from window 1")
	}

	if err := ws.CloseWindow(id1); err != nil {
		t.Fatalf("CloseWindow(%d): %v", id1, err)
	}
	m2WorkspaceWaitChanClosed(t, sh1.SessionExited(), "window 1 SessionExited")
	if !m2WorkspaceWaitUntil(3*time.Second, func() bool { return ws.WindowCount() == 1 }) {
		t.Fatalf("WindowCount after closing window 1 = %d, want 1", ws.WindowCount())
	}
	if ws.Shell(id1) != nil {
		t.Fatalf("Shell(%d) after close = non-nil, want nil", id1)
	}
	if ws.Shell(id2) != sh2 {
		t.Fatalf("Shell(%d) after closing window 1 mismatch", id2)
	}
	select {
	case <-allClosed:
		t.Fatal("AllClosed() closed while window 2 is still alive")
	default:
	}
	if got := allClosedCount.Load(); got != 0 {
		t.Fatalf("OnAllClosed fired %d times while window 2 is still alive, want 0", got)
	}

	// Window 2 must remain fully usable after window 1 closes.
	t2.Type("ows_win_two_after_close")
	t2.Key(0, ui.KeyEscape)
	m2WorkspaceWaitForText(t, sh2, clock, t2, "ws_win_two_after_close")

	// Exit window 2 from inside Neovim (:qa!).
	t2.Type(":qa!")
	t2.Key(0, ui.KeyEnter)

	m2WorkspaceWaitChanClosed(t, sh2.SessionExited(), "window 2 SessionExited")
	m2WorkspaceWaitChanClosed(t, allClosed, "Workspace.AllClosed")
	if !m2WorkspaceWaitUntil(2*time.Second, func() bool { return ws.WindowCount() == 0 && allClosedCount.Load() == 1 }) {
		t.Fatalf("after last window exit: WindowCount=%d OnAllClosed=%d, want 0 and 1", ws.WindowCount(), allClosedCount.Load())
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("Workspace.Close() after all windows exited: %v", err)
	}
	if got := allClosedCount.Load(); got != 1 {
		t.Fatalf("OnAllClosed fired %d times after idempotent Close(), want 1", got)
	}
}

func TestM2Workspace_MRUFocusOrderAndCmdNShortcutAndMenu(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	workDir := t.TempDir()
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
	t.Cleanup(func() {
		_ = ws.Close()
	})

	id1, sh1, err := ws.NewWindow(context.Background(), baseReq)
	if err != nil {
		t.Fatalf("NewWindow #1: %v", err)
	}
	id2, sh2, err := ws.NewWindow(context.Background(), baseReq)
	if err != nil {
		t.Fatalf("NewWindow #2: %v", err)
	}

	frontID, frontShell := ws.Front()
	if frontID != id2 || frontShell != sh2 {
		t.Fatalf("initial Front() = (%d, %p), want (%d, %p)", frontID, frontShell, id2, sh2)
	}

	if !ws.FocusWindow(id1) {
		t.Fatalf("FocusWindow(%d) returned false, want true", id1)
	}
	if ws.FocusWindow(999_999) {
		t.Fatal("FocusWindow(999999) returned true for unknown window ID")
	}
	frontID, frontShell = ws.Front()
	if frontID != id1 || frontShell != sh1 {
		t.Fatalf("Front() after FocusWindow(%d) = (%d, %p), want (%d, %p)", id1, frontID, frontShell, id1, sh1)
	}

	infos := ws.Windows()
	if len(infos) != 2 {
		t.Fatalf("len(Windows()) = %d, want 2", len(infos))
	}
	if infos[0].ID != id1 || !infos[0].Focused || infos[1].ID != id2 || infos[1].Focused {
		t.Fatalf("Windows() MRU order = %+v, want [id1 focused, id2 unfocused]", infos)
	}

	// Trigger Cmd+N via ui.Tester -> Workspace.HandleShortcut.
	shortcutHandled := 0
	tester := ui.NewTester(func(c *ui.Context) {
		if ws.HandleShortcut(c, id1) {
			shortcutHandled++
		}
		ui.Box(c).Fill()
	}, 200, 100)
	tester.Key(ui.Cmd, ui.KeyN)
	if shortcutHandled != 1 {
		t.Fatalf("HandleShortcut handled Cmd+N %d times, want 1", shortcutHandled)
	}
	if !m2WorkspaceWaitUntil(3*time.Second, func() bool { return ws.WindowCount() == 3 }) {
		t.Fatalf("WindowCount after Cmd+N = %d, want 3", ws.WindowCount())
	}

	// Verify BuildMenu structure and new-window click action.
	menu := ws.BuildMenu()
	if menu == nil {
		t.Fatal("BuildMenu() returned nil")
	}
	newWinItem := menu.ItemByID("new-window")
	if newWinItem == nil {
		t.Fatal("BuildMenu() missing ItemByID(\"new-window\")")
	}
	if newWinItem.Accelerator != "CmdOrCtrl+N" {
		t.Fatalf("new-window Accelerator = %q, want %q", newWinItem.Accelerator, "CmdOrCtrl+N")
	}
	if newWinItem.Click == nil {
		t.Fatal("new-window Click handler is nil")
	}

	var hasAppRole, hasEditRole, hasWindowRole bool
	for _, item := range menu.Items() {
		switch item.Role {
		case mygo.RoleAppMenu:
			hasAppRole = true
		case mygo.RoleEditMenu:
			hasEditRole = true
		case mygo.RoleWindowMenu:
			hasWindowRole = true
		}
	}
	if !hasAppRole || !hasEditRole || !hasWindowRole {
		t.Fatalf("BuildMenu() missing standard roles: app=%v edit=%v window=%v", hasAppRole, hasEditRole, hasWindowRole)
	}

	newWinItem.Click(newWinItem, nil)
	if !m2WorkspaceWaitUntil(3*time.Second, func() bool { return ws.WindowCount() == 4 }) {
		t.Fatalf("WindowCount after menu new-window Click = %d, want 4", ws.WindowCount())
	}

	// Workspace.Close() must be idempotent and close all windows cleanly.
	if err := ws.Close(); err != nil {
		t.Fatalf("first Workspace.Close(): %v", err)
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("second Workspace.Close() (idempotent): %v", err)
	}
	if got := ws.WindowCount(); got != 0 {
		t.Fatalf("WindowCount after Workspace.Close() = %d, want 0", got)
	}
	m2WorkspaceWaitChanClosed(t, ws.AllClosed(), "Workspace.AllClosed after Close")
}
