package session_test

// Covered P0 IDs: neovim#26980, macvim#579, neovide#2088, neovide#3528.
// Deferred (16 covered + 7 deferred = 23/23): neovide#3047 (Kitty Text Sizing
// Protocol, phase 2), neovim#21458 (mouseshape, M2/phase 2), neovim#19559
// (detach/reattach, phase 2 remote nvim), neovim#24802 (upstream
// inccommand=split limitation), macvim#721 (legacy "Screen error" without a
// modern reproduction), goneovim#265 (terminal mouse click, M2 mouse),
// neovim#3929 (iTerm2 fullscreen + airline, terminal-only).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neovim/go-client/msgpack"

	"nvgui/connection"
	"nvgui/diag"
	"nvgui/protocol"
	"nvgui/screen"
	"nvgui/session"
)

type priorBugFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *priorBugFakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *priorBugFakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func TestPriorBug_Neovim26980_WatchdogStallAndFlushRearm(t *testing.T) {
	clock := &priorBugFakeClock{now: time.Unix(0, 0)}
	recorder := diag.NewRecorder(30*time.Second, clock)
	reducer := screen.NewReducer()
	baselineWire := priorBugRedrawBytes(t, []protocol.Event{
		protocol.GridResize{Grid: 1, Width: 2, Height: 1},
		protocol.Flush{},
	})
	if _, err := recorder.Write(baselineWire); err != nil {
		t.Fatalf("recorder.Write baseline: %v", err)
	}
	baselineStates, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 1, Width: 2, Height: 1},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply baseline: %v", err)
	}
	if len(baselineStates) != 1 {
		t.Fatalf("baseline committed %d states, want 1", len(baselineStates))
	}
	recorder.Keyframe(baselineStates[0])

	stalls := []int{}
	var stallMu sync.Mutex
	watchdog := diag.NewWatchdog(2*time.Second, clock, func(pending int) {
		stallMu.Lock()
		defer stallMu.Unlock()
		stalls = append(stalls, pending)
	})
	const attributeBatches = 600
	events := make([]protocol.Event, 0, attributeBatches*2+2)
	for index := 0; index < attributeBatches; index++ {
		events = append(events, protocol.HlAttrDefine{
			ID:  index + 1,
			RGB: map[string]any{"foreground": int64(index + 1)},
		})
		events = append(events, protocol.GridLine{
			Grid: 1, Row: 0, ColStart: 0,
			Cells: []protocol.LineCell{{Text: "x", HL: index + 1, Repeat: 1}},
		})
		clock.Advance(time.Millisecond)
	}
	watchdog.Observe(events)
	watchdog.Tick()
	watchdog.Tick()
	if got := stallCount(&stallMu, &stalls); got != 0 {
		t.Fatalf("stalls before deadline = %d, want 0", got)
	}
	clock.Advance(2 * time.Second)
	watchdog.Tick()
	watchdog.Tick()
	if got := stallValues(&stallMu, &stalls); len(got) != 1 || got[0] != len(events) {
		t.Fatalf("stalls = %v, want [%d]", got, len(events))
	}
	watchdog.Observe(append(events[:0:0], events...))
	clock.Advance(2 * time.Second)
	watchdog.Tick()
	if got := stallCount(&stallMu, &stalls); got != 1 {
		t.Fatalf("stalls before flush = %d, want exactly one", got)
	}

	wire := priorBugRedrawBytes(t, events)
	if _, err := recorder.Write(wire); err != nil {
		t.Fatalf("recorder.Write: %v", err)
	}
	if len(wire) == 0 {
		t.Fatal("stall redraw batch has no bytes")
	}
	states, err := reducer.Apply(events)
	if err != nil {
		t.Fatalf("Apply no-flush events: %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("committed %d states without flush", len(states))
	}

	directory := filepath.Join(t.TempDir(), "diag")
	if err := recorder.Export(directory, diag.Meta{NvimVersion: "v0.12.6"}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	bundle, err := diag.ReadBundle(directory)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if len(bundle.States) != 1 || bundle.States[0].Seq != 1 {
		t.Fatalf("bundle states = %#v, want one state with Seq 1", bundle.States)
	}
	inbound, err := os.ReadFile(filepath.Join(directory, "inbound.mpack"))
	if err != nil {
		t.Fatalf("read inbound.mpack: %v", err)
	}
	if len(inbound) < len(wire) {
		t.Fatalf("inbound.mpack = %d bytes, want at least the %d-byte stall batch", len(inbound), len(wire))
	}
	messages, err := protocol.ReadMessages(inbound)
	if err != nil {
		t.Fatalf("decode inbound.mpack: %v", err)
	}
	var decodedStall []protocol.Event
	for _, message := range messages {
		if message.Type != 2 || message.Method != "redraw" {
			continue
		}
		decoded, err := protocol.DecodeRedraw(message.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw inbound: %v", err)
		}
		decodedStall = append(decodedStall, decoded...)
	}
	if !reflect.DeepEqual(decodedStall, events) {
		t.Fatalf("stall batch decoded %d events, want the original %d hl_attr_define/grid_line events:\n got %#v\nwant %#v",
			len(decodedStall), len(events), decodedStall, events)
	}
	metaData, err := os.ReadFile(filepath.Join(directory, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	if !strings.Contains(string(metaData), "v0.12.6") {
		t.Fatalf("meta.json lost nvim_version: %s", metaData)
	}
	for _, name := range []string{"keyframe.json", "keyframe.gob", "replay-from.gob", "timing.jsonl", "draw-list.jsonl"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("diagnostic bundle missing %s: %v", name, err)
		}
	}

	flushMessage := priorBugRedrawBytes(t, []protocol.Event{protocol.Flush{}})
	if _, err := recorder.Write(flushMessage); err != nil {
		t.Fatalf("recorder.Write flush: %v", err)
	}
	watchdog.Observe([]protocol.Event{protocol.Flush{}})
	flushStates, err := reducer.Apply([]protocol.Event{protocol.Flush{}})
	if err != nil {
		t.Fatalf("Apply flush: %v", err)
	}
	if len(flushStates) != 1 {
		t.Fatalf("flush committed %d states, want 1", len(flushStates))
	}

	clock.Advance(2 * time.Second)
	watchdog.Tick()
	if got := stallCount(&stallMu, &stalls); got != 1 {
		t.Fatalf("stalls after flush without new redraw = %d, want 1", got)
	}
	watchdog.Observe([]protocol.Event{protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "y", Repeat: 1}}}})
	clock.Advance(2 * time.Second)
	watchdog.Tick()
	if got := stallValues(&stallMu, &stalls); len(got) != 2 || got[1] != 1 {
		t.Fatalf("stalls after flush rearm = %v, want [..., 1]", got)
	}
}

func TestPriorBug_MacVim579_PickerFloatOpenFilterCloseRestoresMainGrid(t *testing.T) {
	sess := startPriorBugSession(t, 80, 24)
	var buffer int
	if err := sess.Nvim().Call("nvim_create_buf", &buffer, false, true); err != nil {
		t.Fatalf("nvim_create_buf: %v", err)
	}
	if err := sess.Nvim().Call("nvim_buf_set_lines", nil, buffer, 0, -1, true, []string{"MAIN_GRID_SENTINEL"}); err != nil {
		t.Fatalf("nvim_buf_set_lines: %v", err)
	}
	if err := sess.Nvim().Call("nvim_set_current_buf", nil, buffer); err != nil {
		t.Fatalf("nvim_set_current_buf: %v", err)
	}
	baseline := exportPriorBugBundle(t, sess)
	assertPriorBugGridText(t, baseline.States[len(baseline.States)-1], "MAIN_GRID_SENTINEL")
	baselineLayers := len(screen.Layers(baseline.States[len(baseline.States)-1]))

	for cycle := 0; cycle < 3; cycle++ {
		t.Run(fmt.Sprintf("cycle-%d", cycle), func(t *testing.T) {
			var floatBuffer int
			if err := sess.Nvim().Call("nvim_create_buf", &floatBuffer, false, true); err != nil {
				t.Fatalf("nvim_create_buf float: %v", err)
			}
			if err := sess.Nvim().Call("nvim_buf_set_lines", nil, floatBuffer, 0, -1, true, []string{
				"PICKER_ONE", "PICKER_TWO", "PICKER_THREE",
			}); err != nil {
				t.Fatalf("nvim_buf_set_lines float: %v", err)
			}
			var window int
			if err := sess.Nvim().Call("nvim_open_win", &window, floatBuffer, true, map[string]any{
				"relative": "editor",
				"row":      2,
				"col":      3,
				"width":    18,
				"height":   3,
				"style":    "minimal",
				"border":   "single",
			}); err != nil {
				t.Fatalf("nvim_open_win: %v", err)
			}
			opened := exportBarrierPriorBugBundle(t, sess)
			openState := opened.States[len(opened.States)-1]
			if !priorBugGridContains(openState, "PICKER_ONE") {
				t.Fatal("open picker text is missing from Committed Screen")
			}
			assertPriorBugGridText(t, openState, "MAIN_GRID_SENTINEL")
			if got := len(screen.Layers(openState)); got <= baselineLayers {
				t.Fatalf("layers after float open = %d, want > baseline %d", got, baselineLayers)
			}

			if err := sess.Nvim().Call("nvim_buf_set_lines", nil, floatBuffer, 0, -1, true, []string{
				"FILTERED", "PICKER_TWO", "PICKER_THREE",
			}); err != nil {
				t.Fatalf("filter picker: %v", err)
			}
			filtered := exportBarrierPriorBugBundle(t, sess)
			filteredState := filtered.States[len(filtered.States)-1]
			if !priorBugGridContains(filteredState, "FILTERED") {
				t.Fatal("filtered picker text is missing from Committed Screen")
			}
			if priorBugGridContains(filteredState, "PICKER_ONE") {
				t.Fatal("stale picker row remains after filter")
			}
			if got := len(screen.Layers(filteredState)); got <= baselineLayers {
				t.Fatalf("layers after float update = %d, want > baseline %d", got, baselineLayers)
			}

			if err := sess.Nvim().Call("nvim_win_close", nil, window, true); err != nil {
				t.Fatalf("nvim_win_close: %v", err)
			}
			closed := exportBarrierPriorBugBundle(t, sess)
			closedState := closed.States[len(closed.States)-1]
			assertPriorBugGridText(t, closedState, "MAIN_GRID_SENTINEL")
			if priorBugGridContains(closedState, "FILTERED") || priorBugGridContains(closedState, "PICKER_ONE") {
				t.Fatal("picker text remains after close")
			}
			if got := len(screen.Layers(closedState)); got != baselineLayers {
				t.Fatalf("layers after close = %d, want baseline %d", got, baselineLayers)
			}
		})
	}
}

func TestPriorBug_Neovide2088_ImageEscapeDegradesWithoutGridCorruption(t *testing.T) {
	sess := startPriorBugSession(t, 80, 24)
	if err := sess.Nvim().Call("nvim_ui_send", nil, "\x1b_Ga=T;f=24;s=1;v=1;AAAA\x1b\\"); err != nil {
		t.Fatalf("nvim_ui_send: %v", err)
	}
	var namespace int
	if err := sess.Nvim().Call("nvim_create_namespace", &namespace, "nvgui-prior-2088"); err != nil {
		t.Fatalf("nvim_create_namespace: %v", err)
	}
	if err := sess.Nvim().Call("nvim_buf_set_extmark", nil, 0, namespace, 0, 0, map[string]any{
		"virt_lines": []any{[]any{[]any{"VIRT_LINE_PLACEHOLDER"}}},
	}); err != nil {
		t.Fatalf("nvim_buf_set_extmark virt_lines: %v", err)
	}
	if err := sess.Nvim().Command("redraw!"); err != nil {
		t.Fatalf("redraw!: %v", err)
	}
	var barrier int
	if err := sess.Nvim().Eval("1", &barrier); err != nil {
		t.Fatalf("RPC barrier: %v", err)
	}
	bundle := exportPriorBugBundle(t, sess)
	state := bundle.States[len(bundle.States)-1]
	if state == nil || state.Grids[1] == nil {
		t.Fatal("Committed Screen is missing root grid 1")
	}
	if !priorBugGridContains(state, "VIRT_LINE_PLACEHOLDER") {
		t.Fatal("VIRT_LINE_PLACEHOLDER is missing from Committed Screen")
	}
	for gridID, grid := range state.Grids {
		for rowIndex, row := range grid.Cells {
			rowText := priorBugRowText(row)
			if strings.ContainsRune(rowText, 0x1b) ||
				strings.Contains(rowText, "_Ga=T") || strings.Contains(rowText, "AAAA\x1b\\") {
				t.Fatalf("escape bytes entered grid %d row %d: %q", gridID, rowIndex, rowText)
			}
		}
	}
	select {
	case pending := <-sess.Stalls():
		t.Fatalf("image escape produced watchdog stall with %d pending events", pending)
	default:
	}
}

func TestPriorBug_Neovide3528_Nvim0126StartupForwardCompat(t *testing.T) {
	sess := startPriorBugSession(t, 80, 24)
	var version map[string]any
	if err := sess.Nvim().Eval("api_info().version", &version); err != nil {
		t.Fatalf("read api_info().version: %v", err)
	}
	major, _ := version["major"].(int64)
	minor, _ := version["minor"].(int64)
	patch, _ := version["patch"].(int64)
	if major != 0 || minor != 12 || patch != 6 {
		t.Fatalf("NVIM api version = %#v, want v0.12.6", version)
	}
	info, err := sess.Nvim().APIInfo()
	if err != nil {
		t.Fatalf("APIInfo: %v", err)
	}
	if len(info) < 2 {
		t.Fatalf("APIInfo = %#v, want at least 2 fields", info)
	}
	for _, command := range []string{
		"set pumblend=25",
		"set guifont=Monaco:h13",
		"set termguicolors",
		"set termguicolors!",
		"set termguicolors",
	} {
		if err := sess.Nvim().Command(command); err != nil {
			t.Fatalf("trigger real UI option %q: %v", command, err)
		}
	}
	if err := sess.Nvim().Command("redraw!"); err != nil {
		t.Fatalf("redraw!: %v", err)
	}
	var barrier int
	if err := sess.Nvim().Eval("1", &barrier); err != nil {
		t.Fatalf("RPC barrier: %v", err)
	}
	bundle, directory := exportPriorBugBundleWithDir(t, sess)
	if len(bundle.States) == 0 {
		t.Fatal("Committed Screen is empty")
	}
	inbound, err := os.ReadFile(filepath.Join(directory, "inbound.mpack"))
	if err != nil {
		t.Fatalf("read inbound.mpack: %v", err)
	}
	messages, err := protocol.ReadMessages(inbound)
	if err != nil {
		t.Fatalf("decode inbound.mpack: %v", err)
	}
	optionSet := map[string]bool{}
	for _, message := range messages {
		if message.Type != 2 || message.Method != "redraw" {
			continue
		}
		events, err := protocol.DecodeRedraw(message.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw inbound: %v", err)
		}
		for _, event := range events {
			unknown, ok := event.(protocol.Unknown)
			if !ok || unknown.Name != "option_set" || len(unknown.Args) == 0 {
				continue
			}
			name, ok := unknown.Args[0].(string)
			if !ok {
				continue
			}
			optionSet[name] = true
		}
	}
	for _, name := range []string{"pumblend", "guifont", "termguicolors"} {
		if !optionSet[name] {
			t.Fatalf("inbound.mpack lost real option_set %q: %#v", name, optionSet)
		}
	}
	states, err := diag.Replay(directory)
	if err != nil {
		t.Fatalf("diag.Replay: %v", err)
	}
	if len(states) == 0 {
		t.Fatal("diag.Replay returned no states")
	}
	state := bundle.States[len(bundle.States)-1]
	if state.Grids[1] == nil || state.Grids[1].Width != 80 || state.Grids[1].Height != 24 {
		t.Fatalf("root grid = %#v, want 80x24", state.Grids[1])
	}
	canonical, err := screen.Canonical(state)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	if decoded["grids"] == nil {
		t.Fatal("canonical JSON has no grids")
	}
	if bundle.Meta.NvimVersion != "v0.12.6" {
		t.Fatalf("meta NvimVersion = %q, want v0.12.6", bundle.Meta.NvimVersion)
	}
}

func startPriorBugSession(t *testing.T, width, height int) *session.Session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sess, err := session.Start(ctx, connection.Options{
		Args:   []string{"--clean", "-n", "--noplugin"},
		Width:  width,
		Height: height,
	}, nil)
	if err != nil {
		cancel()
		t.Fatalf("session.Start: %v", err)
	}
	t.Cleanup(func() {
		if err := sess.Nvim().Command("qa!"); err != nil && !isPriorBugCleanExit(err) {
			t.Errorf("qa!: %v", err)
		}
		if err := sess.Close(); err != nil {
			t.Errorf("session.Close: %v", err)
		}
		cancel()
	})
	return sess
}

func isPriorBugCleanExit(err error) bool {
	if err == nil {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "EOF") || strings.Contains(text, "closed") ||
		strings.Contains(text, "exit status") || strings.Contains(text, "signal:")
}

func exportPriorBugBundle(t *testing.T, sess *session.Session) *diag.Bundle {
	t.Helper()
	bundle, _ := exportPriorBugBundleWithDir(t, sess)
	return bundle
}

func exportBarrierPriorBugBundle(t *testing.T, sess *session.Session) *diag.Bundle {
	t.Helper()
	if err := sess.Nvim().Command("redraw!"); err != nil {
		t.Fatalf("redraw!: %v", err)
	}
	var barrier int
	if err := sess.Nvim().Eval("1", &barrier); err != nil {
		t.Fatalf("RPC barrier: %v", err)
	}
	return exportPriorBugBundle(t, sess)
}

func exportPriorBugBundleWithDir(t *testing.T, sess *session.Session) (*diag.Bundle, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "diag")
	if err := sess.ExportDiag(directory); err != nil {
		t.Fatalf("ExportDiag: %v", err)
	}
	bundle, err := diag.ReadBundle(directory)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if len(bundle.States) == 0 {
		t.Fatal("bundle has no committed states")
	}
	return bundle, directory
}

func priorBugRedrawBytes(t *testing.T, events []protocol.Event) []byte {
	t.Helper()
	groups := make([]any, 0, len(events))
	for _, event := range events {
		switch typed := event.(type) {
		case protocol.HlAttrDefine:
			groups = append(groups, []any{"hl_attr_define", []any{int64(typed.ID), typed.RGB}})
		case protocol.GridLine:
			groups = append(groups, []any{"grid_line", []any{
				int64(typed.Grid), int64(typed.Row), int64(typed.ColStart),
				[]any{[]any{typed.Cells[0].Text, int64(typed.Cells[0].HL), int64(typed.Cells[0].Repeat)}},
				typed.Wrap,
			}})
		case protocol.GridResize:
			groups = append(groups, []any{"grid_resize", []any{
				int64(typed.Grid), int64(typed.Width), int64(typed.Height),
			}})
		case protocol.Flush:
			groups = append(groups, []any{"flush", []any{}})
		default:
			t.Fatalf("unsupported event %T", event)
		}
	}
	var wire []byte
	encoder := msgpack.NewEncoder(&priorBugBytesWriter{wire: &wire})
	if err := encoder.Encode([]any{int64(2), "redraw", groups}); err != nil {
		t.Fatalf("msgpack encode: %v", err)
	}
	return wire
}

type priorBugBytesWriter struct{ wire *[]byte }

func (writer *priorBugBytesWriter) Write(p []byte) (int, error) {
	*writer.wire = append(*writer.wire, p...)
	return len(p), nil
}

func assertPriorBugGridText(t *testing.T, state *screen.State, text string) {
	t.Helper()
	if !priorBugGridContains(state, text) {
		t.Fatalf("Committed Screen lost %q", text)
	}
}

func priorBugGridContains(state *screen.State, text string) bool {
	for _, grid := range state.Grids {
		for _, row := range grid.Cells {
			if strings.Contains(priorBugRowText(row), text) {
				return true
			}
		}
	}
	return false
}

func priorBugRowText(row []screen.Cell) string {
	var builder strings.Builder
	for _, cell := range row {
		builder.WriteString(cell.Text)
	}
	return builder.String()
}

func stallCount(mu *sync.Mutex, stalls *[]int) int {
	return len(stallValues(mu, stalls))
}

func stallValues(mu *sync.Mutex, stalls *[]int) []int {
	mu.Lock()
	defer mu.Unlock()
	return append([]int(nil), (*stalls)...)
}
