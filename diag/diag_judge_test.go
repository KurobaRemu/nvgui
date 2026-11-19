package diag_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"nvgui/diag"
	"nvgui/oracle"
	"nvgui/protocol"
	"nvgui/screen"
)

type fakeClock struct{ now time.Time }

func (clock *fakeClock) Now() time.Time { return clock.now }

func (clock *fakeClock) Advance(duration time.Duration) { clock.now = clock.now.Add(duration) }

type timingRecord struct {
	Offset int64 `json:"offset"`
	TMS    int64 `json:"t_ms"`
}

func TestRecorderGoldenExportReplay(t *testing.T) {
	for _, golden := range oracle.List() {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			exportAt := len(golden.Inbound) / 2
			if exportAt == 0 {
				t.Fatal("golden inbound is empty")
			}
			clock := &fakeClock{now: time.Unix(0, 0)}
			recorder := diag.NewRecorder(30*time.Second, clock)
			reducer := screen.NewReducer()
			var latest *screen.State
			allMessages, err := protocol.ReadMessages(golden.Inbound)
			if err != nil {
				t.Fatalf("ReadMessages: %v", err)
			}
			appliedMsgs := 0
			bytesWritten := 0
			random := rand.New(rand.NewSource(int64(len(golden.Name)) + 1))
			for bytesWritten < exportAt {
				remaining := exportAt - bytesWritten
				chunkSize := 1 + random.Intn(97)
				if chunkSize > remaining {
					chunkSize = remaining
				}
				chunk := golden.Inbound[bytesWritten : bytesWritten+chunkSize]
				count, err := recorder.Write(chunk)
				if err != nil || count != len(chunk) {
					t.Fatalf("Write = (%d, %v), want (%d, nil)", count, err, len(chunk))
				}
				bytesWritten += count
				clock.Advance(time.Duration(1+random.Intn(20)) * time.Millisecond)
				completeCount := completeMessageCount(t, allMessages, golden.Inbound, golden.Inbound[:bytesWritten])
				for _, message := range allMessages[appliedMsgs:completeCount] {
					if message.Type != 2 || message.Method != "redraw" {
						continue
					}
					events, err := protocol.DecodeRedraw(message.Args)
					if err != nil {
						t.Fatalf("DecodeRedraw: %v", err)
					}
					committed, err := reducer.Apply(events)
					if err != nil {
						t.Fatalf("Apply: %v", err)
					}
					for _, state := range committed {
						recorder.Keyframe(state)
						latest = state
					}
				}
				appliedMsgs = completeCount
			}
			if latest == nil {
				t.Fatal("no committed state before export")
			}
			wantCanonical, err := screen.Canonical(latest)
			if err != nil {
				t.Fatalf("Canonical(Latest): %v", err)
			}
			directory := filepath.Join(t.TempDir(), "bundle")
			meta := diag.Meta{
				NvimVersion: golden.Meta.NvimVersion,
				APILevel:    golden.Meta.APILevel,
				UIOptions:   golden.Meta.UIOptions,
				GUIVersion:  "test",
				GOOS:        "test",
			}
			if err := recorder.Export(directory, meta); err != nil {
				t.Fatalf("Export: %v", err)
			}
			assertBundleFiles(t, directory)
			states, err := diag.Replay(directory)
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
			_, difference := compareCanonical(wantCanonical, gotCanonical)
			if difference != "" {
				t.Fatalf("Replay last frame differs from Latest at %s", difference)
			}
		})
	}
}

func TestRecorderWindowRetentionBound(t *testing.T) {
	golden := firstDiagGolden(t)
	clock := &fakeClock{now: time.Unix(0, 0)}
	const window = 30 * time.Second
	const duration = 120 * time.Second
	const step = 100 * time.Millisecond
	recorder := diag.NewRecorder(30*time.Second, clock)
	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	type keyframeRecord struct {
		at     time.Time
		offset int
	}
	type messageRecord struct {
		at     time.Time
		offset int
		bytes  int
	}
	keyframes := []keyframeRecord{}
	writtenMessages := []messageRecord{}
	reducer := screen.NewReducer()
	var latest *screen.State
	totalBytes := 0
	for elapsed, index := time.Duration(0), 0; elapsed <= duration; elapsed += step {
		clock.now = time.Unix(0, 0).Add(elapsed)
		message := messages[index%len(messages)]
		messageOffset := totalBytes
		count, err := recorder.Write(message.Raw)
		if err != nil || count != len(message.Raw) {
			t.Fatalf("Write = (%d, %v), want (%d, nil)", count, err, len(message.Raw))
		}
		totalBytes += count
		writtenMessages = append(writtenMessages, messageRecord{
			at:     clock.Now(),
			offset: messageOffset,
			bytes:  count,
		})
		if message.Type == 2 && message.Method == "redraw" {
			events, err := protocol.DecodeRedraw(message.Args)
			if err != nil {
				t.Fatalf("DecodeRedraw: %v", err)
			}
			states, err := reducer.Apply(events)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			for _, state := range states {
				recorder.Keyframe(state)
				latest = state
				keyframes = append(keyframes, keyframeRecord{
					at:     clock.Now(),
					offset: messageOffset,
				})
			}
		}
		index++
	}
	if len(keyframes) == 0 {
		t.Fatal("no keyframe recorded during 120s")
	}
	windowStart := clock.Now().Add(-window)
	var retainedKeyframe keyframeRecord
	foundRetainedKeyframe := false
	for _, keyframe := range keyframes {
		if keyframe.at.After(windowStart) {
			break
		}
		retainedKeyframe = keyframe
		foundRetainedKeyframe = true
	}
	if !foundRetainedKeyframe {
		t.Fatal("no keyframe at or before window start")
	}
	if latest == nil {
		t.Fatal("no committed state during 120s")
	}
	windowBytes := 0
	for _, message := range writtenMessages {
		if !message.at.Before(windowStart) {
			windowBytes += message.bytes
		}
	}
	upperBound := totalBytes - retainedKeyframe.offset
	if upperBound >= totalBytes/2 {
		t.Fatalf("window upper bound = %d, want strictly below half of %d written bytes", upperBound, totalBytes)
	}
	directory := filepath.Join(t.TempDir(), "bundle")
	if err := recorder.Export(directory, diag.Meta{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	assertBundleFiles(t, directory)
	wantCanonical, err := screen.Canonical(latest)
	if err != nil {
		t.Fatalf("Canonical(Latest): %v", err)
	}
	states, err := diag.Replay(directory)
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
	if _, difference := compareCanonical(wantCanonical, gotCanonical); difference != "" {
		t.Fatalf("Replay last frame differs from full replay at %s", difference)
	}
	timing := readTiming(t, directory)
	if len(timing) == 0 {
		t.Fatal("timing.jsonl is empty")
	}
	if timing[0].TMS < 0 {
		t.Fatalf("first timing t_ms = %d, want non-negative relative to exported keyframe", timing[0].TMS)
	}
	if timing[0].Offset != 0 {
		t.Fatalf("first timing offset = %d, want 0 relative to retained inbound", timing[0].Offset)
	}
	if len(timing) > 1 {
		last := timing[len(timing)-1]
		if last.TMS < timing[0].TMS {
			t.Fatalf("last timing t_ms = %d, want >= first %d relative to exported keyframe", last.TMS, timing[0].TMS)
		}
	}
	for index := 1; index < len(timing); index++ {
		if timing[index].Offset <= timing[index-1].Offset || timing[index].TMS < timing[index-1].TMS {
			t.Fatalf("timing not monotonic at line %d: %+v after %+v", index+1, timing[index], timing[index-1])
		}
	}
	inbound, err := os.ReadFile(filepath.Join(directory, "inbound.mpack"))
	if err != nil {
		t.Fatalf("read inbound.mpack: %v", err)
	}
	if len(inbound) > upperBound {
		t.Fatalf("retained inbound bytes = %d, want <= %d bytes covering the window from a legal keyframe", len(inbound), upperBound)
	}
	if len(inbound) >= totalBytes/2 {
		t.Fatalf("retained inbound bytes = %d grows with total duration, want < %d", len(inbound), totalBytes/2)
	}
	if len(inbound) < windowBytes {
		t.Fatalf("retained inbound bytes = %d, want >= %d bytes from the 30s window", len(inbound), windowBytes)
	}
}

func TestRecorderExcludesHalfWrittenMessage(t *testing.T) {
	golden := firstDiagGolden(t)
	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatal("need at least two messages")
	}
	complete := len(golden.Inbound) - len(messages[len(messages)-1].Raw)/2
	if complete <= 0 {
		t.Fatal("last message is too short to split")
	}
	clock := &fakeClock{now: time.Unix(0, 0)}
	recorder := diag.NewRecorder(30*time.Second, clock)
	if _, err := recorder.Write(golden.Inbound[:complete]); err != nil {
		t.Fatalf("Write: %v", err)
	}
	reducer := screen.NewReducer()
	for _, message := range completeMessagesInPrefix(t, messages, golden.Inbound, golden.Inbound[:complete]) {
		if message.Type == 2 && message.Method == "redraw" {
			events, err := protocol.DecodeRedraw(message.Args)
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
	directory := filepath.Join(t.TempDir(), "bundle")
	if err := recorder.Export(directory, diag.Meta{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	inbound, err := os.ReadFile(filepath.Join(directory, "inbound.mpack"))
	if err != nil {
		t.Fatalf("read inbound.mpack: %v", err)
	}
	if containsTail(inbound, golden.Inbound[:complete]) {
		t.Fatalf("diagnostic bundle contains half-written trailing bytes")
	}
	if _, err := protocol.ReadMessages(inbound); err != nil {
		t.Fatalf("bundle inbound has incomplete message: %v", err)
	}
	if _, err := diag.Replay(directory); err != nil {
		t.Fatalf("Replay with half-written tail: %v", err)
	}
}

func TestWatchdogStallAndFlushRearm(t *testing.T) {
	for _, golden := range oracle.List() {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			if golden.Meta.Inconclusive {
				t.Skip("inconclusive watchdog fixture")
			}
			clock := &fakeClock{now: time.Unix(0, 0)}
			stalls := []int{}
			watchdog := diag.NewWatchdog(2*time.Second, clock, func(pendingEvents int) {
				stalls = append(stalls, pendingEvents)
			})
			messages, err := protocol.ReadMessages(golden.Inbound)
			if err != nil {
				t.Fatalf("ReadMessages: %v", err)
			}
			if golden.Name == "s10-bulk-delete" {
				pendingEvents := 0
				for _, message := range messages {
					if message.Type != 2 || message.Method != "redraw" {
						continue
					}
					events, err := protocol.DecodeRedraw(message.Args)
					if err != nil {
						t.Fatalf("DecodeRedraw: %v", err)
					}
					withoutFlush := make([]protocol.Event, 0, len(events))
					for _, event := range events {
						if _, isFlush := event.(protocol.Flush); isFlush {
							continue
						}
						withoutFlush = append(withoutFlush, event)
					}
					if len(withoutFlush) > 0 {
						pendingEvents += len(withoutFlush)
						watchdog.Observe(withoutFlush)
					}
				}
				if pendingEvents == 0 {
					t.Fatal("s10-bulk-delete has no redraw events before flush")
				}
				clock.Advance(3 * time.Second)
				watchdog.Tick()
				clock.Advance(10 * time.Second)
				watchdog.Tick()
				if len(stalls) != 1 || stalls[0] != pendingEvents {
					t.Fatalf("s10-bulk-delete stalls = %v, want one stall with %d pending events", stalls, pendingEvents)
				}
				return
			}
			for _, message := range messages {
				if message.Type != 2 || message.Method != "redraw" {
					continue
				}
				events, err := protocol.DecodeRedraw(message.Args)
				if err != nil {
					t.Fatalf("DecodeRedraw: %v", err)
				}
				watchdog.Observe(events)
				watchdog.Observe(events)
				clock.Advance(time.Second)
				watchdog.Tick()
			}
			if len(stalls) != 0 {
				t.Fatalf("normal golden triggered %d watchdog stalls: %v", len(stalls), stalls)
			}
		})
	}

	clock := &fakeClock{now: time.Unix(0, 0)}
	stalls := []int{}
	watchdog := diag.NewWatchdog(2*time.Second, clock, func(pendingEvents int) {
		stalls = append(stalls, pendingEvents)
	})
	redrawOnly := []protocol.Event{protocol.GridResize{Grid: 1, Width: 80, Height: 24}}
	watchdog.Observe(redrawOnly)
	clock.Advance(3 * time.Second)
	watchdog.Tick()
	clock.Advance(10 * time.Second)
	watchdog.Tick()
	if len(stalls) != 1 || stalls[0] != 1 {
		t.Fatalf("stalls = %v, want one pending event", stalls)
	}
	watchdog.Observe([]protocol.Event{protocol.Flush{}})
	watchdog.Observe(redrawOnly)
	clock.Advance(3 * time.Second)
	watchdog.Tick()
	if len(stalls) != 2 || stalls[1] != 1 {
		t.Fatalf("stalls after Flush rearm = %v, want two pending-event stalls", stalls)
	}
}

func completeMessageCount(t *testing.T, messages []protocol.Message, full, prefix []byte) int {
	t.Helper()
	if len(prefix) > len(full) {
		t.Fatalf("prefix has %d bytes, full stream has %d bytes", len(prefix), len(full))
	}
	offset := 0
	count := 0
	for _, message := range messages {
		end := offset + len(message.Raw)
		if end > len(full) || !bytes.Equal(message.Raw, full[offset:end]) {
			t.Fatalf("message Raw does not match full inbound at offset %d", offset)
		}
		if end > len(prefix) {
			return count
		}
		if !bytes.Equal(message.Raw, prefix[offset:end]) {
			t.Fatalf("message Raw does not match prefix at offset %d", offset)
		}
		offset = end
		count++
	}
	return count
}

func completeMessagesInPrefix(t *testing.T, messages []protocol.Message, full, prefix []byte) []protocol.Message {
	t.Helper()
	return messages[:completeMessageCount(t, messages, full, prefix)]
}

func replayAllGoldenStates(t *testing.T, golden oracle.Golden) []*screen.State {
	t.Helper()
	reducer := screen.NewReducer()
	var states []*screen.State
	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	for _, message := range messages {
		if message.Type != 2 || message.Method != "redraw" {
			continue
		}
		events, err := protocol.DecodeRedraw(message.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw: %v", err)
		}
		committed, err := reducer.Apply(events)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		states = append(states, committed...)
	}
	return states
}

func maxMessageBytes(messages []protocol.Message) int {
	maximum := 1
	for _, message := range messages {
		if len(message.Raw) > maximum {
			maximum = len(message.Raw)
		}
	}
	return maximum
}

func readTiming(t *testing.T, directory string) []timingRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "timing.jsonl"))
	if err != nil {
		t.Fatalf("read timing.jsonl: %v", err)
	}
	var records []timingRecord
	for lineNumber, line := range splitJSONLines(data) {
		if len(line) == 0 {
			continue
		}
		var record timingRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("timing.jsonl line %d: %v", lineNumber+1, err)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool { return records[left].Offset < records[right].Offset })
	return records
}

func splitJSONLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for index, value := range data {
		if value == '\n' {
			lines = append(lines, data[start:index])
			start = index + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

func assertBundleFiles(t *testing.T, directory string) {
	t.Helper()
	for _, name := range []string{"meta.json", "keyframe.json", "keyframe.gob", "inbound.mpack", "timing.jsonl"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("bundle missing %s: %v", name, err)
		}
	}
	for _, name := range []string{"draw-list", "input-trace"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("bundle missing directory %s: %v", name, err)
		}
	}
	metaData, err := os.ReadFile(filepath.Join(directory, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("decode meta.json: %v", err)
	}
	for _, key := range []string{"exported_at", "window_ms"} {
		if _, exists := meta[key]; !exists {
			t.Fatalf("meta.json missing %q", key)
		}
	}
}

func containsTail(bundle, written []byte) bool {
	for length := 1; length < len(written) && length <= len(bundle); length++ {
		if string(bundle[len(bundle)-length:]) == string(written[len(written)-length:]) {
			return true
		}
	}
	return false
}

func firstDiagGolden(t *testing.T) oracle.Golden {
	t.Helper()
	for _, golden := range oracle.List() {
		if golden.Name == "s01-boot" {
			return golden
		}
	}
	t.Fatal("s01-boot golden missing")
	return oracle.Golden{}
}

func TestMetaWindowMilliseconds(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	recorder := diag.NewRecorder(1234*time.Millisecond, clock)
	golden := firstDiagGolden(t)
	if _, err := recorder.Write(golden.Inbound); err != nil {
		t.Fatalf("Write: %v", err)
	}
	directory := filepath.Join(t.TempDir(), "bundle")
	if err := recorder.Export(directory, diag.Meta{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	want := float64(1234)
	if got, ok := meta["window_ms"].(float64); !ok || got != want {
		t.Fatalf("window_ms = %v, want %v", meta["window_ms"], want)
	}
	if err := recorder.Export(directory, diag.Meta{}); err == nil {
		t.Fatal("Export into non-empty directory succeeded, want error")
	}
}

func compareCanonical(want, got []byte) (int, string) {
	var wantValue, gotValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		return 0, "want: " + err.Error()
	}
	if err := json.Unmarshal(got, &gotValue); err != nil {
		return 0, "got: " + err.Error()
	}
	wildcards := 0
	if difference := compareCanonicalValue(wantValue, gotValue, "$", &wildcards); difference != "" {
		return wildcards, difference
	}
	return wildcards, ""
}

func compareCanonicalValue(want, got any, path string, wildcards *int) string {
	if isWildcardCell(want) {
		*wildcards++
		return ""
	}
	switch expected := want.(type) {
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: type %T, want object", path, got)
		}
		if len(expected) != len(actual) {
			return fmt.Sprintf("%s: object has %d keys, want %d", path, len(actual), len(expected))
		}
		for key, wantChild := range expected {
			gotChild, exists := actual[key]
			if !exists {
				return path + "." + key + ": missing key"
			}
			if difference := compareCanonicalValue(wantChild, gotChild, path+"."+key, wildcards); difference != "" {
				return difference
			}
		}
		return ""
	case []any:
		actual, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: type %T, want array", path, got)
		}
		if len(expected) != len(actual) {
			return fmt.Sprintf("%s: array length %d, want %d", path, len(actual), len(expected))
		}
		for index := range expected {
			if difference := compareCanonicalValue(expected[index], actual[index], path+"["+strconv.Itoa(index)+"]", wildcards); difference != "" {
				return difference
			}
		}
		return ""
	case float64:
		actual, ok := got.(float64)
		if !ok || expected != actual {
			return fmt.Sprintf("%s: %v, want %v", path, got, expected)
		}
		return ""
	case string:
		actual, ok := got.(string)
		if !ok || expected != actual {
			return fmt.Sprintf("%s: %q, want %q", path, got, expected)
		}
		return ""
	case bool:
		actual, ok := got.(bool)
		if !ok || expected != actual {
			return fmt.Sprintf("%s: %v, want %v", path, got, expected)
		}
		return ""
	case nil:
		if got != nil {
			return fmt.Sprintf("%s: %v, want null", path, got)
		}
		return ""
	default:
		return fmt.Sprintf("%s: unsupported canonical type %T", path, want)
	}
}

func isWildcardCell(value any) bool {
	cell, ok := value.([]any)
	if !ok || len(cell) != 2 {
		return false
	}
	text, ok := cell[0].(string)
	return ok && text == "\uFFFD"
}
