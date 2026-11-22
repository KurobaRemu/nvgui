package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/neovim/go-client/msgpack"
)

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

type captureReader struct {
	source *bytes.Reader
	raw    []byte
}

func (reader *captureReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	count, err := reader.source.Read(buffer)
	reader.raw = append(reader.raw, buffer[:count]...)
	return count, err
}

var handleExtensions = msgpack.ExtensionMap{
	0: func(data []byte) (any, error) { return decodeHandle(0, data) },
	1: func(data []byte) (any, error) { return decodeHandle(1, data) },
	2: func(data []byte) (any, error) { return decodeHandle(2, data) },
}

func decodeHandle(kind int8, data []byte) (Handle, error) {
	id, err := decodeExt(data)
	if err != nil {
		return Handle{}, err
	}
	return Handle{Kind: kind, ID: id}, nil
}

func decodeExt(data []byte) (int64, error) {
	switch {
	case len(data) == 1 && data[0] <= 0x7f:
		return int64(data[0]), nil
	case len(data) == 2 && data[0] == 0xcc:
		return int64(data[1]), nil
	case len(data) == 3 && data[0] == 0xcd:
		return int64(uint16(data[2]) | uint16(data[1])<<8), nil
	case len(data) == 5 && data[0] == 0xce:
		return int64(uint32(data[4]) | uint32(data[3])<<8 | uint32(data[2])<<16 | uint32(data[1])<<24), nil
	case len(data) == 2 && data[0] == 0xd0:
		return int64(int8(data[1])), nil
	case len(data) == 3 && data[0] == 0xd1:
		return int64(int16(uint16(data[2]) | uint16(data[1])<<8)), nil
	case len(data) == 5 && data[0] == 0xd2:
		return int64(int32(uint32(data[4]) | uint32(data[3])<<8 | uint32(data[2])<<16 | uint32(data[1])<<24)), nil
	case len(data) == 1 && data[0] >= 0xe0:
		return int64(int8(data[0])), nil
	default:
		return 0, fmt.Errorf("protocol: error decoding extension bytes %x", data)
	}
}

func ReadMessages(data []byte) ([]Message, error) {
	messages := []Message{}
	position := 0
	for position < len(data) {
		start := position
		reader := &captureReader{source: bytes.NewReader(data[position:])}
		decoder := msgpack.NewDecoder(reader)
		decoder.SetExtensions(handleExtensions)
		var fields []any
		if err := decoder.Decode(&fields); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return messages, fmt.Errorf("incomplete message at byte %d: %w", start, err)
			}
			return nil, fmt.Errorf("decode message at byte %d: %w", start, err)
		}
		raw := append([]byte(nil), reader.raw...)
		position += len(raw)
		if len(fields) < 1 {
			return nil, fmt.Errorf("message at byte %d has no type", start)
		}
		messageType, err := asInt(fields[0])
		if err != nil {
			return nil, fmt.Errorf("message type at byte %d: %w", start, err)
		}
		message := Message{Type: int(messageType), Raw: raw}
		switch message.Type {
		case 0:
			if len(fields) < 4 {
				return nil, fmt.Errorf("request at byte %d has %d fields", start, len(fields))
			}
			message.Method, err = asString(fields[2])
			if err != nil {
				return nil, fmt.Errorf("request method at byte %d: %w", start, err)
			}
			message.Args, err = asAnySlice(fields[3])
			if err != nil {
				return nil, fmt.Errorf("request args at byte %d: %w", start, err)
			}
		case 1:
			message.Args = nil
		case 2:
			if len(fields) < 3 {
				return nil, fmt.Errorf("notification at byte %d has %d fields", start, len(fields))
			}
			message.Method, err = asString(fields[1])
			if err != nil {
				return nil, fmt.Errorf("notification method at byte %d: %w", start, err)
			}
			message.Args, err = asAnySlice(fields[2])
			if err != nil {
				return nil, fmt.Errorf("notification args at byte %d: %w", start, err)
			}
		default:
			return nil, fmt.Errorf("message at byte %d has unknown type %d", start, message.Type)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func DecodeRedraw(args []any) ([]Event, error) {
	events := make([]Event, 0, len(args))
	for _, rawGroup := range args {
		group, ok := rawGroup.([]any)
		if !ok {
			return nil, fmt.Errorf("redraw event is %T, want array", rawGroup)
		}
		if len(group) < 2 {
			return nil, fmt.Errorf("redraw event has %d elements, want name and parameter tuples", len(group))
		}
		name, ok := group[0].(string)
		if !ok {
			return nil, fmt.Errorf("redraw event name is %T, want string", group[0])
		}
		for index := 1; index < len(group); index++ {
			parameterTuple, ok := group[index].([]any)
			if !ok {
				return nil, fmt.Errorf("%s parameter tuple %d is %T, want array", name, index-1, group[index])
			}
			event, err := decodeEvent(name, parameterTuple)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

func decodeEvent(name string, args []any) (Event, error) {
	switch name {
	case "grid_resize":
		if err := requireArgs(name, args, 3); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:3])
		if err != nil {
			return nil, err
		}
		return GridResize{Grid: values[0], Width: values[1], Height: values[2]}, nil
	case "grid_clear":
		if err := requireArgs(name, args, 1); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:1])
		if err != nil {
			return nil, err
		}
		return GridClear{Grid: values[0]}, nil
	case "grid_destroy":
		if err := requireArgs(name, args, 1); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:1])
		if err != nil {
			return nil, err
		}
		return GridDestroy{Grid: values[0]}, nil
	case "grid_cursor_goto":
		if err := requireArgs(name, args, 3); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:3])
		if err != nil {
			return nil, err
		}
		return GridCursorGoto{Grid: values[0], Row: values[1], Col: values[2]}, nil
	case "grid_line":
		return decodeGridLine(args)
	case "grid_scroll":
		if err := requireArgs(name, args, 7); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:7])
		if err != nil {
			return nil, err
		}
		return GridScroll{
			Grid: values[0], Top: values[1], Bot: values[2], Left: values[3],
			Right: values[4], Rows: values[5], Cols: values[6],
		}, nil
	case "hl_attr_define":
		return decodeHlAttrDefine(args)
	case "default_colors_set":
		if err := requireArgs(name, args, 5); err != nil {
			return nil, err
		}
		values, err := int64s(name, args[:5])
		if err != nil {
			return nil, err
		}
		return DefaultColorsSet{
			RGBFG: values[0], RGBBG: values[1], RGBSP: values[2],
			CtermFG: values[3], CtermBG: values[4],
		}, nil
	case "mode_info_set":
		return decodeModeInfoSet(args)
	case "mode_change":
		if err := requireArgs(name, args, 2); err != nil {
			return nil, err
		}
		mode, err := asString(args[0])
		if err != nil {
			return nil, fmt.Errorf("mode_change mode: %w", err)
		}
		values, err := ints(name, args[1:2])
		if err != nil {
			return nil, err
		}
		return ModeChange{Mode: mode, ModeIdx: values[0]}, nil
	case "win_pos":
		if err := requireArgs(name, args, 6); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:6], 1)
		if err != nil {
			return nil, err
		}
		return WinPos{
			Grid: values[0], Win: values[1], StartRow: values[2], StartCol: values[3],
			Width: values[4], Height: values[5],
		}, nil
	case "win_float_pos":
		return decodeWinFloatPos(args)
	case "win_external_pos":
		if err := requireArgs(name, args, 2); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:2], 1)
		if err != nil {
			return nil, err
		}
		return WinExternalPos{Grid: values[0], Win: values[1]}, nil
	case "win_hide":
		if err := requireArgs(name, args, 1); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:1])
		if err != nil {
			return nil, err
		}
		return WinHide{Grid: values[0]}, nil
	case "win_close":
		if err := requireArgs(name, args, 1); err != nil {
			return nil, err
		}
		values, err := ints(name, args[:1])
		if err != nil {
			return nil, err
		}
		return WinClose{Grid: values[0]}, nil
	case "msg_set_pos":
		return decodeMsgSetPos(args)
	case "flush":
		return Flush{}, nil
	default:
		return Unknown{Name: name, Args: append([]any(nil), args...)}, nil
	}
}

func decodeGridLine(args []any) (Event, error) {
	const name = "grid_line"
	if err := requireArgs(name, args, 5); err != nil {
		return nil, err
	}
	values, err := ints(name, args[:3])
	if err != nil {
		return nil, err
	}
	rawCells, ok := args[3].([]any)
	if !ok {
		return nil, fmt.Errorf("grid_line cells are %T, want array", args[3])
	}
	wrap, err := asBool(args[4])
	if err != nil {
		return nil, fmt.Errorf("grid_line wrap: %w", err)
	}
	cells := make([]LineCell, 0, len(rawCells))
	highlight := 0
	for index, rawCell := range rawCells {
		triple, ok := rawCell.([]any)
		if !ok || len(triple) == 0 || len(triple) > 3 {
			return nil, fmt.Errorf("grid_line cell %d has %d fields, want 1-3", index, len(triple))
		}
		text, err := asString(triple[0])
		if err != nil {
			return nil, fmt.Errorf("grid_line cell %d text: %w", index, err)
		}
		if len(triple) > 1 && triple[1] != nil {
			value, err := asInt(triple[1])
			if err != nil {
				return nil, fmt.Errorf("grid_line cell %d hl: %w", index, err)
			}
			highlight = int(value)
		}
		repeat := 1
		if len(triple) > 2 && triple[2] != nil {
			value, err := asInt(triple[2])
			if err != nil {
				return nil, fmt.Errorf("grid_line cell %d repeat: %w", index, err)
			}
			if value < 0 {
				return nil, fmt.Errorf("grid_line cell %d repeat is negative: %d", index, value)
			}
			repeat = int(value)
		}
		cells = append(cells, LineCell{Text: text, HL: highlight, Repeat: repeat})
	}
	return GridLine{
		Grid: values[0], Row: values[1], ColStart: values[2],
		Cells: cells, Wrap: wrap,
	}, nil
}

func decodeHlAttrDefine(args []any) (Event, error) {
	const name = "hl_attr_define"
	if err := requireArgs(name, args, 2); err != nil {
		return nil, err
	}
	values, err := ints(name, args[:1])
	if err != nil {
		return nil, err
	}
	rgb, err := normalizeIntegers(args[1])
	if err != nil {
		return nil, fmt.Errorf("hl_attr_define rgb: %w", err)
	}
	attributes, ok := rgb.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hl_attr_define rgb is %T, want map", args[1])
	}
	return HlAttrDefine{ID: values[0], RGB: attributes}, nil
}

func decodeModeInfoSet(args []any) (Event, error) {
	const name = "mode_info_set"
	if err := requireArgs(name, args, 2); err != nil {
		return nil, err
	}
	enabled, err := asBool(args[0])
	if err != nil {
		return nil, fmt.Errorf("mode_info_set cursor_style_enabled: %w", err)
	}
	rawModes, ok := args[1].([]any)
	if !ok {
		return nil, fmt.Errorf("mode_info_set mode_info is %T, want array", args[1])
	}
	modes := make([]map[string]any, 0, len(rawModes))
	for index, rawMode := range rawModes {
		value, err := normalizeIntegers(rawMode)
		if err != nil {
			return nil, fmt.Errorf("mode_info_set mode %d: %w", index, err)
		}
		mode, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mode_info_set mode %d is %T, want map", index, rawMode)
		}
		modes = append(modes, mode)
	}
	return ModeInfoSet{CursorStyleEnabled: enabled, ModeInfo: modes}, nil
}

func decodeWinFloatPos(args []any) (Event, error) {
	const name = "win_float_pos"
	if err := requireArgs(name, args, 11); err != nil {
		return nil, err
	}
	integerValues, err := ints(name, []any{args[0], args[1], args[3], args[7], args[8], args[9], args[10]}, 1)
	if err != nil {
		return nil, err
	}
	anchor, err := asString(args[2])
	if err != nil {
		return nil, fmt.Errorf("win_float_pos anchor: %w", err)
	}
	anchorRow, err := asFloat(args[4])
	if err != nil {
		return nil, fmt.Errorf("win_float_pos anchor_row: %w", err)
	}
	anchorCol, err := asFloat(args[5])
	if err != nil {
		return nil, fmt.Errorf("win_float_pos anchor_col: %w", err)
	}
	mouseEnabled, err := asBool(args[6])
	if err != nil {
		return nil, fmt.Errorf("win_float_pos mouse_enabled: %w", err)
	}
	return WinFloatPos{
		Grid: integerValues[0], Win: integerValues[1], Anchor: anchor,
		AnchorGrid: integerValues[2], AnchorRow: anchorRow, AnchorCol: anchorCol,
		MouseEnabled: mouseEnabled, ZIndex: integerValues[3], CompIndex: integerValues[4],
		ScreenRow: integerValues[5], ScreenCol: integerValues[6],
	}, nil
}

func decodeMsgSetPos(args []any) (Event, error) {
	const name = "msg_set_pos"
	if err := requireArgs(name, args, 6); err != nil {
		return nil, err
	}
	values, err := ints(name, []any{args[0], args[1], args[4], args[5]})
	if err != nil {
		return nil, err
	}
	scrolled, err := asBool(args[2])
	if err != nil {
		return nil, fmt.Errorf("msg_set_pos scrolled: %w", err)
	}
	sepChar, err := asString(args[3])
	if err != nil {
		return nil, fmt.Errorf("msg_set_pos sep_char: %w", err)
	}
	return MsgSetPos{
		Grid: values[0], Row: values[1], Scrolled: scrolled, SepChar: sepChar,
		ZIndex: values[2], CompIndex: values[3],
	}, nil
}

func requireArgs(name string, args []any, want int) error {
	if len(args) < want {
		return fmt.Errorf("%s has %d arguments, want at least %d", name, len(args), want)
	}
	return nil
}

func ints(name string, args []any, handlePositions ...int) ([]int, error) {
	handle := map[int]bool{}
	for _, position := range handlePositions {
		handle[position] = true
	}
	values := make([]int, len(args))
	for index, arg := range args {
		if handle[index] {
			value, err := handleID(arg)
			if err != nil {
				return nil, fmt.Errorf("%s argument %d: %w", name, index, err)
			}
			values[index] = value
			continue
		}
		value, err := asInt(arg)
		if err != nil {
			return nil, fmt.Errorf("%s argument %d: %w", name, index, err)
		}
		values[index] = int(value)
	}
	return values, nil
}

func int64s(name string, args []any) ([]int64, error) {
	values := make([]int64, len(args))
	for index, arg := range args {
		value, err := asInt(arg)
		if err != nil {
			return nil, fmt.Errorf("%s argument %d: %w", name, index, err)
		}
		values[index] = value
	}
	return values, nil
}

func asAnySlice(value any) ([]any, error) {
	if value == nil {
		return nil, nil
	}
	slice, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("value is %T, want array", value)
	}
	return slice, nil
}

func asString(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	default:
		return "", fmt.Errorf("value is %T, want string", value)
	}
}

func asBool(value any) (bool, error) {
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("value is %T, want bool", value)
	}
	return boolean, nil
}

func asInt(value any) (int64, error) {
	switch value := value.(type) {
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		if uint64(value) > math.MaxInt64 {
			return 0, fmt.Errorf("uint %d overflows int64", value)
		}
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		if value > math.MaxInt64 {
			return 0, fmt.Errorf("uint64 %d overflows int64", value)
		}
		return int64(value), nil
	case float32:
		return integralFloat(float64(value))
	case float64:
		return integralFloat(value)
	default:
		return 0, fmt.Errorf("value is %T, want integer", value)
	}
}

func asFloat(value any) (float64, error) {
	switch value := value.(type) {
	case float32:
		return float64(value), nil
	case float64:
		return value, nil
	default:
		integer, err := asInt(value)
		if err != nil {
			return 0, fmt.Errorf("value is %T, want number", value)
		}
		return float64(integer), nil
	}
}

func integralFloat(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value > math.MaxInt64 || value < math.MinInt64 {
		return 0, fmt.Errorf("float %v is not an int64", value)
	}
	return int64(value), nil
}

func handleID(value any) (int, error) {
	switch value := value.(type) {
	case Handle:
		return int(value.ID), nil
	default:
		number, err := asInt(value)
		if err != nil {
			return 0, fmt.Errorf("window handle is %T, want handle or integer", value)
		}
		return int(number), nil
	}
}

func normalizeIntegers(value any) (any, error) {
	switch value := value.(type) {
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case uint:
		if uint64(value) > math.MaxInt64 {
			return nil, fmt.Errorf("uint %d overflows int64", value)
		}
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		if value > math.MaxInt64 {
			return nil, fmt.Errorf("uint64 %d overflows int64", value)
		}
		return int64(value), nil
	case float32:
		return integralFloat(float64(value))
	case float64:
		return integralFloat(value)
	case []any:
		items := make([]any, len(value))
		for index, item := range value {
			normalized, err := normalizeIntegers(item)
			if err != nil {
				return nil, err
			}
			items[index] = normalized
		}
		return items, nil
	case map[string]any:
		attributes := make(map[string]any, len(value))
		for key, item := range value {
			normalized, err := normalizeIntegers(item)
			if err != nil {
				return nil, err
			}
			attributes[key] = normalized
		}
		return attributes, nil
	case Handle:
		return value, nil
	default:
		return value, nil
	}
}
