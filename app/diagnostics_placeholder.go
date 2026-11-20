package app

import (
	"errors"

	"github.com/egoist/mygo/ui"
)

var errDiagnosticsNotImplemented = errors.New("app: diagnostics not implemented")

type diagnosticsImpl struct{}

func newDiagnostics() *Diagnostics {
	return &Diagnostics{impl: &diagnosticsImpl{}}
}

func diagnosticsBundleToScenario(_ *Diagnostics, _ string, _ string) error {
	return errDiagnosticsNotImplemented
}

func diagnosticsShortcut(_ *Diagnostics, _ *ui.Context) bool {
	return false
}
