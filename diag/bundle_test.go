package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neovim/go-client/msgpack"

	"nvgui/protocol"
	"nvgui/screen"
)

type bundleTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *bundleTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *bundleTestClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

type bundleTestBuffer struct{ data []byte }

func (buffer *bundleTestBuffer) Write(p []byte) (int, error) {
	buffer.data = append(buffer.data, p...)
	return len(p), nil
}

func (buffer *bundleTestBuffer) Bytes() []byte { return append([]byte(nil), buffer.data...) }

func bundleTestRedraw(t *testing.T, text string) []byte {
	t.Helper()
	var buffer bundleTestBuffer
	events := []any{
		[]any{"grid_resize", []any{1, 1, 1}},
		[]any{"grid_line", []any{1, 0, 0, []any{[]any{text}}, false}},
		[]any{"flush", []any{}},
	}
	if err := msgpack.NewEncoder(&buffer).Encode([]any{2, "redraw", events}); err != nil {
		t.Fatalf("encode redraw: %v", err)
	}
	if _, err := protocol.ReadMessages(buffer.Bytes()); err != nil {
		t.Fatalf("decode redraw: %v", err)
	}
	return buffer.Bytes()
}

func bundleTestExport(t *testing.T, recorder *Recorder, dir string, meta Meta, inputs ...TraceEvent) error {
	t.Helper()
	for _, input := range inputs {
		recorder.RecordInput(input)
	}
	return recorder.Export(dir, meta)
}

func bundleTestRecorder(t *testing.T, clock *bundleTestClock, inputs ...TraceEvent) (*Recorder, string) {
	t.Helper()
	recorder := NewRecorder(30*time.Second, clock)
	reducer := screen.NewReducer()
	message := bundleTestRedraw(t, "x")
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parsed, err := protocol.ReadMessages(message)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	events, err := protocol.DecodeRedraw(parsed[0].Args)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	states, err := reducer.Apply(events)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, state := range states {
		recorder.Keyframe(state)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := bundleTestExport(t, recorder, dir, Meta{}, inputs...); err != nil {
		t.Fatalf("Export: %v", err)
	}
	return recorder, dir
}

func bundleTestValidInputs() []TraceEvent {
	base := time.Unix(1_700_000_000, 0).UTC()
	return []TraceEvent{
		{Seq: 1, At: base, Kind: TraceKeyDown, Key: 1},
		{Seq: 2, At: base.Add(time.Millisecond), Kind: TraceText, Text: "x"},
	}
}

func TestBundleRecorderInputsWindowAndBounds(t *testing.T) {
	clock := &bundleTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
	recorder := NewRecorder(30*time.Second, clock)
	recorder.RecordInput(TraceEvent{Seq: 1, Kind: TraceText, Text: "old"})
	clock.Advance(31 * time.Second)
	recorder.RecordInput(TraceEvent{Seq: 2, Kind: TraceText, Text: "new"})
	recorder.mu.Lock()
	inputs := append([]recordedInput(nil), recorder.inputs...)
	recorder.mu.Unlock()
	if len(inputs) != 1 || inputs[0].event.Text != "new" || inputs[0].at != clock.Now().Add(-31*time.Second+31*time.Second) {
		t.Fatalf("retained inputs = %+v, want one recent event with clock time", inputs)
	}
	if inputs[0].event.At != (time.Time{}) {
		t.Fatalf("recorded event At = %v, want caller value untouched", inputs[0].event.At)
	}

	for number := 0; number < maxRecordedInputs+10; number++ {
		recorder.RecordInput(TraceEvent{Seq: uint64(3 + number), Kind: TraceText, Text: "x"})
	}
	recorder.mu.Lock()
	length := len(recorder.inputs)
	recorder.mu.Unlock()
	if length > maxRecordedInputs {
		t.Fatalf("retained inputs = %d, want <= %d", length, maxRecordedInputs)
	}

	clock.Advance(31 * time.Second)
	if _, err := recorder.Write(bundleTestRedraw(t, "y")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	recorder.mu.Lock()
	length = len(recorder.inputs)
	recorder.mu.Unlock()
	if length != 0 {
		t.Fatalf("inputs after trim = %d, want 0", length)
	}
}

func TestBundleRecordInputAutoSequence(t *testing.T) {
	clock := &bundleTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
	recorder, dir := bundleTestRecorder(t, clock,
		TraceEvent{Kind: TraceKeyDown, Key: 1},
		TraceEvent{Kind: TraceText, Text: "x"},
		TraceEvent{Seq: 10, Kind: TraceKeyUp, Key: 1},
		TraceEvent{Kind: TraceCommand, Text: "w"},
	)
	if recorder == nil {
		t.Fatal("nil recorder")
	}
	bundle, err := ReadBundle(dir)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	want := []uint64{1, 2, 10, 11}
	if len(bundle.InputEvents) != len(want) {
		t.Fatalf("InputEvents len = %d, want %d", len(bundle.InputEvents), len(want))
	}
	for index, event := range bundle.InputEvents {
		if event.Seq != want[index] {
			t.Errorf("InputEvents[%d].Seq = %d, want %d", index, event.Seq, want[index])
		}
	}
}

func TestBundleLargeJSONLLineRoundTrip(t *testing.T) {
	clock := &bundleTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
	recorder := NewRecorder(30*time.Second, clock)
	reducer := screen.NewReducer()
	message := bundleTestRedraw(t, strings.Repeat("界", 200_000))
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parsed, err := protocol.ReadMessages(message)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	events, err := protocol.DecodeRedraw(parsed[0].Args)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	states, err := reducer.Apply(events)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, state := range states {
		recorder.Keyframe(state)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := recorder.Export(dir, Meta{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
	if err != nil {
		t.Fatalf("read draw-list.jsonl: %v", err)
	}
	if len(data) <= 128*1024 {
		t.Fatalf("draw-list.jsonl len = %d, want > 128KB", len(data))
	}
	bundle, err := ReadBundle(dir)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if len(bundle.DrawLists) == 0 {
		t.Fatal("ReadBundle returned no DrawLists")
	}
	replayed, err := ReplayBundle(dir)
	if err != nil {
		t.Fatalf("ReplayBundle: %v", err)
	}
	want, err := json.Marshal(bundle.DrawLists[0])
	if err != nil {
		t.Fatalf("marshal recorded draw list: %v", err)
	}
	got, err := json.Marshal(replayed.DrawLists[0])
	if err != nil {
		t.Fatalf("marshal replayed draw list: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("large draw list changed across replay")
	}
}

func TestBundleExportFailureLeavesNoDirectory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs []TraceEvent
	}{
		{"invalid-kind", []TraceEvent{{Seq: 1, Kind: TraceKind(250), Text: "x"}}},
		{"out-of-order-seq", []TraceEvent{
			{Seq: 2, Kind: TraceText, Text: "x"},
			{Seq: 1, Kind: TraceText, Text: "y"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &bundleTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
			recorder := NewRecorder(30*time.Second, clock)
			reducer := screen.NewReducer()
			message := bundleTestRedraw(t, "x")
			if _, err := recorder.Write(message); err != nil {
				t.Fatalf("Write: %v", err)
			}
			parsed, err := protocol.ReadMessages(message)
			if err != nil {
				t.Fatalf("ReadMessages: %v", err)
			}
			events, err := protocol.DecodeRedraw(parsed[0].Args)
			if err != nil {
				t.Fatalf("DecodeRedraw: %v", err)
			}
			states, err := reducer.Apply(events)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			for _, state := range states {
				recorder.Keyframe(state)
			}
			dir := filepath.Join(t.TempDir(), "bundle")
			if err := bundleTestExport(t, recorder, dir, Meta{}, tc.inputs...); err == nil {
				t.Fatal("Export succeeded with invalid input trace")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("export directory exists after failure: %v", err)
			}
		})
	}
}

func TestBundleConcurrentRecordingAndExport(t *testing.T) {
	clock := &bundleTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
	recorder := NewRecorder(30*time.Second, clock)
	reducer := screen.NewReducer()
	message := bundleTestRedraw(t, "x")
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parsed, err := protocol.ReadMessages(message)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	events, err := protocol.DecodeRedraw(parsed[0].Args)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	states, err := reducer.Apply(events)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, state := range states {
		recorder.Keyframe(state)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for number := 0; number < 40; number++ {
				switch worker {
				case 0:
					if _, err := recorder.Write(message); err != nil {
						t.Errorf("Write: %v", err)
						return
					}
				case 1:
					recorder.Keyframe(states[0])
				case 2:
					recorder.RecordInput(TraceEvent{Kind: TraceText, Text: "x"})
				case 3:
					dir := filepath.Join(t.TempDir(), fmt.Sprintf("export-%d-%d", worker, number))
					if err := recorder.Export(dir, Meta{}); err != nil {
						t.Errorf("Export: %v", err)
						return
					}
				}
			}
		}(worker)
	}
	wait.Wait()
}

func TestBundleReadAndReplayRejectCorruption(t *testing.T) {
	clock := &bundleTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
	_, validDir := bundleTestRecorder(t, clock, bundleTestValidInputs()...)

	copyBundle := func(t *testing.T) string {
		t.Helper()
		dst := filepath.Join(t.TempDir(), "bundle")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		err := filepath.Walk(validDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(validDir, path)
			if err != nil || rel == "." {
				return err
			}
			target := filepath.Join(dst, rel)
			if info.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		})
		if err != nil {
			t.Fatalf("copy bundle: %v", err)
		}
		return dst
	}
	writeFile := func(t *testing.T, dir, name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	replaceJSONField := func(t *testing.T, data []byte, name string, value any) []byte {
		t.Helper()
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		payload[name] = encoded
		encoded, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode JSON: %v", err)
		}
		return encoded
	}

	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		replay bool
	}{
		{"wrong-version", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "meta.json", replaceJSONField(t, data, "bundle_version", 999))
		}, false},
		{"bad-exported-at", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "meta.json", replaceJSONField(t, data, "exported_at", "not-a-time"))
		}, false},
		{"zero-exported-at", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "meta.json", replaceJSONField(t, data, "exported_at", time.Time{}))
		}, false},
		{"negative-window-ms", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "meta.json", replaceJSONField(t, data, "window_ms", -1))
		}, false},
		{"bad-timing-json", func(t *testing.T, dir string) {
			writeFile(t, dir, "timing.jsonl", []byte("{not-json}\n"))
		}, false},
		{"bad-timing-offset", func(t *testing.T, dir string) {
			writeFile(t, dir, "timing.jsonl", []byte("{\"offset\":-1}\n"))
		}, false},
		{"decreasing-timing-offset", func(t *testing.T, dir string) {
			writeFile(t, dir, "timing.jsonl", []byte("{\"offset\":3}\n{\"offset\":2}\n"))
		}, false},
		{"invalid-input-kind", func(t *testing.T, dir string) {
			writeFile(t, dir, "input-trace.jsonl", []byte("{\"seq\":1,\"kind\":250}\n"))
		}, false},
		{"zero-input-seq", func(t *testing.T, dir string) {
			writeFile(t, dir, "input-trace.jsonl", []byte("{\"seq\":0,\"kind\":3}\n"))
		}, false},
		{"out-of-order-input-seq", func(t *testing.T, dir string) {
			writeFile(t, dir, "input-trace.jsonl", []byte("{\"seq\":2,\"kind\":3}\n{\"seq\":1,\"kind\":3}\n"))
		}, false},
		{"out-of-order-draw-list-seq", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var list map[string]any
			lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
			if err := json.Unmarshal(lines[0], &list); err != nil {
				t.Fatal(err)
			}
			list["seq"] = 99
			line, err := json.Marshal(list)
			if err != nil {
				t.Fatal(err)
			}
			lines = append([][]byte{line}, line)
			writeFile(t, dir, "draw-list.jsonl", append(bytes.Join(lines, []byte("\n")), '\n'))
		}, false},
		{"frame-count-mismatch", func(t *testing.T, dir string) {
			writeFile(t, dir, "draw-list.jsonl", []byte(""))
		}, false},
		{"empty-line", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "draw-list.jsonl", append(data, []byte("\n{}\n")...))
		}, false},
		{"whitespace-only-line", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "draw-list.jsonl", append(data, []byte("   \n")...))
		}, false},
		{"trailing-whitespace-line", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "draw-list.jsonl", append(data, []byte(" \n")...))
		}, false},
		{"blank-line-inside", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "draw-list.jsonl", append([]byte("\n"), data...))
		}, false},
		{"tampered-index-count", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "draw-list", "index.json"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "draw-list/index.json", replaceJSONField(t, data, "count", 999))
		}, false},
		{"unexpected-index-entry", func(t *testing.T, dir string) {
			writeFile(t, dir, "draw-list/unexpected.json", []byte("{}\n"))
		}, false},
		{"index-subdirectory", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "draw-list", "extra"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"tampered-draw-list-caught-by-replay", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var list map[string]any
			if err := json.Unmarshal(bytes.TrimSuffix(data, []byte("\n")), &list); err != nil {
				t.Fatal(err)
			}
			width, _ := list["width"].(float64)
			list["width"] = width + 7
			line, err := json.Marshal(list)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "draw-list.jsonl", append(line, '\n'))
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyBundle(t)
			tc.mutate(t, dir)
			if tc.replay {
				if _, err := ReadBundle(dir); err != nil {
					t.Fatalf("ReadBundle should accept content tampering: %v", err)
				}
				if _, err := ReplayBundle(dir); err == nil {
					t.Fatal("ReplayBundle accepted tampered draw list")
				}
				return
			}
			if _, err := ReadBundle(dir); err == nil {
				t.Fatal("ReadBundle accepted corrupted bundle")
			}
		})
	}

	t.Run("crlf-lines", func(t *testing.T) {
		dir := copyBundle(t)
		data, err := os.ReadFile(filepath.Join(dir, "draw-list.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, "draw-list.jsonl", bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")))
		if _, err := ReadBundle(dir); err != nil {
			t.Fatalf("ReadBundle should accept CRLF JSONL: %v", err)
		}
	})
}

func TestBundleIndexHasNoFrameFiles(t *testing.T) {
	clock := &bundleTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
	_, dir := bundleTestRecorder(t, clock, bundleTestValidInputs()...)
	for _, name := range []string{"draw-list", "input-trace"} {
		entries, err := os.ReadDir(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s/: %v", name, err)
		}
		if len(entries) != 1 || entries[0].Name() != "index.json" {
			t.Fatalf("%s/ entries = %v, want only index.json", name, entries)
		}
	}
}
