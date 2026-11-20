package diag

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/neovim/go-client/msgpack"

	"nvgui/protocol"
	"nvgui/screen"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func testMessage(t *testing.T) []byte {
	t.Helper()
	var buffer bytesBuffer
	encoder := msgpack.NewEncoder(&buffer)
	if err := encoder.Encode([]any{2, "redraw", []any{}}); err != nil {
		t.Fatalf("encode message: %v", err)
	}
	if _, err := protocol.ReadMessages(buffer.Bytes()); err != nil {
		t.Fatalf("decode test message: %v", err)
	}
	return buffer.Bytes()
}

func testRedrawMessage(t *testing.T, text string) []byte {
	t.Helper()
	var buffer bytesBuffer
	encoder := msgpack.NewEncoder(&buffer)
	events := []any{
		[]any{"grid_resize", []any{1, 1, 1}},
		[]any{"grid_line", []any{1, 0, 0, []any{[]any{text}}, false}},
		[]any{"flush", []any{}},
	}
	if err := encoder.Encode([]any{2, "redraw", events}); err != nil {
		t.Fatalf("encode redraw message: %v", err)
	}
	if _, err := protocol.ReadMessages(buffer.Bytes()); err != nil {
		t.Fatalf("decode redraw test message: %v", err)
	}
	return buffer.Bytes()
}

func applyRedrawMessages(t *testing.T, recorder *Recorder, reducer *screen.Reducer, messages ...[]byte) {
	t.Helper()
	for _, message := range messages {
		if _, err := recorder.Write(message); err != nil {
			t.Fatalf("Write: %v", err)
		}
		parsed, err := protocol.ReadMessages(message)
		if err != nil {
			t.Fatalf("ReadMessages: %v", err)
		}
		for _, parsedMessage := range parsed {
			events, err := protocol.DecodeRedraw(parsedMessage.Args)
			if err != nil {
				t.Fatalf("DecodeRedraw: %v", err)
			}
			committed, err := reducer.Apply(events)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			for _, state := range committed {
				recorder.Keyframe(state)
			}
		}
	}
}

type bytesBuffer struct {
	data []byte
}

func (buffer *bytesBuffer) Write(p []byte) (int, error) {
	buffer.data = append(buffer.data, p...)
	return len(p), nil
}

func (buffer *bytesBuffer) Bytes() []byte { return append([]byte(nil), buffer.data...) }

func TestRecorderWriteImmediatelyTrims(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	recorder := NewRecorder(time.Second, clock)
	message := testMessage(t)
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("Write: %v", err)
	}
	recorder.Keyframe(&screen.State{
		Grids:      map[int]*screen.Grid{},
		Attrs:      map[int]screen.Attr{0: {}},
		Placements: map[int]screen.Placement{},
		Mode:       "normal",
	})
	clock.Advance(2 * time.Second)
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("Write after window: %v", err)
	}
	recorder.mu.Lock()
	gotMessages := len(recorder.messages)
	gotKeyframes := len(recorder.keyframes)
	recorder.mu.Unlock()
	if gotMessages != 1 {
		t.Fatalf("retained messages = %d, want 1", gotMessages)
	}
	if gotKeyframes != 1 {
		t.Fatalf("retained keyframes = %d, want 1", gotKeyframes)
	}
}

func TestRecorderExportTimestamp(t *testing.T) {
	clock := &testClock{now: time.Unix(10, 0)}
	recorder := NewRecorder(time.Second, clock)
	if _, err := recorder.Write(testMessage(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	clock.Advance(2 * time.Second)
	directory := filepath.Join(t.TempDir(), "bundle")
	if err := recorder.Export(directory, Meta{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var meta struct {
		ExportedAt time.Time `json:"exported_at"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("decode meta.json: %v", err)
	}
	if !meta.ExportedAt.Equal(time.Unix(12, 0)) {
		t.Fatalf("exported_at = %v, want %v", meta.ExportedAt, time.Unix(12, 0))
	}
}

func TestReplayStartsFromRecordedKeyframe(t *testing.T) {
	clock := &testClock{now: time.Unix(10, 0)}
	recorder := NewRecorder(time.Second, clock)
	state := &screen.State{
		Seq: 7,
		Grids: map[int]*screen.Grid{1: {
			Width:  1,
			Height: 1,
			Cells:  [][]screen.Cell{{{Text: "x"}}},
		}},
		Attrs:      map[int]screen.Attr{0: {}},
		Placements: map[int]screen.Placement{},
		Mode:       "insert",
	}
	recorder.Keyframe(state)
	directory := filepath.Join(t.TempDir(), "bundle")
	if err := recorder.Export(directory, Meta{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	states, err := Replay(directory)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("Replay states = %d, want 1", len(states))
	}
	got := states[0]
	if got.Seq != state.Seq || got.Mode != state.Mode || got.Grids[1].Cells[0][0].Text != "x" {
		t.Fatalf("Replay state = %+v, want %+v", got, state)
	}
	got.Grids[1].Cells[0][0].Text = "z"
	if state.Grids[1].Cells[0][0].Text != "x" {
		t.Fatal("Replay state shares mutable state with input")
	}
}

func TestWatchdogContinuousRedrawStall(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	stalls := []int{}
	watchdog := NewWatchdog(2*time.Second, clock, func(pending int) { stalls = append(stalls, pending) })
	events := []protocol.Event{protocol.GridResize{Grid: 1, Width: 80, Height: 24}}
	watchdog.Observe(append(events, protocol.GridResize{Grid: 1, Width: 80, Height: 24}))
	clock.Advance(1500 * time.Millisecond)
	clock.Advance(1500 * time.Millisecond)
	watchdog.Tick()
	if len(stalls) != 1 || stalls[0] != 2 {
		t.Fatalf("stalls = %v, want [2]", stalls)
	}
	watchdog.Observe([]protocol.Event{protocol.Flush{}, protocol.GridResize{Grid: 1, Width: 80, Height: 24}})
	clock.Advance(3 * time.Second)
	watchdog.Tick()
	if len(stalls) != 2 || stalls[1] != 1 {
		t.Fatalf("stalls = %v, want [2 1]", stalls)
	}
}

func TestRecorderPartialWriteTrimsWindow(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	recorder := NewRecorder(time.Second, clock)
	message := testMessage(t)
	state := &screen.State{
		Grids:      map[int]*screen.Grid{},
		Attrs:      map[int]screen.Attr{0: {}},
		Placements: map[int]screen.Placement{},
		Mode:       "normal",
	}
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("write first message: %v", err)
	}
	recorder.Keyframe(state)
	clock.Advance(200 * time.Millisecond)
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("write second message: %v", err)
	}
	recorder.Keyframe(state)
	clock.Advance(2 * time.Second)
	partial := message[:len(message)/2]
	written, err := recorder.Write(partial)
	if err != nil {
		t.Fatalf("write partial message: %v", err)
	}
	if written != len(partial) {
		t.Fatalf("written = %d, want %d", written, len(partial))
	}
	recorder.mu.Lock()
	gotMessages := len(recorder.messages)
	gotKeyframes := len(recorder.keyframes)
	gotPending := len(recorder.pending)
	recorder.mu.Unlock()
	if gotMessages != 0 {
		t.Fatalf("retained messages = %d, want 0", gotMessages)
	}
	if gotKeyframes != 1 {
		t.Fatalf("retained keyframes = %d, want 1", gotKeyframes)
	}
	if gotPending != len(partial) {
		t.Fatalf("retained pending bytes = %d, want %d", gotPending, len(partial))
	}
}

func TestWatchdogPersistsArmedAcrossBatches(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	stalls := []int{}
	watchdog := NewWatchdog(2*time.Second, clock, func(pending int) { stalls = append(stalls, pending) })
	watchdog.Observe([]protocol.Event{protocol.GridResize{Grid: 1, Width: 80, Height: 24}})
	clock.Advance(1500 * time.Millisecond)
	watchdog.Observe([]protocol.Event{protocol.GridResize{Grid: 1, Width: 80, Height: 24}})
	clock.Advance(1500 * time.Millisecond)
	watchdog.Tick()
	if len(stalls) != 1 || stalls[0] != 2 {
		t.Fatalf("stalls = %v, want [2]", stalls)
	}
	watchdog.Observe([]protocol.Event{protocol.GridResize{Grid: 1, Width: 80, Height: 24}})
	clock.Advance(3 * time.Second)
	watchdog.Tick()
	watchdog.mu.Lock()
	reported := watchdog.reported
	watchdog.mu.Unlock()
	if !reported {
		t.Fatal("reported was cleared before Flush")
	}
	if len(stalls) != 1 {
		t.Fatalf("stalls = %v, want [2]", stalls)
	}
}

func TestRecorderReplayAfterTrimmedWindowWithNewRedraw(t *testing.T) {
	clock := &testClock{now: time.Unix(0, 0)}
	recorder := NewRecorder(time.Second, clock)
	reducer := screen.NewReducer()
	applyRedrawMessages(t, recorder, reducer, testRedrawMessage(t, "a"))
	clock.Advance(200 * time.Millisecond)
	applyRedrawMessages(t, recorder, reducer, testRedrawMessage(t, "b"))
	clock.Advance(200 * time.Millisecond)
	applyRedrawMessages(t, recorder, reducer, testRedrawMessage(t, "d"))

	clock.Advance(900 * time.Millisecond)
	message := testRedrawMessage(t, "c")
	half := len(message) / 2
	if _, err := recorder.Write(message[:half]); err != nil {
		t.Fatalf("write half message: %v", err)
	}
	recorder.mu.Lock()
	baseSequence := recorder.messages[0].sequence
	recorder.mu.Unlock()
	if baseSequence <= 0 {
		t.Fatalf("retained base sequence = %d, want trimmed history", baseSequence)
	}
	if _, err := recorder.Write(message[half:]); err != nil {
		t.Fatalf("complete half message: %v", err)
	}
	parsed, err := protocol.ReadMessages(message)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	events, err := protocol.DecodeRedraw(parsed[0].Args)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	if _, err := reducer.Apply(events); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	wantCanonical, err := screen.Canonical(reducer.Latest())
	if err != nil {
		t.Fatalf("Canonical(Latest): %v", err)
	}
	directory := filepath.Join(t.TempDir(), "bundle")
	if err := recorder.Export(directory, Meta{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	states, err := Replay(directory)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(states) == 0 {
		t.Fatal("Replay returned no states")
	}
	gotCanonical, err := screen.Canonical(states[len(states)-1])
	if err != nil {
		t.Fatalf("Canonical(Replay last): %v", err)
	}
	if !bytes.Equal(wantCanonical, gotCanonical) {
		t.Fatalf("Replay last canonical = %s, want %s", gotCanonical, wantCanonical)
	}
}
