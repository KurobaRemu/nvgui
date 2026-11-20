package screen

import (
	"reflect"
	"testing"

	"nvgui/protocol"
)

func TestGridLineRepeatZero(t *testing.T) {
	tests := []struct {
		name  string
		event protocol.GridLine
	}{
		{
			name: "inside row",
			event: protocol.GridLine{
				Grid: 2, Row: 0, ColStart: 1,
				Cells: []protocol.LineCell{
					{Text: "x", HL: 3, Repeat: 1},
					{Text: " ", HL: 0, Repeat: 0},
				},
			},
		},
		{
			name: "at row end",
			event: protocol.GridLine{
				Grid: 2, Row: 0, ColStart: 3,
				Cells: []protocol.LineCell{
					{Text: " ", HL: 0, Repeat: 0},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reducer := reducerWithGrid(t, 3)
			states, err := reducer.Apply([]protocol.Event{
				protocol.HlAttrDefine{ID: 3, RGB: map[string]any{"foreground": int64(7)}},
				protocol.GridLine{
					Grid: 2, Row: 0, ColStart: 0,
					Cells: []protocol.LineCell{
						{Text: "a", HL: 3, Repeat: 1},
						{Text: "b", HL: 3, Repeat: 1},
						{Text: "c", HL: 3, Repeat: 1},
					},
				},
				test.event,
				protocol.Flush{},
			})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			state := committedState(t, states, 1)
			want := []Cell{
				{Text: "a", HL: 3},
				{Text: "b", HL: 3},
				{Text: "c", HL: 3},
			}
			if test.name == "inside row" {
				want[1] = Cell{Text: "x", HL: 3}
			}
			if got := state.Grids[2].Cells[0]; !reflect.DeepEqual(got, want) {
				t.Fatalf("row = %#v, want %#v", got, want)
			}
		})
	}
}

func TestGridLineRejectsNegativeRepeat(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridLine{Grid: 2, Row: 0, Cells: []protocol.LineCell{{Text: "x", Repeat: -1}}},
	}); err == nil {
		t.Fatal("Apply accepted negative repeat")
	}
}

func TestGridLineKeepsTrailingNonDefaultSpace(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	states, err := reducer.Apply([]protocol.Event{
		protocol.HlAttrDefine{ID: 4, RGB: map[string]any{"foreground": int64(9)}},
		protocol.GridLine{
			Grid: 2, Row: 0, ColStart: 0,
			Cells: []protocol.LineCell{
				{Text: "a", HL: 4, Repeat: 1},
				{Text: " ", HL: 4, Repeat: 1},
			},
		},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	state := committedState(t, states, 1)
	want := []Cell{{Text: "a", HL: 4}, {Text: " ", HL: 4}}
	if got := state.Grids[2].Cells[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("row = %#v, want %#v", got, want)
	}
}

func TestGridLineRejectsTrailingSpaceOverflow(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridLine{
			Grid: 2, Row: 0, ColStart: 2,
			Cells: []protocol.LineCell{{Text: " ", HL: 4, Repeat: 1}},
		},
	}); err == nil {
		t.Fatal("Apply accepted trailing space beyond grid width")
	}
}

func TestGridScrollLargeRowsAndNonZeroCols(t *testing.T) {
	tests := []struct {
		name string
		rows int
	}{
		{name: "down at least region height", rows: 3},
		{name: "down beyond region height", rows: 5},
		{name: "up beyond region height", rows: -5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reducer := reducerWithGrid(t, 4)
			states, err := reducer.Apply([]protocol.Event{
				protocol.GridLine{
					Grid: 2, Row: 0, ColStart: 0,
					Cells: []protocol.LineCell{{Text: "a", HL: 2, Repeat: 3}},
				},
				protocol.GridLine{
					Grid: 2, Row: 1, ColStart: 0,
					Cells: []protocol.LineCell{{Text: "b", HL: 3, Repeat: 3}},
				},
				protocol.GridScroll{Grid: 2, Top: 0, Bot: 3, Left: 0, Right: 3, Rows: test.rows},
				protocol.Flush{},
			})
			if err != nil {
				t.Fatalf("Apply with rows %d: %v", test.rows, err)
			}
			state := committedState(t, states, 1)
			want := [][]Cell{
				{{Text: "a", HL: 2}, {Text: "a", HL: 2}, {Text: "a", HL: 2}, {Text: " ", HL: 0}},
				{{Text: "b", HL: 3}, {Text: "b", HL: 3}, {Text: "b", HL: 3}, {Text: " ", HL: 0}},
			}
			if got := state.Grids[2].Cells[:2]; !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %#v, want %#v", got, want)
			}
		})
	}

	reducer := reducerWithGrid(t, 4)
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridScroll{Grid: 2, Top: 0, Bot: 2, Left: 0, Right: 3, Rows: 1, Cols: 1},
	}); err == nil {
		t.Fatal("Apply accepted nonzero cols")
	}
}

func TestGridScrollRejectsInvalidRegion(t *testing.T) {
	tests := []protocol.GridScroll{
		{Grid: 2, Top: -1, Bot: 2, Left: 0, Right: 3},
		{Grid: 2, Top: 0, Bot: 5, Left: 0, Right: 3},
		{Grid: 2, Top: 2, Bot: 1, Left: 0, Right: 3},
		{Grid: 2, Top: 0, Bot: 2, Left: -1, Right: 3},
		{Grid: 2, Top: 0, Bot: 2, Left: 0, Right: 5},
		{Grid: 2, Top: 0, Bot: 2, Left: 2, Right: 1},
		{Grid: 99, Top: 0, Bot: 2, Left: 0, Right: 3},
	}
	for _, event := range tests {
		reducer := reducerWithGrid(t, 4)
		if _, err := reducer.Apply([]protocol.Event{event}); err == nil {
			t.Fatalf("Apply accepted invalid region %+v", event)
		}
	}
}

func TestGridDestroyMissingGridKeepsCommittedState(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	baseline := flushState(t, reducer, protocol.GridLine{
		Grid: 2, Row: 0, ColStart: 0,
		Cells: []protocol.LineCell{{Text: "a", HL: 5, Repeat: 2}},
	})
	if _, err := reducer.Apply([]protocol.Event{
		protocol.HlAttrDefine{ID: 6, RGB: map[string]any{"foreground": int64(6)}},
		protocol.GridDestroy{Grid: 99},
	}); err == nil {
		t.Fatal("Apply accepted destroying a missing grid")
	}
	assertStateUnchanged(t, reducer, baseline)
}

func TestApplyRollsBackToMostRecentFlush(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	baseline := flushState(t, reducer, protocol.GridLine{
		Grid: 2, Row: 0, ColStart: 0,
		Cells: []protocol.LineCell{{Text: "a", HL: 5, Repeat: 2}},
	})
	states, err := reducer.Apply([]protocol.Event{
		protocol.HlAttrDefine{ID: 6, RGB: map[string]any{"foreground": int64(6)}},
		protocol.Flush{},
		protocol.GridLine{
			Grid: 2, Row: 0, ColStart: 0,
			Cells: []protocol.LineCell{{Text: "z", HL: 6, Repeat: 2}},
		},
		protocol.GridDestroy{Grid: 99},
	})
	if err == nil {
		t.Fatal("Apply accepted destroying a missing grid")
	}
	if len(states) != 1 {
		t.Fatalf("Apply returned %d committed states, want 1", len(states))
	}
	assertStateUnchanged(t, reducer, states[0])
	if reducer.Latest() != states[0] {
		t.Fatal("Latest does not point at the most recent committed state")
	}
	if baseline.Seq != 1 || states[0].Seq != 2 {
		t.Fatalf("committed seq values = %d, %d; want 1, 2", baseline.Seq, states[0].Seq)
	}

	next := flushState(t, reducer, protocol.GridLine{
		Grid: 2, Row: 0, ColStart: 0,
		Cells: []protocol.LineCell{{Text: "b", HL: 0, Repeat: 2}},
	})
	if next.Seq != 3 {
		t.Fatalf("next flush seq = %d, want 3", next.Seq)
	}
}

func TestApplyRollsBackToPreFlushState(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridLine{
			Grid: 2, Row: 0, ColStart: 0,
			Cells: []protocol.LineCell{{Text: "a", HL: 5, Repeat: 2}},
		},
		protocol.Flush{},
		protocol.GridDestroy{Grid: 99},
	}); err == nil {
		t.Fatal("Apply accepted destroying a missing grid")
	}
	state := reducer.Latest()
	if state == nil {
		t.Fatal("Latest is nil after rollback")
	}
	if state.Seq != 1 {
		t.Fatalf("Latest seq = %d, want 1", state.Seq)
	}
	if got := state.Grids[2].Cells[0]; !reflect.DeepEqual(got, []Cell{{Text: "a", HL: 5}, {Text: "a", HL: 5}}) {
		t.Fatalf("Latest row = %#v", got)
	}
}

func TestApplyRollsBackBeforeFirstFlush(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridLine{
			Grid: 2, Row: 0, ColStart: 0,
			Cells: []protocol.LineCell{{Text: "a", HL: 5, Repeat: 2}},
		},
		protocol.GridDestroy{Grid: 99},
	}); err == nil {
		t.Fatal("Apply accepted destroying a missing grid")
	}
	if state := reducer.Latest(); state != nil {
		t.Fatalf("Latest = %#v, want nil before first commit", state)
	}
	state := flushState(t, reducer, protocol.GridLine{
		Grid: 2, Row: 0, ColStart: 0,
		Cells: []protocol.LineCell{{Text: "b", HL: 0, Repeat: 2}},
	})
	if state.Seq != 1 {
		t.Fatalf("first committed seq = %d, want 1", state.Seq)
	}
	want := []Cell{{Text: "b", HL: 0}, {Text: "b", HL: 0}}
	if got := state.Grids[2].Cells[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("first committed row = %#v, want %#v", got, want)
	}
}

func TestApplyRollsBackToStateBeforeBatch(t *testing.T) {
	reducer := reducerWithGrid(t, 2)
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridLine{
			Grid: 2, Row: 0, ColStart: 0,
			Cells: []protocol.LineCell{{Text: "a", HL: 5, Repeat: 2}},
		},
	}); err != nil {
		t.Fatalf("prepare pending state: %v", err)
	}
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridLine{
			Grid: 2, Row: 0, ColStart: 0,
			Cells: []protocol.LineCell{{Text: "z", HL: 6, Repeat: 2}},
		},
		protocol.GridDestroy{Grid: 99},
	}); err == nil {
		t.Fatal("Apply accepted destroying a missing grid")
	}
	if reducer.Latest() != nil {
		t.Fatal("Latest is not nil before first commit")
	}
	state := flushState(t, reducer)
	if state.Seq != 1 {
		t.Fatalf("first committed seq = %d, want 1", state.Seq)
	}
	want := []Cell{{Text: "a", HL: 5}, {Text: "a", HL: 5}}
	if got := state.Grids[2].Cells[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("row = %#v, want %#v", got, want)
	}
}

func TestNewReducerFromDoesNotPanicOnApply(t *testing.T) {
	reducer := NewReducerFrom(nil)
	states, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 2, Width: 1, Height: 1},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	state := committedState(t, states, 1)
	if state.Grids[2] == nil || len(state.Grids[2].Cells) != 1 {
		t.Fatalf("committed grid = %#v, want one row", state.Grids[2])
	}
}

func TestHlAttrDefineZeroAndNegativeID(t *testing.T) {
	reducer := NewReducer()
	states, err := reducer.Apply([]protocol.Event{
		protocol.HlAttrDefine{ID: 0, RGB: map[string]any{}},
		protocol.Flush{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	state := committedState(t, states, 1)
	if attr, found := state.Attrs[0]; !found || len(attr) != 0 {
		t.Fatalf("Attrs[0] = %#v, found %v; want empty Attr", attr, found)
	}
	if len(state.Attrs) != 1 {
		t.Fatalf("Attrs = %#v, want only the default empty Attr", state.Attrs)
	}

	if _, err := reducer.Apply([]protocol.Event{
		protocol.HlAttrDefine{ID: -1, RGB: map[string]any{"foreground": int64(1)}},
	}); err == nil {
		t.Fatal("Apply accepted negative highlight id")
	}
	if _, err := reducer.Apply([]protocol.Event{
		protocol.HlAttrDefine{ID: 0, RGB: map[string]any{"foreground": int64(1)}},
	}); err == nil {
		t.Fatal("Apply accepted nonempty default highlight")
	}
}

func reducerWithGrid(t *testing.T, size int) *Reducer {
	t.Helper()
	reducer := NewReducer()
	if _, err := reducer.Apply([]protocol.Event{
		protocol.GridResize{Grid: 2, Width: size, Height: size},
	}); err != nil {
		t.Fatalf("GridResize: %v", err)
	}
	return reducer
}

func flushState(t *testing.T, reducer *Reducer, events ...protocol.Event) *State {
	t.Helper()
	batch := append(append([]protocol.Event(nil), events...), protocol.Flush{})
	states, err := reducer.Apply(batch)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return committedState(t, states, 1)
}

func committedState(t *testing.T, states []*State, want int) *State {
	t.Helper()
	if len(states) != want {
		t.Fatalf("committed state count = %d, want %d", len(states), want)
	}
	return states[len(states)-1]
}

func assertStateUnchanged(t *testing.T, reducer *Reducer, baseline *State) {
	t.Helper()
	state := reducer.Latest()
	if state == nil {
		t.Fatal("Latest is nil")
	}
	want, err := Canonical(baseline)
	if err != nil {
		t.Fatalf("Canonical baseline: %v", err)
	}
	got, err := Canonical(state)
	if err != nil {
		t.Fatalf("Canonical latest: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("Latest changed after error\ngot  %s\nwant %s", got, want)
	}
}
