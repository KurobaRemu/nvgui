package input_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/input"
)

type fakeSender struct {
	mu   sync.Mutex
	sent []string
	gate chan struct{}
}

func (s *fakeSender) Input(keys string) (int, error) {
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, keys)
	return len(keys), nil
}

func (s *fakeSender) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func startQueue(t *testing.T, sender input.Sender, rec *input.TraceRecorder) (*input.Queue, func()) {
	t.Helper()
	q := input.NewQueue(sender, rec)
	if q == nil {
		t.Fatal("NewQueue returned nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- q.Run(ctx)
	}()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			_ = q.Close()
			cancel()
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
				t.Fatal("Queue.Run did not exit after Close/cancel")
			}
		})
	}
	return q, stop
}

func TestInputKeysNotation(t *testing.T) {
	cases := []struct {
		name string
		ev   ui.InputEvent
		want []string // accepted notations
		ok   bool
	}{
		{"a", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA}, []string{"a"}, true},
		{"z", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyZ}, []string{"z"}, true},
		{"0", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key0}, []string{"0"}, true},
		{"9", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key9}, []string{"9"}, true},
		{"enter", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter}, []string{"<CR>"}, true},
		{"esc", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape}, []string{"<Esc>"}, true},
		{"tab", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyTab}, []string{"<Tab>"}, true},
		{"bs", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBackspace}, []string{"<BS>"}, true},
		{"del", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyDelete}, []string{"<Del>"}, true},
		{"space", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeySpace}, []string{"<Space>", " "}, true},
		{"left", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyLeft}, []string{"<Left>"}, true},
		{"right", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyRight}, []string{"<Right>"}, true},
		{"up", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyUp}, []string{"<Up>"}, true},
		{"down", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyDown}, []string{"<Down>"}, true},
		{"home", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyHome}, []string{"<Home>"}, true},
		{"end", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnd}, []string{"<End>"}, true},
		{"pageup", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyPageUp}, []string{"<PageUp>"}, true},
		{"pagedown", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyPageDown}, []string{"<PageDown>"}, true},
		{"f1", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyF1}, []string{"<F1>"}, true},
		{"f12", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyF12}, []string{"<F12>"}, true},
		{"ctrl-c", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Ctrl}, []string{"<C-c>"}, true},
		{"ctrl-[", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBracketLeft, Mods: ui.Ctrl}, []string{"<C-[>"}, true},
		{"alt-x", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyX, Mods: ui.Alt}, []string{"<A-x>"}, true},
		{"super-s", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyS, Mods: ui.Super}, []string{"<D-s>"}, true},
		{"shift-tab", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyTab, Mods: ui.Shift}, []string{"<S-Tab>"}, true},
		{"shift-up", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyUp, Mods: ui.Shift}, []string{"<S-Up>"}, true},
		{"unknown", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyUnknown}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := input.Notation(tc.ev)
			if ok != tc.ok {
				t.Fatalf("Notation(%s) ok = %v (got %q), want ok=%v", tc.name, ok, got, tc.ok)
			}
			if !tc.ok {
				return
			}
			matched := false
			for _, w := range tc.want {
				if got == w {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("Notation(%s) = %q, want one of %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestInputKeysEscapeText(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"<", "<lt>"},
		{"<Esc>", "<lt>Esc>"},
		{"你好，世界！。¥", "你好，世界！。¥"},
		{"🌍", "🌍"},
		{":", ":"},
		{"/", "/"},
	}
	for _, tc := range cases {
		if got := input.EscapeText(tc.in); got != tc.want {
			t.Errorf("EscapeText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInputKeysNoDoubleSendAndSuperYield(t *testing.T) {
	sender := &fakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := startQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}

	// Simulate macOS KeyDown + InputText + KeyUp for "hello": must NOT double-send.
	letters := []struct {
		key  ui.Key
		text string
	}{
		{ui.KeyH, "h"},
		{ui.KeyE, "e"},
		{ui.KeyL, "l"},
		{ui.KeyL, "l"},
		{ui.KeyO, "o"},
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	for i, l := range letters {
		ts := now.Add(time.Duration(i*10) * time.Millisecond)
		if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: l.key, Time: ts}) {
			t.Fatalf("Handler.Handle(KeyDown %s) = false, want true (must consume printable KeyDown)", l.text)
		}
		if !h.Handle(ui.InputEvent{Kind: ui.InputText, Text: l.text, Time: ts.Add(time.Millisecond)}) {
			t.Fatalf("Handler.Handle(InputText %s) = false, want true", l.text)
		}
		h.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: l.key, Time: ts.Add(2 * time.Millisecond)})
	}

	// Non-printable keys send notation immediately.
	if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter, Time: now.Add(100 * time.Millisecond)}) {
		t.Error("Handler.Handle(Enter) = false, want true")
	}
	if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape, Time: now.Add(110 * time.Millisecond)}) {
		t.Error("Handler.Handle(Escape) = false, want true")
	}
	if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyLeft, Time: now.Add(120 * time.Millisecond)}) {
		t.Error("Handler.Handle(Left) = false, want true")
	}
	if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Ctrl, Time: now.Add(130 * time.Millisecond)}) {
		t.Error("Handler.Handle(Ctrl-C) = false, want true")
	}
	// Option/Alt as Meta sends <A-x>.
	if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyX, Mods: ui.Alt, Time: now.Add(140 * time.Millisecond)}) {
		t.Error("Handler.Handle(Alt-X) = false, want true")
	}
	// Super/Cmd shortcut MUST return false (yield to app) and MUST NOT enter nvim.
	if h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Super, Time: now.Add(150 * time.Millisecond)}) {
		t.Error("Handler.Handle(Super-C) = true, want false (must yield Cmd shortcuts to app)")
	}

	stop()

	got := sender.snapshot()
	joined := strings.Join(got, "")
	wantJoined := "hello<CR><Esc><Left><C-c><A-x>"
	if joined != wantJoined {
		t.Fatalf("sent sequence = %q (items=%v), want %q", joined, got, wantJoined)
	}
	if qSent := q.Sent(); strings.Join(qSent, "") != wantJoined {
		t.Errorf("Queue.Sent() = %v, want joined %q", qSent, wantJoined)
	}
}

func TestInputKeysQueueOrderBackpressureAndCancel(t *testing.T) {
	gate := make(chan struct{})
	sender := &fakeSender{gate: gate}
	rec := input.NewTraceRecorder()
	q := input.NewQueue(sender, rec)
	if q == nil {
		t.Fatal("NewQueue returned nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- q.Run(ctx)
	}()

	const total = 40
	var accepted []string
	var mu sync.Mutex
	enqDone := make(chan struct{})
	go func() {
		defer close(enqDone)
		for i := 0; i < total; i++ {
			ch := string(rune('a' + (i % 26)))
			err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: ch})
			if err == nil {
				mu.Lock()
				accepted = append(accepted, ch)
				mu.Unlock()
			}
		}
	}()

	// Release sender gate so queue drains in order.
	close(gate)
	select {
	case <-enqDone:
	case <-time.After(3 * time.Second):
		cancel()
		_ = q.Close()
		t.Fatal("Enqueue goroutine did not complete after sender unblocked")
	}

	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Queue.Run did not exit after Close")
	}

	mu.Lock()
	accCopy := append([]string(nil), accepted...)
	mu.Unlock()
	if len(accCopy) == 0 {
		t.Fatal("no events were accepted by Queue.Enqueue")
	}
	got := sender.snapshot()
	if strings.Join(got, "") != strings.Join(accCopy, "") {
		t.Fatalf("FIFO order or delivery violated: got %q (%d items), accepted %q (%d items)",
			strings.Join(got, ""), len(got), strings.Join(accCopy, ""), len(accCopy))
	}

	// Enqueue after cancel/close must return an error.
	if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "x"}); err == nil {
		t.Fatal("Enqueue after Close/cancel returned nil error, want error")
	}
}

func TestInputKeysTraceRoundTripAndReplay(t *testing.T) {
	rec := input.NewTraceRecorder()
	if rec == nil {
		t.Fatal("NewTraceRecorder returned nil")
	}
	base := time.Unix(1_700_000_000, 0).UTC()
	originals := []ui.InputEvent{
		{Kind: ui.InputKeyDown, Key: ui.KeyA, Code: ui.CodeKeyA, Mods: ui.Ctrl, Repeat: false, Time: base},
		{Kind: ui.InputText, Text: "你好，。！？¥", Time: base.Add(5 * time.Millisecond)},
		{Kind: ui.InputCompose, Text: "ni", Caret: 2, Time: base.Add(10 * time.Millisecond)},
		{Kind: ui.InputKeyUp, Key: ui.KeyA, Code: ui.CodeKeyA, Mods: ui.Ctrl, Time: base.Add(15 * time.Millisecond)},
		{Kind: ui.InputCommand, Text: "copy", Time: base.Add(20 * time.Millisecond)},
	}
	for _, ev := range originals {
		rec.Observe(ev)
	}

	recorded := rec.Events()
	if len(recorded) != len(originals) {
		t.Fatalf("Events() len = %d, want %d", len(recorded), len(originals))
	}
	for i, ev := range recorded {
		if ev.Seq != uint64(i+1) {
			t.Errorf("recorded[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
		if !ev.At.Equal(originals[i].Time) {
			t.Errorf("recorded[%d].At = %v, want %v", i, ev.At, originals[i].Time)
		}
	}

	raw, err := input.MarshalTrace(recorded)
	if err != nil {
		t.Fatalf("MarshalTrace: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("MarshalTrace returned empty bytes")
	}
	decoded, err := input.UnmarshalTrace(raw)
	if err != nil {
		t.Fatalf("UnmarshalTrace: %v", err)
	}
	if len(decoded) != len(recorded) {
		t.Fatalf("UnmarshalTrace len = %d, want %d", len(decoded), len(recorded))
	}
	for i := range recorded {
		if decoded[i].Seq != recorded[i].Seq ||
			!decoded[i].At.Equal(recorded[i].At) ||
			decoded[i].Kind != recorded[i].Kind ||
			decoded[i].Key != recorded[i].Key ||
			decoded[i].Code != recorded[i].Code ||
			decoded[i].Mods != recorded[i].Mods ||
			decoded[i].Repeat != recorded[i].Repeat ||
			decoded[i].Text != recorded[i].Text ||
			decoded[i].Caret != recorded[i].Caret ||
			decoded[i].Cancelled != recorded[i].Cancelled {
			t.Errorf("decoded[%d] = %+v, want %+v", i, decoded[i], recorded[i])
		}
	}

	var replayed []ui.InputEvent
	if err := input.Replay(decoded, func(ev ui.InputEvent) {
		replayed = append(replayed, ev)
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(replayed) != len(originals) {
		t.Fatalf("Replay emitted %d events, want %d", len(replayed), len(originals))
	}
	for i := range originals {
		if replayed[i].Kind != originals[i].Kind ||
			replayed[i].Key != originals[i].Key ||
			replayed[i].Code != originals[i].Code ||
			replayed[i].Mods != originals[i].Mods ||
			replayed[i].Repeat != originals[i].Repeat ||
			replayed[i].Text != originals[i].Text ||
			replayed[i].Caret != originals[i].Caret ||
			replayed[i].Canceled != originals[i].Canceled {
			t.Errorf("replayed[%d] = %+v, want %+v", i, replayed[i], originals[i])
		}
	}

	if _, err := input.UnmarshalTrace([]byte("{truncated-json\n")); err == nil {
		t.Error("UnmarshalTrace on malformed JSONL returned nil error, want error")
	}
}
