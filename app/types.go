package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"nvgui/connection"
	"nvgui/diag"
)

type LaunchRequest struct {
	Args       []string
	Env        []string
	WorkingDir string
	Width      int
	Height     int
	Headless   bool
}

func ParseLaunch(argv []string, env []string, cwd string) (LaunchRequest, error) {
	return parseLaunch(argv, env, cwd)
}

func (r LaunchRequest) ConnectionOptions() connection.Options {
	return launchConnectionOptions(r)
}

func (r LaunchRequest) Doctor() map[string]string {
	return launchDoctor(r)
}

type Clipboard interface {
	ReadText() string
	WriteText(string)
}

type Shell struct{ impl *shellImpl }

func NewShell(req LaunchRequest, clock diag.Clock, clipboard Clipboard) *Shell {
	return newShell(req, clock, clipboard)
}

func (s *Shell) View(c *ui.Context) {
	shellView(s, c)
}

func (s *Shell) Window() *mygo.Window {
	return shellWindow(s)
}

func (s *Shell) SessionExited() <-chan struct{} {
	return shellSessionExited(s)
}

func (s *Shell) ExportDiag(dir string) error {
	return shellExportDiag(s, dir)
}

func (s *Shell) Close() error {
	return shellClose(s)
}

type Diagnostics struct{ impl *diagnosticsImpl }

func NewDiagnostics() *Diagnostics {
	return newDiagnostics()
}

func (d *Diagnostics) BundleToScenario(dir string, out string) error {
	return diagnosticsBundleToScenario(d, dir, out)
}

func (d *Diagnostics) Shortcut(c *ui.Context) bool {
	return diagnosticsShortcut(d, c)
}
