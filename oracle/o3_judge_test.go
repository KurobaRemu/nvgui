package oracle_test

import (
	"encoding/json"
	"strings"
	"testing"

	"nvgui/oracle"
	"nvgui/protocol"
	"nvgui/screen"
)

func TestO3ParseSampleValidation(t *testing.T) {
	validJSON := []byte(`{
		"v": 1,
		"width": 80,
		"height": 24,
		"nvim_version": "v0.12.6",
		"cells": [
			{"row": 0, "col": 0, "text": "世", "width": 2, "hl": {"foreground": 16777215, "bold": true}},
			{"row": 0, "col": 1, "text": "", "width": 0, "hl": {"foreground": 16777215, "bold": true}},
			{"row": 0, "col": 2, "text": "─", "width": 1}
		]
	}`)
	sample, err := oracle.ParseO3Sample(validJSON)
	if err != nil {
		t.Fatalf("ParseO3Sample(valid): %v", err)
	}
	if sample == nil {
		t.Fatal("ParseO3Sample returned nil")
	}
	if sample.Version != 1 || sample.Width != 80 || sample.Height != 24 || len(sample.Cells) != 3 {
		t.Fatalf("parsed sample = %+v", sample)
	}

	invalidCases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"bad-json", "{not-json"},
		{"wrong-version", `{"v":2,"width":80,"height":24,"cells":[]}`},
		{"zero-width", `{"v":1,"width":0,"height":24,"cells":[]}`},
		{"oob-row", `{"v":1,"width":80,"height":24,"cells":[{"row":24,"col":0,"text":"a","width":1}]}`},
		{"oob-col", `{"v":1,"width":80,"height":24,"cells":[{"row":0,"col":-1,"text":"a","width":1}]}`},
		{"bad-width", `{"v":1,"width":80,"height":24,"cells":[{"row":0,"col":0,"text":"a","width":3}]}`},
		{"duplicate-cell", `{"v":1,"width":80,"height":24,"cells":[{"row":1,"col":2,"text":"a","width":1},{"row":1,"col":2,"text":"a","width":1}]}`},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := oracle.ParseO3Sample([]byte(tc.raw)); err == nil {
				t.Errorf("ParseO3Sample(%s) = nil error, want error", tc.name)
			}
		})
	}
}

func TestO3CompareOrderIndependenceAndFirstDiff(t *testing.T) {
	a := &oracle.O3Sample{
		Version: 1,
		Width:   120,
		Height:  40,
		Cells: []oracle.O3Cell{
			{Row: 2, Col: 5, Text: "b", Width: 1, HL: map[string]any{"bold": true, "foreground": int64(0x112233)}},
			{Row: 0, Col: 0, Text: "世", Width: 2, HL: map[string]any{"italic": true}},
		},
	}
	b := &oracle.O3Sample{
		Version: 1,
		Width:   120,
		Height:  40,
		Cells: []oracle.O3Cell{
			{Row: 0, Col: 0, Text: "世", Width: 2, HL: map[string]any{"italic": true}},
			{Row: 2, Col: 5, Text: "b", Width: 1, HL: map[string]any{"foreground": 0x112233, "bold": true}},
		},
	}
	if diff, ok := oracle.CompareO3(a, b); !ok || diff != "" {
		t.Fatalf("CompareO3(equivalent) = (%q, %v), want (\"\", true)", diff, ok)
	}

	// Text mismatch reports exact cell path.
	bText := *b
	bText.Cells = append([]oracle.O3Cell(nil), b.Cells...)
	bText.Cells[1].Text = "x"
	diff, ok := oracle.CompareO3(a, &bText)
	if ok || !strings.Contains(diff, "cell[2,5].text") {
		t.Fatalf("CompareO3(text mismatch) = (%q, %v), want cell[2,5].text diff", diff, ok)
	}

	// HL attribute mismatch reports exact hl key path.
	bHL := *b
	bHL.Cells = append([]oracle.O3Cell(nil), b.Cells...)
	bHL.Cells[1].HL = map[string]any{"foreground": int64(0x999999), "bold": true}
	diff, ok = oracle.CompareO3(a, &bHL)
	if ok || !strings.Contains(diff, "cell[2,5].hl.foreground") {
		t.Fatalf("CompareO3(hl mismatch) = (%q, %v), want cell[2,5].hl.foreground diff", diff, ok)
	}

	// Size mismatch reports size path.
	bSize := *b
	bSize.Width = 80
	diff, ok = oracle.CompareO3(a, &bSize)
	if ok || !strings.Contains(diff, "size") {
		t.Fatalf("CompareO3(size mismatch) = (%q, %v), want size diff", diff, ok)
	}
}

func TestO3ExtractFromGoldenStates(t *testing.T) {
	byName := map[string]oracle.Golden{}
	for _, g := range oracle.List() {
		byName[g.Name] = g
	}
	for _, name := range []string{"s01-boot", "s05-wide-chars", "s06-highlight"} {
		golden, ok := byName[name]
		if !ok {
			t.Fatalf("missing golden %s", name)
		}
		messages, err := protocol.ReadMessages(golden.Inbound)
		if err != nil {
			t.Fatalf("%s: ReadMessages: %v", name, err)
		}
		reducer := screen.NewReducer()
		var last *screen.State
		for _, msg := range messages {
			if msg.Type != 2 || msg.Method != "redraw" {
				continue
			}
			events, err := protocol.DecodeRedraw(msg.Args)
			if err != nil {
				t.Fatalf("%s: DecodeRedraw: %v", name, err)
			}
			committed, err := reducer.Apply(events)
			if err != nil {
				t.Fatalf("%s: Apply: %v", name, err)
			}
			if len(committed) > 0 {
				last = committed[len(committed)-1]
			}
		}
		if last == nil {
			t.Fatalf("%s: no committed state", name)
		}
		root := last.Grids[1]
		coords := [][2]int{{0, 0}, {0, 1}, {0, 2}, {0, 3}}
		if root.Height > 1 {
			coords = append(coords, [2]int{1, 0}, [2]int{1, 1})
		}
		sample, err := oracle.ExtractO3FromState(last, coords)
		if err != nil {
			t.Fatalf("%s: ExtractO3FromState: %v", name, err)
		}
		raw, err := json.Marshal(sample)
		if err != nil {
			t.Fatalf("%s: json.Marshal: %v", name, err)
		}
		reparsed, err := oracle.ParseO3Sample(raw)
		if err != nil {
			t.Fatalf("%s: ParseO3Sample round-trip: %v", name, err)
		}
		if diff, eq := oracle.CompareO3(sample, reparsed); !eq {
			t.Fatalf("%s: round-trip CompareO3 diff: %s", name, diff)
		}
		if name == "s05-wide-chars" {
			// Row 0 col 0 is "世" (double-width), col 1 is continuation cell.
			if sample.Cells[0].Text != "世" || sample.Cells[0].Width != 2 {
				t.Errorf("s05-wide-chars cell(0,0) = %+v, want Text=\"世\" Width=2", sample.Cells[0])
			}
			if sample.Cells[1].Text != "" || sample.Cells[1].Width != 0 {
				t.Errorf("s05-wide-chars cell(0,1) = %+v, want continuation Text=\"\" Width=0", sample.Cells[1])
			}
		}
	}
}
