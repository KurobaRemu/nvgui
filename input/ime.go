package input

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"nvgui/screen"
)

type IMEState struct {
	Mode    string
	Preedit string
	Caret   int
	Allowed bool
}

type handlerImpl struct {
	queue *Queue

	opMu    sync.Mutex
	mu      sync.Mutex
	mode    string
	preedit string
	caret   int
	allowed bool
}

func newHandler(queue *Queue) *Handler {
	return &Handler{impl: &handlerImpl{queue: queue, mode: "normal"}}
}

func handlerHandle(handler *Handler, ev ui.InputEvent) bool {
	if handler == nil || handler.impl == nil || handler.impl.queue == nil {
		return false
	}
	impl := handler.impl
	impl.opMu.Lock()
	defer impl.opMu.Unlock()
	physicalOnly := false
	switch ev.Kind {
	case ui.InputKeyDown, ui.InputKeyUp:
		if ev.Mods&ui.Super != 0 {
			return false
		}
		if _, ok := Notation(ev); !ok {
			if ev.Key != ui.KeyUnknown || ev.Code == ui.CodeUnknown || isModifierOrLockCode(ev.Code) {
				return false
			}
			physicalOnly = true
		}
	case ui.InputText, ui.InputCompose, ui.InputCommand:
	default:
		return false
	}

	impl.mu.Lock()
	allowed := impl.allowed
	impl.mu.Unlock()
	if err := impl.queue.enqueue(ev, allowed); err != nil {
		return false
	}
	if physicalOnly {
		return false
	}
	impl.mu.Lock()
	switch ev.Kind {
	case ui.InputCompose:
		if ev.Canceled || ev.Text == "" {
			impl.preedit = ""
			impl.caret = 0
		} else {
			impl.preedit = ev.Text
			caret := ev.Caret
			if caret < 0 {
				caret = 0
			}
			if maxCaret := utf8.RuneCountInString(ev.Text); caret > maxCaret {
				caret = maxCaret
			}
			impl.caret = caret
		}
	case ui.InputText:
		impl.preedit = ""
		impl.caret = 0
	case ui.InputKeyDown:
		if endsComposition(ev) {
			impl.preedit = ""
			impl.caret = 0
		}
	}
	impl.mu.Unlock()
	switch ev.Kind {
	case ui.InputKeyDown, ui.InputKeyUp, ui.InputText, ui.InputCompose:
		return true
	default:
		return false
	}
}

func endsComposition(ev ui.InputEvent) bool {
	if ev.Mods&ui.Super != 0 {
		return false
	}
	if ev.Key == ui.KeyEscape && ev.Mods&(ui.Ctrl|ui.Alt) == 0 {
		return true
	}
	if ev.Mods&ui.Ctrl == 0 || ev.Mods&ui.Alt != 0 {
		return false
	}
	return ev.Key == ui.KeyC || ev.Key == ui.KeyBracketLeft
}

func handlerMode(handler *Handler) string {
	if handler == nil || handler.impl == nil {
		return ""
	}
	handler.impl.mu.Lock()
	defer handler.impl.mu.Unlock()
	return handler.impl.mode
}

func handlerSetMode(handler *Handler, mode string) {
	if handler == nil || handler.impl == nil {
		return
	}
	impl := handler.impl
	impl.opMu.Lock()
	defer impl.opMu.Unlock()
	allowed := imeAllowed(mode)
	if !allowed && handler.impl.queue != nil {
		handler.impl.queue.resetComposition()
	}
	handler.impl.mu.Lock()
	defer handler.impl.mu.Unlock()
	handler.impl.mode = mode
	handler.impl.allowed = allowed
	if !allowed {
		handler.impl.preedit = ""
		handler.impl.caret = 0
	}
}

func imeAllowed(mode string) bool {
	family := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(mode)), func(r rune) bool { return r == '_' || r == '-' })
	if len(family) == 0 {
		return false
	}
	switch family[0] {
	case "insert", "cmdline", "terminal", "replace":
		return true
	default:
		return false
	}
}

func handlerPreedit(handler *Handler) string {
	return handler.State().Preedit
}

func handlerIMEAllowed(handler *Handler) bool {
	return handler.State().Allowed
}

func (h *Handler) State() IMEState {
	if h == nil || h.impl == nil {
		return IMEState{}
	}
	h.impl.mu.Lock()
	defer h.impl.mu.Unlock()
	return IMEState{
		Mode:    h.impl.mode,
		Preedit: h.impl.preedit,
		Caret:   h.impl.caret,
		Allowed: h.impl.allowed,
	}
}

func handlerCaret(_ *Handler, row, col int, cell CellSize) ui.Rect {
	if cell.Width <= 0 || cell.Height <= 0 {
		return ui.Rect{}
	}
	if row < 0 {
		row = 0
	}
	if col < 0 {
		col = 0
	}
	return ui.Rect{
		X: float32(col) * cell.Width,
		Y: float32(row) * cell.Height,
		W: cell.Width,
		H: cell.Height,
	}
}

func (h *Handler) CaretFor(state *screen.State, cell CellSize) ui.Rect {
	if state == nil {
		return ui.Rect{}
	}
	row, col := state.Cursor.Row, state.Cursor.Col
	if placement, ok := state.Placements[state.Cursor.Grid]; ok {
		switch placement.Kind {
		case screen.PlacedWin:
			row += placement.Row
			col += placement.Col
		case screen.PlacedFloat:
			row += placement.ScreenRow
			col += placement.ScreenCol
		case screen.PlacedExternal:
			return ui.Rect{}
		}
	} else if state.Msg != nil && state.Msg.Grid == state.Cursor.Grid {
		row += state.Msg.Row
	}
	return handlerCaret(h, row, col, cell)
}
