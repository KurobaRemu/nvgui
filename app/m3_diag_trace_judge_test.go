package app_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/input"
)

type m3DiagClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *m3DiagClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *m3DiagClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type m3DiagClipboard struct {
	mu   sync.Mutex
	text string
}

func (c *m3DiagClipboard) ReadText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

func (c *m3DiagClipboard) WriteText(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = s
}

func TestM3AppExportDiagIncludesInputTraceAndDeduplicates(t *testing.T) {
	clock := &m3DiagClock{now: time.Unix(1_700_000_000, 0).UTC()}
	clip := &m3DiagClipboard{}
	req := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        os.Environ(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}

	shell, err := app.Start(context.Background(), req, clock, clip)
	if err != nil {
		t.Fatalf("app.Start(headless): %v", err)
	}
	if shell == nil {
		t.Fatal("app.Start returned nil shell")
	}
	t.Cleanup(func() {
		_ = shell.Close()
		if exited := shell.SessionExited(); exited != nil {
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
			}
		}
	})

	tester := ui.NewTester(shell.View, 800, 480)

	// 1. Wait for first flush to commit (ExportDiag succeeds once keyframe exists).
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(time.Millisecond)
		tester.Frame()
		probeDir := filepath.Join(t.TempDir(), "probe")
		if err := shell.ExportDiag(probeDir); err == nil {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("headless shell did not commit initial flush within 5s")
	}

	// 2. Enter insert mode and wait for mode_change("insert") to refresh InputMethod(true).
	tester.Type("i")
	inInsert := false
	insertDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(insertDeadline) {
		clock.Advance(time.Millisecond)
		tester.Frame()
		if active, noIME := tester.TextInputActive(); active && !noIME {
			inInsert = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !inInsert {
		active, noIME := tester.TextInputActive()
		t.Fatalf("shell did not switch to insert InputMethod(true) within 5s (active=%v, noIME=%v)", active, noIME)
	}

	// 3. Inject Compose + Text + Escape while in insert mode, then wait to return to normal mode.
	tester.Compose("ni", 2)
	tester.Compose("", 0)
	tester.Type("你好")
	tester.Key(0, ui.KeyEscape)

	backNormal := false
	normalDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(normalDeadline) {
		clock.Advance(time.Millisecond)
		tester.Frame()
		if active, noIME := tester.TextInputActive(); active && noIME {
			backNormal = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !backNormal {
		active, noIME := tester.TextInputActive()
		t.Fatalf("shell did not return to normal InputMethod(false) after <Esc> (active=%v, noIME=%v)", active, noIME)
	}

	// 4. First ExportDiag: input-trace.jsonl must be non-empty, ReplayBundle & input.Replay must succeed.
	dir1 := filepath.Join(t.TempDir(), "bundle1")
	if err := shell.ExportDiag(dir1); err != nil {
		t.Fatalf("first ExportDiag: %v", err)
	}
	traceBytes1, err := os.ReadFile(filepath.Join(dir1, "input-trace.jsonl"))
	if err != nil {
		t.Fatalf("read input-trace.jsonl: %v", err)
	}
	if len(bytes.TrimSpace(traceBytes1)) == 0 {
		t.Fatal("ExportDiag produced empty input-trace.jsonl; input trace bridge is missing")
	}

	bundle1, err := diag.ReplayBundle(dir1)
	if err != nil {
		t.Fatalf("diag.ReplayBundle(bundle1): %v", err)
	}
	if len(bundle1.InputEvents) < 2 {
		t.Fatalf("bundle1.InputEvents len = %d, want >= 2", len(bundle1.InputEvents))
	}
	var prevSeq uint64
	for idx, ev := range bundle1.InputEvents {
		if ev.Seq == 0 || ev.Seq <= prevSeq {
			t.Fatalf("bundle1.InputEvents[%d].Seq = %d, want strictly increasing after %d", idx, ev.Seq, prevSeq)
		}
		prevSeq = ev.Seq
	}

	var replayed []ui.InputEvent
	if err := input.Replay(bundle1.InputEvents, func(ev ui.InputEvent) {
		replayed = append(replayed, ev)
	}); err != nil {
		t.Fatalf("input.Replay(bundle1.InputEvents): %v", err)
	}
	if len(replayed) != len(bundle1.InputEvents) {
		t.Fatalf("input.Replay emitted %d events, want %d", len(replayed), len(bundle1.InputEvents))
	}
	var sawComposeNi, sawTextCJK, sawEscape bool
	for _, ev := range replayed {
		switch ev.Kind {
		case ui.InputCompose:
			if ev.Text == "ni" {
				sawComposeNi = true
			}
		case ui.InputText:
			if ev.Text == "你好" {
				sawTextCJK = true
			}
		case ui.InputKeyDown:
			if ev.Key == ui.KeyEscape {
				sawEscape = true
			}
		}
	}
	if !sawComposeNi || !sawTextCJK || !sawEscape {
		t.Fatalf("replayed input trace missing expected events: compose(ni)=%v text(你好)=%v keyDown(Esc)=%v (events=%+v)",
			sawComposeNi, sawTextCJK, sawEscape, bundle1.InputEvents)
	}

	// 5. Consecutive second ExportDiag must not duplicate any Seq already recorded.
	dir2 := filepath.Join(t.TempDir(), "bundle2")
	if err := shell.ExportDiag(dir2); err != nil {
		t.Fatalf("second ExportDiag: %v", err)
	}
	bundle2, err := diag.ReplayBundle(dir2)
	if err != nil {
		t.Fatalf("diag.ReplayBundle(bundle2): %v", err)
	}
	if len(bundle2.InputEvents) != len(bundle1.InputEvents) {
		t.Fatalf("consecutive ExportDiag changed InputEvents len from %d to %d (duplicate or dropped events)",
			len(bundle1.InputEvents), len(bundle2.InputEvents))
	}
	for i := range bundle1.InputEvents {
		if bundle2.InputEvents[i].Seq != bundle1.InputEvents[i].Seq {
			t.Fatalf("bundle2.InputEvents[%d].Seq = %d, want %d", i, bundle2.InputEvents[i].Seq, bundle1.InputEvents[i].Seq)
		}
	}

	// 6. Inject one additional event and verify a third ExportDiag appends strictly newer Seq without duplicating prior events.
	tester.Type("j")
	dir3 := filepath.Join(t.TempDir(), "bundle3")
	if err := shell.ExportDiag(dir3); err != nil {
		t.Fatalf("third ExportDiag: %v", err)
	}
	bundle3, err := diag.ReplayBundle(dir3)
	if err != nil {
		t.Fatalf("diag.ReplayBundle(bundle3): %v", err)
	}
	if len(bundle3.InputEvents) <= len(bundle2.InputEvents) {
		t.Fatalf("third ExportDiag did not include newly injected event: len=%d, prev=%d",
			len(bundle3.InputEvents), len(bundle2.InputEvents))
	}
	prevSeq = 0
	for i, ev := range bundle3.InputEvents {
		if ev.Seq == 0 || ev.Seq <= prevSeq {
			t.Fatalf("bundle3.InputEvents[%d].Seq = %d, want strictly increasing after %d", i, ev.Seq, prevSeq)
		}
		prevSeq = ev.Seq
	}
	for i := range bundle2.InputEvents {
		if bundle3.InputEvents[i].Seq != bundle2.InputEvents[i].Seq {
			t.Fatalf("bundle3 prefix[%d].Seq = %d, want %d", i, bundle3.InputEvents[i].Seq, bundle2.InputEvents[i].Seq)
		}
	}
}
