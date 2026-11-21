package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/diag"
	"nvgui/input"
	"nvgui/renderlist"
)

type testClipboard struct {
	mu   sync.Mutex
	text string
}

func (clipboard *testClipboard) ReadText() string {
	clipboard.mu.Lock()
	defer clipboard.mu.Unlock()
	return clipboard.text
}

func (clipboard *testClipboard) WriteText(value string) {
	clipboard.mu.Lock()
	defer clipboard.mu.Unlock()
	clipboard.text = value
}

func withoutNVGUIHeadless(env []string) []string {
	result := make([]string, 0, len(env)+2)
	for _, entry := range env {
		if strings.HasPrefix(entry, "NVGUI_HEADLESS=") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func startTestShell(test *testing.T, clipboard Clipboard) *Shell {
	test.Helper()
	req := LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        withoutNVGUIHeadless(os.Environ()),
		WorkingDir: test.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	shell, err := Start(context.Background(), req, nil, clipboard)
	if err != nil {
		test.Fatalf("Start(headless): %v", err)
	}
	if shell == nil || shell.impl == nil {
		test.Fatal("Start returned nil shell")
	}
	test.Cleanup(func() { _ = shell.Close() })
	return shell
}

func waitUntil(test *testing.T, timeout time.Duration, what string, ready func() bool) {
	test.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-deadline.C:
			test.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

func mustNotPanic(test *testing.T, what string, action func()) {
	test.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			test.Errorf("%s panicked: %v", what, recovered)
		}
	}()
	action()
}

func evalString(test *testing.T, shell *Shell, expression string) string {
	test.Helper()
	var value string
	if err := shell.impl.session.Nvim().Eval(expression, &value); err != nil {
		test.Fatalf("Eval(%s): %v", expression, err)
	}
	return value
}

func bufferText(test *testing.T, shell *Shell) string {
	test.Helper()
	var lines []string
	if err := shell.impl.session.Nvim().Eval("getline(1, '$')", &lines); err != nil {
		test.Fatalf("read buffer: %v", err)
	}
	return strings.Join(lines, "\n")
}

func setBufferLine(test *testing.T, shell *Shell, line string) {
	test.Helper()
	commands := []string{"%delete _", "call setline(1, '" + line + "')", "call cursor(1, 1)"}
	for _, command := range commands {
		if err := shell.impl.session.Nvim().Command(command); err != nil {
			test.Fatalf("Command(%s): %v", command, err)
		}
	}
}

func setRegister(test *testing.T, shell *Shell, value string) {
	test.Helper()
	if err := shell.impl.session.Nvim().Command("let @\" = '" + value + "'"); err != nil {
		test.Fatalf("set unnamed register: %v", err)
	}
}

func queueHas(shell *Shell, want string) bool {
	for _, sent := range shell.impl.queue.Sent() {
		if sent == want {
			return true
		}
	}
	return false
}

func modeIsVisual(mode string) bool {
	normalized := strings.ToLower(mode)
	return normalized == "v" || normalized == "\x16" || strings.HasPrefix(normalized, "visual")
}

func TestParseLaunchOptionsAndEnvironment(test *testing.T) {
	env := []string{"PATH=/tmp", "NVGUI_HEADLESS=0", "NVGUI_HEADLESS=1"}
	req, err := ParseLaunch([]string{"--new-window", "--headless", "notes.md"}, env, test.TempDir())
	if err != nil {
		test.Fatalf("ParseLaunch: %v", err)
	}
	if !req.Headless {
		test.Error("--headless did not set Headless=true")
	}
	if strings.Join(req.Args, "\x00") != "notes.md" {
		test.Errorf("Args = %v, want [notes.md]", req.Args)
	}
	if req.Width != 80 || req.Height != 24 {
		test.Errorf("default size = %dx%d, want 80x24", req.Width, req.Height)
	}

	req, err = ParseLaunch([]string{"--", "--headless", "--new-window", "notes.md"}, []string{"PATH=/tmp"}, test.TempDir())
	if err != nil {
		test.Fatalf("ParseLaunch(--): %v", err)
	}
	wantArgs := []string{"--", "--headless", "--new-window", "notes.md"}
	if strings.Join(req.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		test.Errorf("Args = %v, want %v", req.Args, wantArgs)
	}
	if req.Headless {
		test.Error("--headless after -- was parsed as an nvgui option")
	}

	req, err = ParseLaunch([]string{"--new-window"}, []string{"NVGUI_HEADLESS=0", "NVGUI_HEADLESS=1"}, test.TempDir())
	if err != nil {
		test.Fatalf("ParseLaunch(env): %v", err)
	}
	if !req.Headless {
		test.Error("NVGUI_HEADLESS last-wins did not set Headless=true")
	}
	if got := envValue([]string{"PATH=first", "PATH=second"}, "PATH"); got != "second" {
		test.Errorf("envValue(PATH) = %q, want last value", got)
	}
}

func TestDoctorUsesRequestPath(test *testing.T) {
	root := test.TempDir()
	good := filepath.Join(root, "good")
	bad := filepath.Join(root, "bad")
	if err := os.MkdirAll(good, 0o755); err != nil {
		test.Fatal(err)
	}
	if err := os.MkdirAll(bad, 0o755); err != nil {
		test.Fatal(err)
	}
	script := filepath.Join(good, "nvim")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "nvim"), []byte("not executable\n"), 0o644); err != nil {
		test.Fatal(err)
	}
	req := LaunchRequest{Env: []string{"PATH=" + bad + ":" + good}, WorkingDir: root}
	doctor := req.Doctor()
	if doctor["PATH"] != bad+":"+good {
		test.Errorf("Doctor()[PATH] = %q", doctor["PATH"])
	}
	if doctor["nvim"] != script {
		test.Fatalf("Doctor()[nvim] = %q, want %q", doctor["nvim"], script)
	}

	req = LaunchRequest{Env: []string{"PATH=" + bad}, WorkingDir: root}
	if got := req.Doctor()["nvim"]; got != "" {
		test.Errorf("Doctor() with defined PATH fell back to host nvim %q", got)
	}
	req = LaunchRequest{Env: []string{"PATH="}, WorkingDir: root}
	if got := req.Doctor()["nvim"]; got != "" {
		test.Errorf("Doctor() with empty PATH fell back to host nvim %q", got)
	}
	req = LaunchRequest{Env: []string{"HOME=/tmp"}, WorkingDir: root}
	want, err := exec.LookPath("nvim")
	if err != nil {
		want = ""
	}
	if got := req.Doctor()["nvim"]; got != want {
		test.Errorf("Doctor() without PATH = %q, want LookPath %q", got, want)
	}
}

func TestNewShellUnstartedIsSafe(test *testing.T) {
	shell := NewShell(LaunchRequest{}, nil, nil)
	mustNotPanic(test, "View(nil)", func() { shell.View(nil) })
	mustNotPanic(test, "AttachInput(nil)", func() { shell.AttachInput(nil) })
	mustNotPanic(test, "Tester", func() {
		tester := ui.NewTester(shell.View, 200, 100)
		tester.Frame()
		tester.Key(0, ui.KeyA)
	})
	if err := shell.Resize(800, 400); err == nil {
		test.Error("Resize on unstarted Shell returned nil error")
	}
	if shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "paste"}) {
		test.Error("HandleEditCommand on unstarted Shell returned true")
	}
	if err := shell.ExportDiag(test.TempDir()); err == nil {
		test.Error("ExportDiag on unstarted Shell returned nil error")
	}
	if shell.SessionExited() != nil {
		test.Error("SessionExited on unstarted Shell returned non-nil channel")
	}
	if shell.Window() != nil {
		test.Error("Window on unstarted Shell returned non-nil window")
	}
	if err := shell.Close(); err != nil {
		test.Errorf("Close on unstarted Shell: %v", err)
	}
	if err := shell.Resize(800, 400); err == nil {
		test.Error("Resize after Close returned nil error")
	}
}

func TestAttachInputKeepsFocusPreeditAndCaret(test *testing.T) {
	clipboard := &testClipboard{}
	shell := startTestShell(test, clipboard)
	tester := ui.NewTester(shell.View, 800, 480)
	waitUntil(test, 5*time.Second, "initial frame", func() bool {
		tester.Frame()
		return shell.impl.session.Latest() != nil && shell.impl.view.Caret().W > 0
	})
	if !tester.Focused("nvgui-input") {
		test.Error("input box is not focused after the first frame")
	}
	tester.ClickAt(400, 240)
	tester.Frame()
	if !tester.Focused("nvgui-input") {
		test.Error("clicking the window lost input focus")
	}
	active, noInputMethod := tester.TextInputActive()
	if !active || !noInputMethod {
		test.Errorf("normal mode text input = (%v, %v), want (true, true)", active, noInputMethod)
	}
	if rect, active := tester.TextCaret(); rect.W <= 0 || rect.H <= 0 || active {
		test.Errorf("normal mode TextCaret = (%+v, %v), want positive rect and IME inactive", rect, active)
	}

	state := shell.impl.session.Latest()
	cell := shell.impl.resolver.Cell()
	want := shell.impl.handler.CaretFor(state, input.CellSize{Width: cell.Width, Height: cell.Height})
	shell.impl.view.SetFrame(renderlist.Frame{})
	if got := shell.impl.caret(); got != want {
		test.Errorf("caret() zero-frame fallback = %+v, want %+v", got, want)
	}

	tester.Type("i")
	waitUntil(test, 5*time.Second, "insert mode and IME", func() bool {
		tester.Frame()
		active, noInputMethod := tester.TextInputActive()
		return active && !noInputMethod && shell.impl.handler.IMEAllowed()
	})
	lastShapes := shell.impl.resolver.ShapeCalls()
	stable := 0
	waitUntil(test, 5*time.Second, "shape cache to settle", func() bool {
		tester.Frame()
		shapes := shell.impl.resolver.ShapeCalls()
		if shapes == lastShapes {
			stable++
		} else {
			stable = 0
			lastShapes = shapes
		}
		return stable >= 3
	})
	tester.Compose("中", 1)
	tester.Frame()
	if got := shell.impl.handler.Preedit(); got != "中" {
		test.Fatalf("Preedit() = %q, want 中", got)
	}
	if rect, active := tester.TextCaret(); !active || rect.W <= 0 || rect.H <= 0 {
		test.Errorf("insert mode TextCaret = (%+v, %v), want positive rect and IME active", rect, active)
	}
	if shell.impl.resolver.ShapeCalls() <= lastShapes {
		test.Error("preedit frame did not shape preedit glyphs")
	}
}

func TestRefreshFrameUsesSeqAndCachesServices(test *testing.T) {
	shell := startTestShell(test, &testClipboard{})
	tester := ui.NewTester(shell.View, 800, 480)
	waitUntil(test, 5*time.Second, "initial frame", func() bool {
		tester.Frame()
		return shell.impl.session.Latest() != nil && shell.impl.lastSeq != 0
	})
	state := shell.impl.session.Latest()
	beforeShapes := shell.impl.resolver.ShapeCalls()
	shell.impl.refreshFrame()
	shell.impl.refreshFrame()
	if after := shell.impl.resolver.ShapeCalls(); after != beforeShapes {
		test.Errorf("refreshFrame rebuilt stable Seq: shape calls %d -> %d", beforeShapes, after)
	}
	shell.impl.mu.Lock()
	shell.impl.lastSeq = 0
	shell.impl.mu.Unlock()
	shell.impl.refreshFrame()
	if shell.impl.lastSeq != state.Seq {
		test.Errorf("refreshFrame lastSeq = %d, want %d", shell.impl.lastSeq, state.Seq)
	}
	if shell.impl.handler.Mode() != state.Mode {
		test.Errorf("refreshFrame mode = %q, want %q", shell.impl.handler.Mode(), state.Mode)
	}
	shell.View(nil)
	shell.impl.mu.Lock()
	services := shell.impl.services
	shell.impl.mu.Unlock()
	if services.ReadClipboard() != "" {
		test.Error("View(nil) overwrote cached Services")
	}
}

func TestHandleEditCommandPasteAndShortcuts(test *testing.T) {
	shell := startTestShell(test, nil)
	tester := ui.NewTester(shell.View, 800, 480)
	tester.Frame()
	clipboardText := "a<\\b\r\nc"
	tester.SetClipboard(clipboardText)
	tester.Type("i")
	waitUntil(test, 5*time.Second, "insert mode", func() bool {
		tester.Frame()
		return evalString(test, shell, "mode()") == "i" && shell.impl.handler.IMEAllowed()
	})
	tester.Command("paste")
	normalized := "a<\\b\nc"
	wantSent := input.EscapeText(normalized)
	waitUntil(test, 5*time.Second, "paste through input queue", func() bool {
		tester.Frame()
		return queueHas(shell, wantSent)
	})
	if got := tester.Clipboard(); got != clipboardText {
		test.Fatalf("clipboard == nil read from wrong source: %q", got)
	}
	waitUntil(test, 5*time.Second, "pasted buffer", func() bool {
		tester.Frame()
		return bufferText(test, shell) == normalized
	})

	if runtime.GOOS == "darwin" {
		strictShell := startTestShell(test, nil)
		var context *ui.Context
		strictTester := ui.NewTester(func(c *ui.Context) {
			context = c
			strictShell.View(c)
		}, 800, 480)
		strictTester.Frame()
		if context == nil {
			test.Fatal("tester did not provide a UI context")
		}
		strictTester.SetClipboard("sentinel")
		modifiers := []ui.Modifiers{
			ui.Cmd | ui.Ctrl,
			ui.Cmd | ui.Shift,
			ui.Cmd | ui.Alt,
			ui.Cmd | ui.Shift | ui.Alt | ui.Ctrl,
		}
		for _, mods := range modifiers {
			event := ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyC, Mods: mods}
			if strictShell.HandleEditCommand(context, event) {
				test.Errorf("Cmd+extra modifier %v triggered edit command", mods)
			}
			if strictTester.Clipboard() != "sentinel" {
				test.Errorf("Cmd+extra modifier %v changed clipboard", mods)
			}
		}
	}
}

func TestHandleEditCommandCutModes(test *testing.T) {
	test.Run("normal", func(test *testing.T) {
		clipboard := &testClipboard{}
		shell := startTestShell(test, clipboard)
		setBufferLine(test, shell, "payload")
		setRegister(test, shell, "copied")
		tester := ui.NewTester(shell.View, 800, 480)
		tester.Frame()
		if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "copy"}) {
			test.Fatal("copy command returned false")
		}
		if clipboard.ReadText() != "copied" {
			test.Fatalf("copy clipboard = %q, want copied", clipboard.ReadText())
		}
		shell.impl.handler.SetMode("normal")
		if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "cut"}) {
			test.Fatal("normal cut returned false")
		}
		if clipboard.ReadText() != "copied" {
			test.Errorf("normal cut clipboard = %q, want copied", clipboard.ReadText())
		}
		waitUntil(test, 5*time.Second, "normal cut deletion", func() bool {
			return bufferText(test, shell) == "ayload"
		})
		if strings.Contains(bufferText(test, shell), "\"+x") {
			test.Error("normal cut sent literal \"+x to buffer")
		}
	})

	test.Run("visual", func(test *testing.T) {
		clipboard := &testClipboard{}
		shell := startTestShell(test, clipboard)
		setBufferLine(test, shell, "payload")
		tester := ui.NewTester(shell.View, 800, 480)
		tester.Frame()
		if _, err := shell.impl.session.Nvim().Input("v"); err != nil {
			test.Fatalf("enter visual mode: %v", err)
		}
		waitUntil(test, 5*time.Second, "visual mode", func() bool {
			tester.Frame()
			return modeIsVisual(evalString(test, shell, "mode()"))
		})
		shell.impl.handler.SetMode("visual")
		if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "cut"}) {
			test.Fatal("visual cut returned false")
		}
		if clipboard.ReadText() != "p" {
			test.Errorf("visual cut clipboard = %q, want p", clipboard.ReadText())
		}
		waitUntil(test, 5*time.Second, "visual cut deletion", func() bool {
			return bufferText(test, shell) == "ayload"
		})
	})

	test.Run("insert", func(test *testing.T) {
		clipboard := &testClipboard{}
		shell := startTestShell(test, clipboard)
		setBufferLine(test, shell, "payload")
		setRegister(test, shell, "copied")
		tester := ui.NewTester(shell.View, 800, 480)
		tester.Frame()
		if _, err := shell.impl.session.Nvim().Input("i"); err != nil {
			test.Fatalf("enter insert mode: %v", err)
		}
		waitUntil(test, 5*time.Second, "insert mode", func() bool {
			tester.Frame()
			return evalString(test, shell, "mode()") == "i"
		})
		shell.impl.handler.SetMode("insert")
		if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "cut"}) {
			test.Fatal("insert cut returned false")
		}
		if clipboard.ReadText() != "copied" {
			test.Errorf("insert cut clipboard = %q, want copied", clipboard.ReadText())
		}
		if got := bufferText(test, shell); got != "payload" {
			test.Errorf("insert cut buffer = %q, want payload", got)
		}
	})
}

func TestResizeClampAndClose(test *testing.T) {
	shell := startTestShell(test, &testClipboard{})
	if err := shell.Resize(0, 0); err != nil {
		test.Fatalf("Resize(0, 0): %v", err)
	}
	shell.impl.mu.Lock()
	lastColumns, lastRows := shell.impl.lastColumns, shell.impl.lastRows
	shell.impl.mu.Unlock()
	if lastColumns != 1 || lastRows != 1 {
		test.Errorf("Resize(0, 0) sent %dx%d, want 1x1", lastColumns, lastRows)
	}
	var columns int
	if err := shell.impl.session.Nvim().Eval("&columns", &columns); err != nil {
		test.Fatalf("Eval(&columns): %v", err)
	}
	if columns < 1 {
		test.Errorf("&columns after Resize(0, 0) = %d, want >= 1", columns)
	}
	var lines int
	if err := shell.impl.session.Nvim().Eval("&lines", &lines); err != nil {
		test.Fatalf("Eval(&lines): %v", err)
	}
	if lines < 1 {
		test.Errorf("&lines after Resize(0, 0) = %d, want >= 1", lines)
	}
	if err := shell.Close(); err != nil {
		test.Fatalf("Close: %v", err)
	}
	if err := shell.Resize(800, 400); err == nil {
		test.Error("Resize after Close returned nil error")
	}
}

func TestConcurrentCloseIdempotent(test *testing.T) {
	shell := startTestShell(test, &testClipboard{})
	exited := shell.SessionExited()
	const closers = 8
	var wait sync.WaitGroup
	errors := make(chan error, closers)
	wait.Add(closers)
	for range closers {
		go func() {
			defer wait.Done()
			errors <- shell.Close()
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			test.Errorf("concurrent Close: %v", err)
		}
	}
	select {
	case <-exited:
	default:
		test.Error("SessionExited did not close after concurrent Close")
	}
	select {
	case <-shell.impl.closeDone:
	default:
		test.Error("closeDone did not close")
	}
	shell.impl.mu.Lock()
	closed := shell.impl.closed
	shell.impl.mu.Unlock()
	if !closed {
		test.Error("closed flag not set after concurrent Close")
	}
	if err := shell.Close(); err != nil {
		test.Errorf("Close after concurrent Close: %v", err)
	}
}

func TestCloseDoneWaitsForSessionTeardown(test *testing.T) {
	shell := startTestShell(test, &testClipboard{})
	exited := shell.SessionExited()
	closeReturned := make(chan struct{})
	go func() {
		defer close(closeReturned)
		if err := shell.Close(); err != nil {
			test.Errorf("Close: %v", err)
		}
	}()
	select {
	case <-closeReturned:
	case <-time.After(5 * time.Second):
		test.Fatal("Close did not return")
	}
	select {
	case <-shell.impl.closeDone:
	default:
		test.Error("Close returned before closeDone")
	}
	select {
	case <-exited:
	default:
		test.Error("SessionExited did not close before closeDone")
	}
	wait := make(chan struct{})
	go func() {
		shell.impl.wg.Wait()
		close(wait)
	}()
	select {
	case <-wait:
	case <-time.After(5 * time.Second):
		test.Fatal("Close did not wait for shell goroutines")
	}
}

func TestSyncInputTraceIsOrderedAndDeduplicated(test *testing.T) {
	test.Run("unstarted shell does not advance cursor", func(test *testing.T) {
		recorder := input.NewTraceRecorder()
		recorder.Observe(ui.InputEvent{Kind: ui.InputText, Text: "x"})
		impl := &shellImpl{recorder: recorder}
		impl.syncInputTrace()
		if impl.forwardedInputSeq != 0 {
			test.Fatalf("forwardedInputSeq = %d, want 0", impl.forwardedInputSeq)
		}
	})

	test.Run("real shell exports exact ordered events", func(test *testing.T) {
		const events = 64
		shell := startTestShell(test, &testClipboard{})
		tester := ui.NewTester(shell.View, 800, 480)
		waitUntil(test, 5*time.Second, "initial committed frame", func() bool {
			tester.Frame()
			return shell.impl.session.Latest() != nil
		})
		recorder := shell.impl.recorder
		baseTime := time.Unix(1_700_000_000, 0).UTC()
		for index := 0; index < events; index++ {
			recorder.Observe(ui.InputEvent{
				Kind: ui.InputText,
				Text: "x",
				Time: baseTime.Add(time.Duration(index) * time.Millisecond),
			})
		}

		const workers = 8
		start := make(chan struct{})
		errors := make(chan error, workers)
		var wait sync.WaitGroup
		for worker := 0; worker < workers; worker++ {
			wait.Add(1)
			go func(worker int) {
				defer wait.Done()
				<-start
				for range 20 {
					if worker == 0 {
						dir := filepath.Join(test.TempDir(), "concurrent")
						if err := shell.ExportDiag(dir); err != nil {
							errors <- err
							return
						}
						if _, err := diag.ReadBundle(dir); err != nil {
							errors <- err
							return
						}
						continue
					}
					shell.impl.syncInputTrace()
				}
			}(worker)
		}
		close(start)
		wait.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				test.Fatalf("concurrent ExportDiag: %v", err)
			}
		}

		shell.impl.inputSyncMu.Lock()
		forwarded := shell.impl.forwardedInputSeq
		shell.impl.inputSyncMu.Unlock()
		if forwarded != events {
			test.Fatalf("forwardedInputSeq = %d, want %d", forwarded, events)
		}

		dir := filepath.Join(test.TempDir(), "bundle")
		if err := shell.ExportDiag(dir); err != nil {
			test.Fatalf("ExportDiag: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "input-trace.jsonl"))
		if err != nil {
			test.Fatalf("read input-trace.jsonl: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != events {
			test.Fatalf("input-trace.jsonl lines = %d, want %d", len(lines), events)
		}
		seen := make(map[uint64]bool, events)
		for index, line := range lines {
			var event diag.TraceEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				test.Fatalf("decode input-trace.jsonl line %d: %v", index+1, err)
			}
			if want := uint64(index + 1); event.Seq != want {
				test.Fatalf("input-trace.jsonl line %d Seq = %d, want %d", index+1, event.Seq, want)
			}
			if seen[event.Seq] {
				test.Fatalf("input-trace.jsonl duplicate Seq %d", event.Seq)
			}
			seen[event.Seq] = true
		}
		bundle, err := diag.ReadBundle(dir)
		if err != nil {
			test.Fatalf("diag.ReadBundle: %v", err)
		}
		if len(bundle.InputEvents) != events {
			test.Fatalf("InputEvents len = %d, want %d", len(bundle.InputEvents), events)
		}
		var previous uint64
		for index, event := range bundle.InputEvents {
			if event.Seq != uint64(index+1) || event.Seq <= previous {
				test.Fatalf("InputEvents[%d].Seq = %d, want %d strictly increasing after %d", index, event.Seq, index+1, previous)
			}
			previous = event.Seq
		}
	})
}
