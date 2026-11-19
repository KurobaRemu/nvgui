package screen_test

import (
	"encoding/json"
	"sort"
	"strconv"
	"testing"

	"nvgui/oracle"
	"nvgui/screen"
)

func TestLayersTableDriven(t *testing.T) {
	tests := []struct {
		name  string
		state *screen.State
		want  []screen.Layer
	}{
		{
			name:  "nil state has no layers",
			state: nil,
			want:  nil,
		},
		{
			name: "win grids by id then floats and msg by compindex",
			state: &screen.State{
				Grids: map[int]*screen.Grid{1: {}, 2: {}, 3: {}, 4: {}, 5: {}, 6: {}, 7: {}, 8: {}},
				Placements: map[int]screen.Placement{
					2: {Kind: screen.PlacedWin, Row: 2, Col: 3},
					3: {Kind: screen.PlacedWin, Row: 4, Col: 5},
					4: {Kind: screen.PlacedFloat, CompIndex: 2, ScreenRow: 8, ScreenCol: 9},
					5: {Kind: screen.PlacedFloat, CompIndex: 1, ScreenRow: 6, ScreenCol: 7},
					6: {Kind: screen.PlacedExternal},
					7: {Kind: screen.PlacedWin, Row: 10, Col: 11},
				},
				Msg: &screen.MsgPlacement{Grid: 8, Row: 12, CompIndex: 3},
			},
			want: []screen.Layer{
				{Grid: 1},
				{Grid: 2, ScreenRow: 2, ScreenCol: 3},
				{Grid: 3, ScreenRow: 4, ScreenCol: 5},
				{Grid: 7, ScreenRow: 10, ScreenCol: 11},
				{Grid: 5, ScreenRow: 6, ScreenCol: 7},
				{Grid: 4, ScreenRow: 8, ScreenCol: 9},
				{Grid: 8, ScreenRow: 12},
			},
		},
		{
			name: "hidden and unplaced grids are omitted",
			state: &screen.State{
				Grids: map[int]*screen.Grid{1: {}, 2: {}, 3: {}},
				Placements: map[int]screen.Placement{
					2: {Kind: screen.PlacedExternal},
				},
			},
			want: []screen.Layer{{Grid: 1}},
		},
		{
			name: "float placement overrides msg layer on the same grid",
			state: &screen.State{
				Grids: map[int]*screen.Grid{1: {}, 2: {}, 3: {}},
				Placements: map[int]screen.Placement{
					2: {Kind: screen.PlacedWin, Row: 1, Col: 2},
					3: {Kind: screen.PlacedFloat, CompIndex: 0, ScreenRow: 3, ScreenCol: 4},
				},
				Msg: &screen.MsgPlacement{Grid: 3, Row: 9, CompIndex: 0},
			},
			want: []screen.Layer{
				{Grid: 1},
				{Grid: 2, ScreenRow: 1, ScreenCol: 2},
				{Grid: 3, ScreenRow: 3, ScreenCol: 4},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			got := screen.Layers(test.state)
			if len(got) != len(test.want) {
				t.Fatalf("Layers() = %#v, want %#v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("Layers()[%d] = %+v, want %+v", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestGoldenLastFrameLayersMatchCanonicalPlacements(t *testing.T) {
	for _, golden := range oracle.List() {
		golden := golden
		t.Run(golden.Name, func(t *testing.T) {
			if len(golden.Frames) == 0 {
				t.Fatal("golden has no frames")
			}
			var frame struct {
				Grids      map[string]json.RawMessage `json:"grids"`
				Placements map[string]goldenPlacement `json:"placements"`
				Msg        *goldenMsgPlacement        `json:"msg"`
			}
			last := golden.Frames[len(golden.Frames)-1]
			if err := json.Unmarshal(last, &frame); err != nil {
				t.Fatalf("decode final frame: %v", err)
			}
			want := expectedGoldenLayerGroups(frame.Grids, frame.Placements, frame.Msg)

			states := replayBatches(t, decodeGoldenBatches(t, golden))
			if len(states) != len(golden.Frames) {
				t.Fatalf("frame count = %d, want %d", len(states), len(golden.Frames))
			}
			got := screen.Layers(states[len(states)-1])
			assertGroupedLayers(t, got, want)
		})
	}
}

type goldenPlacement struct {
	Kind      string `json:"kind"`
	Row       int    `json:"row"`
	Col       int    `json:"col"`
	ScreenRow int    `json:"screen_row"`
	ScreenCol int    `json:"screen_col"`
	CompIndex int    `json:"compindex"`
}

type goldenMsgPlacement struct {
	Grid      int `json:"grid"`
	Row       int `json:"row"`
	CompIndex int `json:"compindex"`
}

type layerGroup struct {
	compIndex   int
	composed    bool
	strictOrder bool
	layers      []screen.Layer
}

func expectedGoldenLayerGroups(grids map[string]json.RawMessage, placements map[string]goldenPlacement, msg *goldenMsgPlacement) []layerGroup {
	var winLayers []screen.Layer
	composedByIndex := map[int][]screen.Layer{}
	for key, placement := range placements {
		gridID, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		if _, exists := grids[key]; !exists {
			continue
		}
		switch placement.Kind {
		case "win":
			winLayers = append(winLayers, screen.Layer{Grid: gridID, ScreenRow: placement.Row, ScreenCol: placement.Col})
		case "float":
			layer := screen.Layer{Grid: gridID, ScreenRow: placement.ScreenRow, ScreenCol: placement.ScreenCol}
			composedByIndex[placement.CompIndex] = append(composedByIndex[placement.CompIndex], layer)
		}
	}
	if msg != nil {
		if _, exists := grids[strconv.Itoa(msg.Grid)]; exists {
			if _, placed := placements[strconv.Itoa(msg.Grid)]; !placed {
				layer := screen.Layer{Grid: msg.Grid, ScreenRow: msg.Row}
				composedByIndex[msg.CompIndex] = append(composedByIndex[msg.CompIndex], layer)
			}
		}
	}
	sort.Slice(winLayers, func(left, right int) bool { return winLayers[left].Grid < winLayers[right].Grid })
	groups := make([]layerGroup, 0, len(composedByIndex)+2)
	if _, exists := grids["1"]; exists {
		groups = append(groups, layerGroup{layers: []screen.Layer{{Grid: 1}}, strictOrder: true})
	}
	if len(winLayers) > 0 {
		groups = append(groups, layerGroup{layers: winLayers, strictOrder: true})
	}
	indices := make([]int, 0, len(composedByIndex))
	for compIndex := range composedByIndex {
		indices = append(indices, compIndex)
	}
	sort.Ints(indices)
	for _, compIndex := range indices {
		groups = append(groups, layerGroup{compIndex: compIndex, composed: true, layers: composedByIndex[compIndex]})
	}
	return groups
}

func assertGroupedLayers(t *testing.T, got []screen.Layer, groups []layerGroup) {
	t.Helper()
	wantCount := 0
	for _, group := range groups {
		wantCount += len(group.layers)
	}
	if len(got) != wantCount {
		t.Fatalf("Layers() = %#v, want %d layers from %#v", got, wantCount, groups)
	}
	offset := 0
	for groupIndex, group := range groups {
		segment := got[offset : offset+len(group.layers)]
		offset += len(group.layers)
		if groupIndex > 0 {
			previous := groups[groupIndex-1]
			if previous.composed && (!group.composed || previous.compIndex >= group.compIndex) {
				t.Fatalf("layer composition groups are not strictly ordered by compindex: %#v", groups)
			}
		}
		if group.strictOrder {
			for index := range group.layers {
				if segment[index] != group.layers[index] {
					t.Fatalf("Layers() segment = %#v, want %#v", segment, group.layers)
				}
			}
		} else {
			assertLayerSet(t, segment, group.layers)
		}
	}
}

func assertLayerSet(t *testing.T, got, want []screen.Layer) {
	t.Helper()
	remaining := append([]screen.Layer(nil), want...)
	for _, layer := range got {
		found := false
		for index, candidate := range remaining {
			if layer == candidate {
				remaining = append(remaining[:index], remaining[index+1:]...)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("unexpected layer %+v in group; want %#v", layer, want)
		}
	}
	if len(remaining) != 0 {
		t.Fatalf("missing layers %#v in group; got %#v", remaining, got)
	}
}
