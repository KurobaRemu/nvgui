package screen_test

// Covered P0 IDs: neovide#1584, neovide#720, neovide#3150, neovim#25377,
// vimr#768, neovim#38916.
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
	"path/filepath"
	"strings"
	"testing"

	"nvgui/connection"
	"nvgui/diag"
	"nvgui/protocol"
	"nvgui/screen"
	"nvgui/session"
)

func TestPriorBug_Neovide1584_Neovide720_MultigridFloatLayerOrderAndBlend(t *testing.T) {
	reducer := screen.NewReducer()
	events := []protocol.Event{
		protocol.GridResize{Grid: 1, Width: 20, Height: 8},
		protocol.GridResize{Grid: 2, Width: 6, Height: 3},
		protocol.GridResize{Grid: 3, Width: 6, Height: 3},
		protocol.GridResize{Grid: 4, Width: 6, Height: 3},
		protocol.HlAttrDefine{ID: 1, RGB: map[string]any{"foreground": int64(0x112233), "blend": int64(30)}},
		protocol.GridLine{Grid: 2, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "B", HL: 1, Repeat: 1}}},
		protocol.GridLine{Grid: 3, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "C", HL: 1, Repeat: 1}}},
		protocol.GridLine{Grid: 4, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "D", HL: 1, Repeat: 1}}},
		protocol.WinFloatPos{
			Grid: 3, Win: 1003, Anchor: "NW", AnchorGrid: 1,
			AnchorRow: 2, AnchorCol: 4, ZIndex: 20, CompIndex: 1,
			ScreenRow: 2, ScreenCol: 4,
		},
		protocol.WinFloatPos{
			Grid: 2, Win: 1002, Anchor: "NW", AnchorGrid: 1,
			AnchorRow: 1, AnchorCol: 2, ZIndex: 50, CompIndex: 2,
			ScreenRow: 1, ScreenCol: 2,
		},
		protocol.WinFloatPos{
			Grid: 4, Win: 1004, Anchor: "NW", AnchorGrid: 1,
			AnchorRow: 0, AnchorCol: 0, ZIndex: 10, CompIndex: 3,
			ScreenRow: 0, ScreenCol: 0,
		},
		protocol.Flush{},
	}
	committed, err := reducer.Apply(events)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	state := lastCommitted(t, committed)
	wantLayers := []screen.Layer{
		{Grid: 1},
		{Grid: 3, ScreenRow: 2, ScreenCol: 4},
		{Grid: 2, ScreenRow: 1, ScreenCol: 2},
		{Grid: 4, ScreenRow: 0, ScreenCol: 0},
	}
	gotLayers := screen.Layers(state)
	if fmt.Sprint(gotLayers) != fmt.Sprint(wantLayers) {
		t.Fatalf("Layers = %#v, want %#v", gotLayers, wantLayers)
	}
	for gridID, text := range map[int]string{2: "B", 3: "C", 4: "D"} {
		if cell := state.Grids[gridID].Cells[0][0]; cell.Text != text || cell.HL != 1 {
			t.Fatalf("grid %d cell = %+v, want %q with hl 1", gridID, cell, text)
		}
	}
	if blend, ok := state.Attrs[1]["blend"]; !ok || blend != int64(30) {
		t.Fatalf("blend = %#v, want 30", state.Attrs[1]["blend"])
	}
	canonical, err := screen.Canonical(state)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	var canonicalValue map[string]any
	if err := json.Unmarshal(canonical, &canonicalValue); err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	placements := canonicalValue["placements"].(map[string]any)
	for _, key := range []string{"2", "3", "4"} {
		if _, exists := placements[key]; !exists {
			t.Fatalf("canonical placements missing float grid %s", key)
		}
	}
	if !strings.Contains(string(canonical), `"blend":30`) {
		t.Fatalf("canonical JSON lost blend attribute: %s", canonical)
	}

	closed, err := reducer.Apply([]protocol.Event{
		protocol.WinClose{Grid: 2},
		protocol.GridClear{Grid: 2},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply close: %v", err)
	}
	closedState := lastCommitted(t, closed)
	for _, layer := range screen.Layers(closedState) {
		if layer.Grid == 2 {
			t.Fatal("closed float grid 2 remains a layer")
		}
	}
}

func TestPriorBug_Neovide3150_StatuslinePreservedAcrossSplitsAndResize(t *testing.T) {
	for _, lastStatus := range []int{2, 3} {
		t.Run(fmt.Sprintf("laststatus=%d", lastStatus), func(t *testing.T) {
			sess := startPriorBugSession(t, 80, 24)
			if err := sess.Nvim().SetOption("laststatus", lastStatus); err != nil {
				t.Fatalf("set laststatus: %v", err)
			}
			if err := sess.Nvim().Command("set statusline=NVGUI_STATUSLINE_MARK"); err != nil {
				t.Fatalf("set statusline: %v", err)
			}
			if err := sess.Nvim().Command("new"); err != nil {
				t.Fatalf("split: %v", err)
			}
			initial := exportBarrierPriorBugBundle(t, sess)
			assertStatuslineLayout(t, sess, initial.States[len(initial.States)-1], "NVGUI_STATUSLINE_MARK", lastStatus)

			if err := sess.Nvim().TryResizeUI(60, 18); err != nil {
				t.Fatalf("nvim_ui_try_resize: %v", err)
			}
			if err := sess.Nvim().Command("vnew"); err != nil {
				t.Fatalf("vertical split: %v", err)
			}
			if err := sess.Nvim().Command("redraw!"); err != nil {
				t.Fatalf("redraw!: %v", err)
			}
			var barrier int
			if err := sess.Nvim().Eval("1", &barrier); err != nil {
				t.Fatalf("RPC barrier: %v", err)
			}
			resized := exportAndReadPriorBugBundle(t, sess)
			state := resized.States[len(resized.States)-1]
			assertStatuslineLayout(t, sess, state, "NVGUI_STATUSLINE_MARK", lastStatus)
			if state.Grids[1] == nil || state.Grids[1].Width != 60 || state.Grids[1].Height != 18 {
				t.Fatalf("root grid after resize = %#v, want 60x18", state.Grids[1])
			}
			if len(state.Grids) < 2 {
				t.Fatalf("grid count after splits = %d, want at least 2", len(state.Grids))
			}
		})
	}
}

func TestPriorBug_Neovim25377_NoPartialCommitBeforeFlush(t *testing.T) {
	reducer := screen.NewReducer()
	batches := [][]protocol.Event{
		{
			protocol.GridResize{Grid: 1, Width: 8, Height: 3},
			protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "S", Repeat: 1}}},
		},
		{
			protocol.GridLine{Grid: 1, Row: 1, ColStart: 0, Cells: []protocol.LineCell{{Text: "T", Repeat: 1}}},
			protocol.HlAttrDefine{ID: 9, RGB: map[string]any{"foreground": int64(0x123456)}},
		},
		{
			protocol.GridLine{Grid: 1, Row: 2, ColStart: 0, Cells: []protocol.LineCell{{Text: "U", HL: 9, Repeat: 1}}},
		},
		{protocol.Flush{}},
	}
	for index, batch := range batches[:3] {
		committed, err := reducer.Apply(batch)
		if err != nil {
			t.Fatalf("Apply batch %d: %v", index, err)
		}
		if len(committed) != 0 {
			t.Fatalf("batch %d committed %d states before flush", index, len(committed))
		}
		if latest := reducer.Latest(); latest != nil {
			t.Fatalf("Latest() exposed a half frame after batch %d: Seq %d", index, latest.Seq)
		}
	}
	committed, err := reducer.Apply(batches[3])
	if err != nil {
		t.Fatalf("Apply flush: %v", err)
	}
	if len(committed) != 1 {
		t.Fatalf("flush committed %d states, want 1", len(committed))
	}
	state := committed[0]
	if state.Seq != 1 || reducer.Latest() != state {
		t.Fatalf("Latest() = %p (Seq %d), want committed state %p (Seq 1)", reducer.Latest(), state.Seq, state)
	}
	for row, text := range []string{"S", "T", "U"} {
		if got := state.Grids[1].Cells[row][0].Text; got != text {
			t.Fatalf("row %d = %q, want %q", row, got, text)
		}
	}
	if _, exists := state.Attrs[9]; !exists {
		t.Fatal("flush commit lost highlight definition 9")
	}
}

func TestPriorBug_VimR768_FloatCloseAndIdempotentGridDestroy(t *testing.T) {
	reducer := screen.NewReducer()
	events := []protocol.Event{
		protocol.GridResize{Grid: 1, Width: 12, Height: 5},
		protocol.GridResize{Grid: 2, Width: 6, Height: 2},
		protocol.GridLine{Grid: 1, Row: 0, ColStart: 0, Cells: []protocol.LineCell{{Text: "M", Repeat: 1}}},
		protocol.WinFloatPos{
			Grid: 2, Win: 1002, Anchor: "NW", AnchorGrid: 1,
			ZIndex: 10, CompIndex: 1, ScreenRow: 1, ScreenCol: 1,
		},
		protocol.Flush{},
	}
	if _, err := reducer.Apply(events); err != nil {
		t.Fatalf("Apply setup: %v", err)
	}
	before := reducer.Latest()

	closing := []protocol.Event{
		protocol.GridLine{Grid: 1, Row: 1, ColStart: 0, Cells: []protocol.LineCell{{Text: "K", Repeat: 1}}},
		protocol.WinClose{Grid: 2},
		protocol.GridDestroy{Grid: 2},
		protocol.GridDestroy{Grid: 2},
		protocol.GridDestroy{Grid: 999},
		protocol.WinClose{Grid: 999},
		protocol.Flush{},
	}
	committed, err := reducer.Apply(closing)
	if err != nil {
		t.Fatalf("Apply close/destroy batch: %v", err)
	}
	if len(committed) != 1 {
		t.Fatalf("committed %d states, want 1", len(committed))
	}
	state := committed[0]
	if got := state.Grids[1].Cells[1][0].Text; got != "K" {
		t.Fatalf("same-batch main grid update = %q, want %q", got, "K")
	}
	if _, exists := state.Grids[2]; exists {
		t.Fatal("destroyed float grid 2 remains in Grids")
	}
	if _, exists := state.Placements[2]; exists {
		t.Fatal("closed float grid 2 remains in Placements")
	}
	for _, layer := range screen.Layers(state) {
		if layer.Grid == 2 {
			t.Fatal("destroyed float grid 2 remains a layer")
		}
	}
	if got := before.Grids[1].Cells[1][0].Text; got != " " {
		t.Fatalf("previous committed state mutated to %q", got)
	}
}

func TestPriorBug_Neovim38916_NoWidgetExtCoexistenceWithMsgSetPos(t *testing.T) {
	sess := startPriorBugSession(t, 80, 24)
	options := []struct {
		name string
		want bool
	}{
		{"ext_linegrid", true},
		{"ext_multigrid", true},
		{"ext_hlstate", true},
		{"ext_cmdline", false},
		{"ext_messages", false},
		{"ext_popupmenu", false},
	}
	var uis []map[string]any
	if err := sess.Nvim().Eval("nvim_list_uis()", &uis); err != nil {
		t.Fatalf("nvim_list_uis: %v", err)
	}
	if len(uis) == 0 {
		t.Fatal("nvim_list_uis returned no UI")
	}
	for _, option := range options {
		value := uis[0][option.name]
		if value == nil {
			value = false
		}
		if value != option.want {
			t.Fatalf("UI option %s = %#v, want %v", option.name, value, option.want)
		}
	}

	const marker = "NVGUI_MSG_38916"
	const mainText = "MAIN_GRID_SENTINEL"
	if err := sess.Nvim().Call("nvim_buf_set_lines", nil, 0, 0, -1, true, []string{mainText}); err != nil {
		t.Fatalf("nvim_buf_set_lines: %v", err)
	}
	if err := sess.Nvim().Command(fmt.Sprintf("echomsg %q", marker)); err != nil {
		t.Fatalf("echomsg: %v", err)
	}
	if err := sess.Nvim().Command("redraw!"); err != nil {
		t.Fatalf("redraw!: %v", err)
	}
	var barrier int
	if err := sess.Nvim().Eval("1", &barrier); err != nil {
		t.Fatalf("RPC barrier: %v", err)
	}

	bundle := exportAndReadPriorBugBundle(t, sess)
	state := bundle.States[len(bundle.States)-1]
	if state == nil {
		t.Fatal("bundle has no committed states")
	}
	if state.Msg == nil {
		t.Fatal("real Neovim message produced no Msg placement")
	}
	messageGrid := state.Grids[state.Msg.Grid]
	if messageGrid == nil {
		t.Fatalf("message grid %d is missing from Committed Screen", state.Msg.Grid)
	}
	if !priorBugGridContains(messageGrid, marker) {
		t.Fatalf("message grid %d lost %q", state.Msg.Grid, marker)
	}
	if state.Grids[1] == nil || !anyPriorBugGridContains(state, mainText) {
		t.Fatalf("main grid text %q is not intact", mainText)
	}
	if _, exists := state.Placements[state.Msg.Grid]; exists {
		t.Fatalf("message grid %d unexpectedly has a window placement", state.Msg.Grid)
	}
	layers := screen.Layers(state)
	if len(layers) == 0 || layers[len(layers)-1].Grid != state.Msg.Grid {
		t.Fatalf("layers = %#v, want message grid %d last", layers, state.Msg.Grid)
	}
	if layers[len(layers)-1].ScreenRow != state.Msg.Row {
		t.Fatalf("message layer row = %d, want Msg.Row %d", layers[len(layers)-1].ScreenRow, state.Msg.Row)
	}
	canonical, err := screen.Canonical(state)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	var canonicalValue struct {
		Grids map[string]struct {
			Rows [][][2]any `json:"rows"`
		} `json:"grids"`
		Msg *struct {
			Grid int `json:"grid"`
		} `json:"msg"`
	}
	if err := json.Unmarshal(canonical, &canonicalValue); err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	if canonicalValue.Msg == nil || canonicalValue.Msg.Grid != state.Msg.Grid {
		t.Fatalf("canonical msg = %+v, want grid %d", canonicalValue.Msg, state.Msg.Grid)
	}
	canonicalRowText := func(row [][2]any) string {
		var builder strings.Builder
		for _, cell := range row {
			text, _ := cell[0].(string)
			builder.WriteString(text)
		}
		return builder.String()
	}
	canonicalContains := func(text string) bool {
		for _, grid := range canonicalValue.Grids {
			for _, row := range grid.Rows {
				if strings.Contains(canonicalRowText(row), text) {
					return true
				}
			}
		}
		return false
	}
	if !canonicalContains(marker) || !canonicalContains(mainText) {
		t.Fatal("canonical JSON lost real message or main grid text")
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

func exportAndReadPriorBugBundle(t *testing.T, sess *session.Session) *diag.Bundle {
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
	return exportAndReadPriorBugBundle(t, sess)
}

func assertStatuslineLayout(t *testing.T, sess *session.Session, state *screen.State, marker string, lastStatus int) {
	t.Helper()
	markerRows := map[int][]int{}
	markerOccurrences := 0
	for _, layer := range screen.Layers(state) {
		grid := state.Grids[layer.Grid]
		if grid == nil {
			continue
		}
		for rowIndex, row := range grid.Cells {
			if occurrences := strings.Count(priorBugRowText(row), marker); occurrences > 0 {
				markerRows[layer.Grid] = append(markerRows[layer.Grid], rowIndex)
				markerOccurrences += occurrences
			}
		}
	}
	if len(markerRows) == 0 {
		t.Fatal("statusline marker is missing from Committed Screen")
	}
	var statuslineCount int
	if err := sess.Nvim().Eval(`len(filter(range(1, winnr('$')), 'getwinvar(v:val, "&statusline") !=# ""'))`, &statuslineCount); err != nil {
		t.Fatalf("count statuslines: %v", err)
	}
	if statuslineCount == 0 {
		t.Fatal("Neovim reported no window with statusline")
	}
	if lastStatus == 2 {
		var windowCount int
		if err := sess.Nvim().Eval("winnr('$')", &windowCount); err != nil {
			t.Fatalf("count windows: %v", err)
		}
		if markerOccurrences != windowCount {
			t.Fatalf("laststatus=2 produced %d statusline markers for %d windows, want one per window", markerOccurrences, windowCount)
		}
		if statuslineCount != windowCount {
			t.Fatalf("laststatus=2 reports %d windows with statusline, want %d", statuslineCount, windowCount)
		}
		if len(markerRows) != 1 {
			t.Fatalf("laststatus=2 statuslines are spread across %d grids, want the root grid", len(markerRows))
		}
	}
	if lastStatus == 3 {
		root := state.Grids[1]
		if root == nil {
			t.Fatal("root grid 1 is missing")
		}
		globalRow := root.Height - 2
		foundGlobal := false
		for gridID, rows := range markerRows {
			for _, row := range rows {
				if gridID == 1 && row == globalRow {
					foundGlobal = true
				}
			}
		}
		if !foundGlobal {
			t.Fatalf("laststatus=3 global statusline is missing at row %d: %#v", globalRow, markerRows)
		}
		if markerOccurrences != 1 {
			t.Fatalf("laststatus=3 produced %d statusline markers, want only the global root row", markerOccurrences)
		}
	}
}

func priorBugGridContains(grid *screen.Grid, text string) bool {
	for _, row := range grid.Cells {
		if strings.Contains(priorBugRowText(row), text) {
			return true
		}
	}
	return false
}

func anyPriorBugGridContains(state *screen.State, text string) bool {
	for _, grid := range state.Grids {
		if priorBugGridContains(grid, text) {
			return true
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

func lastCommitted(t *testing.T, states []*screen.State) *screen.State {
	t.Helper()
	if len(states) == 0 {
		t.Fatal("no committed state")
	}
	return states[len(states)-1]
}
