package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

func workspaceBuildMenuFor(w *Workspace) *mygo.Menu {
	impl := workspaceImplOf(w)
	openWindow := func(*mygo.MenuItem, *mygo.Window) {
		if impl == nil {
			return
		}
		impl.asyncTask(func() {
			if _, _, err := workspaceNewWindow(w, context.Background(), LaunchRequest{}); err != nil {
				fmt.Fprintln(os.Stderr, "nvgui: new window:", err)
			}
		})
	}
	closeWindow := func(_ *mygo.MenuItem, win *mygo.Window) {
		if win != nil || impl == nil {
			return
		}
		id, _ := workspaceFront(w)
		if id == 0 {
			return
		}
		impl.asyncTask(func() {
			if err := workspaceCloseWindow(w, id); err != nil {
				fmt.Fprintln(os.Stderr, "nvgui: close window:", err)
			}
		})
	}
	exportDiagnostics := func(*mygo.MenuItem, *mygo.Window) {
		if impl == nil {
			return
		}
		impl.mu.Lock()
		if impl.closed || len(impl.mru) == 0 {
			impl.mu.Unlock()
			return
		}
		id := impl.mru[0]
		entry := impl.windows[id]
		if entry == nil || entry.shell == nil {
			impl.mu.Unlock()
			return
		}
		shell := entry.shell
		base := entry.workingDir
		impl.mu.Unlock()
		if base == "" {
			base = os.TempDir()
		}
		outDir := filepath.Join(base, fmt.Sprintf("nvgui-diag-%d", time.Now().UnixNano()))
		impl.asyncTask(func() {
			if err := shell.ExportDiag(outDir); err != nil {
				fmt.Fprintln(os.Stderr, "nvgui: export diagnostics:", err)
			}
		})
	}
	targetShell := func(win *mygo.Window) *Shell {
		if impl == nil {
			return nil
		}
		if win != nil {
			impl.mu.Lock()
			for _, entry := range impl.windows {
				if entry != nil && entry.window == win {
					sh := entry.shell
					impl.mu.Unlock()
					return sh
				}
			}
			impl.mu.Unlock()
		}
		_, sh := workspaceFront(w)
		return sh
	}
	zoomIn := func(_ *mygo.MenuItem, win *mygo.Window) {
		if sh := targetShell(win); sh != nil {
			_ = sh.ZoomIn()
		}
	}
	zoomOut := func(_ *mygo.MenuItem, win *mygo.Window) {
		if sh := targetShell(win); sh != nil {
			_ = sh.ZoomOut()
		}
	}
	zoomReset := func(_ *mygo.MenuItem, win *mygo.Window) {
		if sh := targetShell(win); sh != nil {
			_ = sh.ZoomReset()
		}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{ID: "new-window", Label: "New Window", Accelerator: "CmdOrCtrl+N", Click: openWindow},
			{ID: "close-window", Role: mygo.RoleClose, Click: closeWindow},
			mygo.Separator(),
			{ID: "export-diag", Label: "Export Diagnostics Bundle", Accelerator: "CmdOrCtrl+Shift+D", Click: exportDiagnostics},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "View", Submenu: []*mygo.MenuItem{
			{ID: "zoom-in", Label: "Zoom In", Accelerator: "CmdOrCtrl+Plus", Click: zoomIn},
			{ID: "zoom-out", Label: "Zoom Out", Accelerator: "CmdOrCtrl+-", Click: zoomOut},
			{ID: "zoom-reset", Label: "Actual Size", Accelerator: "CmdOrCtrl+0", Click: zoomReset},
			mygo.Separator(),
			{ID: "toggle-fullscreen", Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}
