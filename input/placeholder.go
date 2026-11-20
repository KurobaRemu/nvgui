package input

import (
	"context"
	"errors"

	"github.com/egoist/mygo/ui"
	"nvgui/screen"
)

var errNotImplemented = errors.New("input: not implemented")

type traceRecorderImpl struct{}

func newTraceRecorder() *TraceRecorder {
	return &TraceRecorder{impl: &traceRecorderImpl{}}
}

func traceRecorderObserve(_ *TraceRecorder, _ ui.InputEvent) {}

func traceRecorderEvents(_ *TraceRecorder) []TraceEvent {
	return nil
}

func marshalTrace(_ []TraceEvent) ([]byte, error) {
	return nil, errNotImplemented
}

func unmarshalTrace(_ []byte) ([]TraceEvent, error) {
	return nil, errNotImplemented
}

func replayTrace(_ []TraceEvent, _ func(ui.InputEvent)) error {
	return errNotImplemented
}

type queueImpl struct{}

func newQueue(_ Sender, _ *TraceRecorder) *Queue {
	return &Queue{impl: &queueImpl{}}
}

func queueRun(_ *Queue, _ context.Context) error {
	return errNotImplemented
}

func queueClose(_ *Queue) error {
	return nil
}

func queueEnqueue(_ *Queue, _ ui.InputEvent) error {
	return errNotImplemented
}

func queueSent(_ *Queue) []string {
	return nil
}

type handlerImpl struct{}

func newHandler(_ *Queue) *Handler {
	return &Handler{impl: &handlerImpl{}}
}

func handlerHandle(_ *Handler, _ ui.InputEvent) bool {
	return false
}

func handlerMode(_ *Handler) string {
	return ""
}

func handlerSetMode(_ *Handler, _ string) {}

func handlerPreedit(_ *Handler) string {
	return ""
}

func handlerIMEAllowed(_ *Handler) bool {
	return false
}

func handlerCaret(_ *Handler, _, _ int, _ CellSize) ui.Rect {
	return ui.Rect{}
}

func escapeText(_ string) string {
	return ""
}

type IMEState struct {
	Mode    string
	Preedit string
	Caret   int
	Allowed bool
}

func (h *Handler) State() IMEState {
	return IMEState{}
}

func (h *Handler) CaretFor(_ *screen.State, _ CellSize) ui.Rect {
	return ui.Rect{}
}

func Notation(_ ui.InputEvent) (string, bool) {
	return "", false
}
