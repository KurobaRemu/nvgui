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
			{ID: "toggle-fullscreen", Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}
