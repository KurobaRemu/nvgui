package input

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/neovim/go-client/msgpack/rpc"
	"github.com/neovim/go-client/nvim"

	"nvgui/connection"
	"nvgui/screen"
)

type imeTestSender struct {
	mu          sync.Mutex
	sent        []string
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func (sender *imeTestSender) Input(keys string) (int, error) {
	if sender.started != nil {
		sender.startOnce.Do(func() { close(sender.started) })
	}
	if sender.release != nil {
		<-sender.release
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.sent = append(sender.sent, keys)
	return len(keys), nil
}

func (sender *imeTestSender) unblock() {
	if sender.release != nil {
		sender.releaseOnce.Do(func() { close(sender.release) })
	}
}

func (sender *imeTestSender) snapshot() []string {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return append([]string(nil), sender.sent...)
}

func imeStartQueue(test *testing.T, sender Sender, recorder *TraceRecorder) (*Queue, func()) {
	test.Helper()
	queue := NewQueue(sender, recorder)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- queue.Run(ctx)
	}()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			_ = queue.Close()
			cancel()
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
				test.Fatal("Queue.Run did not exit after Close/cancel")
			}
		})
	}
	return queue, stop
}

func imeWaitSignal(test *testing.T, signal <-chan struct{}, label string) {
	test.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		test.Fatalf("%s did not arrive", label)
	}
}

func imeWaitQueueState(test *testing.T, queue *Queue, label string, ready func(*queueImpl) bool) {
	test.Helper()
	impl := queue.impl
	done := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			impl.mu.Lock()
			ok := ready(impl)
			impl.mu.Unlock()
			if ok {
				close(done)
				return
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-done:
		close(stop)
	case <-time.After(3 * time.Second):
		close(stop)
		test.Fatalf("timed out waiting for %s", label)
	}
}

func imeHandle(test *testing.T, handler *Handler, event ui.InputEvent) {
	test.Helper()
	if !handler.Handle(event) {
		test.Fatalf("Handle(%+v) = false, want true", event)
	}
}

func imeAssertSent(test *testing.T, sender *imeTestSender, want []string) {
	test.Helper()
	got := sender.snapshot()
	if !slices.Equal(got, want) {
		test.Fatalf("sent = %v, want %v", got, want)
	}
}

func imeAssertTrace(test *testing.T, recorder *TraceRecorder, want []ui.InputEvent) {
	test.Helper()
	events := recorder.Events()
	if len(events) != len(want) {
		test.Fatalf("TraceRecorder events = %d, want %d", len(events), len(want))
	}
	for index, event := range want {
		recorded := events[index]
		if recorded.Seq != uint64(index+1) ||
			!recorded.At.Equal(event.Time) ||
			recorded.Kind != traceKindOf(event.Kind) ||
			recorded.Key != uint16(event.Key) ||
			recorded.Code != uint16(event.Code) ||
			recorded.Mods != uint8(event.Mods) ||
			recorded.Repeat != event.Repeat ||
			recorded.Text != event.Text ||
			recorded.Caret != event.Caret ||
			recorded.Cancelled != event.Canceled {
			test.Fatalf("TraceRecorder event %d = %+v, want %+v", index, recorded, event)
		}
	}
}

func imeRectEqual(got, want ui.Rect) bool {
	const epsilon = 0.001
	return math.Abs(float64(got.X-want.X)) <= epsilon &&
		math.Abs(float64(got.Y-want.Y)) <= epsilon &&
		math.Abs(float64(got.W-want.W)) <= epsilon &&
		math.Abs(float64(got.H-want.H)) <= epsilon
}

func imeAssertRect(test *testing.T, label string, got, want ui.Rect) {
	test.Helper()
	if !imeRectEqual(got, want) {
		test.Errorf("%s = %+v, want %+v", label, got, want)
	}
}

func imeMixedCase(mode string) string {
	if mode == "" {
		return mode
	}
	return strings.ToUpper(mode[:1]) + mode[1:]
}

func imeModeVariants(mode string) []string {
	variants := []string{mode, strings.ToUpper(mode), " \t" + imeMixedCase(mode) + " \n"}
	if strings.Contains(mode, "_") {
		hyphenated := strings.ReplaceAll(mode, "_", "-")
		variants = append(variants, " \t"+strings.ToUpper(hyphenated)+" \n")
	}
	return variants
}

func imeStartNvim(test *testing.T) *nvim.Nvim {
	test.Helper()
	conn, err := connection.Start(context.Background(), connection.Options{
		Args:   []string{"--clean"},
		Env:    os.Environ(),
		Dir:    test.TempDir(),
		Width:  80,
		Height: 24,
	})
	if err != nil {
		test.Fatalf("connection.Start(nvim --clean): %v", err)
	}
	test.Cleanup(func() {
		if err := conn.Close(); err != nil {
			test.Errorf("connection.Close: %v", err)
		}
	})
	return conn.Nvim()
}

func imeQuitNvim(test *testing.T, client *nvim.Nvim) {
	test.Helper()
	if err := client.Command("qa!"); err != nil && !errors.Is(err, rpc.ErrClosed) {
		test.Fatalf("nvim quit: %v", err)
	}
}

func imeNvimMode(test *testing.T, client *nvim.Nvim) string {
	test.Helper()
	mode, err := client.Mode()
	if err != nil {
		test.Fatalf("nvim_get_mode: %v", err)
	}
	return mode.Mode
}

func imeAssertNvimMode(test *testing.T, client *nvim.Nvim, want string) {
	test.Helper()
	if got := imeNvimMode(test, client); got != want {
		test.Fatalf("nvim_get_mode().mode = %q, want %q", got, want)
	}
}

func imeAssertBuffer(test *testing.T, client *nvim.Nvim, want ...string) {
	test.Helper()
	buffer, err := client.CurrentBuffer()
	if err != nil {
		test.Fatalf("CurrentBuffer: %v", err)
	}
	raw, err := client.BufferLines(buffer, 0, -1, true)
	if err != nil {
		test.Fatalf("BufferLines: %v", err)
	}
	lines := make([]string, len(raw))
	for index := range raw {
		lines[index] = string(raw[index])
	}
	if !reflect.DeepEqual(lines, want) {
		test.Fatalf("headless nvim buffer = %q, want %q", lines, want)
	}
}

func imeDrain(test *testing.T, client *nvim.Nvim) {
	test.Helper()
	if err := client.FeedKeys("", "x", false); err != nil {
		test.Fatalf("drain Neovim typeahead: %v", err)
	}
}

func imeHandleSequence(test *testing.T, handler *Handler, events []ui.InputEvent) {
	test.Helper()
	for _, event := range events {
		want := true
		if event.Kind == ui.InputKeyDown || event.Kind == ui.InputKeyUp {
			_, known := Notation(event)
			want = known && event.Mods&ui.Super == 0
		}
		if got := handler.Handle(event); got != want {
			test.Fatalf("Handle(%+v) = %v, want %v", event, got, want)
		}
	}
}

func imePunctuationEvents(base time.Time) []ui.InputEvent {
	return []ui.InputEvent{
		{Kind: ui.InputText, Text: "：", Time: base},
		{Kind: ui.InputText, Text: "；", Time: base.Add(time.Millisecond)},
		{Kind: ui.InputText, Text: "，", Time: base.Add(2 * time.Millisecond)},
		{Kind: ui.InputText, Text: "。", Time: base.Add(3 * time.Millisecond)},
		{Kind: ui.InputText, Text: "？", Time: base.Add(4 * time.Millisecond)},
		{Kind: ui.InputText, Text: "！", Time: base.Add(5 * time.Millisecond)},
		{Kind: ui.InputText, Text: "【", Time: base.Add(6 * time.Millisecond)},
		{Kind: ui.InputText, Text: "】", Time: base.Add(7 * time.Millisecond)},
		{Kind: ui.InputText, Text: "「", Time: base.Add(8 * time.Millisecond)},
		{Kind: ui.InputText, Text: "」", Time: base.Add(9 * time.Millisecond)},
		{Kind: ui.InputText, Text: "『", Time: base.Add(10 * time.Millisecond)},
		{Kind: ui.InputText, Text: "』", Time: base.Add(11 * time.Millisecond)},
		{Kind: ui.InputText, Text: "《", Time: base.Add(12 * time.Millisecond)},
		{Kind: ui.InputText, Text: "〈", Time: base.Add(13 * time.Millisecond)},
		{Kind: ui.InputText, Text: "》", Time: base.Add(14 * time.Millisecond)},
		{Kind: ui.InputText, Text: "〉", Time: base.Add(15 * time.Millisecond)},
		{Kind: ui.InputText, Text: "（", Time: base.Add(16 * time.Millisecond)},
		{Kind: ui.InputText, Text: "）", Time: base.Add(17 * time.Millisecond)},
		{Kind: ui.InputText, Text: "“", Time: base.Add(18 * time.Millisecond)},
		{Kind: ui.InputText, Text: "”", Time: base.Add(19 * time.Millisecond)},
		{Kind: ui.InputText, Text: "‘", Time: base.Add(20 * time.Millisecond)},
		{Kind: ui.InputText, Text: "’", Time: base.Add(21 * time.Millisecond)},
		{Kind: ui.InputText, Text: "・", Time: base.Add(22 * time.Millisecond)},
		{Kind: ui.InputText, Text: "ー", Time: base.Add(23 * time.Millisecond)},
		{Kind: ui.InputText, Text: "·", Time: base.Add(24 * time.Millisecond)},
		{Kind: ui.InputText, Text: "……", Time: base.Add(25 * time.Millisecond)},
		{Kind: ui.InputText, Text: "…", Time: base.Add(26 * time.Millisecond)},
		{Kind: ui.InputText, Text: "——", Time: base.Add(27 * time.Millisecond)},
		{Kind: ui.InputText, Text: "—", Time: base.Add(28 * time.Millisecond)},
		{Kind: ui.InputText, Text: "～", Time: base.Add(29 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＠", Time: base.Add(30 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＃", Time: base.Add(31 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＄", Time: base.Add(32 * time.Millisecond)},
		{Kind: ui.InputText, Text: "％", Time: base.Add(33 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＾", Time: base.Add(34 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＆", Time: base.Add(35 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＊", Time: base.Add(36 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＋", Time: base.Add(37 * time.Millisecond)},
		{Kind: ui.InputText, Text: "－", Time: base.Add(38 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＝", Time: base.Add(39 * time.Millisecond)},
		{Kind: ui.InputText, Text: "／", Time: base.Add(40 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＜", Time: base.Add(41 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＞", Time: base.Add(42 * time.Millisecond)},
		{Kind: ui.InputText, Text: "［", Time: base.Add(43 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＼", Time: base.Add(44 * time.Millisecond)},
		{Kind: ui.InputText, Text: "］", Time: base.Add(45 * time.Millisecond)},
		{Kind: ui.InputText, Text: "＿", Time: base.Add(46 * time.Millisecond)},
		{Kind: ui.InputText, Text: "｀", Time: base.Add(47 * time.Millisecond)},
		{Kind: ui.InputText, Text: "｛", Time: base.Add(48 * time.Millisecond)},
		{Kind: ui.InputText, Text: "｜", Time: base.Add(49 * time.Millisecond)},
		{Kind: ui.InputText, Text: "｝", Time: base.Add(50 * time.Millisecond)},
		{Kind: ui.InputText, Text: "、", Time: base.Add(51 * time.Millisecond)},
		{Kind: ui.InputText, Text: "￥", Time: base.Add(52 * time.Millisecond)},
		{Kind: ui.InputText, Text: "¥", Time: base.Add(53 * time.Millisecond)},
	}
}

func imePunctuationNormalWant() []string {
	return []string{
		":", ";", ",", ".", "?", "!", "[", "]", "[", "]", "{", "}",
		"<lt>", "<lt>", ">", ">", "(", ")", `"`, `"`, "'", "'", "/", "-", "`",
		"^", "^", "_", "_", "~", "@", "#", "$", "%", "^", "&", "*", "+", "-", "=",
		"/", "<lt>", ">", "[", "<Bslash>", "]", "_", "`", "{", "|", "}",
		"<Bslash>", "$", "$",
	}
}

func TestIMECompositionConsumesKeysAndCommitsOnce(test *testing.T) {
	cases := []struct {
		name       string
		endCompose bool
		commit     string
	}{
		{name: "empty compose then commit", endCompose: true, commit: "你"},
		{name: "direct commit", endCompose: false, commit: "好"},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			sender := &imeTestSender{}
			recorder := NewTraceRecorder()
			queue, stop := imeStartQueue(test, sender, recorder)
			defer stop()
			handler := NewHandler(queue)
			handler.SetMode("insert")

			baseTime := time.Unix(1_700_000_000, 0).UTC()
			events := make([]ui.InputEvent, 0, 20)
			appendEvent := func(event ui.InputEvent) {
				event.Time = baseTime.Add(time.Duration(len(events)) * time.Millisecond)
				events = append(events, event)
				imeHandle(test, handler, event)
			}

			appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "ni", Caret: 2})
			for _, key := range []ui.Key{ui.KeyEnter, ui.KeyBackspace, ui.KeyLeft, ui.KeySpace, ui.Key1} {
				appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: key})
				appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: key})
			}
			state := handler.State()
			if state.Preedit != "ni" || state.Caret != 2 || !state.Allowed || state.Mode != "insert" {
				test.Fatalf("State() while composing = %+v, want Preedit=ni Caret=2 Allowed=true Mode=insert", state)
			}

			if testCase.endCompose {
				appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "", Caret: 0})
			}
			appendEvent(ui.InputEvent{Kind: ui.InputText, Text: testCase.commit})
			state = handler.State()
			if state.Preedit != "" || state.Caret != 0 {
				test.Fatalf("State() after commit = %+v, want empty Preedit and zero Caret", state)
			}
			appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
			appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyEnter})

			stop()
			imeAssertTrace(test, recorder, events)
			imeAssertSent(test, sender, []string{testCase.commit, "<CR>"})
		})
	}
}

func TestIMEComposeCancelEmptyAndCaretClamp(test *testing.T) {
	sender := &imeTestSender{}
	recorder := NewTraceRecorder()
	queue, stop := imeStartQueue(test, sender, recorder)
	defer stop()
	handler := NewHandler(queue)
	handler.SetMode("insert")

	baseTime := time.Unix(1_700_000_000, 0).UTC()
	events := make([]ui.InputEvent, 0, 20)
	appendEvent := func(event ui.InputEvent) {
		event.Time = baseTime.Add(time.Duration(len(events)) * time.Millisecond)
		events = append(events, event)
		imeHandle(test, handler, event)
	}

	appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "ni", Caret: 2})
	appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "discard", Caret: 4, Canceled: true})
	state := handler.State()
	if state.Preedit != "" || state.Caret != 0 {
		test.Fatalf("State() after canceled compose = %+v, want empty Preedit and zero Caret", state)
	}
	appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyJ})
	appendEvent(ui.InputEvent{Kind: ui.InputText, Text: "j"})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyJ})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyEnter})

	appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "", Caret: 9})
	state = handler.State()
	if state.Preedit != "" || state.Caret != 0 {
		test.Fatalf("State() after empty compose = %+v, want empty Preedit and zero Caret", state)
	}
	appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyK})
	appendEvent(ui.InputEvent{Kind: ui.InputText, Text: "k"})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyK})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyEnter})

	appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "你好", Caret: -3})
	state = handler.State()
	if state.Preedit != "你好" || state.Caret != 0 {
		test.Fatalf("State() after negative caret = %+v, want Preedit=你好 Caret=0", state)
	}
	appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "世界", Caret: 99})
	state = handler.State()
	if state.Preedit != "世界" || state.Caret != 2 {
		test.Fatalf("State() after oversized caret = %+v, want Preedit=世界 Caret=2", state)
	}
	appendEvent(ui.InputEvent{Kind: ui.InputText, Text: "你"})

	stop()
	imeAssertTrace(test, recorder, events)
	imeAssertSent(test, sender, []string{"j", "<CR>", "k", "<CR>", "你"})
}

func TestIMESetModeClearsCompositionAndEpoch(test *testing.T) {
	sender := &imeTestSender{started: make(chan struct{}), release: make(chan struct{})}
	recorder := NewTraceRecorder()
	queue, stop := imeStartQueue(test, sender, recorder)
	defer func() {
		sender.unblock()
		stop()
	}()
	handler := NewHandler(queue)
	handler.SetMode("insert")

	baseTime := time.Unix(1_700_000_000, 0).UTC()
	events := make([]ui.InputEvent, 0, 10)
	appendEvent := func(event ui.InputEvent) {
		event.Time = baseTime.Add(time.Duration(len(events)) * time.Millisecond)
		events = append(events, event)
		imeHandle(test, handler, event)
	}

	appendEvent(ui.InputEvent{Kind: ui.InputText, Text: "a"})
	imeWaitSignal(test, sender.started, "first sender delivery")
	appendEvent(ui.InputEvent{Kind: ui.InputCompose, Text: "ni", Caret: 2})
	if state := handler.State(); state.Preedit != "ni" || state.Caret != 2 {
		test.Fatalf("State() before SetMode = %+v, want Preedit=ni Caret=2", state)
	}

	handler.SetMode("normal")
	state := handler.State()
	if state.Preedit != "" || state.Caret != 0 || state.Allowed || state.Mode != "normal" {
		test.Fatalf("State() after SetMode(normal) = %+v, want empty Preedit, zero Caret, disallowed", state)
	}
	if handler.IMEAllowed() {
		test.Fatal("IMEAllowed() after SetMode(normal) = true, want false")
	}

	appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyJ})
	appendEvent(ui.InputEvent{Kind: ui.InputText, Text: "j"})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyJ})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyK})
	appendEvent(ui.InputEvent{Kind: ui.InputText, Text: "k"})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyK})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
	appendEvent(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyEnter})

	sender.unblock()
	stop()
	imeAssertTrace(test, recorder, events)
	imeAssertSent(test, sender, []string{"a", "j", "k", "<CR>"})
}

func TestIMEResetCompositionAfterEmptyQueueDeliversNormalKeys(test *testing.T) {
	sender := &imeTestSender{}
	queue, stop := imeStartQueue(test, sender, nil)
	defer stop()
	handler := NewHandler(queue)
	handler.SetMode("insert")

	imeHandle(test, handler, ui.InputEvent{Kind: ui.InputCompose, Text: "ni", Caret: 2})
	imeWaitQueueState(test, queue, "compose consumed with empty queue", func(impl *queueImpl) bool {
		return impl.composing && impl.length == 0
	})

	handler.SetMode("normal")
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeyBackspace, ui.KeyLeft} {
		imeHandle(test, handler, ui.InputEvent{Kind: ui.InputKeyDown, Key: key})
		imeHandle(test, handler, ui.InputEvent{Kind: ui.InputKeyUp, Key: key})
	}

	stop()
	imeAssertSent(test, sender, []string{"<CR>", "<BS>", "<Left>"})
}

func TestIMEResetCompositionKeepsQueuedNormalKeys(test *testing.T) {
	sender := &imeTestSender{started: make(chan struct{}), release: make(chan struct{})}
	queue, stop := imeStartQueue(test, sender, nil)
	defer func() {
		sender.unblock()
		stop()
	}()
	handler := NewHandler(queue)
	handler.SetMode("normal")

	imeHandle(test, handler, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
	imeWaitSignal(test, sender.started, "blocking sender delivery")

	backlog := []ui.InputEvent{
		{Kind: ui.InputKeyDown, Key: ui.KeyEnter},
		{Kind: ui.InputKeyUp, Key: ui.KeyEnter},
		{Kind: ui.InputKeyDown, Key: ui.KeyBackspace},
		{Kind: ui.InputKeyUp, Key: ui.KeyBackspace},
		{Kind: ui.InputKeyDown, Key: ui.KeyD, Mods: ui.Ctrl},
		{Kind: ui.InputKeyUp, Key: ui.KeyD, Mods: ui.Ctrl},
		{Kind: ui.InputKeyDown, Key: ui.KeyJ},
		{Kind: ui.InputKeyUp, Key: ui.KeyJ},
	}
	for _, event := range backlog {
		imeHandle(test, handler, event)
	}
	queue.impl.mu.Lock()
	queued := queue.impl.length
	queue.impl.mu.Unlock()
	if queued != len(backlog) {
		test.Fatalf("queued events = %d, want %d", queued, len(backlog))
	}

	handler.SetMode("normal")
	sender.unblock()
	stop()
	imeAssertSent(test, sender, []string{"<CR>", "<CR>", "<BS>", "<C-d>", "j"})
}

func TestIMEAllowedModeMatrix(test *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{mode: "normal", want: false},
		{mode: "visual", want: false},
		{mode: "visual_line", want: false},
		{mode: "visual_block", want: false},
		{mode: "operator", want: false},
		{mode: "operator-pending", want: false},
		{mode: "more", want: false},
		{mode: "confirm", want: false},
		{mode: "showmatch", want: false},
		{mode: "insert", want: true},
		{mode: "replace", want: true},
		{mode: "cmdline", want: true},
		{mode: "cmdline_normal", want: true},
		{mode: "cmdline_insert", want: true},
		{mode: "cmdline_replace", want: true},
		{mode: "terminal", want: true},
	}
	handler := NewHandler(NewQueue(&imeTestSender{}, nil))
	for _, testCase := range cases {
		for _, variant := range imeModeVariants(testCase.mode) {
			handler.SetMode(variant)
			if got := handler.IMEAllowed(); got != testCase.want {
				test.Errorf("IMEAllowed() after SetMode(%q) = %v, want %v", variant, got, testCase.want)
			}
			state := handler.State()
			if state.Allowed != testCase.want || state.Mode != variant {
				test.Errorf("State() after SetMode(%q) = %+v, want Allowed=%v Mode=%q", variant, state, testCase.want, variant)
			}
			if got := handler.Mode(); got != variant {
				test.Errorf("Mode() after SetMode(%q) = %q, want %q", variant, got, variant)
			}
		}
	}
}

func TestIMEChinesePunctuationPassThrough(test *testing.T) {
	sender := &imeTestSender{}
	recorder := NewTraceRecorder()
	queue, stop := imeStartQueue(test, sender, recorder)
	defer stop()
	handler := NewHandler(queue)
	handler.SetMode("insert")

	baseTime := time.Unix(1_700_000_000, 0).UTC()
	punctuation := imePunctuationEvents(baseTime)
	events := make([]ui.InputEvent, 0, len(punctuation)*2)
	wantSent := make([]string, 0, len(punctuation)*2)
	for _, event := range punctuation {
		events = append(events, event)
		wantSent = append(wantSent, escapeText(event.Text))
		imeHandle(test, handler, event)
	}
	handler.SetMode("normal")
	normalEvents := make([]ui.InputEvent, len(punctuation))
	for index, event := range punctuation {
		normalEvents[index] = event
		normalEvents[index].Time = baseTime.Add(time.Duration(len(punctuation)+index) * time.Millisecond)
		events = append(events, normalEvents[index])
	}
	imeHandleSequence(test, handler, normalEvents)
	wantSent = append(wantSent, imePunctuationNormalWant()...)

	stop()
	imeAssertTrace(test, recorder, events)
	imeAssertSent(test, sender, wantSent)
}

func TestIMENormalModeSCIMColonReplayFromVMTrace(test *testing.T) {
	baseTime := time.Unix(1_700_000_000, 0).UTC()
	sequence := []ui.InputEvent{
		{Kind: ui.InputKeyDown, Key: ui.KeyBracketLeft, Code: ui.CodeBracketLeft, Mods: ui.Ctrl, Time: baseTime},
		{Kind: ui.InputKeyUp, Key: ui.KeyBracketLeft, Code: ui.CodeBracketLeft, Mods: ui.Ctrl, Time: baseTime.Add(time.Millisecond)},
		{Kind: ui.InputKeyDown, Code: ui.KeyCode(51), Mods: ui.Shift, Time: baseTime.Add(2 * time.Millisecond)},
		{Kind: ui.InputText, Text: "：", Time: baseTime.Add(3 * time.Millisecond)},
		{Kind: ui.InputKeyUp, Code: ui.KeyCode(51), Time: baseTime.Add(4 * time.Millisecond)},
	}

	test.Run("direct", func(test *testing.T) {
		sender := &imeTestSender{}
		recorder := NewTraceRecorder()
		queue, stop := imeStartQueue(test, sender, recorder)
		defer stop()
		handler := NewHandler(queue)
		handler.SetMode("normal")
		for _, event := range sequence {
			handler.Handle(event)
		}
		stop()
		imeAssertSent(test, sender, []string{"<C-[>", ":"})
		imeAssertTrace(test, recorder, sequence)
	})
	test.Run("trace round trip", func(test *testing.T) {
		recorder := NewTraceRecorder()
		for _, event := range sequence {
			recorder.Observe(event)
		}
		imeAssertTrace(test, recorder, sequence)
		for _, event := range recorder.Events() {
			if event.Code == 51 && (event.Kind == KeyDown || event.Kind == KeyUp) &&
				(event.Key != 0 || event.Kind == KeyDown && event.Mods != uint8(ui.Shift)) {
				test.Fatalf("recorded physical event = %+v, want Key:0 Code:51 Mods:Shift", event)
			}
		}
		raw, err := MarshalTrace(recorder.Events())
		if err != nil {
			test.Fatalf("MarshalTrace: %v", err)
		}
		decoded, err := UnmarshalTrace(raw)
		if err != nil {
			test.Fatalf("UnmarshalTrace: %v", err)
		}
		sender := &imeTestSender{}
		queue, stop := imeStartQueue(test, sender, nil)
		defer stop()
		handler := NewHandler(queue)
		handler.SetMode("normal")
		if err := Replay(decoded, func(event ui.InputEvent) { handler.Handle(event) }); err != nil {
			test.Fatalf("Replay: %v", err)
		}
		stop()
		imeAssertSent(test, sender, []string{"<C-[>", ":"})
	})

	client := imeStartNvim(test)
	queue, stop := imeStartQueue(test, client, nil)
	handler := NewHandler(queue)
	handler.SetMode("normal")
	for _, event := range sequence {
		handler.Handle(event)
	}
	stop()
	imeDrain(test, client)
	imeAssertNvimMode(test, client, "c")
	if _, err := client.Input("<Esc>"); err != nil {
		test.Fatalf("leave cmdline: %v", err)
	}
	imeDrain(test, client)
	imeAssertNvimMode(test, client, "n")
	imeQuitNvim(test, client)
}

func TestIMENormalModeCJKPunctuationDisambiguationAndModes(test *testing.T) {
	test.Run("mode mapping matrix", func(test *testing.T) {
		for _, mode := range []string{"normal", "visual", "operator-pending"} {
			test.Run(mode, func(test *testing.T) {
				sender := &imeTestSender{}
				queue, stop := imeStartQueue(test, sender, nil)
				defer stop()
				handler := NewHandler(queue)
				handler.SetMode(mode)
				imeHandleSequence(test, handler, imePunctuationEvents(time.Unix(1_700_000_000, 0).UTC()))
				stop()
				imeAssertSent(test, sender, imePunctuationNormalWant())
			})
		}
	})

	test.Run("physical code disambiguation", func(test *testing.T) {
		cases := []struct {
			name string
			down ui.KeyCode
			text string
			want string
		}{
			{"SCIM ideographic comma", ui.CodeBackslash, "、", "<Bslash>"},
			{"Kotoeri ideographic comma", ui.CodeComma, "、", ","},
			{"SCIM Shift+4 yen", ui.CodeDigit4, "￥", "$"},
			{"SCIM Shift+4 yen sign", ui.CodeDigit4, "¥", "$"},
			{"SCIM backslash yen", ui.CodeBackslash, "￥", "<Bslash>"},
			{"JIS yen", ui.CodeIntlYen, "¥", "<Bslash>"},
			{"default yen", ui.CodeUnknown, "￥", "$"},
		}
		for _, testCase := range cases {
			test.Run(testCase.name, func(test *testing.T) {
				sender := &imeTestSender{}
				queue, stop := imeStartQueue(test, sender, nil)
				defer stop()
				handler := NewHandler(queue)
				handler.SetMode("normal")
				base := time.Unix(1_700_000_000, 0).UTC()
				events := []ui.InputEvent{
					{Kind: ui.InputKeyDown, Code: testCase.down, Time: base},
					{Kind: ui.InputText, Text: testCase.text, Time: base.Add(time.Millisecond)},
					{Kind: ui.InputKeyUp, Code: testCase.down, Time: base.Add(2 * time.Millisecond)},
					{Kind: ui.InputText, Text: "、", Time: base.Add(3 * time.Millisecond)},
					{Kind: ui.InputText, Text: "￥", Time: base.Add(4 * time.Millisecond)},
					{Kind: ui.InputKeyDown, Key: ui.KeyA, Code: ui.CodeKeyA, Time: base.Add(5 * time.Millisecond)},
					{Kind: ui.InputText, Text: "、", Time: base.Add(6 * time.Millisecond)},
					{Kind: ui.InputCompose, Text: "ni", Caret: 2, Time: base.Add(7 * time.Millisecond)},
					{Kind: ui.InputText, Text: "￥", Time: base.Add(8 * time.Millisecond)},
				}
				imeHandleSequence(test, handler, events)
				stop()
				imeAssertSent(test, sender, []string{testCase.want, "<Bslash>", "$", "<Bslash>", "$"})
			})
		}
	})

	test.Run("IME modes pass through", func(test *testing.T) {
		texts := []string{"：", "，", "。", "、", "￥", "！", "？"}
		for _, mode := range []string{"insert", "cmdline_normal", "replace", "terminal"} {
			test.Run(mode, func(test *testing.T) {
				sender := &imeTestSender{}
				queue, stop := imeStartQueue(test, sender, nil)
				defer stop()
				handler := NewHandler(queue)
				handler.SetMode(mode)
				events := make([]ui.InputEvent, 0, len(texts))
				want := make([]string, 0, len(texts))
				for index, text := range texts {
					events = append(events, ui.InputEvent{Kind: ui.InputText, Text: text, Time: time.Unix(1_700_000_000, int64(index)*int64(time.Millisecond)).UTC()})
					want = append(want, text)
				}
				imeHandleSequence(test, handler, events)
				stop()
				imeAssertSent(test, sender, want)
			})
		}
	})

	test.Run("unrelated text stays unchanged", func(test *testing.T) {
		sender := &imeTestSender{}
		queue, stop := imeStartQueue(test, sender, nil)
		defer stop()
		handler := NewHandler(queue)
		handler.SetMode("normal")
		texts := []string{"ä", "ö", "ü", "ß", "â", "^", "~", "´", "你", "好", "日本語", "你好，世界！"}
		events := make([]ui.InputEvent, 0, len(texts))
		want := make([]string, 0, len(texts))
		for index, text := range texts {
			events = append(events, ui.InputEvent{Kind: ui.InputText, Text: text, Time: time.Unix(1_700_000_000, int64(index)*int64(time.Millisecond)).UTC()})
			want = append(want, text)
		}
		imeHandleSequence(test, handler, events)
		stop()
		imeAssertSent(test, sender, want)
	})

	test.Run("per slot IME snapshot under backpressure", func(test *testing.T) {
		sender := &imeTestSender{started: make(chan struct{}), release: make(chan struct{})}
		queue, stop := imeStartQueue(test, sender, nil)
		defer func() {
			sender.unblock()
			stop()
		}()
		handler := NewHandler(queue)
		handler.SetMode("insert")
		base := time.Unix(1_700_000_000, 0).UTC()
		imeHandle(test, handler, ui.InputEvent{Kind: ui.InputText, Text: "，", Time: base})
		imeWaitSignal(test, sender.started, "first sender delivery")
		imeHandle(test, handler, ui.InputEvent{Kind: ui.InputText, Text: "：", Time: base.Add(time.Millisecond)})
		imeWaitQueueState(test, queue, "queued second insert punctuation", func(impl *queueImpl) bool { return impl.length == 1 })
		handler.SetMode("normal")
		for _, text := range []string{"，", "："} {
			event := ui.InputEvent{Kind: ui.InputText, Text: text, Time: base.Add(2 * time.Millisecond)}
			imeHandle(test, handler, event)
		}
		sender.unblock()
		stop()
		imeAssertSent(test, sender, []string{"，", "：", ",", ":"})
	})
}

func TestIMENormalModeCJKPunctuationHeadlessNvim(test *testing.T) {
	client := imeStartNvim(test)
	if _, err := client.Input("i"); err != nil {
		test.Fatalf("enter insert mode: %v", err)
	}
	imeDrain(test, client)
	queue, stop := imeStartQueue(test, client, nil)
	handler := NewHandler(queue)
	handler.SetMode("insert")
	base := time.Unix(1_700_000_000, 0).UTC()
	event := func(index int, ev ui.InputEvent) ui.InputEvent {
		ev.Time = base.Add(time.Duration(index) * time.Millisecond)
		return ev
	}
	events := []ui.InputEvent{
		event(0, ui.InputEvent{Kind: ui.InputText, Text: "你好，世界。"}),
		event(6, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBracketLeft, Code: ui.CodeBracketLeft, Mods: ui.Ctrl}),
		event(7, ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyBracketLeft, Code: ui.CodeBracketLeft, Mods: ui.Ctrl}),
	}
	imeHandleSequence(test, handler, events)
	stop()
	imeDrain(test, client)
	imeAssertNvimMode(test, client, "n")
	imeAssertBuffer(test, client, "你好，世界。")

	queue, stop = imeStartQueue(test, client, nil)
	handler = NewHandler(queue)
	handler.SetMode("normal")
	normalEvents := []ui.InputEvent{
		event(10, ui.InputEvent{Kind: ui.InputText, Text: "："}),
		event(11, ui.InputEvent{Kind: ui.InputText, Text: "％"}),
		event(12, ui.InputEvent{Kind: ui.InputText, Text: "s"}),
		event(13, ui.InputEvent{Kind: ui.InputText, Text: "/"}),
		event(14, ui.InputEvent{Kind: ui.InputText, Text: "世界"}),
		event(15, ui.InputEvent{Kind: ui.InputText, Text: "/"}),
		event(16, ui.InputEvent{Kind: ui.InputText, Text: "nvim"}),
		event(17, ui.InputEvent{Kind: ui.InputText, Text: "／"}),
		event(18, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter, Code: ui.CodeEnter}),
		event(19, ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyEnter, Code: ui.CodeEnter}),
	}
	imeHandleSequence(test, handler, normalEvents)
	stop()
	imeDrain(test, client)
	imeAssertNvimMode(test, client, "n")
	imeAssertBuffer(test, client, "你好，nvim。")

	queue, stop = imeStartQueue(test, client, nil)
	handler = NewHandler(queue)
	handler.SetMode("normal")
	endEvents := []ui.InputEvent{
		event(20, ui.InputEvent{Kind: ui.InputKeyDown, Code: ui.CodeDigit4, Mods: ui.Shift}),
		event(21, ui.InputEvent{Kind: ui.InputText, Text: "￥"}),
		event(22, ui.InputEvent{Kind: ui.InputKeyUp, Code: ui.CodeDigit4}),
		event(23, ui.InputEvent{Kind: ui.InputText, Text: "a"}),
		event(24, ui.InputEvent{Kind: ui.InputText, Text: "！"}),
	}
	imeHandleSequence(test, handler, endEvents)
	stop()
	imeDrain(test, client)
	imeAssertNvimMode(test, client, "i")
	if _, err := client.Input("<Esc>"); err != nil {
		test.Fatalf("leave insert mode: %v", err)
	}
	imeDrain(test, client)
	imeAssertNvimMode(test, client, "n")
	imeAssertBuffer(test, client, "你好，nvim。!")
	imeQuitNvim(test, client)
}

func TestIMECaretAndCaretForBoundaries(test *testing.T) {
	handler := NewHandler(NewQueue(&imeTestSender{}, nil))
	cell := CellSize{Width: 9, Height: 18}
	rootRect := ui.Rect{X: 63, Y: 72, W: 9, H: 18}

	imeAssertRect(test, "CaretFor(nil)", handler.CaretFor(nil, cell), ui.Rect{})
	imeAssertRect(test, "Caret(zero cell)", handler.Caret(1, 2, CellSize{}), ui.Rect{})
	imeAssertRect(test, "Caret(negative width)", handler.Caret(1, 2, CellSize{Width: -9, Height: 18}), ui.Rect{})
	imeAssertRect(test, "Caret(zero height)", handler.Caret(1, 2, CellSize{Width: 9, Height: 0}), ui.Rect{})
	imeAssertRect(test, "Caret(negative coordinates)", handler.Caret(-3, -7, cell), ui.Rect{X: 0, Y: 0, W: 9, H: 18})
	imeAssertRect(test, "CaretFor(negative cell)", handler.CaretFor(&screen.State{
		Grids:  map[int]*screen.Grid{1: {Width: 80, Height: 24}},
		Cursor: screen.Cursor{Grid: 1, Row: 4, Col: 7},
	}, CellSize{}), ui.Rect{})
	imeAssertRect(test, "CaretFor(negative coordinates)", handler.CaretFor(&screen.State{
		Grids:  map[int]*screen.Grid{1: {Width: 80, Height: 24}},
		Cursor: screen.Cursor{Grid: 1, Row: -2, Col: -5},
	}, cell), ui.Rect{X: 0, Y: 0, W: 9, H: 18})

	rootState := &screen.State{
		Grids:  map[int]*screen.Grid{1: {Width: 80, Height: 24}},
		Cursor: screen.Cursor{Grid: 1, Row: 4, Col: 7},
	}
	imeAssertRect(test, "CaretFor(root)", handler.CaretFor(rootState, cell), rootRect)

	winState := &screen.State{
		Grids: map[int]*screen.Grid{2: {Width: 20, Height: 6}},
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedWin, Row: 2, Col: 3, Width: 20, Height: 6},
		},
		Cursor: screen.Cursor{Grid: 2, Row: 1, Col: 4},
	}
	imeAssertRect(test, "CaretFor(PlacedWin)", handler.CaretFor(winState, cell), ui.Rect{X: 63, Y: 54, W: 9, H: 18})

	floatState := &screen.State{
		Grids: map[int]*screen.Grid{2: {Width: 20, Height: 6}},
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedFloat, ScreenRow: 3, ScreenCol: 10, Width: 20, Height: 6},
		},
		Cursor: screen.Cursor{Grid: 2, Row: 2, Col: 5},
	}
	imeAssertRect(test, "CaretFor(PlacedFloat)", handler.CaretFor(floatState, cell), ui.Rect{X: 135, Y: 90, W: 9, H: 18})

	msgState := &screen.State{
		Grids:  map[int]*screen.Grid{3: {Width: 80, Height: 24}},
		Msg:    &screen.MsgPlacement{Grid: 3, Row: 6},
		Cursor: screen.Cursor{Grid: 3, Row: 1, Col: 2},
	}
	imeAssertRect(test, "CaretFor(Msg)", handler.CaretFor(msgState, cell), ui.Rect{X: 18, Y: 126, W: 9, H: 18})

	coveredMsgState := &screen.State{
		Grids: map[int]*screen.Grid{4: {Width: 20, Height: 6}},
		Placements: map[int]screen.Placement{
			4: {Kind: screen.PlacedFloat, ScreenRow: 8, ScreenCol: 9, Width: 20, Height: 6},
		},
		Msg:    &screen.MsgPlacement{Grid: 4, Row: 100},
		Cursor: screen.Cursor{Grid: 4, Row: 1, Col: 2},
	}
	imeAssertRect(test, "CaretFor(float covers Msg)", handler.CaretFor(coveredMsgState, cell), ui.Rect{X: 99, Y: 162, W: 9, H: 18})

	externalState := &screen.State{
		Grids: map[int]*screen.Grid{5: {Width: 20, Height: 6}},
		Placements: map[int]screen.Placement{
			5: {Kind: screen.PlacedExternal},
		},
		Msg:    &screen.MsgPlacement{Grid: 5, Row: 100},
		Cursor: screen.Cursor{Grid: 5, Row: 2, Col: 3},
	}
	imeAssertRect(test, "CaretFor(PlacedExternal)", handler.CaretFor(externalState, cell), ui.Rect{})
}

func TestIMEConcurrentReadersAndWriters(test *testing.T) {
	sender := &imeTestSender{}
	recorder := NewTraceRecorder()
	queue, stop := imeStartQueue(test, sender, recorder)
	defer stop()
	handler := NewHandler(queue)
	handler.SetMode("insert")

	const writerCount = 4
	const readerCount = 4
	const iterations = 80
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for writerIndex := 0; writerIndex < writerCount; writerIndex++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				switch iteration % 4 {
				case 0:
					handler.SetMode("insert")
				case 1:
					handler.SetMode("normal")
				case 2:
					handler.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "ni", Caret: 2})
				default:
					handler.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter})
				}
			}
		}()
	}
	for readerIndex := 0; readerIndex < readerCount; readerIndex++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				_ = handler.State()
				_ = handler.Preedit()
				_ = handler.IMEAllowed()
				_ = handler.Mode()
			}
		}()
	}
	close(start)
	waitGroup.Wait()
	handler.SetMode("insert")
	state := handler.State()
	if state.Mode != "insert" || !state.Allowed || !handler.IMEAllowed() || handler.Mode() != "insert" {
		test.Fatalf("final state = %+v, want insert mode with IME allowed", state)
	}
	stop()
	if len(recorder.Events()) == 0 {
		test.Fatal("TraceRecorder observed no concurrent events")
	}
}

func TestIMEStateReadersRespondDuringEnqueueBackpressure(test *testing.T) {
	sender := &imeTestSender{started: make(chan struct{}), release: make(chan struct{})}
	recorder := NewTraceRecorder()
	queue, stop := imeStartQueue(test, sender, recorder)
	defer func() {
		sender.unblock()
		stop()
	}()
	handler := NewHandler(queue)
	handler.SetMode("insert")

	events := make([]ui.InputEvent, 0, queueCapacity+2)
	first := ui.InputEvent{Kind: ui.InputText, Text: "a"}
	events = append(events, first)
	imeHandle(test, handler, first)
	imeWaitSignal(test, sender.started, "first sender delivery")

	for index := 0; index < queueCapacity; index++ {
		event := ui.InputEvent{Kind: ui.InputText, Text: fmt.Sprintf("%03d", index)}
		if err := queue.Enqueue(event); err != nil {
			test.Fatalf("Enqueue(%d) = %v", index, err)
		}
		events = append(events, event)
	}

	handleStarted := make(chan struct{})
	handleDone := make(chan bool, 1)
	blocked := ui.InputEvent{Kind: ui.InputText, Text: "blocked"}
	go func() {
		close(handleStarted)
		handleDone <- handler.Handle(blocked)
	}()
	<-handleStarted
	runtime.Gosched()
	select {
	case <-handleDone:
		test.Fatal("Handle returned before full queue gained capacity")
	case <-time.After(20 * time.Millisecond):
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		_ = handler.State()
		_ = handler.Preedit()
		_ = handler.IMEAllowed()
		_ = handler.Mode()
	}()
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		test.Fatal("state readers blocked while Handle waited on full queue")
	}

	sender.unblock()
	select {
	case accepted := <-handleDone:
		if !accepted {
			test.Fatal("blocked Handle returned false after queue capacity was released")
		}
	case <-time.After(3 * time.Second):
		test.Fatal("blocked Handle did not complete after queue capacity was released")
	}
	events = append(events, blocked)
	stop()
	imeAssertTrace(test, recorder, events)
	if got := len(sender.snapshot()); got != queueCapacity+2 {
		test.Fatalf("sender calls = %d, want %d", got, queueCapacity+2)
	}
}

func TestIMERapidCancelThenRecomposePreservesCommits(test *testing.T) {
	sender := &imeTestSender{}
	recorder := NewTraceRecorder()
	queue, stop := imeStartQueue(test, sender, recorder)
	defer stop()
	handler := NewHandler(queue)

	var want strings.Builder
	base := time.Unix(1_700_000_000, 0).UTC()
	for round := 0; round < 24; round++ {
		handler.SetMode("insert")
		handler.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "pinyin", Caret: 6, Time: base})
		handler.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBackspace, Time: base})
		handler.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "pinyi", Caret: 5, Time: base})
		switch round % 3 {
		case 0:
			handler.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape, Time: base})
			handler.SetMode("normal")
			want.WriteString("<Esc>")
		case 1:
			handler.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "", Canceled: true, Time: base})
			handler.Handle(ui.InputEvent{Kind: ui.InputText, Text: "汉", Time: base})
			want.WriteString("汉")
		case 2:
			handler.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "", Time: base})
			handler.Handle(ui.InputEvent{Kind: ui.InputText, Text: "字", Time: base})
			want.WriteString("字")
		}
	}
	stop()
	if got := handler.Preedit(); got != "" {
		test.Fatalf("residual Preedit = %q", got)
	}
	joined := strings.Join(sender.snapshot(), "")
	if strings.Contains(joined, "pinyin") || strings.Contains(joined, "pinyi") {
		test.Fatalf("preedit leaked: %q", joined)
	}
	if joined != want.String() {
		test.Fatalf("sent = %q, want %q", joined, want.String())
	}
}
