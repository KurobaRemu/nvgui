package input

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

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

	punctuation := []string{"，", "。", "！", "？", "；", "：", "“", "”", "（", "）", "【", "】", "《", "》", "¥", ":", "/"}
	baseTime := time.Unix(1_700_000_000, 0).UTC()
	events := make([]ui.InputEvent, 0, len(punctuation)*2)
	wantSent := make([]string, 0, len(punctuation)*2)
	appendText := func(text string) {
		event := ui.InputEvent{
			Kind: ui.InputText,
			Text: text,
			Time: baseTime.Add(time.Duration(len(events)) * time.Millisecond),
		}
		events = append(events, event)
		wantSent = append(wantSent, text)
		imeHandle(test, handler, event)
	}
	for _, text := range punctuation {
		appendText(text)
	}
	handler.SetMode("normal")
	for _, text := range punctuation {
		appendText(text)
	}

	stop()
	imeAssertTrace(test, recorder, events)
	imeAssertSent(test, sender, wantSent)
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
