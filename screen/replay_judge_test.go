package screen_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/neovim/go-client/msgpack"

	"nvgui/oracle"
	"nvgui/protocol"
	"nvgui/screen"
)

const replacementCell = "\uFFFD"

type redrawBatch struct {
	Args  []any
	Bytes []byte
}

func TestGoldenReplay(t *testing.T) {
	for _, golden := range oracle.List() {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			batches := decodeGoldenBatches(t, golden)
			states := replayBatches(t, batches)
			if len(states) != len(golden.Frames) {
				t.Fatalf("frame count = %d, want %d", len(states), len(golden.Frames))
			}
			wildcardTotal := 0
			for frameIndex, state := range states {
				got, err := screen.Canonical(state)
				if err != nil {
					t.Fatalf("frame %d Canonical: %v", frameIndex, err)
				}
				wildcards, difference := compareCanonical(golden.Frames[frameIndex], got)
				wildcardTotal += wildcards
				if difference != "" {
					t.Fatalf("golden %s frame %d first difference at %s\ngot  %s\nwant %s", golden.Name, frameIndex, difference, compactJSON(got), compactJSON(golden.Frames[frameIndex]))
				}
			}
			if wildcardTotal > 0 {
				t.Logf("golden %s: skipped %d U+FFFD wildcard cells", golden.Name, wildcardTotal)
			}
		})
	}
}

func TestGoldenReplayHighlightBijection(t *testing.T) {
	for _, golden := range oracle.List() {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			batches := decodeGoldenBatches(t, golden)
			remapped := remapHighlightIDs(t, batches, 0x21+int64(len(golden.Name)))
			states := replayBatches(t, remapped)
			if len(states) != len(golden.Frames) {
				t.Fatalf("frame count = %d, want %d", len(states), len(golden.Frames))
			}
			for frameIndex, state := range states {
				got, err := screen.Canonical(state)
				if err != nil {
					t.Fatalf("frame %d Canonical: %v", frameIndex, err)
				}
				_, difference := compareCanonical(golden.Frames[frameIndex], got)
				if difference != "" {
					t.Fatalf("golden %s frame %d differs after hl id bijection at %s", golden.Name, frameIndex, difference)
				}
			}
		})
	}
}

func TestGoldenReplaySplitBeforeFlush(t *testing.T) {
	for _, golden := range oracle.List() {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			batches := decodeGoldenBatches(t, golden)
			states := replaySplitBatches(t, batches)
			if len(states) != len(golden.Frames) {
				t.Fatalf("frame count = %d, want %d", len(states), len(golden.Frames))
			}
			for frameIndex, state := range states {
				got, err := screen.Canonical(state)
				if err != nil {
					t.Fatalf("frame %d Canonical: %v", frameIndex, err)
				}
				_, difference := compareCanonical(golden.Frames[frameIndex], got)
				if difference != "" {
					t.Fatalf("golden %s frame %d differs after split at %s", golden.Name, frameIndex, difference)
				}
			}
		})
	}
}

func TestCommittedStateIsImmutable(t *testing.T) {
	golden := firstScreenGolden(t)
	batches := decodeGoldenBatches(t, golden)
	reducer := screen.NewReducer()
	var snapshots []*screen.State
	var canonicalAtCapture [][]byte
	for _, batch := range batches {
		events, err := protocol.DecodeRedraw(batch.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw: %v", err)
		}
		committed, err := reducer.Apply(events)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		for index, state := range committed {
			if index%2 == 0 {
				data, err := screen.Canonical(state)
				if err != nil {
					t.Fatalf("Canonical: %v", err)
				}
				snapshots = append(snapshots, state)
				canonicalAtCapture = append(canonicalAtCapture, data)
			}
		}
	}
	if len(snapshots) == 0 {
		t.Fatal("no committed states captured")
	}
	for index, state := range snapshots {
		data, err := screen.Canonical(state)
		if err != nil {
			t.Fatalf("Canonical after replay: %v", err)
		}
		if !bytes.Equal(data, canonicalAtCapture[index]) {
			t.Fatalf("committed snapshot %d changed after later Apply calls", index)
		}
	}
}

func TestLatestAndCanonicalRaceFree(t *testing.T) {
	golden := firstScreenGolden(t)
	batches := decodeGoldenBatches(t, golden)
	reducer := screen.NewReducer()
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			state := reducer.Latest()
			if state != nil {
				_, _ = screen.Canonical(state)
			}
		}
	}()
	for _, batch := range batches {
		events, err := protocol.DecodeRedraw(batch.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw: %v", err)
		}
		if _, err := reducer.Apply(events); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	close(stop)
	readers.Wait()
}

func decodeGoldenBatches(t *testing.T, golden oracle.Golden) []redrawBatch {
	t.Helper()
	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	var batches []redrawBatch
	for _, message := range messages {
		if message.Type == 2 && message.Method == "redraw" {
			batches = append(batches, redrawBatch{Args: message.Args, Bytes: message.Raw})
		}
	}
	return batches
}

func replayBatches(t *testing.T, batches []redrawBatch) []*screen.State {
	t.Helper()
	reducer := screen.NewReducer()
	var states []*screen.State
	for _, batch := range batches {
		events, err := protocol.DecodeRedraw(batch.Args)
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

func replaySplitBatches(t *testing.T, batches []redrawBatch) []*screen.State {
	t.Helper()
	reducer := screen.NewReducer()
	var states []*screen.State
	for _, batch := range batches {
		groups := eventGroups(t, batch.Args)
		cut := len(groups) / 2
		if cut == 0 {
			cut = 1
		}
		if cut > len(groups) {
			cut = len(groups)
		}
		for _, part := range [][][]any{groups[:cut], groups[cut:]} {
			if len(part) == 0 {
				continue
			}
			args := make([]any, 0, len(part))
			for _, group := range part {
				args = append(args, group)
			}
			events, err := protocol.DecodeRedraw(args)
			if err != nil {
				t.Fatalf("DecodeRedraw split: %v", err)
			}
			committed, err := reducer.Apply(events)
			if err != nil {
				t.Fatalf("Apply split: %v", err)
			}
			states = append(states, committed...)
		}
	}
	return states
}

func remapHighlightIDs(t *testing.T, batches []redrawBatch, seed int64) []redrawBatch {
	t.Helper()
	used := map[int64]bool{}
	for _, batch := range batches {
		for _, group := range eventGroups(t, batch.Args) {
			name, ok := group[0].(string)
			if !ok {
				continue
			}
			switch name {
			case "hl_attr_define":
				if len(group) > 1 {
					if id, ok := integerValue(group[1]); ok {
						used[id] = true
					}
				}
			case "grid_line":
				for _, item := range cellsOf(t, group) {
					if len(item) > 1 {
						if id, ok := integerValue(item[1]); ok {
							used[id] = true
						}
					}
				}
			case "mode_info_set":
				if len(group) > 2 {
					for _, raw := range asSlice(group[2]) {
						info, ok := raw.(map[string]any)
						if !ok {
							continue
						}
						for _, key := range []string{"attr_id", "attr_id_lm"} {
							if id, ok := integerValue(info[key]); ok {
								used[id] = true
							}
						}
					}
				}
			}
		}
	}
	ids := make([]int64, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	random := rand.New(rand.NewSource(seed))
	shuffled := append([]int64(nil), ids...)
	random.Shuffle(len(shuffled), func(left, right int) {
		shuffled[left], shuffled[right] = shuffled[right], shuffled[left]
	})
	mapping := map[int64]int64{}
	for index, id := range ids {
		mapping[id] = shuffled[index]
	}
	if len(mapping) < 2 {
		t.Fatalf("highlight id set too small for bijection: %v", ids)
	}
	seen := map[int64]bool{}
	for _, target := range mapping {
		if seen[target] {
			t.Fatalf("highlight id mapping is not injective: %v", mapping)
		}
		seen[target] = true
	}

	remapped := make([]redrawBatch, 0, len(batches))
	for _, batch := range batches {
		var groups []any
		for _, original := range eventGroups(t, batch.Args) {
			group := deepCopyAny(original).([]any)
			name, _ := group[0].(string)
			switch name {
			case "hl_attr_define":
				if len(group) > 1 {
					group[1] = mapID(group[1], mapping)
				}
			case "grid_line":
				if len(group) > 4 {
					cells := asSlice(group[4])
					for index, raw := range cells {
						cell := deepCopyAny(raw).([]any)
						if len(cell) > 1 {
							cell[1] = mapID(cell[1], mapping)
						}
						cells[index] = cell
					}
					group[4] = cells
				}
			case "mode_info_set":
				if len(group) > 2 {
					infos := asSlice(group[2])
					for index, raw := range infos {
						info := deepCopyAny(raw).(map[string]any)
						for _, key := range []string{"attr_id", "attr_id_lm"} {
							if value, exists := info[key]; exists {
								info[key] = mapID(value, mapping)
							}
						}
						infos[index] = info
					}
					group[2] = infos
				}
			}
			groups = append(groups, group)
		}
		remapped = append(remapped, redrawBatch{
			Args:  groups,
			Bytes: msgpackEncode(t, []any{int64(2), "redraw", groups}),
		})
	}
	return remapped
}

func eventGroups(t *testing.T, args []any) [][]any {
	t.Helper()
	groups := make([][]any, 0, len(args))
	for _, raw := range args {
		group, ok := raw.([]any)
		if !ok {
			t.Fatalf("redraw argument %T is not an event tuple", raw)
		}
		groups = append(groups, group)
	}
	return groups
}

func cellsOf(t *testing.T, group []any) [][]any {
	t.Helper()
	if len(group) < 5 {
		return nil
	}
	var cells [][]any
	for _, raw := range asSlice(group[4]) {
		cell, ok := raw.([]any)
		if !ok {
			t.Fatalf("grid_line cell %T is not an array", raw)
		}
		cells = append(cells, cell)
	}
	return cells
}

func asSlice(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return nil
}

func integerValue(value any) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case uint64:
		return int64(number), true
	case int:
		return int64(number), true
	case float64:
		return int64(number), true
	default:
		return 0, false
	}
}

func mapID(value any, mapping map[int64]int64) any {
	id, ok := integerValue(value)
	if !ok {
		return value
	}
	if mapped, found := mapping[id]; found {
		return mapped
	}
	return value
}

func deepCopyAny(value any) any {
	switch typed := value.(type) {
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = deepCopyAny(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = deepCopyAny(item)
		}
		return result
	case map[any]any:
		result := make(map[any]any, len(typed))
		for key, item := range typed {
			result[key] = deepCopyAny(item)
		}
		return result
	default:
		return typed
	}
}

func msgpackEncode(t *testing.T, value any) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := msgpack.NewEncoder(&buffer).Encode(value); err != nil {
		t.Fatalf("msgpack encode: %v", err)
	}
	return buffer.Bytes()
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
			childPath := path + "[" + strconv.Itoa(index) + "]"
			if difference := compareCanonicalValue(expected[index], actual[index], childPath, wildcards); difference != "" {
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
	return ok && text == replacementCell
}

func compactJSON(data []byte) string {
	return strings.TrimSpace(string(data))
}

func firstScreenGolden(t *testing.T) oracle.Golden {
	t.Helper()
	goldens := oracle.List()
	if len(goldens) == 0 {
		t.Fatal("oracle.List() returned no goldens")
	}
	return goldens[0]
}
