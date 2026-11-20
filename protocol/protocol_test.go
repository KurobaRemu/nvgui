package protocol_test

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/neovim/go-client/msgpack"

	"nvgui/protocol"
)

func TestDecodeGridLineRepeat(t *testing.T) {
	tests := []struct {
		name string
		cell []any
		want int
	}{
		{name: "omitted", cell: []any{"a"}, want: 1},
		{name: "nil", cell: []any{"a", int64(7), nil}, want: 1},
		{name: "zero", cell: []any{"b", int64(0), int64(0)}, want: 0},
		{name: "positive", cell: []any{"c", int64(9), int64(3)}, want: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events, err := protocol.DecodeRedraw([]any{[]any{
				"grid_line", []any{int64(1), int64(0), int64(0), []any{test.cell}, false},
			}})
			if err != nil {
				t.Fatalf("DecodeRedraw: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("event count = %d, want 1", len(events))
			}
			line, ok := events[0].(protocol.GridLine)
			if !ok {
				t.Fatalf("event type = %T, want protocol.GridLine", events[0])
			}
			if len(line.Cells) != 1 {
				t.Fatalf("cell count = %d, want 1", len(line.Cells))
			}
			if line.Cells[0].Repeat != test.want {
				t.Fatalf("Repeat = %d, want %d", line.Cells[0].Repeat, test.want)
			}
		})
	}
}

func TestDecodeGridLineNegativeRepeat(t *testing.T) {
	_, err := protocol.DecodeRedraw([]any{[]any{
		"grid_line", []any{int64(1), int64(0), int64(0), []any{[]any{"a", int64(0), int64(-1)}}, false},
	}})
	if err == nil {
		t.Fatal("DecodeRedraw accepted a negative repeat")
	}
}

func TestDecodeGridLineHighlightInheritance(t *testing.T) {
	events, err := protocol.DecodeRedraw([]any{[]any{
		"grid_line", []any{
			int64(1), int64(0), int64(0),
			[]any{
				[]any{"a", int64(7), int64(2)},
				[]any{"b", int64(0), int64(0)},
				[]any{"c", nil, int64(1)},
				[]any{"d"},
			},
			false,
		},
	}})
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	line, ok := events[0].(protocol.GridLine)
	if !ok {
		t.Fatalf("event type = %T, want protocol.GridLine", events[0])
	}
	want := []protocol.LineCell{
		{Text: "a", HL: 7, Repeat: 2},
		{Text: "b", HL: 0, Repeat: 0},
		{Text: "c", HL: 0, Repeat: 1},
		{Text: "d", HL: 0, Repeat: 1},
	}
	if !reflect.DeepEqual(line.Cells, want) {
		t.Fatalf("cells = %#v, want %#v", line.Cells, want)
	}
}

func TestNeovimMessagesKeepExplicitZero(t *testing.T) {
	notification := []any{
		int64(2), "redraw", []any{
			[]any{"hl_attr_define", []any{int64(7), map[string]any{"foreground": int64(1)}}},
			[]any{"grid_line", []any{
				int64(1), int64(0), int64(0),
				[]any{
					[]any{"a", int64(7), int64(2)},
					[]any{" ", int64(0), int64(0)},
				},
				false,
			}},
		},
	}
	var buffer bytes.Buffer
	if err := msgpack.NewEncoder(&buffer).Encode(notification); err != nil {
		t.Fatalf("msgpack encode: %v", err)
	}
	messages, err := protocol.ReadMessages(buffer.Bytes())
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	events, err := protocol.DecodeRedraw(messages[0].Args)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	attribute, ok := events[0].(protocol.HlAttrDefine)
	if !ok || attribute.ID != 7 {
		t.Fatalf("first event = %#v, want HlAttrDefine{ID: 7}", events[0])
	}
	line, ok := events[1].(protocol.GridLine)
	if !ok {
		t.Fatalf("second event = %T, want protocol.GridLine", events[1])
	}
	wantCells := []protocol.LineCell{
		{Text: "a", HL: 7, Repeat: 2},
		{Text: " ", HL: 0, Repeat: 0},
	}
	if !reflect.DeepEqual(line.Cells, wantCells) {
		t.Fatalf("cells = %#v, want %#v", line.Cells, wantCells)
	}
}
