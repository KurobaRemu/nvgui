package app

import (
	"context"
	"errors"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var errM2WorkspaceNotImplemented = errors.New("app: m2 workspace not implemented")

type workspaceImpl struct{}

func newWorkspace(_ WorkspaceOptions) *Workspace {
	return &Workspace{impl: &workspaceImpl{}}
}

func workspaceNewWindow(_ *Workspace, _ context.Context, _ LaunchRequest) (uint64, *Shell, error) {
	return 0, nil, errM2WorkspaceNotImplemented
}

func workspaceCloseWindow(_ *Workspace, _ uint64) error {
	return errM2WorkspaceNotImplemented
}

func workspaceWindows(_ *Workspace) []WindowInfo {
	return nil
}

func workspaceWindowCount(_ *Workspace) int {
	return 0
}

func workspaceShell(_ *Workspace, _ uint64) *Shell {
	return nil
}

func workspaceFront(_ *Workspace) (uint64, *Shell) {
	return 0, nil
}

func workspaceFocusWindow(_ *Workspace, _ uint64) bool {
	return false
}

func workspaceHandleShortcut(_ *Workspace, _ *ui.Context, _ uint64) bool {
	return false
}

func workspaceBuildMenu(_ *Workspace) *mygo.Menu {
	return nil
}

func workspaceAllClosed(_ *Workspace) <-chan struct{} {
	return nil
}

func workspaceClose(_ *Workspace) error {
	return errM2WorkspaceNotImplemented
}
