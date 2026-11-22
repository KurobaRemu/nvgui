// Ticket 42: keyboard and CJK input P0 regression set.
// Explicitly excluded/deferred (12 covered + 5 deferred = 17/17 relevant P0 IDs):
//   - neovide#1006: Linux fcitx5, not a macOS host deterministic scenario.
//   - neovide#1488 / neovide#2330: real-window initial focus; VM L2 l2_runner.py validates.
//   - neovim#27509: upstream Kitty key-release protocol proposal.
//   - neovim#23645: upstream modifiable terminal emulator feature request.
//
// These five are also registered in wiki/m3-exit.md.
package input_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/neovim/go-client/nvim"

	"nvgui/connection"
	"nvgui/input"
)

type priorBugSender struct {
	mu   sync.Mutex
	sent []string
}

func (s *priorBugSender) Input(keys string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, keys)
	return len(keys), nil
}

func (s *priorBugSender) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func startPriorBugQueue(t *testing.T, sender input.Sender) (*input.Queue, func()) {
	t.Helper()
	queue := input.NewQueue(sender, input.NewTraceRecorder())
	if queue == nil {
		t.Fatal("NewQueue returned nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- queue.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if err := queue.Close(); err != nil {
				t.Errorf("Queue.Close: %v", err)
			}
			cancel()
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
				t.Fatal("Queue.Run did not exit")
			}
		})
	}
	return queue, stop
}

func priorBugTester(t *testing.T, handler *input.Handler) *ui.Tester {
	t.Helper()
	return ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().AutoFocus().HandleInput(handler.Handle).
			TextCaret(handler.Caret(1, 2, input.CellSize{Width: 9, Height: 18})).
			InputMethod(handler.IMEAllowed())
	}, 400, 200)
}

func assertPriorBugSent(t *testing.T, sender *priorBugSender, want []string) {
	t.Helper()
	if got := sender.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sender calls = %q, want %q", got, want)
	}
}

func assertPriorBugQueueSent(t *testing.T, queue *input.Queue, want []string) {
	t.Helper()
	if got := queue.Sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Queue.Sent() = %q, want %q", got, want)
	}
}

func assertPriorBugBufferLines(t *testing.T, client *nvim.Nvim, want []string) {
	t.Helper()
	if got := priorBugBufferLines(t, client); !reflect.DeepEqual(got, want) {
		t.Fatalf("headless nvim buffer = %q, want %q", got, want)
	}
}

func startPriorBugNvim(t *testing.T) *nvim.Nvim {
	t.Helper()
	conn, err := connection.Start(context.Background(), connection.Options{
		Args:   []string{"--clean", "-n"},
		Env:    os.Environ(),
		Dir:    t.TempDir(),
		Width:  80,
		Height: 24,
	})
	if err != nil {
		t.Fatalf("connection.Start(nvim --clean): %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.Nvim()
}

func priorBugBufferLines(t *testing.T, client *nvim.Nvim) []string {
	t.Helper()
	buffer, err := client.CurrentBuffer()
	if err != nil {
		t.Fatalf("CurrentBuffer: %v", err)
	}
	raw, err := client.BufferLines(buffer, 0, -1, true)
	if err != nil {
		t.Fatalf("BufferLines: %v", err)
	}
	lines := make([]string, len(raw))
	for index := range raw {
		lines[index] = string(raw[index])
	}
	return lines
}

func TestPriorBug_Neovide445_FVim36_NonQwertyLayoutTextOnceNoDuplicate(t *testing.T) {
	sender := &priorBugSender{}
	queue, stop := startPriorBugQueue(t, sender)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")
	tester := priorBugTester(t, handler)

	type layoutStroke struct {
		layout   string
		physical ui.Key
		text     string
	}
	strokes := []layoutStroke{
		{"Dvorak", ui.KeyA, "ä"},
		{"AZERTY", ui.KeyQ, "@"},
		{"German", ui.KeySemicolon, "ö"},
		{"German", ui.KeyBracketLeft, "ü"},
		{"German", ui.KeyMinus, "ß"},
		{"Finnish", ui.KeyBracketRight, "["},
	}
	want := make([]string, 0, len(strokes))
	for _, stroke := range strokes {
		tester.KeyAt(0, stroke.physical)
		tester.Type(stroke.text)
		want = append(want, stroke.text)
	}
	for _, stroke := range strokes {
		tester.TypeKey(0, stroke.physical, stroke.text)
		want = append(want, stroke.text)
	}
	if active, noIME := tester.TextInputActive(); !active || noIME {
		t.Fatalf("insert mode TextInputActive = (%v, %v), want (true, false)", active, noIME)
	}
	stop()
	assertPriorBugSent(t, sender, want)
	assertPriorBugQueueSent(t, queue, want)
}

func TestPriorBug_NeovimQt270_DeadKeyComposeSpaceAndRepeat(t *testing.T) {
	sender := &priorBugSender{}
	queue, stop := startPriorBugQueue(t, sender)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")
	tester := priorBugTester(t, handler)

	for _, dead := range []string{"^", "~", "´"} {
		tester.Compose(dead, 1)
		if got := handler.State().Preedit; got != dead {
			t.Fatalf("dead key %q preedit = %q, want %q", dead, got, dead)
		}
		tester.Compose("", 0)
		tester.Type(dead)
		tester.Key(0, ui.KeyJ)
	}
	tester.Compose("^", 1)
	tester.Key(0, ui.KeySpace)
	tester.Compose("", 0)
	tester.Type("^")
	tester.Compose("^", 1)
	tester.TypeKey(0, ui.KeyA, "â")
	tester.Compose("", 0)
	tester.Compose("^", 1)
	tester.Compose("^", 1)
	tester.Compose("", 0)
	tester.Type("^^")
	tester.Key(0, ui.KeyK)

	if got := handler.State().Preedit; got != "" {
		t.Fatalf("residual preedit = %q, want empty", got)
	}
	stop()
	assertPriorBugSent(t, sender, []string{"^", "j", "~", "j", "´", "j", "^", "â", "^^", "k"})
}

func TestPriorBug_Neovim11061_AltGrAndOptionProducedBracket(t *testing.T) {
	sender := &priorBugSender{}
	queue, stop := startPriorBugQueue(t, sender)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")

	base := time.Unix(1_700_000_000, 0).UTC()
	events := []ui.InputEvent{
		{Kind: ui.InputText, Text: "[", Time: base},
		{Kind: ui.InputKeyDown, Key: ui.Key5, Mods: ui.Alt, Time: base.Add(time.Millisecond)},
		{Kind: ui.InputText, Text: "∞", Time: base.Add(time.Millisecond)},
		{Kind: ui.InputKeyUp, Key: ui.Key5, Mods: ui.Alt, Time: base.Add(2 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyX, Mods: ui.Alt, Time: base.Add(3 * time.Millisecond)},
		{Kind: ui.InputText, Text: "≈", Mods: ui.Alt, Time: base.Add(3 * time.Millisecond)},
		{Kind: ui.InputKeyUp, Key: ui.KeyX, Mods: ui.Alt, Time: base.Add(4 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.Key5, Mods: ui.Alt, Time: base.Add(5 * time.Millisecond)},
		{Kind: ui.InputText, Text: "[", Time: base.Add(5 * time.Millisecond)},
		{Kind: ui.InputKeyUp, Key: ui.Key5, Mods: ui.Alt, Time: base.Add(6 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.Key5, Mods: ui.Alt, Time: base.Add(7 * time.Millisecond)},
		{Kind: ui.InputText, Text: "ä", Mods: ui.Alt, Time: base.Add(7 * time.Millisecond)},
		{Kind: ui.InputKeyUp, Key: ui.Key5, Mods: ui.Alt, Time: base.Add(8 * time.Millisecond)},
		{Kind: ui.InputText, Text: "]", Time: base.Add(9 * time.Millisecond)},
		{Kind: ui.InputText, Text: "j", Time: base.Add(10 * time.Millisecond)},
	}
	for _, event := range events {
		if !handler.Handle(event) {
			t.Fatalf("Handler.Handle(%+v) = false, want true", event)
		}
	}
	notation, ok := input.Notation(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key5, Mods: ui.Alt})
	if !ok || notation != "<A-5>" {
		t.Fatalf("Notation(explicit Option-5) = (%q, %v), want (<A-5>, true)", notation, ok)
	}
	stop()
	assertPriorBugSent(t, sender, []string{"[", "<A-5>", "<A-x>", "<A-5>", "<A-5>", "]", "j"})
}

func TestPriorBug_Neovim17867_Neovim5916_DistinguishCtrlI_CtrlM_CtrlBracket_Tab(t *testing.T) {
	cases := []struct {
		name string
		ev   ui.InputEvent
		want string
	}{
		{"ctrl bracket", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBracketLeft, Mods: ui.Ctrl}, "<C-[>"},
		{"ctrl m", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyM, Mods: ui.Ctrl}, "<C-m>"},
		{"ctrl i", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyI, Mods: ui.Ctrl}, "<C-i>"},
		{"tab", ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyTab}, "<Tab>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := input.Notation(tc.ev)
			if !ok || got != tc.want {
				t.Fatalf("Notation(%s) = (%q, %v), want (%q, true)", tc.name, got, ok, tc.want)
			}
		})
	}
	seen := make(map[string]bool)
	for _, tc := range cases {
		if seen[tc.want] {
			t.Fatalf("duplicate notation %q", tc.want)
		}
		seen[tc.want] = true
	}

	sender := &priorBugSender{}
	queue, stop := startPriorBugQueue(t, sender)
	defer stop()
	handler := input.NewHandler(queue)
	for _, tc := range cases {
		if !handler.Handle(tc.ev) {
			t.Fatalf("Handler.Handle(%s) = false, want true", tc.name)
		}
	}
	stop()
	want := make([]string, 0, len(cases))
	for _, tc := range cases {
		want = append(want, tc.want)
	}
	assertPriorBugSent(t, sender, want)
}

func TestPriorBug_FVim167_MacOSCtrlKeyNoTextEcho(t *testing.T) {
	sender := &priorBugSender{}
	queue, stop := startPriorBugQueue(t, sender)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")
	tester := priorBugTester(t, handler)

	chords := []struct {
		key   ui.Key
		echo  string
		plain string
		want  string
	}{
		{ui.KeyL, "l", "q", "<C-l>"},
		{ui.KeyA, "a", "w", "<C-a>"},
		{ui.KeyBracketLeft, "[", "e", "<C-[>"},
		{ui.KeyPeriod, ".", "r", "<C-.>"},
	}
	want := make([]string, 0, len(chords)+1)
	for _, chord := range chords {
		tester.TypeKey(ui.Ctrl, chord.key, chord.echo)
		tester.TypeKey(0, chord.key, chord.plain)
		want = append(want, chord.want, chord.plain)
	}
	controlEchoes := []struct {
		key  ui.Key
		echo string
		want string
	}{
		{ui.KeyA, "\x01", "<C-a>"},
		{ui.KeyBracketLeft, "\x1b", "<C-[>"},
		{ui.KeySpace, " ", "<C-Space>"},
		{ui.KeyBackslash, "\x1c", "<C-\\>"},
		{ui.KeyBracketRight, "\x1d", "<C-]>"},
		{ui.Key6, "\x1e", "<C-6>"},
		{ui.KeyMinus, "\x1f", "<C-->"},
		{ui.KeyZ, "\x1a", "<C-z>"},
		{ui.KeyA, "A", "<C-a>"},
		{ui.KeyA, "a", "<C-a>"},
		{ui.KeyBracketLeft, "{", "<C-[>"},
		{ui.KeyPeriod, ">", "<C-.>"},
		{ui.Key5, "%", "<C-5>"},
	}
	for _, chord := range controlEchoes {
		tester.TypeKey(ui.Ctrl, chord.key, chord.echo)
		want = append(want, chord.want)
	}
	for _, text := range []string{"-", "<", "你"} {
		if !handler.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA, Mods: ui.Ctrl}) {
			t.Fatal("Handler.Handle(Ctrl-A) = false, want true")
		}
		if !handler.Handle(ui.InputEvent{Kind: ui.InputText, Text: text}) {
			t.Fatalf("Handler.Handle(Ctrl text %q) = false, want true", text)
		}
		if !handler.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyA, Mods: ui.Ctrl}) {
			t.Fatal("Handler.Handle(Ctrl-A up) = false, want true")
		}
		want = append(want, "<C-a>", input.EscapeText(text))
	}
	for _, modified := range []ui.InputEvent{
		{Kind: ui.InputText, Text: "\x01", Mods: ui.Ctrl, Key: ui.KeyUnknown},
		{Kind: ui.InputText, Text: "a", Mods: ui.Ctrl, Key: ui.KeyA},
	} {
		if !handler.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA, Mods: ui.Ctrl}) {
			t.Fatal("Handler.Handle(Ctrl-A) = false, want true")
		}
		if !handler.Handle(modified) {
			t.Fatalf("Handler.Handle(%+v) = false, want true", modified)
		}
		if !handler.Handle(ui.InputEvent{Kind: ui.InputKeyUp, Key: ui.KeyA, Mods: ui.Ctrl}) {
			t.Fatal("Handler.Handle(Ctrl-A up) = false, want true")
		}
		want = append(want, "<C-a>")
	}
	tester.Type("x")
	want = append(want, "x")
	stop()
	assertPriorBugSent(t, sender, want)
}

func TestPriorBug_Neovide2567_ChinesePunctuationNotConvertedToASCII(t *testing.T) {
	const punctuation = "。，！？¥"
	client := startPriorBugNvim(t)
	queue, stop := startPriorBugQueue(t, client)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")
	tester := priorBugTester(t, handler)
	if _, err := client.Input("i"); err != nil {
		t.Fatalf("enter insert mode: %v", err)
	}
	for _, char := range punctuation {
		tester.Type(string(char))
	}
	stop()
	want := []string{"。", "，", "！", "？", "¥"}
	assertPriorBugQueueSent(t, queue, want)
	if _, err := client.Input("<Esc>"); err != nil {
		t.Fatalf("leave insert mode: %v", err)
	}
	if err := client.FeedKeys("", "x", false); err != nil {
		t.Fatalf("drain Neovim typeahead: %v", err)
	}
	assertPriorBugBufferLines(t, client, []string{punctuation})
}

func TestPriorBug_Neovide2723_NormalColonAndSlashModeTransitions(t *testing.T) {
	sender := &priorBugSender{}
	queue, stop := startPriorBugQueue(t, sender)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")
	tester := priorBugTester(t, handler)
	tester.Compose("stale", 5)
	handler.SetMode("normal")
	if handler.IMEAllowed() {
		t.Fatal("normal mode IMEAllowed = true, want false")
	}
	if got := handler.State().Preedit; got != "" {
		t.Fatalf("normal mode preedit = %q, want empty", got)
	}
	tester.Frame()
	active, noIME := tester.TextInputActive()
	if !active || !noIME {
		t.Fatalf("normal mode TextInputActive = (%v, %v), want (true, true)", active, noIME)
	}

	for _, command := range []string{":", "/"} {
		tester.Type(command)
		handler.SetMode("cmdline_normal")
		if !handler.IMEAllowed() {
			t.Fatalf("cmdline after %q IMEAllowed = false, want true", command)
		}
		if got := handler.State().Preedit; got != "" {
			t.Fatalf("cmdline after %q preedit = %q, want empty", command, got)
		}
		handler.SetMode("normal")
		if handler.IMEAllowed() {
			t.Fatalf("normal after %q IMEAllowed = true, want false", command)
		}
	}
	tester.Type("j")
	tester.Type("k")
	stop()
	assertPriorBugSent(t, sender, []string{":", "/", "j", "k"})
}

func TestPriorBug_Neovide2813_RapidComposeEditCancelStressNoPanicOrLeak(t *testing.T) {
	sender := &priorBugSender{}
	queue, stop := startPriorBugQueue(t, sender)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")
	tester := priorBugTester(t, handler)

	readerDone := make(chan struct{})
	var readers sync.WaitGroup
	for range 3 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-readerDone:
					return
				default:
					_ = handler.State()
					_ = handler.Preedit()
					_ = handler.IMEAllowed()
					_ = handler.Mode()
				}
			}
		}()
	}

	const rounds = 64
	want := make([]string, 0, rounds)
	for round := range rounds {
		tester.Compose(fmt.Sprintf("compose-%d", round), 1)
		tester.Key(0, ui.KeyBackspace)
		tester.Key(0, ui.KeyLeft)
		tester.Key(0, ui.KeyRight)
		switch round % 4 {
		case 0:
			tester.Key(0, ui.KeyEscape)
			handler.SetMode("normal")
			handler.SetMode("insert")
			want = append(want, "<Esc>")
		case 1:
			tester.Compose("", 0)
			tester.Type("汉")
			want = append(want, "汉")
		case 2:
			tester.Compose("変換", 2)
			tester.Key(0, ui.KeyEnter)
			tester.Compose("", 0)
			tester.Type("字")
			want = append(want, "字")
		case 3:
			handler.SetMode("normal")
			handler.SetMode("insert")
			tester.Type("本")
			want = append(want, "本")
		}
	}
	close(readerDone)
	readers.Wait()
	if got := handler.State().Preedit; got != "" {
		t.Fatalf("residual preedit = %q, want empty", got)
	}
	stop()
	assertPriorBugSent(t, sender, want)
}

func TestPriorBug_VimR1099_JapaneseReturnConfirmDoesNotInsertNewline(t *testing.T) {
	client := startPriorBugNvim(t)
	queue, stop := startPriorBugQueue(t, client)
	defer stop()
	handler := input.NewHandler(queue)
	handler.SetMode("insert")
	tester := priorBugTester(t, handler)
	if _, err := client.Input("i"); err != nil {
		t.Fatalf("enter insert mode: %v", err)
	}

	tester.Compose("nihongo", 7)
	tester.Key(0, ui.KeySpace)
	tester.Key(0, ui.KeyTab)
	tester.Key(0, ui.KeyBackspace)
	tester.Key(0, ui.KeyLeft)
	tester.Key(0, ui.KeyRight)
	tester.Compose("日本語", 3)
	tester.Key(0, ui.KeyEnter)
	tester.Compose("", 0)
	tester.Type("日本語")
	if got := handler.State().Preedit; got != "" {
		t.Fatalf("residual preedit = %q, want empty", got)
	}
	stop()
	assertPriorBugQueueSent(t, queue, []string{"日本語"})
	if _, err := client.Input("<Esc>"); err != nil {
		t.Fatalf("leave insert mode: %v", err)
	}
	if err := client.FeedKeys("", "x", false); err != nil {
		t.Fatalf("drain Neovim typeahead: %v", err)
	}
	assertPriorBugBufferLines(t, client, []string{"日本語"})
}
