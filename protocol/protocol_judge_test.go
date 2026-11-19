package protocol_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/neovim/go-client/msgpack"

	"nvgui/oracle"
	"nvgui/protocol"
)

func TestGoldenMessagesAndRedraws(t *testing.T) {
	for _, golden := range oracle.List() {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			messages, err := protocol.ReadMessages(golden.Inbound)
			if err != nil {
				t.Fatalf("ReadMessages: %v", err)
			}
			var joined []byte
			redrawCount := 0
			knownEvents := map[string]bool{}
			for _, message := range messages {
				joined = append(joined, message.Raw...)
				if message.Method != "redraw" || message.Type != 2 {
					continue
				}
				redrawCount++
				events, err := protocol.DecodeRedraw(message.Args)
				if err != nil {
					t.Fatalf("DecodeRedraw for redraw %d: %v", redrawCount, err)
				}
				for _, event := range events {
					if _, isUnknown := event.(protocol.Unknown); isUnknown {
						t.Fatalf("redraw %d decoded a known event as Unknown: %#v", redrawCount, event)
					}
					knownEvents[event.EventName()] = true
				}
			}
			if !bytes.Equal(joined, golden.Inbound) {
				t.Fatalf("Raw concatenation differs from inbound: got %d bytes, want %d", len(joined), len(golden.Inbound))
			}
			if redrawCount != golden.Meta.RedrawCount {
				t.Fatalf("redraw count = %d, want %d", redrawCount, golden.Meta.RedrawCount)
			}
			if knownEvents["unknown"] || knownEvents["Unknown"] {
				t.Fatal("a known redraw event decoded with name Unknown")
			}
			if !knownEvents["flush"] {
				t.Fatal("no flush event decoded")
			}
		})
	}
}

func TestDecodeRedrawForwardCompatibility(t *testing.T) {
	golden := firstGolden(t)
	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	for messageIndex, message := range messages {
		if message.Method != "redraw" || message.Type != 2 {
			continue
		}
		baseline, err := protocol.DecodeRedraw(message.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw baseline message %d: %v", messageIndex, err)
		}
		extended := append([]any(nil), message.Args...)
		for eventIndex := range extended {
			group, ok := extended[eventIndex].([]any)
			if !ok || len(group) == 0 {
				continue
			}
			augmented := append([]any(nil), group...)
			augmented = append(augmented, int64(0x5eed))
			extended[eventIndex] = augmented
		}
		got, err := protocol.DecodeRedraw(extended)
		if err != nil {
			t.Fatalf("DecodeRedraw with appended future parameters message %d: %v", messageIndex, err)
		}
		if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("decode changed with appended parameters at message %d:\n got %#v\nwant %#v", messageIndex, got, baseline)
		}
	}

	unknown, err := protocol.DecodeRedraw([]any{[]any{"future_ui_event", int64(7), "extra"}})
	if err != nil {
		t.Fatalf("DecodeRedraw unknown name: %v", err)
	}
	if len(unknown) != 1 {
		t.Fatalf("unknown event count = %d, want 1", len(unknown))
	}
	if _, ok := unknown[0].(protocol.Unknown); !ok {
		t.Fatalf("unknown event type = %T, want protocol.Unknown", unknown[0])
	}
	if unknown[0].EventName() != "future_ui_event" {
		t.Fatalf("unknown event name = %q", unknown[0].EventName())
	}
}

func TestDecodeRedrawMalformedEvents(t *testing.T) {
	malformed := [][]any{
		{[]any{"grid_resize", int64(1)}},
		{[]any{"grid_line", int64(1), int64(0), int64(0), "not-cells", false}},
		{[]any{"grid_line", int64(1), int64(0), int64(0), []any{[]any{}}, false}},
		{[]any{"hl_attr_define", int64(1), "not-a-map"}},
		{[]any{"default_colors_set", "not-an-int"}},
		{[]any{"mode_info_set", int64(1)}},
		{[]any{"mode_change", int64(1), int64(0)}},
		{[]any{"win_float_pos", int64(1), int64(1000), "NW", int64(1), int64(0), int64(0), true}},
		{[]any{"msg_set_pos", int64(3), int64(22), false}},
		{[]any{int64(123), int64(1)}},
	}
	for index, args := range malformed {
		args := args
		t.Run(fmt.Sprintf("case-%02d", index), func(t *testing.T) {
			assertDecodeError(t, args)
		})
	}
}

func TestDecodeRedrawRandomTruncations(t *testing.T) {
	golden := firstGolden(t)
	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	random := rand.New(rand.NewSource(100))
	for seed := 0; seed < 100; seed++ {
		message := messages[random.Intn(len(messages))]
		payload := msgpackEncodeAny(t, message.Args)
		if len(payload) > 1 {
			payload = payload[:1+random.Intn(len(payload)-1)]
		}
		var truncated any
		decoder := msgpack.NewDecoder(bytes.NewReader(payload))
		_ = decoder.Decode(&truncated)
		var args []any
		switch value := truncated.(type) {
		case []any:
			args = value
		default:
			args = []any{truncated}
		}
		assertDecodeError(t, args)
	}
}

func TestGridLineExplicitHighlight(t *testing.T) {
	args := []any{
		[]any{
			"grid_line", int64(1), int64(3), int64(2),
			[]any{
				[]any{"a", int64(7), int64(2)},
				[]any{"b"},
				[]any{"", int64(9)},
				[]any{"c"},
			},
			false,
		},
	}
	events, err := protocol.DecodeRedraw(args)
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
	if line.Grid != 1 || line.Row != 3 || line.ColStart != 2 {
		t.Fatalf("grid/row/col = %d/%d/%d, want 1/3/2", line.Grid, line.Row, line.ColStart)
	}
	wantByCell := []protocol.LineCell{
		{Text: "a", HL: 7},
		{Text: "a", HL: 7},
		{Text: "b", HL: 7},
		{Text: "", HL: 9},
		{Text: "c", HL: 9},
	}
	var expanded []protocol.LineCell
	for index, cell := range line.Cells {
		if cell.Repeat < 1 {
			t.Fatalf("cell %d Repeat = %d, want >= 1", index, cell.Repeat)
		}
		for count := 0; count < cell.Repeat; count++ {
			expanded = append(expanded, protocol.LineCell{Text: cell.Text, HL: cell.HL})
		}
	}
	if !reflect.DeepEqual(expanded, wantByCell) {
		t.Fatalf("expanded cells = %#v, want %#v", expanded, wantByCell)
	}
	if line.Wrap {
		t.Fatal("wrap = true, want false")
	}
}

func TestHandleExtensionIDs(t *testing.T) {
	for _, kind := range []int8{0, 1, 2} {
		payload := []byte{0xd2, 0, 0, 0x12, 0x34}
		message := msgpackEncodeAny(t, []any{int64(2), "redraw", []any{
			msgpackExtension{kind: int(kind), data: payload},
		}})
		messages, err := protocol.ReadMessages(message)
		if err != nil {
			t.Fatalf("ReadMessages with extension %d: %v", kind, err)
		}
		if len(messages) != 1 {
			t.Fatalf("message count = %d, want 1", len(messages))
		}
		handle, ok := messages[0].Args[0].(protocol.Handle)
		if !ok {
			t.Fatalf("extension value type = %T, want protocol.Handle", messages[0].Args[0])
		}
		if handle.Kind != kind || handle.ID != 0x1234 {
			t.Fatalf("extension %d decoded as %+v, want ID 0x1234", kind, handle)
		}
	}
}

func assertDecodeError(t *testing.T, args []any) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("DecodeRedraw panicked: %v", recovered)
		}
	}()
	events, err := protocol.DecodeRedraw(args)
	if err == nil {
		t.Fatalf("DecodeRedraw returned %#v, want error", events)
	}
}

func firstGolden(t *testing.T) oracle.Golden {
	t.Helper()
	goldens := oracle.List()
	if len(goldens) == 0 {
		t.Fatal("oracle.List() returned no goldens")
	}
	return goldens[0]
}

type msgpackExtension struct {
	kind int
	data []byte
}

func (extension msgpackExtension) MarshalMsgPack(encoder *msgpack.Encoder) error {
	return encoder.PackExtension(extension.kind, extension.data)
}

func msgpackEncodeAny(t *testing.T, value any) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := msgpack.NewEncoder(&buffer).Encode(value); err != nil {
		t.Fatalf("msgpack encode: %v", err)
	}
	return buffer.Bytes()
}
