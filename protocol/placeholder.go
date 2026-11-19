package protocol

// placeholder

import "errors"

var errNotImplemented = errors.New("not implemented")

type Handle struct {
	Kind int8
	ID   int64
}

type Message struct {
	Type   int
	Method string
	Args   []any
	Raw    []byte
}

type Event interface{ EventName() string }

type LineCell struct {
	Text   string
	HL     int
	Repeat int
}

type GridResize struct{ Grid, Width, Height int }
type GridClear struct{ Grid int }
type GridDestroy struct{ Grid int }
type GridCursorGoto struct{ Grid, Row, Col int }
type GridLine struct {
	Grid, Row, ColStart int
	Cells               []LineCell
	Wrap                bool
}
type GridScroll struct{ Grid, Top, Bot, Left, Right, Rows, Cols int }
type HlAttrDefine struct {
	ID  int
	RGB map[string]any
}
type DefaultColorsSet struct{ RGBFG, RGBBG, RGBSP, CtermFG, CtermBG int64 }
type ModeInfoSet struct {
	CursorStyleEnabled bool
	ModeInfo           []map[string]any
}
type ModeChange struct {
	Mode    string
	ModeIdx int
}
type WinPos struct {
	Grid, Win, StartRow, StartCol, Width, Height int
}
type WinFloatPos struct {
	Grid, Win                               int
	Anchor                                  string
	AnchorGrid                              int
	AnchorRow, AnchorCol                    float64
	MouseEnabled                            bool
	ZIndex, CompIndex, ScreenRow, ScreenCol int
}
type WinExternalPos struct{ Grid, Win int }
type WinHide struct{ Grid int }
type WinClose struct{ Grid int }
type MsgSetPos struct {
	Grid, Row         int
	Scrolled          bool
	SepChar           string
	ZIndex, CompIndex int
}
type Flush struct{}
type Unknown struct {
	Name string
	Args []any
}

func (GridResize) EventName() string       { return "grid_resize" }
func (GridClear) EventName() string        { return "grid_clear" }
func (GridDestroy) EventName() string      { return "grid_destroy" }
func (GridCursorGoto) EventName() string   { return "grid_cursor_goto" }
func (GridLine) EventName() string         { return "grid_line" }
func (GridScroll) EventName() string       { return "grid_scroll" }
func (HlAttrDefine) EventName() string     { return "hl_attr_define" }
func (DefaultColorsSet) EventName() string { return "default_colors_set" }
func (ModeInfoSet) EventName() string      { return "mode_info_set" }
func (ModeChange) EventName() string       { return "mode_change" }
func (WinPos) EventName() string           { return "win_pos" }
func (WinFloatPos) EventName() string      { return "win_float_pos" }
func (WinExternalPos) EventName() string   { return "win_external_pos" }
func (WinHide) EventName() string          { return "win_hide" }
func (WinClose) EventName() string         { return "win_close" }
func (MsgSetPos) EventName() string        { return "msg_set_pos" }
func (Flush) EventName() string            { return "flush" }
func (event Unknown) EventName() string    { return event.Name }

func ReadMessages(data []byte) ([]Message, error) {
	return nil, errNotImplemented
}

func DecodeRedraw(args []any) ([]Event, error) {
	return nil, errNotImplemented
}
