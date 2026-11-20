package input_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/input"
	"nvgui/screen"
)

func TestInputIMEPreeditAndCommitOnce(t *testing.T) {
	sender := &fakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := startQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	h.SetMode("insert")
	if !h.IMEAllowed() {
		t.Fatal("IMEAllowed() in insert mode = false, want true")
	}

	cell := input.CellSize{Width: 9, Height: 18}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).
			Fill().
			AutoFocus().
			HandleInput(h.Handle).
			TextCaret(h.Caret(1, 2, cell)).
			InputMethod(h.IMEAllowed())
	}, 400, 200)

	active, noIME := tester.TextInputActive()
	if !active || noIME {
		t.Fatalf("insert mode TextInputActive() = (active=%v, noIME=%v), want (true, false)", active, noIME)
	}

	// Preedit updates state and must NOT send anything to nvim.
	tester.Compose("n", 1)
	if got := h.Preedit(); got != "n" {
		t.Fatalf("Preedit after Compose(\"n\", 1) = %q, want \"n\"", got)
	}
	tester.Compose("ni", 2)
	st := h.State()
	if st.Preedit != "ni" || st.Caret != 2 || !st.Allowed {
		t.Fatalf("State() = %+v, want Preedit=\"ni\" Caret=2 Allowed=true", st)
	}
	if sent := sender.snapshot(); len(sent) != 0 {
		t.Fatalf("preedit leaked into sender: %v", sent)
	}

	// Commit clears preedit and sends "你" exactly once.
	tester.Compose("", 0)
	tester.Type("你")
	if got := h.Preedit(); got != "" {
		t.Fatalf("Preedit after commit = %q, want empty", got)
	}

	stop()
	got := sender.snapshot()
	if len(got) != 1 || got[0] != "你" {
		t.Fatalf("committed sender calls = %v, want [\"你\"]", got)
	}
}

func TestInputIMEModeSwitchingAndNormalJK(t *testing.T) {
	sender := &fakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := startQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}

	disallowed := []string{"normal", "visual", "visual_line", "visual_block", "operator"}
	for _, m := range disallowed {
		h.SetMode(m)
		if h.IMEAllowed() || h.State().Allowed {
			t.Errorf("SetMode(%q): IMEAllowed=%v State.Allowed=%v, want false", m, h.IMEAllowed(), h.State().Allowed)
		}
		if h.Mode() != m {
			t.Errorf("Mode() = %q, want %q", h.Mode(), m)
		}
	}

	allowed := []string{"insert", "cmdline_normal", "cmdline_insert", "terminal"}
	for _, m := range allowed {
		h.SetMode(m)
		if !h.IMEAllowed() || !h.State().Allowed {
			t.Errorf("SetMode(%q): IMEAllowed=%v State.Allowed=%v, want true", m, h.IMEAllowed(), h.State().Allowed)
		}
	}

	// Switch to normal mode and verify InputMethod(false) + direct j/k navigation.
	h.SetMode("normal")
	cell := input.CellSize{Width: 9, Height: 18}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).
			Fill().
			AutoFocus().
			HandleInput(h.Handle).
			TextCaret(h.Caret(0, 0, cell)).
			InputMethod(h.IMEAllowed())
	}, 400, 200)

	active, noIME := tester.TextInputActive()
	if !active || !noIME {
		t.Fatalf("normal mode TextInputActive() = (active=%v, noIME=%v), want (true, true)", active, noIME)
	}
	tester.TypeKey(0, ui.KeyJ, "j")
	tester.TypeKey(0, ui.KeyK, "k")

	stop()
	if got := strings.Join(sender.snapshot(), ""); got != "jk" {
		t.Fatalf("normal mode j/k sent = %q, want \"jk\"", got)
	}
}

func TestInputIMEChinesePunctuationAndCancel(t *testing.T) {
	sender := &fakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := startQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	h.SetMode("insert")
	now := time.Unix(1_700_000_000, 0).UTC()

	// Chinese punctuation and ':' '/' must pass through unchanged.
	puncts := []string{"。", "，", "！", "？", "¥", ":", "/"}
	for i, p := range puncts {
		h.Handle(ui.InputEvent{Kind: ui.InputText, Text: p, Time: now.Add(time.Duration(i) * time.Millisecond)})
	}

	// Active preedit canceled by Escape, Ctrl-C, Ctrl-[ must clear preedit, send notation, and not swallow subsequent j/k.
	cancels := []struct {
		ev   ui.InputEvent
		want string
	}{
		{ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape}, "<Esc>"},
		{ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Ctrl}, "<C-c>"},
		{ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBracketLeft, Mods: ui.Ctrl}, "<C-[>"},
	}
	for i, c := range cancels {
		ts := now.Add(time.Duration(100+i*20) * time.Millisecond)
		h.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "zhong", Caret: 5, Time: ts})
		if h.Preedit() != "zhong" {
			t.Fatalf("step %d: Preedit = %q, want \"zhong\"", i, h.Preedit())
		}
		ev := c.ev
		ev.Time = ts.Add(time.Millisecond)
		h.Handle(ev)
		if h.Preedit() != "" {
			t.Fatalf("step %d: Preedit after %s = %q, want empty", i, c.want, h.Preedit())
		}
		h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyJ, Time: ts.Add(2 * time.Millisecond)})
		h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "j", Time: ts.Add(3 * time.Millisecond)})
		h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyK, Time: ts.Add(4 * time.Millisecond)})
		h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "k", Time: ts.Add(5 * time.Millisecond)})
	}

	stop()
	got := strings.Join(sender.snapshot(), "")
	want := "。，！？¥:/<Esc>jk<C-c>jk<C-[>jk"
	if got != want {
		t.Fatalf("punctuation+cancel sequence = %q, want %q", got, want)
	}
}

func TestInputIMECaretPositionAndTraceObservation(t *testing.T) {
	sender := &fakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := startQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	h.SetMode("insert")

	cell := input.CellSize{Width: 9, Height: 18}
	// Root grid cursor at row=4, col=7.
	rRoot := h.Caret(4, 7, cell)
	if math.Abs(float64(rRoot.X-63)) > 0.5 || math.Abs(float64(rRoot.Y-72)) > 0.5 || rRoot.W <= 0 || math.Abs(float64(rRoot.H-18)) > 0.5 {
		t.Errorf("Caret(4, 7) = %+v, want X=63 Y=72 W>0 H=18", rRoot)
	}

	// Float grid cursor at ScreenRow=3, ScreenCol=10, local Row=2, Col=5 => screen (5, 15).
	state := &screen.State{
		Seq: 1,
		Grids: map[int]*screen.Grid{
			1: {Width: 80, Height: 24},
			2: {Width: 20, Height: 6},
		},
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedFloat, ScreenRow: 3, ScreenCol: 10, Width: 20, Height: 6, ZIndex: 50},
		},
		Cursor: screen.Cursor{Grid: 2, Row: 2, Col: 5},
		Mode:   "insert",
	}
	rFloat := h.CaretFor(state, cell)
	if math.Abs(float64(rFloat.X-135)) > 0.5 || math.Abs(float64(rFloat.Y-90)) > 0.5 || rFloat.W <= 0 || math.Abs(float64(rFloat.H-18)) > 0.5 {
		t.Errorf("CaretFor(float) = %+v, want X=135 Y=90 W>0 H=18", rFloat)
	}

	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).
			Fill().
			AutoFocus().
			HandleInput(h.Handle).
			TextCaret(rFloat).
			InputMethod(h.IMEAllowed())
	}, 400, 200)
	gotCaret, ok := tester.TextCaret()
	if !ok {
		t.Fatal("Tester.TextCaret() ok = false in insert mode, want true")
	}
	if math.Abs(float64(gotCaret.X-rFloat.X)) > 0.5 || math.Abs(float64(gotCaret.Y-rFloat.Y)) > 0.5 {
		t.Errorf("Tester.TextCaret() = %+v, want %+v", gotCaret, rFloat)
	}

	// Verify TraceRecorder observes Compose, Text, and KeyDown routed through Handler.
	tester.Compose("han", 3)
	tester.Compose("", 0)
	tester.Type("汉")
	tester.Key(0, ui.KeyEscape)

	stop()
	events := rec.Events()
	var sawCompose, sawText, sawKeyDown bool
	for _, ev := range events {
		switch ev.Kind {
		case input.Compose:
			sawCompose = true
		case input.Text:
			if ev.Text == "汉" {
				sawText = true
			}
		case input.KeyDown:
			sawKeyDown = true
		}
	}
	if !sawCompose || !sawText || !sawKeyDown {
		t.Errorf("TraceRecorder missing events: compose=%v text=%v keyDown=%v (total=%d)", sawCompose, sawText, sawKeyDown, len(events))
	}
}
