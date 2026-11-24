package protocol_test

import (
	"testing"

	"nvgui/protocol"
)

func TestMouseEventsAndOptionSetParsing(t *testing.T) {
	if got := (protocol.MouseOn{}).EventName(); got != "mouse_on" {
		t.Fatalf("MouseOn.EventName() = %q, want %q", got, "mouse_on")
	}
	if got := (protocol.MouseOff{}).EventName(); got != "mouse_off" {
		t.Fatalf("MouseOff.EventName() = %q, want %q", got, "mouse_off")
	}

	raw := []any{
		[]any{"mouse_on", []any{}},
		[]any{"mouse_off", []any{}},
		[]any{"option_set", []any{"mousemoveevent", true}},
		[]any{"option_set", []any{"mousemoveevent", false}},
	}
	events, err := protocol.DecodeRedraw(raw)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("DecodeRedraw len = %d, want 4", len(events))
	}
	for index, ev := range events {
		if _, isUnknown := ev.(protocol.Unknown); !isUnknown {
			t.Fatalf("event %d (%s) decoded as %T, want protocol.Unknown to preserve locks/21.sha256", index, ev.EventName(), ev)
		}
	}

	if enabled, ok := protocol.ParseMouseEvent(events[0]); !ok || !enabled {
		t.Fatalf("ParseMouseEvent(decoded mouse_on) = (%v, %v), want (true, true)", enabled, ok)
	}
	if enabled, ok := protocol.ParseMouseEvent(events[1]); !ok || enabled {
		t.Fatalf("ParseMouseEvent(decoded mouse_off) = (%v, %v), want (false, true)", enabled, ok)
	}
	if enabled, ok := protocol.ParseMouseEvent(protocol.MouseOn{}); !ok || !enabled {
		t.Fatalf("ParseMouseEvent(MouseOn) = (%v, %v), want (true, true)", enabled, ok)
	}
	if enabled, ok := protocol.ParseMouseEvent(protocol.MouseOff{}); !ok || enabled {
		t.Fatalf("ParseMouseEvent(MouseOff) = (%v, %v), want (false, true)", enabled, ok)
	}
	for _, nonMouse := range []protocol.Event{
		nil,
		protocol.Flush{},
		protocol.Unknown{Name: "busy_start"},
	} {
		if enabled, ok := protocol.ParseMouseEvent(nonMouse); ok || enabled {
			t.Fatalf("ParseMouseEvent(%#v) = (%v, %v), want (false, false)", nonMouse, enabled, ok)
		}
	}

	if name, value, ok := protocol.ParseOptionSet(events[2]); !ok || name != "mousemoveevent" || value != true {
		t.Fatalf("ParseOptionSet(mousemoveevent=true) = (%q, %#v, %v), want (\"mousemoveevent\", true, true)", name, value, ok)
	}
	if enabled, ok := protocol.ParseMouseMoveEventOption(events[2]); !ok || !enabled {
		t.Fatalf("ParseMouseMoveEventOption(true) = (%v, %v), want (true, true)", enabled, ok)
	}
	if enabled, ok := protocol.ParseMouseMoveEventOption(events[3]); !ok || enabled {
		t.Fatalf("ParseMouseMoveEventOption(false) = (%v, %v), want (false, true)", enabled, ok)
	}
	for _, bad := range []protocol.Event{
		nil,
		protocol.Flush{},
		protocol.Unknown{Name: "option_set", Args: []any{"guifont", "Menlo:h14"}},
		protocol.Unknown{Name: "option_set", Args: []any{"mousemoveevent", "not-a-bool"}},
	} {
		if enabled, ok := protocol.ParseMouseMoveEventOption(bad); ok || enabled {
			t.Fatalf("ParseMouseMoveEventOption(%#v) = (%v, %v), want (false, false)", bad, enabled, ok)
		}
	}
}
