package input_test

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/input"
)

type recordedMouseCall struct {
	button   string
	action   string
	modifier string
	grid     int
	row      int
	col      int
	count    int
}

type fakeMouseSender struct {
	mu      sync.Mutex
	events  []string
	mouse   []recordedMouseCall
	gate    chan struct{}
	batch   bool
	onFirst chan struct{}
	onceHit sync.Once
}

func (s *fakeMouseSender) Input(keys string) (int, error) {
	s.onceHit.Do(func() {
		if s.onFirst != nil {
			close(s.onFirst)
		}
	})
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "key:"+keys)
	return len(keys), nil
}

func (s *fakeMouseSender) InputMouse(button, action, modifier string, grid, row, col int) error {
	return s.recordMouse(button, action, modifier, grid, row, col, 1)
}

func (s *fakeMouseSender) InputMouseBatch(button, action, modifier string, grid, row, col, count int) error {
	if !s.batch {
		for range count {
			if err := s.recordMouse(button, action, modifier, grid, row, col, 1); err != nil {
				return err
			}
		}
		return nil
	}
	return s.recordMouse(button, action, modifier, grid, row, col, count)
}

func (s *fakeMouseSender) recordMouse(button, action, modifier string, grid, row, col, count int) error {
	s.onceHit.Do(func() {
		if s.onFirst != nil {
			close(s.onFirst)
		}
	})
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	call := recordedMouseCall{
		button:   button,
		action:   action,
		modifier: modifier,
		grid:     grid,
		row:      row,
		col:      col,
		count:    count,
	}
	s.mouse = append(s.mouse, call)
	s.events = append(s.events, fmt.Sprintf("mouse:%s:%s:%s:g%d:(%d,%d)x%d", button, action, modifier, grid, row, col, count))
	return nil
}

func (s *fakeMouseSender) snapshotMouse() []recordedMouseCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedMouseCall(nil), s.mouse...)
}

func (s *fakeMouseSender) snapshotEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func newTestMouseRig(t *testing.T, batch bool) (*input.MouseHandler, *input.Handler, *input.Queue, *fakeMouseSender, *input.TraceRecorder, func()) {
	t.Helper()
	sender := &fakeMouseSender{batch: batch}
	rec := input.NewTraceRecorder()
	q, stop := startQueue(t, sender, rec)
	m := input.NewMouseHandler(q)
	m.SetGeometry(input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 80, Rows: 24})
	h := input.NewHandler(q)
	return m, h, q, sender, rec, stop
}

func TestMouseButtonMappingAndModifiers(t *testing.T) {
	modCases := []struct {
		mods ui.Modifiers
		want string
	}{
		{0, ""},
		{ui.Shift, "S"},
		{ui.Ctrl, "C"},
		{ui.Alt, "A"},
		{ui.Super, "D"},
		{ui.Shift | ui.Ctrl, "SC"},
		{ui.Shift | ui.Ctrl | ui.Alt | ui.Super, "SCAD"},
	}
	for _, tc := range modCases {
		if got := input.FormatMouseModifiers(tc.mods); got != tc.want {
			t.Errorf("FormatMouseModifiers(%v) = %q, want %q", tc.mods, got, tc.want)
		}
	}

	m, _, _, sender, _, stop := newTestMouseRig(t, true)
	defer stop()

	buttons := []struct {
		code int
		want string
	}{
		{0, "left"},
		{1, "right"},
		{2, "middle"},
		{3, "x1"},
		{4, "x2"},
	}
	for idx, b := range buttons {
		x := float32(idx*10 + 5)
		y := float32(25) // row 1
		if !m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: b.code, X: x, Y: y, Mods: ui.Ctrl, Clicks: 1}) {
			t.Fatalf("PointerDown button %d returned false", b.code)
		}
		if !m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: b.code, X: x, Y: y, Mods: ui.Ctrl, Clicks: 1}) {
			t.Fatalf("PointerUp button %d returned false", b.code)
		}
	}
	// Unknown buttons must be rejected cleanly.
	if m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: -1, X: 5, Y: 5}) {
		t.Fatal("unknown button -1 returned true")
	}
	if m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: 9, X: 5, Y: 5}) {
		t.Fatal("unknown button 9 returned true")
	}

	stop()
	got := sender.snapshotMouse()
	if len(got) != len(buttons)*2 {
		t.Fatalf("sent mouse count = %d, want %d (%+v)", len(got), len(buttons)*2, got)
	}
	for idx, b := range buttons {
		press := got[idx*2]
		release := got[idx*2+1]
		wantPress := recordedMouseCall{button: b.want, action: "press", modifier: "C", grid: 0, row: 1, col: idx, count: 1}
		wantRelease := recordedMouseCall{button: b.want, action: "release", modifier: "C", grid: 0, row: 1, col: idx, count: 1}
		if press != wantPress {
			t.Errorf("button %d press = %+v, want %+v", b.code, press, wantPress)
		}
		if release != wantRelease {
			t.Errorf("button %d release = %+v, want %+v", b.code, release, wantRelease)
		}
	}
}

func TestMouseDragDedupAndHasMovedReleaseDebounce(t *testing.T) {
	m, _, _, sender, _, stop := newTestMouseRig(t, true)
	defer stop()

	// Press at cell (row=2, col=3): pixel (35, 45).
	if !m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 35, Y: 45}) {
		t.Fatal("PointerDown returned false")
	}
	if !m.Dragging() {
		t.Fatal("Dragging() = false after PointerDown")
	}

	// Sub-cell pixel jitters inside cell (2, 3): MUST emit 0 drag RPCs.
	for _, pt := range [][2]float32{{30.1, 40.2}, {39.9, 59.8}, {32.0, 48.0}, {38.5, 51.2}} {
		m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: pt[0], Y: pt[1]})
	}

	// Cross into cell (2, 4) -> 1 drag.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 42, Y: 45})
	// Jitters inside (2, 4) -> 0 drags.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 48, Y: 55})
	// Cross into cell (3, 5) -> 1 drag.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 54, Y: 66})

	// Release arrives with jittered pixel slightly into (3, 6): hasMoved debounce MUST pin release to last dragged cell (3, 5).
	if !m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, X: 61, Y: 66}) {
		t.Fatal("PointerUp returned false")
	}
	if m.Dragging() {
		t.Fatal("Dragging() = true after PointerUp")
	}

	// Double/triple click sequence without drag (hasMoved=false) sends press/release pairs at clicked cell.
	for clicks := 1; clicks <= 3; clicks++ {
		m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 12, Y: 22, Clicks: clicks})
		m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, X: 12, Y: 22, Clicks: clicks})
	}

	stop()
	got := sender.snapshotMouse()
	want := []recordedMouseCall{
		{button: "left", action: "press", grid: 0, row: 2, col: 3, count: 1},
		{button: "left", action: "drag", grid: 0, row: 2, col: 4, count: 1},
		{button: "left", action: "drag", grid: 0, row: 3, col: 5, count: 1},
		{button: "left", action: "release", grid: 0, row: 3, col: 5, count: 1},
		{button: "left", action: "press", grid: 0, row: 1, col: 1, count: 1},
		{button: "left", action: "release", grid: 0, row: 1, col: 1, count: 1},
		{button: "left", action: "press", grid: 0, row: 1, col: 1, count: 1},
		{button: "left", action: "release", grid: 0, row: 1, col: 1, count: 1},
		{button: "left", action: "press", grid: 0, row: 1, col: 1, count: 1},
		{button: "left", action: "release", grid: 0, row: 1, col: 1, count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("mouse calls len = %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMouseCanceledForcesReleaseAndPreventsStuckDrag(t *testing.T) {
	m, _, _, sender, _, stop := newTestMouseRig(t, true)
	defer stop()

	// Canceled PointerUp when NOT dragging must not emit anything.
	if m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, X: -1e6, Y: -1e6, Canceled: true}) {
		t.Fatal("Canceled PointerUp while idle returned true")
	}

	// Start drag at (2, 3), move to (4, 6), then cancel with sentinel coordinates (-1e6, -1e6).
	m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 35, Y: 45})
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 65, Y: 85})
	if !m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, X: -1e6, Y: -1e6, Canceled: true}) {
		t.Fatal("Canceled PointerUp during drag returned false")
	}
	if m.Dragging() {
		t.Fatal("Dragging() still true after Canceled release")
	}

	// Further moves after cancel must NOT emit drag events.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: -1, X: 105, Y: 105})

	stop()
	got := sender.snapshotMouse()
	want := []recordedMouseCall{
		{button: "left", action: "press", grid: 0, row: 2, col: 3, count: 1},
		{button: "left", action: "drag", grid: 0, row: 4, col: 6, count: 1},
		{button: "left", action: "release", grid: 0, row: 4, col: 6, count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("mouse calls len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMouseOffAndMouseMoveEventGating(t *testing.T) {
	m, _, _, sender, _, stop := newTestMouseRig(t, true)
	defer stop()

	// mousemoveevent=false by default: hovering across many cells emits 0 RPCs and returns false.
	for col := 0; col < 10; col++ {
		if m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: -1, X: float32(col*10 + 5), Y: 25}) {
			t.Fatalf("hover move at col %d with mousemoveevent=false returned true", col)
		}
	}

	// Enable mousemoveevent: cross-cell move emits "move", same-cell jitters emit 0.
	m.SetMouseMoveEvent(true)
	// Pointer was already at (row=1, col=9), moving within (1, 9) emits 0.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: -1, X: 96, Y: 28})
	// Cross to (row=2, col=10) -> emits 1 move.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: -1, X: 105, Y: 45})
	// Jitter inside (2, 10) -> 0 moves.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: -1, X: 109, Y: 58})
	// Cross to (row=2, col=11) -> emits 1 move.
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: -1, X: 112, Y: 45, Mods: ui.Shift})

	// Turn mouse_off: suppresses ALL mouse RPCs (click, drag, move, release, scroll).
	m.SetEnabled(false)
	m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 25, Y: 45})
	m.Handle(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 55, Y: 85})
	m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, X: 55, Y: 85})
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 85, DY: 40, Precise: true})
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 85, DY: 40, Precise: false})

	stop()
	got := sender.snapshotMouse()
	want := []recordedMouseCall{
		{button: "move", action: "", modifier: "", grid: 0, row: 2, col: 10, count: 1},
		{button: "move", action: "", modifier: "S", grid: 0, row: 2, col: 11, count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("mouse calls len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMouseScrollMacOSSignAndUnitsLocked(t *testing.T) {
	// Locks mygo internal/darwin/surface.go:851-855 conventions:
	// - mygo negates AppKit scrollingDeltaX/Y so DY > 0 scrolls view down ("down") and DX > 0 scrolls view right ("right").
	// - Non-precise wheel notches are scaled by 40 DIPs per notch in mygo darwin backend.
	m, _, _, sender, _, stop := newTestMouseRig(t, true)
	defer stop()

	// Precise trackpad: cell is 10x20 DIPs.
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DY: 20, Precise: true})                  // +1 cell -> down x1
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DY: -40, Precise: true, Momentum: true}) // -2 cells -> up x2
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DX: 10, Precise: true})                  // +1 cell -> right x1
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DX: -20, Precise: true, Mods: ui.Shift}) // -2 cells -> left x2

	// Discrete mouse wheel (!Precise): 40 DIPs == 1 notch.
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DY: 40, Precise: false})
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DY: -80, Precise: false})
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DX: 40, Precise: false})
	m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 55, Y: 65, DX: -40, Precise: false})

	stop()
	got := sender.snapshotMouse()
	want := []recordedMouseCall{
		{button: "wheel", action: "down", modifier: "", grid: 0, row: 3, col: 5, count: 1},
		{button: "wheel", action: "up", modifier: "", grid: 0, row: 3, col: 5, count: 2},
		{button: "wheel", action: "right", modifier: "", grid: 0, row: 3, col: 5, count: 1},
		{button: "wheel", action: "left", modifier: "S", grid: 0, row: 3, col: 5, count: 2},
		{button: "wheel", action: "down", modifier: "", grid: 0, row: 3, col: 5, count: 1},
		{button: "wheel", action: "up", modifier: "", grid: 0, row: 3, col: 5, count: 2},
		{button: "wheel", action: "right", modifier: "", grid: 0, row: 3, col: 5, count: 1},
		{button: "wheel", action: "left", modifier: "", grid: 0, row: 3, col: 5, count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("scroll calls len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("scroll[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMouseContinuous120HzTrackpadStream30StepsAndOrderingBarriers(t *testing.T) {
	gate := make(chan struct{})
	onFirst := make(chan struct{})
	sender := &fakeMouseSender{gate: gate, batch: true, onFirst: onFirst}
	rec := input.NewTraceRecorder()
	q, stop := startQueue(t, sender, rec)
	defer stop()

	m := input.NewMouseHandler(q)
	// Cell height 16 DIPs, width 8 DIPs -> matches typical 14pt Retina font DIP metrics.
	m.SetGeometry(input.CellSize{Width: 8, Height: 16}, input.GridBounds{Cols: 80, Rows: 24})
	h := input.NewHandler(q)

	base := time.Unix(1_700_000_000, 0).UTC()
	stepDT := 8333 * time.Microsecond // ~120Hz
	ts := func(step int) time.Time { return base.Add(time.Duration(step) * stepDT) }

	// Step 0: inject an anchor key so the consumer blocks on gate while the 30-step stream queues up.
	if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyG, Time: ts(0)}) {
		t.Fatal("anchor KeyDown failed")
	}
	if !h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "g", Time: ts(0)}) {
		t.Fatal("anchor InputText failed")
	}
	select {
	case <-onFirst:
	case <-time.After(3 * time.Second):
		t.Fatal("queue consumer did not reach sender gate")
	}

	rawScrollEvents := 0

	// Phase 1 (steps 1..10): slow 120Hz sub-cell vertical scroll (4.8 DIPs = 0.3 cell/step -> 3.0 cells total).
	for step := 1; step <= 10; step++ {
		rawScrollEvents++
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 84, Y: 84, DY: 4.8, Precise: true, Time: ts(step)}) {
			t.Fatalf("phase 1 step %d returned false", step)
		}
	}

	// Phase 2 (steps 11..16): fast vertical flick (24 DIPs = 1.5 cells/step -> 9.0 cells total; coalesces with Phase 1!).
	for step := 11; step <= 16; step++ {
		rawScrollEvents++
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 84, Y: 84, DY: 24.0, Precise: true, Time: ts(step)}) {
			t.Fatalf("phase 2 step %d returned false", step)
		}
	}

	// Strict ordering barrier 1: keyboard key "j" mid-stream.
	h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyJ, Time: ts(17)})
	h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "j", Time: ts(17)})
	h.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyJ, Time: ts(17)})

	// Phase 3 (steps 18..23): direction reversal upward with momentum (-12 DIPs = -0.75 cell/step -> -4.5 cells -> 5 up ticks from 0.0).
	for step := 18; step <= 23; step++ {
		rawScrollEvents++
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 84, Y: 84, DY: -12.0, Precise: true, Momentum: step >= 21, Time: ts(step)}) {
			t.Fatalf("phase 3 step %d returned false", step)
		}
	}

	// Strict ordering barrier 2: left click at (row=5, col=10).
	m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 84, Y: 84, Time: ts(24)})
	m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, X: 84, Y: 84, Time: ts(24)})

	// Phase 4a (steps 25..27): horizontal scroll right (+12 DIPs = +1.5 cells/step -> 4 right ticks).
	for step := 25; step <= 27; step++ {
		rawScrollEvents++
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 84, Y: 84, DX: 12.0, Precise: true, Time: ts(step)}) {
			t.Fatalf("phase 4a step %d returned false", step)
		}
	}

	// Strict ordering barrier 3: keyboard key "k".
	h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyK, Time: ts(28)})
	h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "k", Time: ts(28)})
	h.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyK, Time: ts(28)})

	// Phase 4b (steps 28..32): horizontal reversal left (-16 DIPs = -2.0 cells/step -> 10 left ticks; 5 steps so total raw scroll events = 30).
	for step := 28; step <= 32; step++ {
		rawScrollEvents++
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 84, Y: 84, DX: -16.0, Precise: true, Time: ts(step)}) {
			t.Fatalf("phase 4b step %d returned false", step)
		}
	}

	if rawScrollEvents != 30 {
		t.Fatalf("rawScrollEvents = %d, want 30", rawScrollEvents)
	}

	// Release sender gate and drain queue completely.
	close(gate)
	stop()

	events := sender.snapshotEvents()
	wantEvents := []string{
		"key:g",
		"mouse:wheel:down::g0:(5,10)x12", // Phase 1 (3) + Phase 2 (9) coalesced into 1 merged batch!
		"key:j",
		"mouse:wheel:up::g0:(5,10)x5", // Phase 3 coalesced into 1 merged batch!
		"mouse:left:press::g0:(5,10)x1",
		"mouse:left:release::g0:(5,10)x1",
		"mouse:wheel:right::g0:(5,10)x4", // Phase 4a coalesced into 1 merged batch!
		"key:k",
		"mouse:wheel:left::g0:(5,10)x10", // Phase 4b coalesced into 1 merged batch!
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("dispatched stream len = %d, want %d\n got: %v\nwant: %v", len(events), len(wantEvents), events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Errorf("stream[%d] = %q, want %q", i, events[i], wantEvents[i])
		}
	}

	sentMouse := q.SentMouse()
	if len(sentMouse) >= rawScrollEvents/2 {
		t.Fatalf("SentMouse len = %d is not significantly lower than %d raw scroll events", len(sentMouse), rawScrollEvents)
	}
	if len(sentMouse) != 6 {
		t.Fatalf("SentMouse len = %d, want 6 (4 coalesced scroll batches + press + release)", len(sentMouse))
	}

	// Verify diag Input Trace round-trips cleanly with mouse events recorded.
	recorded := rec.Events()
	rawTrace, err := input.MarshalTrace(recorded)
	if err != nil {
		t.Fatalf("MarshalTrace: %v", err)
	}
	decoded, err := input.UnmarshalTrace(rawTrace)
	if err != nil {
		t.Fatalf("UnmarshalTrace: %v", err)
	}
	var sawMouseTrace bool
	if err := input.Replay(decoded, func(ev ui.InputEvent) {
		if ev.Kind == ui.InputCommand && strings.HasPrefix(ev.Text, "mouse:") {
			sawMouseTrace = true
		}
	}); err != nil {
		t.Fatalf("Replay(mouse trace): %v", err)
	}
	if !sawMouseTrace {
		t.Fatal("diag Input Trace did not contain any mouse trace entries")
	}
}

func TestMouseBoundaryClampingInvalidInputsAndDualAxisScroll(t *testing.T) {
	t.Run("coordinate clamping including extreme finite floats", func(t *testing.T) {
		cases := []struct {
			name    string
			x, y    float32
			wantRow int
			wantCol int
		}{
			{"negative", -50, -20, 0, 0},
			{"over_bounds", 5000, 5000, 23, 79},
			{"extreme_positive_finite", 1e20, 1e20, 23, 79},
			{"extreme_negative_finite", -1e20, -1e20, 0, 0},
			{"origin", 0, 0, 0, 0},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				m, _, _, sender, _, stop := newTestMouseRig(t, true)
				defer stop()
				if !m.Handle(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: tc.x, Y: tc.y}) {
					t.Fatalf("PointerDown(%v, %v) returned false", tc.x, tc.y)
				}
				if !m.Handle(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0, X: tc.x, Y: tc.y}) {
					t.Fatalf("PointerUp(%v, %v) returned false", tc.x, tc.y)
				}
				stop()
				got := sender.snapshotMouse()
				if len(got) != 2 {
					t.Fatalf("mouse calls len = %d, want 2: %+v", len(got), got)
				}
				for idx, call := range got {
					if call.row != tc.wantRow || call.col != tc.wantCol {
						t.Errorf("call[%d] pos = (%d, %d), want (%d, %d)", idx, call.row, call.col, tc.wantRow, tc.wantCol)
					}
				}
			})
		}
	})

	t.Run("invalid geometry and NaN/Inf rejected with zero RPCs", func(t *testing.T) {
		nan := float32(math.NaN())
		posInf := float32(math.Inf(1))
		negInf := float32(math.Inf(-1))

		cases := []struct {
			name   string
			cell   input.CellSize
			bounds input.GridBounds
			ev     ui.InputEvent
		}{
			{"zero_cell", input.CellSize{Width: 0, Height: 0}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 10, Y: 10}},
			{"negative_cell", input.CellSize{Width: -1, Height: 20}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 10, Y: 10}},
			{"zero_bounds", input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 0, Rows: 0}, ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 10, Y: 10}},
			{"zero_rows", input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 80, Rows: 0}, ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 10, Y: 10}},
			{"nan_pointer_x", input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: nan, Y: 10}},
			{"inf_pointer_y", input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 10, Y: posInf}},
			{"neginf_pointer_move", input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputPointerMove, Button: -1, X: negInf, Y: 10}},
			{"nan_scroll_dy", input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputScroll, X: 10, Y: 10, DY: nan, Precise: true}},
			{"inf_scroll_dx", input.CellSize{Width: 10, Height: 20}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputScroll, X: 10, Y: 10, DX: posInf, Precise: true}},
			{"zero_cell_scroll", input.CellSize{Width: 0, Height: 0}, input.GridBounds{Cols: 80, Rows: 24}, ui.InputEvent{Kind: ui.InputScroll, X: 10, Y: 10, DY: 20, Precise: true}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				m, _, _, sender, _, stop := newTestMouseRig(t, true)
				defer stop()
				m.SetGeometry(tc.cell, tc.bounds)
				if m.Handle(tc.ev) {
					t.Fatalf("Handle(%s) = true, want false", tc.name)
				}
				stop()
				if got := sender.snapshotMouse(); len(got) != 0 {
					t.Fatalf("Handle(%s) emitted %d RPCs (%+v), want 0", tc.name, len(got), got)
				}
			})
		}
	})

	t.Run("same-frame dual-axis scroll and extreme finite scroll clamp", func(t *testing.T) {
		m, _, _, sender, _, stop := newTestMouseRig(t, true)
		defer stop()

		// Cell is 10x20: DX=+20 (+2 cols -> right x2), DY=+40 (+2 rows -> down x2) in a single event.
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 35, Y: 45, DX: 20, DY: 40, Precise: true}) {
			t.Fatal("dual-axis InputScroll returned false")
		}
		// Extreme finite scroll must clamp safely without int32 overflow/wraparound.
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 35, Y: 45, DY: 1e20, Precise: true}) {
			t.Fatal("extreme positive finite InputScroll returned false")
		}
		if !m.Handle(ui.InputEvent{Kind: ui.InputScroll, X: 35, Y: 45, DY: -1e20, Precise: true}) {
			t.Fatal("extreme negative finite InputScroll returned false")
		}

		stop()
		got := sender.snapshotMouse()
		if len(got) < 4 {
			t.Fatalf("mouse calls len = %d, want >= 4: %+v", len(got), got)
		}
		wantDual0 := recordedMouseCall{button: "wheel", action: "down", grid: 0, row: 2, col: 3, count: 2}
		wantDual1 := recordedMouseCall{button: "wheel", action: "right", grid: 0, row: 2, col: 3, count: 2}
		if got[0] != wantDual0 || got[1] != wantDual1 {
			t.Fatalf("dual-axis calls = [%+v, %+v], want [%+v, %+v]", got[0], got[1], wantDual0, wantDual1)
		}
		if got[2].button != "wheel" || got[2].action != "down" || got[2].count <= 0 {
			t.Fatalf("extreme +1e20 scroll call = %+v, want positive wheel down", got[2])
		}
		if got[3].button != "wheel" || got[3].action != "up" || got[3].count <= 0 {
			t.Fatalf("extreme -1e20 scroll call = %+v, want positive wheel up", got[3])
		}
	})
}
