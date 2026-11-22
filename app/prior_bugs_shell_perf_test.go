// Ticket 43: deterministic app-shell and paste-throughput P0 regression set.
// Covered (4 functions / 4 IDs): neovide#1259, neovim#1822, neovide#1175, neovim#19783.
// Explicitly deferred (31 total across this set; registered here instead of wiki/m3-exit.md,
// which is outside this ticket's write scope):
//   - 文字渲染 (6): equalsraf/neovim-qt#166 (M4.4 连字), neovim#32539 (二期变字号 statusline),
//     vhakulinen/gnvim#85 (Linux Pango), neovide#589, neovide#1231, rohit-px2/nvui#87 (L3 人眼 A/B).
//   - macOS 应用壳 (14): neovide#1332 (M2.1), neovide#1586 (M2.2), neovide#2378 (M2),
//     macvim#312, macvim#497 (M2.4), neovim#3344, neovim#11393 (TUI 终端协议不适用),
//     macvim#525, macvim#947, macvim#1119, neovide#459, neovide#1859, neovide#2384, vimr#683
//     (历史平台崩溃无现代复现).
//   - 性能 (1): neovim#6950 (上游 jemalloc 5.0 分配器 bug，不适用).
//   - 动画 (3): neovide#850, neovim#279, neovim#8715 (全部 M4).
//   - 分发 (7): neovim-qt#771, macvim#552, macvim#952, neovide#1679, neovim#18504,
//     neovim#9630, gnvim#219 (全部 M5 签名公证/dmg/cask).
//
// 13 covered P0 IDs + 31 deferred = 44/44; no subjective or milestone-specific scenario is forced.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/input"
)

func priorBugPoll(t *testing.T, timeout time.Duration, what string, ready func() bool) {
	t.Helper()
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
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

func TestPriorBug_Neovide1259_ParseLaunchNormalizesFileArgsWithoutEmptyBuffer(t *testing.T) {
	cwd := t.TempDir()
	single := filepath.Join(cwd, "notes.md")
	withSpace := filepath.Join(cwd, "My Notes.md")
	env := []string{"PATH=/usr/bin:/bin", "HOME=/tmp"}
	cases := []struct {
		name string
		argv []string
		want []string
	}{
		{name: "single file", argv: []string{single}, want: []string{single}},
		{name: "path with spaces", argv: []string{withSpace}, want: []string{withSpace}},
		{name: "normalized flags", argv: []string{"--new-window", "--headless", withSpace}, want: []string{withSpace}},
		{name: "double dash and flags", argv: []string{"--", "-n", "--clean", withSpace}, want: []string{"--", "-n", "--clean", withSpace}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req, err := ParseLaunch(testCase.argv, env, cwd)
			if err != nil {
				t.Fatalf("ParseLaunch(%v): %v", testCase.argv, err)
			}
			if !reflect.DeepEqual(req.Args, testCase.want) {
				t.Fatalf("Args = %q, want %q", req.Args, testCase.want)
			}
			for index, arg := range req.Args {
				if arg == "" {
					t.Fatalf("Args[%d] is empty: %q", index, req.Args)
				}
			}
			if req.WorkingDir != cwd || !reflect.DeepEqual(req.Env, env) {
				t.Fatalf("WorkingDir/Env = %q/%q, want stable cwd and env", req.WorkingDir, req.Env)
			}
			options := req.ConnectionOptions()
			if !reflect.DeepEqual(options.Args, testCase.want) {
				t.Fatalf("ConnectionOptions().Args = %q, want %q", options.Args, testCase.want)
			}
			for index, arg := range options.Args {
				if arg == "" {
					t.Fatalf("ConnectionOptions().Args[%d] is empty: %q", index, options.Args)
				}
			}
			if len(req.Args) == 0 || req.Args[len(req.Args)-1] == "" {
				t.Fatalf("launch would produce an empty buffer: Args = %q", req.Args)
			}
		})
	}
}

func TestPriorBug_Neovim1822_ClipboardMultilineCopyPasteFidelity(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	clipboard := &testClipboard{}
	shell := startTestShell(t, clipboard)
	tester := ui.NewTester(shell.View, 800, 480)
	lines := []string{"first <tag> \\path", "第二行：中文、※", "third\ttab | pipe", "", "last `quote`"}
	source := strings.Join(lines, "\n")
	yanked := source + "\n"
	replacement := make([][]byte, len(lines))
	for index, line := range lines {
		replacement[index] = []byte(line)
	}
	buffer, err := shell.impl.session.Nvim().CurrentBuffer()
	if err != nil {
		t.Fatalf("CurrentBuffer: %v", err)
	}
	if err := shell.impl.session.Nvim().SetBufferLines(buffer, 0, -1, true, replacement); err != nil {
		t.Fatalf("SetBufferLines: %v", err)
	}
	if err := shell.impl.session.Nvim().Command("normal! ggVGy"); err != nil {
		t.Fatalf("yank all lines: %v", err)
	}
	if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "copy"}) {
		t.Fatal("HandleEditCommand(copy) = false")
	}
	if got := clipboard.ReadText(); got != yanked {
		t.Fatalf("clipboard after copy = %q, want %q", got, yanked)
	}

	if err := shell.impl.session.Nvim().SetBufferLines(buffer, 0, -1, true, [][]byte{[]byte("")}); err != nil {
		t.Fatalf("clear buffer: %v", err)
	}
	if err := shell.impl.session.Nvim().Command("normal! gg"); err != nil {
		t.Fatalf("move to buffer start: %v", err)
	}
	tester.Type("i")
	priorBugPoll(t, 5*time.Second, "insert mode", func() bool {
		tester.Frame()
		return evalString(t, shell, "mode()") == "i"
	})
	if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "paste"}) {
		t.Fatal("HandleEditCommand(paste) = false")
	}
	priorBugPoll(t, 8*time.Second, "multiline paste into nvim buffer", func() bool {
		tester.Frame()
		return bufferText(t, shell) == yanked
	})
	if got := bufferText(t, shell); got != yanked {
		t.Fatalf("pasted buffer = %q, want %q", got, yanked)
	}
}

func TestPriorBug_Neovide1175_DoctorFindsExecutableNvimFromLaunchEnv(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "missing-exec-bit")
	good := filepath.Join(root, "bin")
	for _, directory := range []string{bad, good} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bad, "nvim"), []byte("not executable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(good, "nvim")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := bad + string(os.PathListSeparator) + good
	req := LaunchRequest{Env: []string{"PATH=" + path}, WorkingDir: root}
	doctor := req.Doctor()
	if doctor["PATH"] != path || doctor["nvim"] != executable || doctor["WorkingDir"] != root {
		t.Fatalf("Doctor() = %+v, want PATH/nvim/WorkingDir = %q/%q/%q", doctor, path, executable, root)
	}
	if filepath.Dir(doctor["nvim"]) != good {
		t.Fatalf("Doctor()[nvim] directory = %q, want %q", filepath.Dir(doctor["nvim"]), good)
	}
	if info, err := os.Stat(doctor["nvim"]); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("Doctor()[nvim] = %q is not executable: info=%+v err=%v", doctor["nvim"], info, err)
	}

	missing := LaunchRequest{Env: []string{"PATH=" + bad}, WorkingDir: root}.Doctor()
	if missing["nvim"] != "" || missing["PATH"] != bad {
		t.Fatalf("Doctor() with no executable = %+v, want clear empty nvim status", missing)
	}
}

func TestPriorBug_Neovim19783_LargePasteThroughQueueNoDeadlockOrTruncation(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	clipboard := &testClipboard{}
	shell := startTestShell(t, clipboard)
	tester := ui.NewTester(shell.View, 800, 480)
	buffer, err := shell.impl.session.Nvim().CurrentBuffer()
	if err != nil {
		t.Fatalf("CurrentBuffer: %v", err)
	}
	if err := shell.impl.session.Nvim().SetBufferLines(buffer, 0, -1, true, [][]byte{[]byte("")}); err != nil {
		t.Fatalf("clear buffer: %v", err)
	}
	tester.Type("i")
	priorBugPoll(t, 5*time.Second, "insert mode", func() bool {
		tester.Frame()
		return evalString(t, shell, "mode()") == "i"
	})

	payloads := make([]string, 3)
	for part := range payloads {
		var builder strings.Builder
		for line := 0; builder.Len() < 10*1024; line++ {
			fmt.Fprintf(&builder, "part%d line%03d 中文 <tag> \\backslash | %s\r\n",
				part, line, strings.Repeat("x", 48))
		}
		payloads[part] = builder.String()
		if len(payloads[part]) < 10*1024 {
			t.Fatalf("payload %d = %d bytes, want >= 10 KB", part, len(payloads[part]))
		}
	}
	for part := 1; part < len(payloads); part++ {
		if payloads[part] == payloads[part-1] {
			t.Fatalf("payload %d repeats payload %d", part, part-1)
		}
	}
	normalized := make([]string, len(payloads))
	for part := range payloads {
		normalized[part] = strings.ReplaceAll(strings.ReplaceAll(payloads[part], "\r\n", "\n"), "\r", "\n")
	}
	var fragments strings.Builder
	for _, payload := range normalized {
		fragments.WriteString(payload)
	}
	wantSent := input.EscapeText(fragments.String())
	wantChunks := make([][]string, len(payloads))
	rawChunks := make([]string, 0)
	for part, payload := range normalized {
		for _, chunk := range pasteChunks(payload, maxPasteChunkBytes) {
			wantChunks[part] = append(wantChunks[part], input.EscapeText(chunk))
			rawChunks = append(rawChunks, chunk)
		}
	}
	allChunks := make([]string, 0, len(shell.impl.queue.Sent()))

	for part, payload := range payloads {
		clipboard.WriteText(payload)
		before := len(shell.impl.queue.Sent())
		if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputCommand, Text: "paste"}) {
			t.Fatalf("HandleEditCommand(paste part %d) = false", part)
		}
		priorBugPoll(t, 12*time.Second, fmt.Sprintf("paste part %d delivery", part), func() bool {
			tester.Frame()
			return len(shell.impl.queue.Sent())-before >= len(wantChunks[part])
		})
		sent := shell.impl.queue.Sent()
		if len(sent)-before != len(wantChunks[part]) {
			t.Fatalf("Queue.Sent() after part %d = %d chunks, want %d", part, len(sent)-before, len(wantChunks[part]))
		}
		for index, escapedChunk := range wantChunks[part] {
			if sent[before+index] != escapedChunk {
				t.Fatalf("Queue.Sent() part %d chunk %d = %q, want escaped chunk %q", part, index, sent[before+index], escapedChunk)
			}
		}
		allChunks = append(allChunks, sent[before:]...)
	}

	sent := shell.impl.queue.Sent()
	if got := sent[len(sent)-len(allChunks):]; !reflect.DeepEqual(got, allChunks) {
		t.Fatalf("Queue.Sent() tail = %d chunks, want %d strictly ordered escaped chunks", len(got), len(allChunks))
	}
	if got := strings.Join(allChunks, ""); got != wantSent {
		t.Fatalf("Queue.Sent() paste tail = %d joined bytes, want %d strictly ordered escaped bytes", len(got), len(wantSent))
	}
	receiveChunks(t, rawChunks, allChunks, wantSent)
}

type priorBugRecordingSender struct {
	mu     sync.Mutex
	chunks []string
}

func (sender *priorBugRecordingSender) Input(keys string) (int, error) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.chunks = append(sender.chunks, keys)
	return len(keys), nil
}

func receiveChunks(t *testing.T, rawChunks, wantChunks []string, wantJoined string) {
	t.Helper()
	sender := &priorBugRecordingSender{}
	queue := input.NewQueue(sender, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- queue.Run(ctx) }()
	for _, chunk := range rawChunks {
		if err := queue.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: chunk}); err != nil {
			t.Fatalf("Enqueue(%q): %v", chunk, err)
		}
	}
	priorBugPoll(t, 12*time.Second, "all paste chunks through a real input.Queue", func() bool {
		sender.mu.Lock()
		defer sender.mu.Unlock()
		return len(sender.chunks) == len(wantChunks)
	})
	if err := queue.Close(); err != nil {
		t.Fatalf("queue.Close: %v", err)
	}
	if runErr := <-runDone; runErr != nil && !errors.Is(runErr, context.Canceled) {
		t.Fatalf("queue.Run: %v", runErr)
	}
	sent := queue.Sent()
	if !reflect.DeepEqual(sent, wantChunks) {
		t.Fatalf("recording Sender received %d chunks, want %d strictly ordered chunks", len(sent), len(wantChunks))
	}
	if got := strings.Join(sent, ""); got != wantJoined {
		t.Fatalf("recording Sender received %d joined bytes, want %d", len(got), len(wantJoined))
	}
}
