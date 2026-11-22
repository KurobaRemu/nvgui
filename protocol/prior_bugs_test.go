package protocol_test

// Covered P0 IDs: neovim#22344, neovide#1751, neovim#31316, neovim#33353.
// Deferred (16 covered + 7 deferred = 23/23): neovide#3047 (Kitty Text Sizing
// Protocol, phase 2), neovim#21458 (mouseshape, M2/phase 2), neovim#19559
// (detach/reattach, phase 2 remote nvim), neovim#24802 (upstream
// inccommand=split limitation), macvim#721 (legacy "Screen error" without a
// modern reproduction), goneovim#265 (terminal mouse click, M2 mouse),
// neovim#3929 (iTerm2 fullscreen + airline, terminal-only).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/neovim/go-client/msgpack"

	"nvgui/protocol"
	"nvgui/screen"
)

func TestPriorBug_Neovim22344_Neovide1751_NoiceMalformedAndUnsubscribedRedraw(t *testing.T) {
	cases := []struct {
		name        string
		args        []any
		want        []string
		wantUnknown int
	}{
		{
			name: "unsubscribed noice events",
			args: []any{
				[]any{"msg_show", []any{"cmdline", []any{}, int64(0), []any{}, "extra"}},
				[]any{"cmdline_show", []any{[]any{}, int64(0), ":", ":", int64(0), int64(1), "noice-tail"}},
				[]any{"popupmenu_show", []any{[]any{}, int64(0), int64(0), int64(0), int64(0), int64(99)}},
			},
			want:        []string{"msg_show", "cmdline_show", "popupmenu_show"},
			wantUnknown: 3,
		},
		{
			name: "unknown future ui option and event",
			args: []any{
				[]any{"ui_event_future", []any{map[string]any{"option": true}, []any{int64(7)}}},
				[]any{"option_set", []any{"future_option", true}},
			},
			want:        []string{"ui_event_future", "option_set"},
			wantUnknown: 2,
		},
		{
			name: "known event with future tail",
			args: []any{
				[]any{"grid_resize", []any{int64(1), int64(80), int64(24), "future"}},
				[]any{"flush", []any{}},
			},
			want:        []string{"grid_resize", "flush"},
			wantUnknown: 0,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			events, err := protocol.DecodeRedraw(test.args)
			if err != nil {
				t.Fatalf("DecodeRedraw: %v", err)
			}
			if len(events) != len(test.want) {
				t.Fatalf("events = %#v, want %d", events, len(test.want))
			}
			for index, event := range events {
				if event.EventName() != test.want[index] {
					t.Fatalf("event %d name = %q, want %q", index, event.EventName(), test.want[index])
				}
			}
			unknownCount := 0
			for _, event := range events {
				if unknown, ok := event.(protocol.Unknown); ok {
					unknownCount++
					if len(unknown.Args) == 0 {
						t.Fatalf("Unknown %q lost its parameter tuple", unknown.Name)
					}
				}
			}
			if unknownCount != test.wantUnknown {
				t.Fatalf("Unknown events = %d, want %d", unknownCount, test.wantUnknown)
			}
		})
	}

	malformed := [][]any{
		{[]any{"grid_resize", int64(1), int64(80), int64(24)}},
		{[]any{"msg_show", "flat", int64(0)}},
		{[]any{int64(42), []any{}}},
		{[]any{"grid_line", []any{int64(1), int64(0), int64(0), "not-cells", false}}},
		{"flat-redraw-event"},
	}
	for index, args := range malformed {
		t.Run("malformed", func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("DecodeRedraw case %d panicked: %v", index, recovered)
				}
			}()
			events, err := protocol.DecodeRedraw(args)
			if err == nil {
				t.Fatalf("DecodeRedraw case %d returned %#v, want explicit error", index, events)
			}
		})
	}
}

func TestPriorBug_Neovim31316_FragmentedMsgpackBoundaryAndConcurrentDecode(t *testing.T) {
	groups := []any{
		[]any{"grid_resize", []any{int64(1), int64(80), int64(24)}},
		[]any{"hl_attr_define", []any{int64(7), map[string]any{"foreground": int64(0xabcdef)}}},
		[]any{"grid_line", []any{
			int64(1), int64(0), int64(0),
			[]any{[]any{"A", int64(7), int64(3)}, []any{"B"}},
			false,
		}},
		[]any{"grid_cursor_goto", []any{int64(1), int64(2), int64(4)}},
		[]any{"flush", []any{}},
	}
	first := []any{int64(2), "redraw", groups[:3]}
	second := []any{int64(2), "redraw", groups[3:]}
	var stream bytes.Buffer
	for _, notification := range []any{first, second} {
		if err := msgpack.NewEncoder(&stream).Encode(notification); err != nil {
			t.Fatalf("msgpack encode: %v", err)
		}
	}
	wire := stream.Bytes()
	if len(wire) < 8 {
		t.Fatalf("test stream is too short: %d bytes", len(wire))
	}

	baselineGroups := append([]any(nil), groups...)
	concurrentGroups := priorBugAllDispatchGroups()
	baselineEvents, err := protocol.DecodeRedraw(baselineGroups)
	if err != nil {
		t.Fatalf("DecodeRedraw baseline: %v", err)
	}
	concurrentBaseline, err := protocol.DecodeRedraw(concurrentGroups)
	if err != nil {
		t.Fatalf("DecodeRedraw concurrent baseline: %v", err)
	}
	if len(concurrentBaseline) != len(concurrentGroups) {
		t.Fatalf("concurrent baseline decoded %d events, want %d", len(concurrentBaseline), len(concurrentGroups))
	}
	if _, ok := concurrentBaseline[len(concurrentBaseline)-1].(protocol.Unknown); !ok {
		t.Fatalf("concurrent baseline lost Unknown event: %#v", concurrentBaseline)
	}
	for cut := 1; cut < len(wire); cut++ {
		cut := cut
		t.Run("fragment", func(t *testing.T) {
			var stream bytes.Buffer
			var incremental []protocol.Event
			writeChunk := func(chunk []byte) {
				stream.Write(chunk)
				messages, complete, err := readCompletePriorBugMessages(stream.Bytes())
				if err != nil {
					t.Fatalf("ReadMessages incremental chunk: %v", err)
				}
				for _, message := range messages {
					if message.Type != 2 || message.Method != "redraw" {
						t.Fatalf("unexpected message %+v", message)
					}
					events, err := protocol.DecodeRedraw(message.Args)
					if err != nil {
						t.Fatalf("DecodeRedraw incremental chunk: %v", err)
					}
					incremental = append(incremental, events...)
				}
				stream.Next(complete)
			}
			for _, boundary := range []int{cut, len(wire)} {
				if boundary == cut {
					writeChunk(wire[:cut])
				} else {
					writeChunk(wire[cut:])
				}
			}
			if stream.Len() != 0 {
				t.Fatalf("%d trailing bytes after reassembling two chunks", stream.Len())
			}
			if !reflect.DeepEqual(incremental, baselineEvents) {
				t.Fatalf("incremental events differ after split at byte %d:\n got %#v\nwant %#v", cut, incremental, baselineEvents)
			}
			joined := append(append([]byte(nil), wire[:cut]...), wire[cut:]...)
			messages, err := protocol.ReadMessages(joined)
			if err != nil {
				t.Fatalf("ReadMessages joined: %v", err)
			}
			if len(messages) != 2 {
				t.Fatalf("message count = %d, want 2", len(messages))
			}
			var decoded []protocol.Event
			var rawJoined []byte
			for _, message := range messages {
				if message.Type != 2 || message.Method != "redraw" {
					t.Fatalf("unexpected message %+v", message)
				}
				events, err := protocol.DecodeRedraw(message.Args)
				if err != nil {
					t.Fatalf("DecodeRedraw joined: %v", err)
				}
				decoded = append(decoded, events...)
				rawJoined = append(rawJoined, message.Raw...)
			}
			if !reflect.DeepEqual(decoded, baselineEvents) {
				t.Fatalf("events differ after split at byte %d:\n got %#v\nwant %#v", cut, decoded, baselineEvents)
			}
			if !bytes.Equal(rawJoined, wire) {
				t.Fatalf("Raw concatenation differs after split at byte %d", cut)
			}
		})
	}

	concurrentArgs := append([]any(nil), concurrentGroups...)
	branchCoverage := map[string]int{}
	const readers = 8
	const iterations = 32
	var wait sync.WaitGroup
	errors := make(chan error, readers)
	coverage := make(chan []protocol.Event, readers*iterations)
	start := make(chan struct{})
	for reader := 0; reader < readers; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			local := make([]protocol.Event, 0, len(concurrentBaseline))
			for iteration := 0; iteration < iterations; iteration++ {
				events, err := protocol.DecodeRedraw(concurrentArgs)
				if err != nil {
					errors <- err
					return
				}
				if !reflect.DeepEqual(events, concurrentBaseline) {
					errors <- fmt.Errorf("concurrent decode differs: %#v", events)
					return
				}
				local = append(local, events...)
			}
			coverage <- local
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	close(coverage)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for decoded := range coverage {
		for _, event := range decoded {
			branchCoverage[event.EventName()]++
		}
	}
	if _, ok := branchCoverage["option_set"]; !ok {
		t.Fatalf("concurrent DecodeRedraw never dispatched an Unknown event: %#v", branchCoverage)
	}
	wantBranches := map[string]struct{}{}
	for _, event := range concurrentBaseline {
		wantBranches[event.EventName()] = struct{}{}
	}
	if len(branchCoverage) != len(wantBranches) {
		t.Fatalf("concurrent branch coverage = %#v, want %d distinct event names", branchCoverage, len(wantBranches))
	}
}

func TestPriorBug_Neovim33353_WinViewportExtraArgsAndUnknownGridIgnored(t *testing.T) {
	reducer := screen.NewReducer()
	setup, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 4, Height: 2},
		protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "A", Repeat: 1}}},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("baseline Apply: %v", err)
	}
	if len(setup) != 1 {
		t.Fatalf("baseline committed %d states, want 1", len(setup))
	}
	baseline, err := screen.Canonical(setup[0])
	if err != nil {
		t.Fatalf("baseline Canonical: %v", err)
	}

	cases := []struct {
		name    string
		group   []any
		wantErr bool
	}{
		{
			name: "known grid with standard parameters",
			group: []any{"win_viewport", []any{
				int64(1), protocol.Handle{Kind: 2, ID: 1000}, int64(3), int64(7), int64(11), int64(13),
			}},
		},
		{
			name: "known grid with future tail",
			group: []any{"win_viewport", []any{
				int64(1), protocol.Handle{Kind: 2, ID: 1000}, int64(3), int64(7), int64(11), int64(13),
				map[string]any{"future": true}, []any{"more"},
			}},
		},
		{
			name: "wrong grid with future tail",
			group: []any{"win_viewport", []any{
				int64(97), protocol.Handle{Kind: 2, ID: 1001}, int64(17), int64(19), int64(23), int64(29),
				int64(31), "treesitter-future",
			}},
		},
		{
			name:    "malformed parameter tuple returns explicit error",
			group:   []any{"win_viewport", int64(97), int64(17)},
			wantErr: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("DecodeRedraw panicked: %v", recovered)
				}
			}()
			events, err := protocol.DecodeRedraw([]any{test.group})
			if test.wantErr {
				if err == nil {
					t.Fatalf("DecodeRedraw returned %#v, want error", events)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeRedraw: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("event count = %d, want 1", len(events))
			}
			unknown, ok := events[0].(protocol.Unknown)
			if !ok || unknown.Name != "win_viewport" {
				t.Fatalf("event = %#v, want Unknown win_viewport", events[0])
			}
			if len(unknown.Args) < 6 {
				t.Fatalf("win_viewport args = %d, want at least 6", len(unknown.Args))
			}
			committed, err := reducer.Apply([]protocol.Event{events[0], protocol.Flush{}})
			if err != nil {
				t.Fatalf("Apply win_viewport %q: %v", test.name, err)
			}
			if len(committed) != 1 {
				t.Fatalf("Apply win_viewport %q committed %d states, want 1", test.name, len(committed))
			}
			state := committed[0]
			if _, exists := state.Grids[97]; exists {
				t.Fatal("unknown win_viewport grid 97 was created")
			}
			if len(state.Grids) != len(setup[0].Grids) || len(state.Placements) != len(setup[0].Placements) {
				t.Fatalf("win_viewport changed grid/placement counts: %d/%d, want %d/%d",
					len(state.Grids), len(state.Placements), len(setup[0].Grids), len(setup[0].Placements))
			}
			actual, err := screen.Canonical(state)
			if err != nil {
				t.Fatalf("Canonical after %q: %v", test.name, err)
			}
			if !bytes.Equal(actual, baseline) {
				t.Fatalf("win_viewport %q polluted screen.State:\n got %s\nwant %s", test.name, actual, baseline)
			}
		})
	}
}

func readCompletePriorBugMessages(data []byte) ([]protocol.Message, int, error) {
	messages, err := protocol.ReadMessages(data)
	if err == nil {
		return messages, len(data), nil
	}
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, 0, err
	}
	consumed := 0
	for _, message := range messages {
		consumed += len(message.Raw)
	}
	return messages, consumed, nil
}

func priorBugAllDispatchGroups() []any {
	return []any{
		[]any{"grid_resize", []any{int64(1), int64(4), int64(2)}},
		[]any{"grid_clear", []any{int64(1)}},
		[]any{"grid_destroy", []any{int64(2)}},
		[]any{"grid_cursor_goto", []any{int64(1), int64(0), int64(0)}},
		[]any{"grid_line", []any{
			int64(1), int64(0), int64(0),
			[]any{[]any{"A"}},
			false,
		}},
		[]any{"grid_scroll", []any{int64(1), int64(0), int64(2), int64(0), int64(4), int64(1), int64(0)}},
		[]any{"hl_attr_define", []any{int64(7), map[string]any{"foreground": int64(1)}}},
		[]any{"default_colors_set", []any{int64(1), int64(2), int64(3), int64(4), int64(5)}},
		[]any{"mode_info_set", []any{true, []any{map[string]any{}}}},
		[]any{"mode_change", []any{"normal", int64(0)}},
		[]any{"win_pos", []any{
			int64(2), protocol.Handle{Kind: 2, ID: 1000},
			int64(0), int64(0), int64(10), int64(5),
		}},
		[]any{"win_float_pos", []any{
			int64(3), protocol.Handle{Kind: 2, ID: 1001}, "NW", int64(1),
			int64(0), int64(0), false, int64(10), int64(1), int64(0), int64(0),
		}},
		[]any{"win_external_pos", []any{int64(4), protocol.Handle{Kind: 2, ID: 1002}}},
		[]any{"win_hide", []any{int64(5)}},
		[]any{"win_close", []any{int64(5)}},
		[]any{"msg_set_pos", []any{int64(3), int64(23), false, " ", int64(200), int64(1)}},
		[]any{"flush", []any{}},
		[]any{"option_set", []any{"future_option", true}},
	}
}
