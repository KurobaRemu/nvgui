package app

import (
	"context"
	"errors"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"nvgui/connection"
	"nvgui/diag"
	"nvgui/session"
	"nvgui/text"
)

var errNotImplemented = errors.New("app: not implemented")

type shellImpl struct{}

func parseLaunch(_ []string, _ []string, _ string) (LaunchRequest, error) {
	return LaunchRequest{}, errNotImplemented
}

func launchConnectionOptions(_ LaunchRequest) connection.Options {
	return connection.Options{}
}

func launchDoctor(_ LaunchRequest) map[string]string {
	return nil
}

func newShell(_ LaunchRequest, _ diag.Clock, _ Clipboard) *Shell {
	return &Shell{impl: &shellImpl{}}
}

func shellView(_ *Shell, _ *ui.Context) {}

func shellWindow(_ *Shell) *mygo.Window {
	return nil
}

func shellSessionExited(_ *Shell) <-chan struct{} {
	return nil
}

func shellExportDiag(_ *Shell, _ string) error {
	return errNotImplemented
}

func shellClose(_ *Shell) error {
	return nil
}

func (s *Shell) Resize(width, height int) error {
	return errNotImplemented
}

func (s *Shell) HandleEditCommand(c *ui.Context, ev ui.InputEvent) bool {
	return false
}

func (s *Shell) AttachInput(c *ui.Context) ui.Element {
	return ui.Element{}
}

func Start(ctx context.Context, req LaunchRequest, clock diag.Clock, clipboard Clipboard) (*Shell, error) {
	return nil, errNotImplemented
}

var (
	_ = session.Start
	_ = text.NewResolver
)
