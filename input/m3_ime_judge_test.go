package input_test

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/input"
	"nvgui/screen"
)

type m3FakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (s *m3FakeSender) Input(keys string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, keys)
	return len(keys), nil
}

func (s *m3FakeSender) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func m3StartQueue(t *testing.T, sender input.Sender, rec *input.TraceRecorder) (*input.Queue, func()) {
	t.Helper()
	q := input.NewQueue(sender, rec)
	if q == nil {
		t.Fatal("NewQueue returned nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- q.Run(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = q.Close()
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("Queue.Run did not exit after Close/cancel")
			}
		})
	}
	return q, stop
}

func assertBoolPtr(t *testing.T, name string, got *bool, want bool) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func TestM3SquirrelParseAppOptionsAndBundleCheck(t *testing.T) {
	topLevelYAML := []byte(`
# Squirrel config comment
style:
  color_scheme: native

app_options:
  com.apple.Terminal:
    ascii_mode: true # inline comment
    no_inline: false
  "com.example.nvgui":
    ascii_mode: true
    vim_mode: true
    no_inline: false
  'com.googlecode.iterm2':
    ascii_mode: false
`)
	opts, err := input.ParseSquirrelAppOptions(topLevelYAML)
	if err != nil {
		t.Fatalf("ParseSquirrelAppOptions(top-level): %v", err)
	}
	if len(opts) != 3 {
		t.Fatalf("ParseSquirrelAppOptions(top-level) len = %d, want 3 (%+v)", len(opts), opts)
	}
	nvOpt, ok := opts["com.example.nvgui"]
	if !ok {
		t.Fatalf("missing com.example.nvgui in %+v", opts)
	}
	if nvOpt.BundleID != "com.example.nvgui" {
		t.Errorf("nvOpt.BundleID = %q, want com.example.nvgui", nvOpt.BundleID)
	}
	assertBoolPtr(t, "nvgui.ASCIIMode", nvOpt.ASCIIMode, true)
	assertBoolPtr(t, "nvgui.VIMMode", nvOpt.VIMMode, true)
	assertBoolPtr(t, "nvgui.NoInline", nvOpt.NoInline, false)

	termOpt, ok := opts["com.apple.Terminal"]
	if !ok {
		t.Fatalf("missing com.apple.Terminal in %+v", opts)
	}
	assertBoolPtr(t, "Terminal.ASCIIMode", termOpt.ASCIIMode, true)
	assertBoolPtr(t, "Terminal.NoInline", termOpt.NoInline, false)
	if termOpt.VIMMode != nil {
		t.Errorf("Terminal.VIMMode = %v, want nil when omitted", *termOpt.VIMMode)
	}

	patchYAML := []byte(`
# squirrel.custom.yaml
patch:
  app_options:
    com.example.nvgui:
      ascii_mode: true
      vim_mode: true
  app_options/org.vim.MacVim:
    ascii_mode: true
    no_inline: true
`)
	patchOpts, err := input.ParseSquirrelAppOptions(patchYAML)
	if err != nil {
		t.Fatalf("ParseSquirrelAppOptions(patch): %v", err)
	}
	if len(patchOpts) != 2 {
		t.Fatalf("ParseSquirrelAppOptions(patch) len = %d, want 2 (%+v)", len(patchOpts), patchOpts)
	}
	assertBoolPtr(t, "patch.nvgui.VIMMode", patchOpts["com.example.nvgui"].VIMMode, true)
	assertBoolPtr(t, "patch.MacVim.ASCIIMode", patchOpts["org.vim.MacVim"].ASCIIMode, true)
	assertBoolPtr(t, "patch.MacVim.NoInline", patchOpts["org.vim.MacVim"].NoInline, true)

	// Empty input and config without app_options must return empty map and nil error.
	for name, data := range map[string][]byte{
		"empty":          []byte(""),
		"comments_only":  []byte("# just a comment\n\n"),
		"no_app_options": []byte("style:\n  color_scheme: native\n"),
	} {
		got, err := input.ParseSquirrelAppOptions(data)
		if err != nil {
			t.Errorf("ParseSquirrelAppOptions(%s) error = %v, want nil", name, err)
		}
		if len(got) != 0 {
			t.Errorf("ParseSquirrelAppOptions(%s) len = %d, want 0", name, len(got))
		}
	}

	// Malformed inputs must return an explicit error.
	malformed := map[string][]byte{
		"non_bool_ascii_mode": []byte("app_options:\n  com.example.nvgui:\n    ascii_mode: maybe\n"),
		"tab_indentation":     []byte("app_options:\n\tcom.example.nvgui:\n\t\tascii_mode: true\n"),
	}
	for name, data := range malformed {
		if _, err := input.ParseSquirrelAppOptions(data); err == nil {
			t.Errorf("ParseSquirrelAppOptions(%s) returned nil error, want error", name)
		}
	}

	// CheckSquirrelBundle: matched .app bundle.
	stMatched := input.CheckSquirrelBundle("com.example.nvgui", "/Applications/nvgui.app/Contents/MacOS/nvgui", opts)
	if !stMatched.Matched || stMatched.CLIMismatch {
		t.Fatalf("CheckSquirrelBundle(matched .app) = %+v, want Matched=true CLIMismatch=false", stMatched)
	}
	if stMatched.BundleID != "com.example.nvgui" || stMatched.Option.BundleID != "com.example.nvgui" {
		t.Fatalf("CheckSquirrelBundle(matched .app) option = %+v", stMatched)
	}
	assertBoolPtr(t, "matched.Option.ASCIIMode", stMatched.Option.ASCIIMode, true)

	// CheckSquirrelBundle: CLI launch with empty bundle ID -> CLIMismatch=true and non-empty warning.
	stEmptyCLI := input.CheckSquirrelBundle("", "/usr/local/bin/nvgui", opts)
	if stEmptyCLI.Matched || !stEmptyCLI.CLIMismatch || strings.TrimSpace(stEmptyCLI.Warning) == "" {
		t.Fatalf("CheckSquirrelBundle(empty bundleID CLI) = %+v, want Matched=false CLIMismatch=true non-empty Warning", stEmptyCLI)
	}

	// CheckSquirrelBundle: unmatched CLI path -> CLIMismatch=true and non-empty warning.
	stUnmatchedCLI := input.CheckSquirrelBundle("com.example.other", "/usr/local/bin/nvgui", opts)
	if stUnmatchedCLI.Matched || !stUnmatchedCLI.CLIMismatch || strings.TrimSpace(stUnmatchedCLI.Warning) == "" {
		t.Fatalf("CheckSquirrelBundle(unmatched CLI) = %+v, want Matched=false CLIMismatch=true non-empty Warning", stUnmatchedCLI)
	}

	// CheckSquirrelBundle: unmatched .app launch -> Matched=false, CLIMismatch=false.
	stUnmatchedApp := input.CheckSquirrelBundle("com.example.other", "/Applications/nvgui.app/Contents/MacOS/nvgui", opts)
	if stUnmatchedApp.Matched || stUnmatchedApp.CLIMismatch {
		t.Fatalf("CheckSquirrelBundle(unmatched .app) = %+v, want Matched=false CLIMismatch=false", stUnmatchedApp)
	}
}

func TestM3IMEJapaneseRomajiReturnConfirmNoCR(t *testing.T) {
	sender := &m3FakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := m3StartQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	h.SetMode("insert")
	now := time.Unix(1_700_000_000, 0).UTC()

	// Romaji composition: editing/navigation/space/return keys during composition must NOT leak to Sender (vimr#1099).
	seq := []ui.InputEvent{
		{Kind: ui.InputCompose, Text: "nihongo", Caret: 7, Time: now},
		{Kind: ui.InputKeyDown, Key: ui.KeySpace, Time: now.Add(time.Millisecond)},
		{Kind: ui.InputKeyUp, Key: ui.KeySpace, Time: now.Add(2 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyTab, Time: now.Add(3 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyBackspace, Time: now.Add(4 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyLeft, Time: now.Add(5 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyRight, Time: now.Add(6 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyUp, Time: now.Add(7 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyDown, Time: now.Add(8 * time.Millisecond)},
		{Kind: ui.InputCompose, Text: "日本語", Caret: 3, Time: now.Add(9 * time.Millisecond)},
		{Kind: ui.InputKeyDown, Key: ui.KeyEnter, Time: now.Add(10 * time.Millisecond)},
		{Kind: ui.InputKeyUp, Key: ui.KeyEnter, Time: now.Add(11 * time.Millisecond)},
		{Kind: ui.InputCompose, Text: "", Caret: 0, Time: now.Add(12 * time.Millisecond)},
		{Kind: ui.InputText, Text: "日本語", Time: now.Add(13 * time.Millisecond)},
	}
	for _, ev := range seq {
		h.Handle(ev)
	}
	if got := h.Preedit(); got != "" {
		t.Fatalf("Preedit after Japanese commit = %q, want empty", got)
	}

	stop()
	got := sender.snapshot()
	if len(got) != 1 || got[0] != "日本語" {
		t.Fatalf("Japanese Return confirm sent = %v, want [\"日本語\"] with zero <CR> leak", got)
	}
}

func TestM3IMERapidComposeEditCancelStress(t *testing.T) {
	sender := &m3FakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := m3StartQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	h.SetMode("insert")

	// Concurrent state readers under -race (neovide#2813).
	readerDone := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-readerDone:
				return
			default:
				_ = h.State()
				_ = h.Preedit()
				_ = h.IMEAllowed()
				_ = h.Mode()
			}
		}
	}()

	const rounds = 40
	now := time.Unix(1_700_000_000, 0).UTC()
	var expected strings.Builder
	for i := 0; i < rounds; i++ {
		base := now.Add(time.Duration(i*50) * time.Millisecond)
		h.SetMode("insert")
		h.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "pinyin", Caret: 6, Time: base})
		h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyBackspace, Time: base.Add(time.Millisecond)})
		h.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "pinyi", Caret: 5, Time: base.Add(2 * time.Millisecond)})
		h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyLeft, Time: base.Add(3 * time.Millisecond)})

		switch i % 3 {
		case 0:
			// Cancel via Escape and switch back to normal mode.
			h.Handle(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEscape, Time: base.Add(4 * time.Millisecond)})
			h.SetMode("normal")
			expected.WriteString("<Esc>")
		case 1:
			// Cancel via Canceled compose event, then commit a single token in insert mode.
			h.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "", Canceled: true, Time: base.Add(4 * time.Millisecond)})
			h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "汉", Time: base.Add(5 * time.Millisecond)})
			expected.WriteString("汉")
		case 2:
			// Direct commit clearing preedit.
			h.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "", Caret: 0, Time: base.Add(4 * time.Millisecond)})
			h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "字", Time: base.Add(5 * time.Millisecond)})
			expected.WriteString("字")
		}
	}

	close(readerDone)
	readerWG.Wait()
	stop()

	if got := h.Preedit(); got != "" {
		t.Fatalf("residual Preedit after stress = %q, want empty", got)
	}
	joined := strings.Join(sender.snapshot(), "")
	if strings.Contains(joined, "pinyin") || strings.Contains(joined, "pinyi") {
		t.Fatalf("preedit leaked into sender during stress: %q", joined)
	}
	if joined != expected.String() {
		t.Fatalf("stress sent = %q, want %q", joined, expected.String())
	}
}

func TestM3IMEPunctuationModeTransitionsAndCaretPlacements(t *testing.T) {
	sender := &m3FakeSender{}
	rec := input.NewTraceRecorder()
	q, stop := m3StartQueue(t, sender, rec)
	defer stop()

	h := input.NewHandler(q)
	now := time.Unix(1_700_000_000, 0).UTC()

	// 1. Chinese full-width punctuation verbatim in insert mode (neovide#2567).
	h.SetMode("insert")
	if !h.IMEAllowed() {
		t.Fatal("insert mode IMEAllowed = false, want true")
	}
	for i, ch := range []string{"。", "，", "！", "？", "¥"} {
		h.Handle(ui.InputEvent{Kind: ui.InputText, Text: ch, Time: now.Add(time.Duration(i) * time.Millisecond)})
	}

	// 2. Normal mode ':' and '/' transitions with preedit cleanup (neovide#2723).
	h.Handle(ui.InputEvent{Kind: ui.InputCompose, Text: "stale", Caret: 5, Time: now.Add(20 * time.Millisecond)})
	h.SetMode("normal")
	if h.IMEAllowed() {
		t.Fatal("normal mode IMEAllowed = true, want false")
	}
	if h.Preedit() != "" {
		t.Fatalf("Preedit after SetMode(normal) = %q, want empty", h.Preedit())
	}
	h.Handle(ui.InputEvent{Kind: ui.InputText, Text: ":", Time: now.Add(21 * time.Millisecond)})
	h.SetMode("cmdline_normal")
	if !h.IMEAllowed() {
		t.Fatal("cmdline_normal IMEAllowed = false, want true")
	}
	h.SetMode("normal")
	h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "/", Time: now.Add(22 * time.Millisecond)})
	h.SetMode("cmdline_normal")
	h.SetMode("normal")
	if h.IMEAllowed() {
		t.Fatal("normal mode after cmdline IMEAllowed = true, want false")
	}
	h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "j", Time: now.Add(23 * time.Millisecond)})
	h.Handle(ui.InputEvent{Kind: ui.InputText, Text: "k", Time: now.Add(24 * time.Millisecond)})

	stop()
	if got, want := strings.Join(sender.snapshot(), ""), "。，！？¥:/jk"; got != want {
		t.Fatalf("punctuation + mode transitions sent = %q, want %q", got, want)
	}

	// 3. CaretFor across split (PlacedWin), float (PlacedFloat), msg_set_pos, and external (neovide#1721).
	cell := input.CellSize{Width: 10, Height: 20}
	checkCaret := func(name string, st *screen.State, wantX, wantY, wantW, wantH float32) {
		t.Helper()
		got := h.CaretFor(st, cell)
		if math.Abs(float64(got.X-wantX)) > 0.5 || math.Abs(float64(got.Y-wantY)) > 0.5 ||
			math.Abs(float64(got.W-wantW)) > 0.5 || math.Abs(float64(got.H-wantH)) > 0.5 {
			t.Errorf("CaretFor(%s) = %+v, want {X:%v Y:%v W:%v H:%v}", name, got, wantX, wantY, wantW, wantH)
		}
	}

	checkCaret("split", &screen.State{
		Placements: map[int]screen.Placement{
			2: {Kind: screen.PlacedWin, Row: 5, Col: 40, Width: 40, Height: 18},
		},
		Cursor: screen.Cursor{Grid: 2, Row: 3, Col: 7},
	}, 470, 160, 10, 20)

	checkCaret("float", &screen.State{
		Placements: map[int]screen.Placement{
			3: {Kind: screen.PlacedFloat, ScreenRow: 4, ScreenCol: 12, Width: 20, Height: 8, ZIndex: 50},
		},
		Cursor: screen.Cursor{Grid: 3, Row: 2, Col: 6},
	}, 180, 120, 10, 20)

	checkCaret("msg_set_pos", &screen.State{
		Placements: map[int]screen.Placement{},
		Msg:        &screen.MsgPlacement{Grid: 4, Row: 22},
		Cursor:     screen.Cursor{Grid: 4, Row: 1, Col: 4},
	}, 40, 460, 10, 20)

	checkCaret("external", &screen.State{
		Placements: map[int]screen.Placement{
			5: {Kind: screen.PlacedExternal},
		},
		Cursor: screen.Cursor{Grid: 5, Row: 1, Col: 1},
	}, 0, 0, 0, 0)
}
