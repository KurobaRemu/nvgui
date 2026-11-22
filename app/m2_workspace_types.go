package app

import (
	"context"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"nvgui/diag"
)

type WorkspaceOptions struct {
	BaseRequest   LaunchRequest
	Clock         diag.Clock
	Clipboard     Clipboard
	Headless      bool
	OnAllClosed   func()
	OnWindowAdded func(id uint64, shell *Shell)
}

type WindowInfo struct {
	ID         uint64
	WorkingDir string
	Focused    bool
}

type Workspace struct {
	impl *workspaceImpl
}

func NewWorkspace(opts WorkspaceOptions) *Workspace {
	return newWorkspace(opts)
}

func (w *Workspace) NewWindow(ctx context.Context, req LaunchRequest) (uint64, *Shell, error) {
	return workspaceNewWindow(w, ctx, req)
}

func (w *Workspace) CloseWindow(id uint64) error {
	return workspaceCloseWindow(w, id)
}

func (w *Workspace) Windows() []WindowInfo {
	return workspaceWindows(w)
}

func (w *Workspace) WindowCount() int {
	return workspaceWindowCount(w)
}

func (w *Workspace) Shell(id uint64) *Shell {
	return workspaceShell(w, id)
}

func (w *Workspace) Front() (uint64, *Shell) {
	return workspaceFront(w)
}

func (w *Workspace) FocusWindow(id uint64) bool {
	return workspaceFocusWindow(w, id)
}

func (w *Workspace) HandleShortcut(c *ui.Context, id uint64) bool {
	return workspaceHandleShortcut(w, c, id)
}

func (w *Workspace) BuildMenu() *mygo.Menu {
	return workspaceBuildMenu(w)
}

func (w *Workspace) AllClosed() <-chan struct{} {
	return workspaceAllClosed(w)
}

func (w *Workspace) Close() error {
	return workspaceClose(w)
}
