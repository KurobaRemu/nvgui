package input

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/neovim/go-client/nvim"

	"nvgui/diag"
)

type TraceKind = diag.TraceKind

const (
	KeyDown TraceKind = diag.TraceKeyDown
	KeyUp   TraceKind = diag.TraceKeyUp
	Text    TraceKind = diag.TraceText
	Compose TraceKind = diag.TraceCompose
	Command TraceKind = diag.TraceCommand
)

type TraceEvent = diag.TraceEvent

type TraceRecorder struct{ impl *traceRecorderImpl }

func NewTraceRecorder() *TraceRecorder {
	return newTraceRecorder()
}

func (r *TraceRecorder) Observe(ev ui.InputEvent) {
	traceRecorderObserve(r, ev)
}

func (r *TraceRecorder) Events() []TraceEvent {
	return traceRecorderEvents(r)
}

func MarshalTrace(events []TraceEvent) ([]byte, error) {
	return marshalTrace(events)
}

func UnmarshalTrace(data []byte) ([]TraceEvent, error) {
	return unmarshalTrace(data)
}

func Replay(events []TraceEvent, sink func(ui.InputEvent)) error {
	return replayTrace(events, sink)
}

type Sender interface {
	Input(keys string) (int, error)
}

type MouseSender interface {
	InputMouse(button, action, modifier string, grid, row, col int) error
}

type MouseBatchSender interface {
	InputMouseBatch(button, action, modifier string, grid, row, col, count int) error
}

var (
	_ Sender      = (*nvim.Nvim)(nil)
	_ MouseSender = (*nvim.Nvim)(nil)
)

type MouseCommand struct {
	Button   string
	Action   string
	Modifier string
	Grid     int
	Row      int
	Col      int
	Count    int
	Mods     ui.Modifiers
	Canceled bool
	Time     time.Time
}

type Queue struct{ impl *queueImpl }

func NewQueue(sender Sender, recorder *TraceRecorder) *Queue {
	return newQueue(sender, recorder)
}

func (q *Queue) Run(ctx context.Context) error {
	return queueRun(q, ctx)
}

func (q *Queue) Close() error {
	return queueClose(q)
}

func (q *Queue) Enqueue(ev ui.InputEvent) error {
	return queueEnqueue(q, ev)
}

func (q *Queue) InputMouse(button, action, modifier string, grid, row, col int) error {
	return queueInputMouse(q, button, action, modifier, grid, row, col)
}

func (q *Queue) EnqueueMouse(cmd MouseCommand) error {
	return queueEnqueueMouse(q, cmd)
}

func (q *Queue) Sent() []string {
	return queueSent(q)
}

func (q *Queue) SentMouse() []MouseCommand {
	return queueSentMouse(q)
}

type Handler struct{ impl *handlerImpl }

func NewHandler(q *Queue) *Handler {
	return newHandler(q)
}

func (h *Handler) Handle(ev ui.InputEvent) bool {
	return handlerHandle(h, ev)
}

func (h *Handler) Mode() string {
	return handlerMode(h)
}

func (h *Handler) SetMode(mode string) {
	handlerSetMode(h, mode)
}

func (h *Handler) Preedit() string {
	return handlerPreedit(h)
}

func (h *Handler) IMEAllowed() bool {
	return handlerIMEAllowed(h)
}

func (h *Handler) Caret(row, col int, cell CellSize) ui.Rect {
	return handlerCaret(h, row, col, cell)
}

func EscapeText(text string) string {
	return escapeText(text)
}

type CellSize struct{ Width, Height float32 }

type GridBounds struct{ Cols, Rows int }

type MouseHandler struct{ impl *mouseHandlerImpl }

func NewMouseHandler(q *Queue) *MouseHandler {
	return newMouseHandler(q)
}

func (m *MouseHandler) Handle(ev ui.InputEvent) bool {
	return mouseHandlerHandle(m, ev)
}

func (m *MouseHandler) SetEnabled(enabled bool) {
	mouseHandlerSetEnabled(m, enabled)
}

func (m *MouseHandler) Enabled() bool {
	return mouseHandlerEnabled(m)
}

func (m *MouseHandler) SetMouseMoveEvent(enabled bool) {
	mouseHandlerSetMouseMoveEvent(m, enabled)
}

func (m *MouseHandler) MouseMoveEvent() bool {
	return mouseHandlerMouseMoveEvent(m)
}

func (m *MouseHandler) SetGeometry(cell CellSize, bounds GridBounds) {
	mouseHandlerSetGeometry(m, cell, bounds)
}

func (m *MouseHandler) Dragging() bool {
	return mouseHandlerDragging(m)
}

func (m *MouseHandler) GridPos() (row, col int) {
	return mouseHandlerGridPos(m)
}
