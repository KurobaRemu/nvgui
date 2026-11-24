package input_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/input"
)

func TestNotationTableCoverage(t *testing.T) {
	cases := []struct {
		name string
		ev   ui.InputEvent
		want string
	}{
		{"a", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA}, "a"},
		{"m", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyM}, "m"},
		{"z", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyZ}, "z"},
		{"0", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key0}, "0"},
		{"5", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key5}, "5"},
		{"9", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key9}, "9"},
		{"minus", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyMinus}, "-"},
		{"equal", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEqual}, "="},
		{"comma", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyComma}, ","},
		{"period", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyPeriod}, "."},
		{"slash", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeySlash}, "/"},
		{"semicolon", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeySemicolon}, ";"},
		{"quote", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyQuote}, "'"},
		{"bracket-left", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBracketLeft}, "["},
		{"bracket-right", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBracketRight}, "]"},
		{"backslash", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBackslash}, "<Bslash>"},
		{"backquote", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBackquote}, "`"},
		{"enter", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter}, "<CR>"},
		{"esc", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape}, "<Esc>"},
		{"tab", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyTab}, "<Tab>"},
		{"bs", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBackspace}, "<BS>"},
		{"del", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyDelete}, "<Del>"},
		{"space", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeySpace}, "<Space>"},
		{"left", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyLeft}, "<Left>"},
		{"right", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyRight}, "<Right>"},
		{"up", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyUp}, "<Up>"},
		{"down", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyDown}, "<Down>"},
		{"home", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyHome}, "<Home>"},
		{"end", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnd}, "<End>"},
		{"pageup", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyPageUp}, "<PageUp>"},
		{"pagedown", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyPageDown}, "<PageDown>"},
		{"f1", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyF1}, "<F1>"},
		{"f6", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyF6}, "<F6>"},
		{"f12", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyF12}, "<F12>"},
		{"ctrl-c", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Ctrl}, "<C-c>"},
		{"alt-x", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyX, Mods: ui.Alt}, "<A-x>"},
		{"super-s", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyS, Mods: ui.Super}, "<D-s>"},
		{"shift-tab", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyTab, Mods: ui.Shift}, "<S-Tab>"},
		{"ctrl-shift-a", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA, Mods: ui.Ctrl | ui.Shift}, "<C-S-a>"},
		{"unknown-mod-bits", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA, Mods: ui.Modifiers(0x80)}, "a"},
		{"unknown", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyUnknown}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := input.Notation(tc.ev)
			if tc.want == "" {
				if ok || got != "" {
					t.Fatalf("Notation(%s) = (%q, %v), want (\"\", false)", tc.name, got, ok)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("Notation(%s) = (%q, %v), want (%q, true)", tc.name, got, ok, tc.want)
			}
		})
	}
}

func TestEscapeTextKeycodesAndUTF8(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"less-than", "<", "<lt>"},
		{"literal-key-name", "<Esc>", "<lt>Esc>"},
		{"backslash", `\`, "<Bslash>"},
		{"backslash-then-lt", `\<`, "<Bslash><lt>"},
		{"tab", "\t", "<Tab>"},
		{"newline", "\n", "<NL>"},
		{"carriage-return", "\r", "<CR>"},
		{"escape-byte", "\x1b", "<Esc>"},
		{"delete-byte", "\x7f", "<Del>"},
		{"ctrl-a-byte", "\x01", "<C-a>"},
		{"nul-byte", "\x00", "<Nul>"},
		{"backspace-byte", "\x08", "<BS>"},
		{"ctrl-backslash-byte", "\x1c", `<C-\>`},
		{"ctrl-right-bracket-byte", "\x1d", "<C-]>"},
		{"ctrl-caret-byte", "\x1e", "<C-^>"},
		{"ctrl-underscore-byte", "\x1f", "<C-_>"},
		{"cjk-and-emoji", "你好，世界！。¥🌍", "你好，世界！。¥🌍"},
		{"ascii-unchanged", "hello:/", "hello:/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := input.EscapeText(tc.in); got != tc.want {
				t.Fatalf("EscapeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHandlerNoDoubleSendForEachKey(t *testing.T) {
	sender := &fakeSender{}
	q, stop := startQueue(t, sender, input.NewTraceRecorder())
	defer stop()
	h := input.NewHandler(q)

	keys := []ui.Key{ui.KeyA, ui.KeyH, ui.KeyZ, ui.Key0, ui.Key9, ui.KeySpace, ui.KeyMinus, ui.KeyBracketLeft, ui.KeyBackslash}
	texts := map[ui.Key]string{
		ui.KeyA: "a", ui.KeyH: "h", ui.KeyZ: "z", ui.Key0: "0", ui.Key9: "9",
		ui.KeySpace: " ", ui.KeyMinus: "-", ui.KeyBracketLeft: "[", ui.KeyBackslash: `\`,
	}
	var want strings.Builder
	for _, key := range keys {
		if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: key}) {
			t.Fatalf("KeyDown %v not consumed", key)
		}
		if !h.Handle(ui.InputEvent{Kind: ui.InputText, Text: texts[key]}) {
			t.Fatalf("InputText for %v not consumed", key)
		}
		h.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: key})
		want.WriteString(input.EscapeText(texts[key]))
	}
	stop()
	if got := strings.Join(sender.snapshot(), ""); got != want.String() {
		t.Fatalf("sent = %q, want %q", got, want.String())
	}
}

func TestHandlerKeyDownWithoutTextFallsBackOnKeyUp(t *testing.T) {
	sender := &fakeSender{}
	q, stop := startQueue(t, sender, nil)
	defer stop()
	h := input.NewHandler(q)

	if !h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyX}) {
		t.Fatal("KeyDown x not consumed")
	}
	h.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyX})
	stop()
	if got := strings.Join(sender.snapshot(), ""); got != "x" {
		t.Fatalf("sent = %q, want %q", got, "x")
	}
}

func TestHandlerModifiedTypeableKeySendsNotationOnce(t *testing.T) {
	sender := &fakeSender{}
	q, stop := startQueue(t, sender, input.NewTraceRecorder())
	defer stop()
	h := input.NewHandler(q)

	for _, ev := range []ui.InputEvent{
		{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Ctrl},
		{Kind: ui.InputText, Text: "c", Mods: ui.Ctrl},
		{Kind: ui.InputKeyUp, Key: ui.KeyC, Mods: ui.Ctrl},
		{Kind: ui.InputKeyDown, Key: ui.KeyX, Mods: ui.Alt},
		{Kind: ui.InputText, Text: "x", Mods: ui.Alt},
		{Kind: ui.InputKeyUp, Key: ui.KeyX, Mods: ui.Alt},
	} {
		h.Handle(ev)
	}
	stop()
	if got := strings.Join(sender.snapshot(), ""); got != "<C-c><A-x>" {
		t.Fatalf("sent = %q, want %q", got, "<C-c><A-x>")
	}
}

func TestQueueCancelWhileSenderBlocked(t *testing.T) {
	t.Run("bounded enqueue and cancellation", func(t *testing.T) {
		sender := &blockingSender{
			started: make(chan struct{}),
			release: make(chan struct{}),
		}
		rec := input.NewTraceRecorder()
		q := input.NewQueue(sender, rec)
		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- q.Run(ctx) }()
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(sender.release) }) }
		defer func() {
			cancel()
			release()
		}()

		firstDone := make(chan error, 1)
		go func() {
			firstDone <- q.Enqueue(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
		}()
		select {
		case err := <-firstDone:
			if err != nil {
				t.Fatalf("first Enqueue: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("first Enqueue blocked")
		}
		select {
		case <-sender.started:
		case <-time.After(3 * time.Second):
			t.Fatal("blocking sender did not start")
		}

		enqueueDone := make(chan error, 1)
		go func() {
			for index := 0; index < 256; index++ {
				ev := ui.InputEvent{Kind: ui.InputText, Text: fmt.Sprintf("%03d", index)}
				if err := q.Enqueue(ev); err != nil {
					enqueueDone <- err
					return
				}
			}
			enqueueDone <- nil
		}()
		select {
		case err := <-enqueueDone:
			if err != nil {
				t.Fatalf("bounded Enqueue: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("256 Enqueue calls blocked while sender was blocked")
		}
		if got := len(rec.Events()); got != 257 {
			t.Fatalf("TraceRecorder observed %d successful enqueues, want 257", got)
		}

		rejected := ui.InputEvent{Kind: ui.InputText, Text: "rejected"}
		rejectDone := make(chan error, 1)
		rejectStarted := make(chan struct{})
		go func() {
			close(rejectStarted)
			rejectDone <- q.Enqueue(rejected)
		}()
		<-rejectStarted
		runtime.Gosched()
		cancel()
		select {
		case err := <-rejectDone:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("full Enqueue after cancel = %v, want context.Canceled", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("full Enqueue did not unblock after ctx cancel")
		}
		events := rec.Events()
		if len(events) != 257 {
			t.Fatalf("TraceRecorder observed %d enqueues after rejection, want 257", len(events))
		}
		for _, event := range events {
			if event.Text == rejected.Text {
				t.Fatal("rejected event was recorded")
			}
		}

		release()
		select {
		case runErr := <-runDone:
			if !errors.Is(runErr, context.Canceled) {
				t.Fatalf("Queue.Run = %v, want context.Canceled", runErr)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Queue.Run did not exit")
		}
		if got := strings.Join(sender.snapshot(), ""); got != "<CR>" {
			t.Fatalf("sent = %q, want %q; rejected or queued events were delivered", got, "<CR>")
		}
		if err := q.Close(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Close = %v, want context.Canceled", err)
		}
	})

	t.Run("already canceled run does not deliver", func(t *testing.T) {
		sender := &fakeSender{}
		q := input.NewQueue(sender, nil)
		if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "prequeued"}); err != nil {
			t.Fatalf("prequeued Enqueue: %v", err)
		}
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := q.Run(canceledCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run(canceledCtx) = %v, want context.Canceled", err)
		}
		if got := sender.snapshot(); len(got) != 0 {
			t.Fatalf("sent = %v after canceled Run, want no delivery", got)
		}
		if err := q.Close(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Close = %v, want context.Canceled", err)
		}
	})

	t.Run("close before run", func(t *testing.T) {
		before := queueUserCPUSeconds()
		q := input.NewQueue(&fakeSender{}, nil)
		closeDone := make(chan error, 1)
		go func() { closeDone <- q.Close() }()
		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatalf("Close before Run = %v, want nil", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Close before Run blocked")
		}
		if err := q.Run(context.Background()); err != nil {
			t.Fatalf("Run after Close = %v, want nil", err)
		}
		deadline := time.Now().Add(20 * time.Millisecond)
		for time.Now().Before(deadline) {
			runtime.Gosched()
		}
		if used := queueUserCPUSeconds() - before; used > 10*time.Millisecond {
			t.Fatalf("queue consumed %v user CPU after Close before Run, want idle", used)
		}
		stack := make([]byte, 1<<20)
		stackSize := runtime.Stack(stack, true)
		if strings.Contains(string(stack[:stackSize]), "nvgui/input.(*queueImpl).") {
			t.Fatalf("queue goroutine remained after Close before Run:\n%s", stack[:stackSize])
		}
	})
}

func queueUserCPUSeconds() time.Duration {
	samples := []metrics.Sample{{Name: "/cpu/classes/user:cpu-seconds"}}
	metrics.Read(samples)
	return time.Duration(samples[0].Value.Float64() * float64(time.Second))
}

type blockingSender struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	sent    []string
}

func (s *blockingSender) Input(keys string) (int, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	s.mu.Lock()
	s.sent = append(s.sent, keys)
	s.mu.Unlock()
	return len(keys), nil
}

func (s *blockingSender) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func TestQueueCloseDrainsAcceptedEvents(t *testing.T) {
	sender := &fakeSender{}
	q := input.NewQueue(sender, nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- q.Run(ctx) }()

	for _, text := range []string{"a", "b", "c"} {
		if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: text}); err != nil {
			t.Fatalf("Enqueue(%q): %v", text, err)
		}
	}
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Queue.Run did not exit")
	}
	if got := strings.Join(sender.snapshot(), ""); got != "abc" {
		t.Fatalf("sent = %q, want %q", got, "abc")
	}
	if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "d"}); err == nil {
		t.Fatal("Enqueue after Close returned nil, want error")
	}
}

func TestTraceReplayUsesRecordedTimeAndCancelled(t *testing.T) {
	rec := input.NewTraceRecorder()
	at := time.Unix(1_700_000_000, 123456000).UTC()
	rec.Observe(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA, Code: ui.CodeKeyA, Mods: ui.Ctrl, Repeat: true, Time: at, Canceled: true})
	raw, err := input.MarshalTrace(rec.Events())
	if err != nil {
		t.Fatalf("MarshalTrace: %v", err)
	}
	events, err := input.UnmarshalTrace(raw)
	if err != nil {
		t.Fatalf("UnmarshalTrace: %v", err)
	}
	var got []ui.InputEvent
	if err := input.Replay(events, func(ev ui.InputEvent) { got = append(got, ev) }); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Replay emitted %d events, want 1", len(got))
	}
	want := ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA, Code: ui.CodeKeyA, Mods: ui.Ctrl, Repeat: true, Time: at, Canceled: true}
	if got[0] != want {
		t.Fatalf("replayed = %+v, want %+v", got[0], want)
	}
}

func TestHandlerComposePreventsKeyUpFallback(t *testing.T) {
	sender := &fakeSender{}
	q, stop := startQueue(t, sender, nil)
	defer stop()
	h := input.NewHandler(q)

	h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyN})
	h.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "n", Caret: 1})
	h.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyN})
	stop()
	if got := strings.Join(sender.snapshot(), ""); got != "" {
		t.Fatalf("sent = %q, want no key after InputCompose", got)
	}
}

func TestHandlerModifiedKeyDoesNotSwallowIndependentText(t *testing.T) {
	cases := []struct {
		name string
		down ui.InputEvent
		want string
	}{
		{"super", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Super}, "你"},
		{"ctrl", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: ui.Ctrl}, "<C-c>你"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &fakeSender{}
			q, stop := startQueue(t, sender, nil)
			h := input.NewHandler(q)

			h.Handle(tc.down)
			if !h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "你"}) {
				t.Fatal("independent InputText not consumed")
			}
			stop()
			if got := strings.Join(sender.snapshot(), ""); got != tc.want {
				t.Fatalf("sent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandlerRejectsUnsupportedKeys(t *testing.T) {
	sender := &fakeSender{}
	q, stop := startQueue(t, sender, nil)
	defer stop()
	h := input.NewHandler(q)

	cases := []struct {
		name string
		ev   ui.InputEvent
	}{
		{"back", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBack}},
		{"forward", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyForward}},
		{"context-menu", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyContextMenu}},
		{"pointer-down", ui.InputEvent{Kind: ui.InputPointerDown}},
		{"super-key-up", ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyC, Mods: ui.Super}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if h.Handle(tc.ev) {
				t.Fatalf("Handler.Handle(%s) = true, want false", tc.name)
			}
		})
	}
}

func TestQueueSenderErrorPropagates(t *testing.T) {
	wantErr := errors.New("sender failed")
	q := input.NewQueue(&failingSender{err: wantErr}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := runQueueRecovering(q, ctx)

	if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "a"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- q.Close() }()

	closeErr := waitQueueError(t, closeDone, "Close")
	runErr := waitQueueError(t, runDone, "Run")
	if !errors.Is(closeErr, wantErr) {
		t.Fatalf("Close error = %v, want %v", closeErr, wantErr)
	}
	if !errors.Is(runErr, wantErr) {
		t.Fatalf("Run error = %v, want %v", runErr, wantErr)
	}
}

func TestQueueCloseBeforeRun(t *testing.T) {
	q := input.NewQueue(&fakeSender{}, nil)
	closeDone := make(chan error, 1)
	go func() { closeDone <- q.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close before Run = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		go func() { _ = q.Run(context.Background()) }()
		<-closeDone
		t.Fatal("Close before Run deadlocked")
	}

	if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "a"}); err == nil {
		t.Fatal("Enqueue after Close returned nil, want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := runQueueRecovering(q, ctx)
	if err := waitQueueError(t, runDone, "Run after Close"); err != nil {
		t.Fatalf("Run after Close = %v, want nil", err)
	}
}

func TestQueueDeadlineExceeded(t *testing.T) {
	q := input.NewQueue(&fakeSender{}, nil)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	runDone := runQueueRecovering(q, ctx)

	runErr := waitQueueError(t, runDone, "Run")
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want context.DeadlineExceeded", runErr)
	}
	if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "a"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Enqueue error = %v, want context.DeadlineExceeded", err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- q.Close() }()
	if err := waitQueueError(t, closeDone, "Close"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want context.DeadlineExceeded", err)
	}
}

func TestQueueNilSender(t *testing.T) {
	q := input.NewQueue(nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := runQueueRecovering(q, ctx)

	if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "a"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- q.Close() }()

	closeErr := waitQueueError(t, closeDone, "Close")
	runErr := waitQueueError(t, runDone, "Run")
	if closeErr == nil || strings.Contains(closeErr.Error(), "panic") {
		t.Fatalf("Close error = %v, want explicit sender error without panic", closeErr)
	}
	if runErr == nil || strings.Contains(runErr.Error(), "panic") {
		t.Fatalf("Run error = %v, want explicit sender error without panic", runErr)
	}
}

func TestQueueSentReturnsCopy(t *testing.T) {
	sender := &fakeSender{}
	q, stop := startQueue(t, sender, nil)
	h := input.NewHandler(q)
	for _, text := range []string{"a", "b"} {
		if !h.Handle(ui.InputEvent{Kind: ui.InputText, Text: text}) {
			t.Fatalf("InputText %q not consumed", text)
		}
	}
	stop()

	first := q.Sent()
	if len(first) != 2 {
		t.Fatalf("Sent() len = %d, want 2", len(first))
	}
	first[0] = "mutated"
	second := q.Sent()
	if second[0] != "a" || second[1] != "b" {
		t.Fatalf("Sent() after mutating returned slice = %v, want [a b]", second)
	}
}

func TestQueueBoundedBufferBeforeRun(t *testing.T) {
	rec := input.NewTraceRecorder()
	q := input.NewQueue(&fakeSender{}, rec)
	for i := 0; i < 256; i++ {
		if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "a"}); err != nil {
			t.Fatalf("Enqueue(%d) before Run: %v", i, err)
		}
	}
	if got := len(rec.Events()); got != 256 {
		t.Fatalf("TraceRecorder observed %d accepted enqueues, want 256", got)
	}

	fullDone := make(chan error, 1)
	fullExited := make(chan struct{})
	go func() {
		defer close(fullExited)
		fullDone <- q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "a"})
	}()
	closeDone := make(chan error, 1)
	go func() { closeDone <- q.Close() }()
	closeErr := waitQueueError(t, closeDone, "Close")
	if closeErr != nil {
		t.Fatalf("Close = %v, want nil", closeErr)
	}
	select {
	case err := <-fullDone:
		if err == nil {
			t.Fatal("Enqueue beyond bounded buffer returned nil, want error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("full Enqueue did not return after Close")
	}
	<-fullExited
	if got := len(rec.Events()); got != 256 {
		t.Fatalf("TraceRecorder observed %d accepted enqueues after Close, want 256", got)
	}
	if err := q.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: "a"}); err == nil {
		t.Fatal("Enqueue after Close returned nil, want error")
	}
}

func TestUnmarshalTraceValidation(t *testing.T) {
	valid := func(seq uint64, kind input.TraceKind) input.TraceEvent {
		return input.TraceEvent{Seq: seq, At: time.Unix(1_700_000_000, 0).UTC(), Kind: kind}
	}
	line := func(ev input.TraceEvent) string {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal trace event: %v", err)
		}
		return string(raw)
	}

	first := line(valid(1, input.KeyDown))
	second := line(valid(2, input.Text))
	raw := []byte(first + "\r\n \r\n" + second + "\r\n\r\n")
	decoded, err := input.UnmarshalTrace(raw)
	if err != nil {
		t.Fatalf("UnmarshalTrace with CRLF and blank lines: %v", err)
	}
	if len(decoded) != 2 || decoded[0].Seq != 1 || decoded[1].Seq != 2 {
		t.Fatalf("UnmarshalTrace decoded %+v, want two events with Seq 1 and 2", decoded)
	}
	var replayed int
	if err := input.Replay(decoded, func(ui.InputEvent) { replayed++ }); err != nil {
		t.Fatalf("Replay valid events: %v", err)
	}
	if replayed != 2 {
		t.Fatalf("Replay emitted %d events, want 2", replayed)
	}

	invalid := []struct {
		name   string
		events []input.TraceEvent
	}{
		{"illegal-kind", []input.TraceEvent{valid(1, input.TraceKind(99))}},
		{"zero-seq", []input.TraceEvent{valid(0, input.KeyDown)}},
		{"duplicate-seq", []input.TraceEvent{valid(1, input.KeyDown), valid(1, input.Text)}},
		{"out-of-order-seq", []input.TraceEvent{valid(2, input.KeyDown), valid(1, input.Text)}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			for _, ev := range tc.events {
				raw = append(raw, line(ev)...)
				raw = append(raw, '\n')
			}
			if _, err := input.UnmarshalTrace(raw); err == nil {
				t.Error("UnmarshalTrace returned nil error, want validation error")
			}
			if err := input.Replay(tc.events, func(ui.InputEvent) {}); err == nil {
				t.Error("Replay returned nil error, want validation error")
			}
		})
	}

	if err := input.Replay([]input.TraceEvent{valid(1, input.KeyDown)}, nil); err == nil {
		t.Error("Replay with nil sink returned nil error")
	}
}

type failingSender struct{ err error }

func (s *failingSender) Input(string) (int, error) { return 0, s.err }

func runQueueRecovering(q *input.Queue, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("queue Run panicked: %v", recovered)
			}
		}()
		done <- q.Run(ctx)
	}()
	return done
}

func waitQueueError(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return", what)
		return nil
	}
}

func TestTraceRecorderBoundsEventsWithMonotonicSeq(t *testing.T) {
	recorder := input.NewTraceRecorder()
	const total = 65536 + 12
	for index := 0; index < total; index++ {
		recorder.Observe(ui.InputEvent{Kind: ui.InputText, Text: "a"})
	}
	events := recorder.Events()
	if len(events) != 65536 {
		t.Fatalf("len(events) = %d, want 65536", len(events))
	}
	if events[0].Seq != 13 || events[len(events)-1].Seq != total {
		t.Fatalf("seq range = [%d..%d], want [13..%d]", events[0].Seq, events[len(events)-1].Seq, total)
	}
	raw, err := input.MarshalTrace(events[:5])
	if err != nil {
		t.Fatalf("MarshalTrace: %v", err)
	}
	if _, err := input.UnmarshalTrace(raw); err != nil {
		t.Fatalf("UnmarshalTrace after trim: %v", err)
	}
}
