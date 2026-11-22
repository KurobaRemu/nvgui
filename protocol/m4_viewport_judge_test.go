package protocol_test

import (
	"math"
	"testing"

	"nvgui/protocol"
)

func TestM4Viewport_ParseWinViewport(t *testing.T) {
	t.Run("6 args with Handle window", func(t *testing.T) {
		ev := protocol.Unknown{
			Name: "win_viewport",
			Args: []any{
				int64(2),
				protocol.Handle{Kind: 2, ID: 1004},
				int64(10),
				int64(40),
				int64(15),
				int64(7),
			},
		}
		got, ok, err := protocol.ParseWinViewport(ev)
		if err != nil {
			t.Fatalf("ParseWinViewport(6 args): unexpected err: %v", err)
		}
		if !ok {
			t.Fatal("ParseWinViewport(6 args): ok = false, want true")
		}
		if got.Grid != 2 || got.Win != 1004 || got.TopLine != 10 || got.BotLine != 40 || got.CurLine != 15 || got.CurCol != 7 {
			t.Fatalf("ParseWinViewport(6 args) = %+v, want grid=2 win=1004 top=10 bot=40 cur=(15,7)", got)
		}
		if got.HasLineCount || got.HasScrollDelta || got.ScrollDelta != 0 {
			t.Fatalf("ParseWinViewport(6 args) optional flags = %+v, want HasLineCount=false HasScrollDelta=false", got)
		}
	})

	t.Run("7 args with integer window and line_count", func(t *testing.T) {
		ev := protocol.Unknown{
			Name: "win_viewport",
			Args: []any{
				int64(3),
				int64(1009),
				int64(0),
				int64(24),
				int64(5),
				int64(1),
				int64(250),
			},
		}
		got, ok, err := protocol.ParseWinViewport(ev)
		if err != nil || !ok {
			t.Fatalf("ParseWinViewport(7 args) = (%+v, %v, %v), want ok=true err=nil", got, ok, err)
		}
		if got.Win != 1009 || !got.HasLineCount || got.LineCount != 250 || got.HasScrollDelta {
			t.Fatalf("ParseWinViewport(7 args) = %+v, want win=1009 line_count=250 HasLineCount=true HasScrollDelta=false", got)
		}
	})

	t.Run("8 args with fractional float scroll_delta and Handle window", func(t *testing.T) {
		ev := protocol.Unknown{
			Name: "win_viewport",
			Args: []any{
				int64(1),
				protocol.Handle{Kind: 2, ID: 1000},
				int64(20),
				int64(50),
				int64(25),
				int64(4),
				int64(500),
				float64(1.5),
			},
		}
		got, ok, err := protocol.ParseWinViewport(ev)
		if err != nil || !ok {
			t.Fatalf("ParseWinViewport(8 args) = (%+v, %v, %v), want ok=true err=nil", got, ok, err)
		}
		if got.Grid != 1 || got.Win != 1000 || !got.HasLineCount || got.LineCount != 500 || !got.HasScrollDelta || math.Abs(got.ScrollDelta-1.5) > 1e-9 {
			t.Fatalf("ParseWinViewport(8 args) = %+v, want line_count=500 scroll_delta=1.5", got)
		}
	})

	t.Run("negative fractional scroll_delta with trailing unknown fields", func(t *testing.T) {
		ev := protocol.Unknown{
			Name: "win_viewport",
			Args: []any{
				int64(1),
				protocol.Handle{Kind: 2, ID: 1000},
				int64(18),
				int64(48),
				int64(20),
				int64(0),
				int64(500),
				float64(-0.5),
				map[string]any{"future": true},
				"extra-tail",
			},
		}
		got, ok, err := protocol.ParseWinViewport(ev)
		if err != nil || !ok {
			t.Fatalf("ParseWinViewport(8+tail) = (%+v, %v, %v), want ok=true err=nil", got, ok, err)
		}
		if !got.HasScrollDelta || math.Abs(got.ScrollDelta-(-0.5)) > 1e-9 {
			t.Fatalf("ParseWinViewport(8+tail) ScrollDelta = %v (has=%v), want -0.5", got.ScrollDelta, got.HasScrollDelta)
		}
	})

	t.Run("Neovim33353 map future payload after 6 required args does not fail", func(t *testing.T) {
		ev := protocol.Unknown{
			Name: "win_viewport",
			Args: []any{
				int64(1),
				protocol.Handle{Kind: 2, ID: 1000},
				int64(3),
				int64(7),
				int64(11),
				int64(13),
				map[string]any{"future": true},
				[]any{"more"},
			},
		}
		got, ok, err := protocol.ParseWinViewport(ev)
		if err != nil {
			t.Fatalf("ParseWinViewport(Neovim33353 future tail): unexpected err: %v", err)
		}
		if !ok {
			t.Fatal("ParseWinViewport(Neovim33353 future tail): ok = false, want true")
		}
		if got.Grid != 1 || got.Win != 1000 || got.TopLine != 3 || got.BotLine != 7 || got.CurLine != 11 || got.CurCol != 13 {
			t.Fatalf("ParseWinViewport(Neovim33353) = %+v, want required fields preserved", got)
		}
		if got.HasLineCount || got.HasScrollDelta {
			t.Fatalf("ParseWinViewport(Neovim33353) optional flags = %+v, want false for non-numeric future args", got)
		}
	})

	t.Run("non win_viewport events return zero false nil", func(t *testing.T) {
		for _, ev := range []protocol.Event{
			nil,
			protocol.Flush{},
			protocol.GridResize{Grid: 1, Width: 80, Height: 24},
			protocol.Unknown{Name: "option_set", Args: []any{"guifont", "Menlo:h14"}},
		} {
			got, ok, err := protocol.ParseWinViewport(ev)
			if err != nil || ok || got != (protocol.WinViewport{}) {
				t.Fatalf("ParseWinViewport(%T) = (%+v, %v, %v), want (zero, false, nil)", ev, got, ok, err)
			}
		}
	})

	t.Run("fewer than 6 args or invalid required args return explicit error", func(t *testing.T) {
		badCases := []protocol.Event{
			protocol.Unknown{Name: "win_viewport", Args: []any{int64(1), int64(1000), int64(0), int64(10), int64(1)}},
			protocol.Unknown{Name: "win_viewport", Args: []any{"bad-grid", int64(1000), int64(0), int64(10), int64(1), int64(0)}},
			protocol.Unknown{Name: "win_viewport", Args: []any{int64(1), "bad-win", int64(0), int64(10), int64(1), int64(0)}},
			protocol.Unknown{Name: "win_viewport", Args: []any{int64(1), int64(1000), "bad-top", int64(10), int64(1), int64(0)}},
		}
		for i, ev := range badCases {
			if _, _, err := protocol.ParseWinViewport(ev); err == nil {
				t.Errorf("bad case %d: ParseWinViewport returned nil error, want explicit error", i)
			}
		}
	})
}

func TestM4Viewport_ParseWinViewportMargins(t *testing.T) {
	ev := protocol.Unknown{
		Name: "win_viewport_margins",
		Args: []any{
			int64(4),
			protocol.Handle{Kind: 2, ID: 1007},
			int64(1),
			int64(2),
			int64(3),
			int64(4),
			"future-tail",
		},
	}
	got, ok, err := protocol.ParseWinViewportMargins(ev)
	if err != nil || !ok {
		t.Fatalf("ParseWinViewportMargins = (%+v, %v, %v), want ok=true err=nil", got, ok, err)
	}
	want := protocol.WinViewportMargins{Grid: 4, Win: 1007, Top: 1, Bottom: 2, Left: 3, Right: 4}
	if got != want {
		t.Fatalf("ParseWinViewportMargins = %+v, want %+v", got, want)
	}

	if got, ok, err := protocol.ParseWinViewportMargins(protocol.Flush{}); err != nil || ok || got != (protocol.WinViewportMargins{}) {
		t.Fatalf("ParseWinViewportMargins(Flush) = (%+v, %v, %v), want (zero, false, nil)", got, ok, err)
	}

	badCases := []protocol.Event{
		protocol.Unknown{Name: "win_viewport_margins", Args: []any{int64(4), int64(1007), int64(1), int64(2), int64(3)}},
		protocol.Unknown{Name: "win_viewport_margins", Args: []any{int64(4), int64(1007), "bad", int64(2), int64(3), int64(4)}},
	}
	for i, bad := range badCases {
		if _, _, err := protocol.ParseWinViewportMargins(bad); err == nil {
			t.Errorf("bad margins case %d: returned nil error, want explicit error", i)
		}
	}
}

func TestM4Viewport_ParseBusyAndOptionSet(t *testing.T) {
	if busy, ok := protocol.ParseBusyEvent(protocol.Unknown{Name: "busy_start"}); !ok || !busy {
		t.Fatalf("ParseBusyEvent(busy_start) = (%v, %v), want (true, true)", busy, ok)
	}
	if busy, ok := protocol.ParseBusyEvent(protocol.Unknown{Name: "busy_stop"}); !ok || busy {
		t.Fatalf("ParseBusyEvent(busy_stop) = (%v, %v), want (false, true)", busy, ok)
	}
	if busy, ok := protocol.ParseBusyEvent(protocol.Flush{}); ok || busy {
		t.Fatalf("ParseBusyEvent(Flush) = (%v, %v), want (false, false)", busy, ok)
	}
	if busy, ok := protocol.ParseBusyEvent(nil); ok || busy {
		t.Fatalf("ParseBusyEvent(nil) = (%v, %v), want (false, false)", busy, ok)
	}

	name, value, ok := protocol.ParseOptionSet(protocol.Unknown{
		Name: "option_set",
		Args: []any{"guifont", "JetBrains Mono:h14"},
	})
	if !ok || name != "guifont" || value != "JetBrains Mono:h14" {
		t.Fatalf("ParseOptionSet(guifont) = (%q, %#v, %v), want (\"guifont\", \"JetBrains Mono:h14\", true)", name, value, ok)
	}
	name, value, ok = protocol.ParseOptionSet(protocol.Unknown{
		Name: "option_set",
		Args: []any{"linespace", int64(2)},
	})
	if !ok || name != "linespace" || value != int64(2) {
		t.Fatalf("ParseOptionSet(linespace) = (%q, %#v, %v), want (\"linespace\", int64(2), true)", name, value, ok)
	}
	for _, nonOpt := range []protocol.Event{
		nil,
		protocol.Flush{},
		protocol.Unknown{Name: "option_set", Args: []any{"only_name"}},
		protocol.Unknown{Name: "option_set", Args: []any{int64(123), "val"}},
		protocol.Unknown{Name: "win_viewport", Args: []any{int64(1)}},
	} {
		if n, v, ok := protocol.ParseOptionSet(nonOpt); ok || n != "" || v != nil {
			t.Fatalf("ParseOptionSet(%#v) = (%q, %#v, %v), want (\"\", nil, false)", nonOpt, n, v, ok)
		}
	}
}

func TestM4Viewport_DecodeRedrawKeepsUnknown(t *testing.T) {
	raw := []any{
		[]any{"win_viewport", []any{
			int64(1), protocol.Handle{Kind: 2, ID: 1000}, int64(1), int64(25), int64(3), int64(2), int64(100), float64(2.5),
		}},
		[]any{"win_viewport_margins", []any{
			int64(2), protocol.Handle{Kind: 2, ID: 1002}, int64(1), int64(1), int64(1), int64(1),
		}},
		[]any{"busy_start", []any{}},
		[]any{"busy_stop", []any{}},
		[]any{"option_set", []any{"guifont", "Menlo:h15"}},
	}
	events, err := protocol.DecodeRedraw(raw)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("DecodeRedraw len = %d, want 5", len(events))
	}
	for i, ev := range events {
		if _, isUnknown := ev.(protocol.Unknown); !isUnknown {
			t.Fatalf("event %d (%s) is %T, want protocol.Unknown (must not break locks/21.sha256)", i, ev.EventName(), ev)
		}
	}
	vp, ok, err := protocol.ParseWinViewport(events[0])
	if err != nil || !ok || vp.ScrollDelta != 2.5 {
		t.Fatalf("ParseWinViewport(decoded) = (%+v, %v, %v), want scroll_delta=2.5", vp, ok, err)
	}
	margins, ok, err := protocol.ParseWinViewportMargins(events[1])
	if err != nil || !ok || margins.Top != 1 || margins.Right != 1 {
		t.Fatalf("ParseWinViewportMargins(decoded) = (%+v, %v, %v), want all margins=1", margins, ok, err)
	}
	if busy, ok := protocol.ParseBusyEvent(events[2]); !ok || !busy {
		t.Fatalf("ParseBusyEvent(decoded busy_start) = (%v, %v), want (true, true)", busy, ok)
	}
	if busy, ok := protocol.ParseBusyEvent(events[3]); !ok || busy {
		t.Fatalf("ParseBusyEvent(decoded busy_stop) = (%v, %v), want (false, true)", busy, ok)
	}
	if name, val, ok := protocol.ParseOptionSet(events[4]); !ok || name != "guifont" || val != "Menlo:h15" {
		t.Fatalf("ParseOptionSet(decoded) = (%q, %#v, %v), want guifont Menlo:h15", name, val, ok)
	}
}
