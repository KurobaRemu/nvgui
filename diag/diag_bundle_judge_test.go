package diag_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvgui/diag"
	"nvgui/drawlist"
	"nvgui/oracle"
	"nvgui/protocol"
	"nvgui/screen"
)

func exportGoldenM1Bundle(t *testing.T, golden oracle.Golden, inputs []diag.TraceEvent) string {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	recorder := diag.NewRecorder(30*time.Second, clock)
	reducer := screen.NewReducer()

	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("%s: ReadMessages: %v", golden.Name, err)
	}
	for _, msg := range messages {
		if _, err := recorder.Write(msg.Raw); err != nil {
			t.Fatalf("%s: Write: %v", golden.Name, err)
		}
		// Realistic 1ms message step (avoid coarse 1s/message fake clocks).
		clock.Advance(1 * time.Millisecond)
		if msg.Type != 2 || msg.Method != "redraw" {
			continue
		}
		events, err := protocol.DecodeRedraw(msg.Args)
		if err != nil {
			t.Fatalf("%s: DecodeRedraw: %v", golden.Name, err)
		}
		committed, err := reducer.Apply(events)
		if err != nil {
			t.Fatalf("%s: Apply: %v", golden.Name, err)
		}
		for _, state := range committed {
			recorder.Keyframe(state)
		}
	}
	for _, ev := range inputs {
		recorder.RecordInput(ev)
	}

	dir := filepath.Join(t.TempDir(), golden.Name)
	meta := diag.Meta{
		NvimVersion: golden.Meta.NvimVersion,
		APILevel:    golden.Meta.APILevel,
		UIOptions:   golden.Meta.UIOptions,
		GUIVersion:  "m1-bundle-test",
		GOOS:        "test",
	}
	if err := recorder.Export(dir, meta); err != nil {
		t.Fatalf("%s: Export: %v", golden.Name, err)
	}
	return dir
}

func TestDiagBundleExportReadAndReplay(t *testing.T) {
	byName := map[string]oracle.Golden{}
	for _, g := range oracle.List() {
		byName[g.Name] = g
	}

	base := time.Unix(1_700_000_000, 0).UTC()
	sampleInputs := []diag.TraceEvent{
		{Seq: 1, At: base, Kind: diag.TraceKeyDown, Key: 1, Code: 4},
		{Seq: 2, At: base.Add(2 * time.Millisecond), Kind: diag.TraceCompose, Text: "ni", Caret: 2},
		{Seq: 3, At: base.Add(4 * time.Millisecond), Kind: diag.TraceText, Text: "你"},
		{Seq: 4, At: base.Add(6 * time.Millisecond), Kind: diag.TraceKeyUp, Key: 1, Code: 4},
	}

	for _, name := range []string{"s01-boot", "s04-float-border", "s05-wide-chars", "s06-highlight"} {
		golden, ok := byName[name]
		if !ok {
			t.Fatalf("missing golden %s", name)
		}
		t.Run(name, func(t *testing.T) {
			dir := exportGoldenM1Bundle(t, golden, sampleInputs)

			// M0 files + M1 files and index.json must all exist.
			assertBundleFiles(t, dir)
			for _, rel := range []string{"draw-list.jsonl", "input-trace.jsonl", "draw-list/index.json", "input-trace/index.json"} {
				data, err := os.ReadFile(filepath.Join(dir, rel))
				if err != nil {
					t.Fatalf("missing %s: %v", rel, err)
				}
				if strings.HasSuffix(rel, ".json") && !json.Valid(data) {
					t.Fatalf("%s is not valid JSON", rel)
				}
			}

			bundle, err := diag.ReadBundle(dir)
			if err != nil {
				t.Fatalf("ReadBundle: %v", err)
			}
			if bundle == nil {
				t.Fatal("ReadBundle returned nil bundle")
			}
			if len(bundle.States) == 0 {
				t.Fatal("ReadBundle returned zero States")
			}
			if len(bundle.DrawLists) != len(bundle.States) {
				t.Fatalf("DrawLists len = %d, States len = %d, want equal", len(bundle.DrawLists), len(bundle.States))
			}
			for i := range bundle.States {
				if bundle.States[i] == nil || bundle.DrawLists[i] == nil {
					t.Fatalf("frame %d has nil state or drawlist", i)
				}
				if bundle.DrawLists[i].Seq != bundle.States[i].Seq {
					t.Errorf("frame %d: DrawList.Seq=%d != State.Seq=%d", i, bundle.DrawLists[i].Seq, bundle.States[i].Seq)
				}
				if i > 0 && bundle.DrawLists[i].Seq <= bundle.DrawLists[i-1].Seq {
					t.Errorf("DrawList.Seq not strictly monotonic at %d: %d <= %d", i, bundle.DrawLists[i].Seq, bundle.DrawLists[i-1].Seq)
				}
			}
			if len(bundle.InputEvents) != len(sampleInputs) {
				t.Fatalf("InputEvents len = %d, want %d", len(bundle.InputEvents), len(sampleInputs))
			}
			for i, got := range bundle.InputEvents {
				want := sampleInputs[i]
				if got.Seq != want.Seq || got.Kind != want.Kind || got.Text != want.Text || got.Caret != want.Caret {
					t.Errorf("InputEvents[%d] = %+v, want %+v", i, got, want)
				}
			}

			replayed, err := diag.ReplayBundle(dir)
			if err != nil {
				t.Fatalf("ReplayBundle: %v", err)
			}
			if replayed == nil {
				t.Fatal("ReplayBundle returned nil bundle")
			}
			if len(replayed.States) != len(bundle.States) || len(replayed.DrawLists) != len(bundle.DrawLists) {
				t.Fatalf("ReplayBundle counts = (%d states, %d drawlists), want (%d, %d)",
					len(replayed.States), len(replayed.DrawLists), len(bundle.States), len(bundle.DrawLists))
			}
			for i := range replayed.States {
				gotCanon, err := screen.Canonical(replayed.States[i])
				if err != nil {
					t.Fatalf("Canonical(replayed[%d]): %v", i, err)
				}
				wantCanon, err := screen.Canonical(bundle.States[i])
				if err != nil {
					t.Fatalf("Canonical(bundle[%d]): %v", i, err)
				}
				if !bytes.Equal(gotCanon, wantCanon) {
					t.Fatalf("frame %d: replayed State differs from bundle State", i)
				}

				gotDL, err := drawlist.JSON(replayed.DrawLists[i])
				if err != nil {
					t.Fatalf("JSON(replayed DrawList[%d]): %v", i, err)
				}
				wantDL, err := drawlist.JSON(bundle.DrawLists[i])
				if err != nil {
					t.Fatalf("JSON(bundle DrawList[%d]): %v", i, err)
				}
				if !bytes.Equal(gotDL, wantDL) {
					t.Fatalf("frame %d: replayed DrawList differs from bundle DrawList", i)
				}
			}
		})
	}
}

func TestDiagBundleRejectsCorruption(t *testing.T) {
	golden := firstDiagGolden(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	inputs := []diag.TraceEvent{
		{Seq: 1, At: base, Kind: diag.TraceText, Text: "a"},
		{Seq: 2, At: base.Add(time.Millisecond), Kind: diag.TraceText, Text: "b"},
	}

	copyDir := func(t *testing.T, src string) string {
		t.Helper()
		dst := filepath.Join(t.TempDir(), "corrupt")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		err := filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(src, path)
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

	validDir := exportGoldenM1Bundle(t, golden, inputs)

	t.Run("missing-draw-list-jsonl", func(t *testing.T) {
		dir := copyDir(t, validDir)
		_ = os.Remove(filepath.Join(dir, "draw-list.jsonl"))
		if _, err := diag.ReadBundle(dir); err == nil {
			t.Fatal("ReadBundle with missing draw-list.jsonl returned nil error")
		}
	})

	t.Run("missing-input-trace-jsonl", func(t *testing.T) {
		dir := copyDir(t, validDir)
		_ = os.Remove(filepath.Join(dir, "input-trace.jsonl"))
		if _, err := diag.ReadBundle(dir); err == nil {
			t.Fatal("ReadBundle with missing input-trace.jsonl returned nil error")
		}
	})

	t.Run("truncated-draw-list-line", func(t *testing.T) {
		dir := copyDir(t, validDir)
		p := filepath.Join(dir, "draw-list.jsonl")
		data, _ := os.ReadFile(p)
		_ = os.WriteFile(p, append(data, []byte(`{"v":1,"seq":`)...), 0o644)
		if _, err := diag.ReadBundle(dir); err == nil {
			t.Fatal("ReadBundle with truncated draw-list.jsonl returned nil error")
		}
	})

	t.Run("truncated-input-trace-line", func(t *testing.T) {
		dir := copyDir(t, validDir)
		p := filepath.Join(dir, "input-trace.jsonl")
		data, _ := os.ReadFile(p)
		_ = os.WriteFile(p, append(data, []byte(`{"seq":99,`)...), 0o644)
		if _, err := diag.ReadBundle(dir); err == nil {
			t.Fatal("ReadBundle with truncated input-trace.jsonl returned nil error")
		}
	})

	t.Run("out-of-order-input-trace-seq", func(t *testing.T) {
		dir := copyDir(t, validDir)
		line1, _ := json.Marshal(diag.TraceEvent{Seq: 5, At: base, Kind: diag.TraceText, Text: "x"})
		line2, _ := json.Marshal(diag.TraceEvent{Seq: 3, At: base.Add(time.Millisecond), Kind: diag.TraceText, Text: "y"})
		payload := append(append(line1, '\n'), append(line2, '\n')...)
		_ = os.WriteFile(filepath.Join(dir, "input-trace.jsonl"), payload, 0o644)
		if _, err := diag.ReadBundle(dir); err == nil {
			t.Fatal("ReadBundle with out-of-order input-trace.jsonl Seq returned nil error")
		}
	})

	t.Run("tampered-draw-list-detected-by-replay", func(t *testing.T) {
		dir := copyDir(t, validDir)
		p := filepath.Join(dir, "draw-list.jsonl")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read draw-list.jsonl: %v", err)
		}
		lines := splitJSONLines(data)
		var rebuilt bytes.Buffer
		mutated := false
		for _, line := range lines {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var dl drawlist.List
			if err := json.Unmarshal(line, &dl); err != nil {
				t.Fatalf("unmarshal draw-list.jsonl: %v", err)
			}
			if !mutated {
				dl.Width += 7
				mutated = true
			}
			enc, err := json.Marshal(&dl)
			if err != nil {
				t.Fatalf("marshal mutated draw-list: %v", err)
			}
			rebuilt.Write(enc)
			rebuilt.WriteByte('\n')
		}
		if !mutated {
			t.Fatal("draw-list.jsonl had no lines to tamper")
		}
		if err := os.WriteFile(p, rebuilt.Bytes(), 0o644); err != nil {
			t.Fatalf("write mutated draw-list.jsonl: %v", err)
		}
		if _, err := diag.ReplayBundle(dir); err == nil {
			t.Fatal("ReplayBundle failed to detect tampered draw-list.jsonl")
		}
	})
}
