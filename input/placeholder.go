package input

import (
	"github.com/egoist/mygo/ui"

	"nvgui/screen"
)

func handlerMode(_ *Handler) string { return "" }

func handlerSetMode(_ *Handler, _ string) {}

func handlerPreedit(_ *Handler) string { return "" }

func handlerIMEAllowed(_ *Handler) bool { return false }

func handlerCaret(_ *Handler, _, _ int, _ CellSize) ui.Rect { return ui.Rect{} }

type IMEState struct {
	Mode    string
	Preedit string
	Caret   int
	Allowed bool
}

func (h *Handler) State() IMEState { return IMEState{} }

func (h *Handler) CaretFor(_ *screen.State, _ CellSize) ui.Rect { return ui.Rect{} }
