package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/oracle"
	"nvgui/protocol"
	"nvgui/screen"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeClipboard struct {
	mu   sync.Mutex
	text string
}

func (c *fakeClipboard) ReadText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

func (c *fakeClipboard) WriteText(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = s
}

func startHeadlessShell(t *testing.T, clip *fakeClipboard) (*app.Shell, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
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
		t.Fatal("app.Start returned nil shell with nil error")
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
	return shell, clock
}

func TestAppParseLaunchAndDoctor(t *testing.T) {
	env := []string{
		"PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"HOME=/tmp/nvgui-test-home",
	}
	cwd := "/tmp/nvgui-work"

	req, err := app.ParseLaunch([]string{"--new-window", "notes.md"}, env, cwd)
	if err != nil {
		t.Fatalf("ParseLaunch: %v", err)
	}
	if req.WorkingDir != cwd {
		t.Errorf("WorkingDir = %q, want %q", req.WorkingDir, cwd)
	}
	foundFile := false
	for _, a := range req.Args {
		if a == "notes.md" {
			foundFile = true
		}
	}
	if !foundFile {
		t.Errorf("Args = %v, want to include \"notes.md\"", req.Args)
	}

	opts := req.ConnectionOptions()
	if opts.Dir != cwd {
		t.Errorf("ConnectionOptions().Dir = %q, want %q", opts.Dir, cwd)
	}
	foundPathEnv := false
	for _, e := range opts.Env {
		if e == "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin" {
			foundPathEnv = true
		}
	}
	if !foundPathEnv {
		t.Errorf("ConnectionOptions().Env did not preserve PATH: %v", opts.Env)
	}

	doc := req.Doctor()
	if len(doc) == 0 {
		t.Fatal("Doctor() returned empty map")
	}
	if doc["PATH"] != "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin" {
		t.Errorf("Doctor()[\"PATH\"] = %q, want preserved PATH", doc["PATH"])
	}
	if doc["WorkingDir"] != cwd {
		t.Errorf("Doctor()[\"WorkingDir\"] = %q, want %q", doc["WorkingDir"], cwd)
	}
	if doc["nvim"] == "" {
		t.Errorf("Doctor()[\"nvim\"] is empty: %+v", doc)
	}
}

func TestAppResizeAndSessionCloseLifecycle(t *testing.T) {
	clip := &fakeClipboard{}
	shell, _ := startHeadlessShell(t, clip)

	exited := shell.SessionExited()
	if exited == nil {
		t.Fatal("SessionExited() returned nil channel")
	}

	// Resize with DIP pixel dimensions (and small edge size clamped to >= 1x1) must succeed.
	if err := shell.Resize(960, 540); err != nil {
		t.Fatalf("Resize(960, 540): %v", err)
	}
	if err := shell.Resize(0, 0); err != nil {
		t.Fatalf("Resize(0, 0) (should clamp to >= 1x1): %v", err)
	}

	// Direction 1: Close() terminates nvim and is idempotent.
	if err := shell.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if err := shell.Close(); err != nil {
		t.Fatalf("second Close() (idempotent): %v", err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("SessionExited() did not close within 5s after Shell.Close()")
	}

	// After Close(), Resize must return an error rather than silently swallowing it.
	if err := shell.Resize(800, 400); err == nil {
		t.Error("Resize after Close() returned nil error, want error")
	}
}

func TestAppNvimExitClosesSession(t *testing.T) {
	clip := &fakeClipboard{}
	shell, _ := startHeadlessShell(t, clip)

	exited := shell.SessionExited()
	if exited == nil {
		t.Fatal("SessionExited() returned nil")
	}

	tester := ui.NewTester(shell.View, 800, 480)
	tester.Type(":qa!")
	tester.Key(0, ui.KeyEnter)

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("SessionExited() did not close within 5s after :qa! inside nvim")
	}
}

func TestAppClipboardCommands(t *testing.T) {
	clip := &fakeClipboard{text: "nvgui_clip_token"}
	shell, _ := startHeadlessShell(t, clip)

	tester := ui.NewTester(shell.View, 800, 480)
	tester.SetClipboard("nvgui_clip_token")

	// Enter insert mode, paste via edit command, exit insert mode.
	tester.Type("i")
	tester.Command("paste")
	tester.Key(0, ui.KeyEscape)

	// Yank current line into unnamed register, then trigger copy command.
	clip.WriteText("")
	tester.SetClipboard("")
	tester.Type("yy")

	deadline := time.Now().Add(4 * time.Second)
	copied := ""
	for time.Now().Before(deadline) {
		tester.Command("copy")
		tester.Frame()
		if got := clip.ReadText(); strings.Contains(got, "nvgui_clip_token") {
			copied = got
			break
		}
		if got := tester.Clipboard(); strings.Contains(got, "nvgui_clip_token") {
			copied = got
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !strings.Contains(copied, "nvgui_clip_token") {
		t.Fatalf("copy command did not populate clipboard with pasted text; fake=%q tester=%q",
			clip.ReadText(), tester.Clipboard())
	}

	// Cut command must also populate clipboard cleanly (register "" already holds the yanked line).
	clip.WriteText("")
	tester.SetClipboard("")
	tester.Command("cut")
	cutDeadline := time.Now().Add(3 * time.Second)
	cutCopied := ""
	for time.Now().Before(cutDeadline) {
		tester.Frame()
		if got := clip.ReadText(); strings.Contains(got, "nvgui_clip_token") {
			cutCopied = got
			break
		}
		if got := tester.Clipboard(); strings.Contains(got, "nvgui_clip_token") {
			cutCopied = got
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !strings.Contains(cutCopied, "nvgui_clip_token") {
		t.Errorf("cut command did not write register text to clipboard; fake=%q tester=%q",
			clip.ReadText(), tester.Clipboard())
	}
}

func TestAppExportDiagHeadless(t *testing.T) {
	clip := &fakeClipboard{}
	shell, clock := startHeadlessShell(t, clip)

	tester := ui.NewTester(shell.View, 800, 480)
	tester.Type("idiag_check")
	tester.Key(0, ui.KeyEscape)

	dir := filepath.Join(t.TempDir(), "bundle")
	var lastErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clock.Advance(10 * time.Millisecond)
		tester.Frame()
		target := filepath.Join(t.TempDir(), "bundle")
		if err := shell.ExportDiag(target); err == nil {
			dir = target
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		time.Sleep(30 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("ExportDiag failed: %v", lastErr)
	}
	for _, name := range []string{"meta.json", "keyframe.json", "keyframe.gob", "inbound.mpack", "timing.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("ExportDiag missing %s: %v", name, err)
		}
	}
	states, err := diag.Replay(dir)
	if err != nil {
		t.Fatalf("diag.Replay(ExportDiag): %v", err)
	}
	if len(states) == 0 {
		t.Fatal("diag.Replay returned zero states")
	}
}

func TestAppDiagnosticsBundleToScenario(t *testing.T) {
	// Export a real golden bundle with a secret in Meta.Extra to verify redaction.
	goldens := oracle.List()
	if len(goldens) == 0 {
		t.Fatal("oracle.List() is empty")
	}
	golden := goldens[0]
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	recorder := diag.NewRecorder(30*time.Second, clock)
	reducer := screen.NewReducer()

	messages, err := protocol.ReadMessages(golden.Inbound)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	for _, msg := range messages {
		if _, err := recorder.Write(msg.Raw); err != nil {
			t.Fatalf("recorder.Write: %v", err)
		}
		clock.Advance(1 * time.Millisecond)
		if msg.Type != 2 || msg.Method != "redraw" {
			continue
		}
		events, err := protocol.DecodeRedraw(msg.Args)
		if err != nil {
			t.Fatalf("DecodeRedraw: %v", err)
		}
		committed, err := reducer.Apply(events)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		for _, st := range committed {
			recorder.Keyframe(st)
		}
	}
	recorder.RecordInput(diag.TraceEvent{
		Seq:  1,
		At:   clock.Now(),
		Kind: diag.TraceText,
		Text: "j",
	})

	bundleDir := filepath.Join(t.TempDir(), "bundle")
	secretValue := "super-secret-token-should-not-leak-987654"
	meta := diag.Meta{
		NvimVersion: golden.Meta.NvimVersion,
		APILevel:    golden.Meta.APILevel,
		UIOptions:   golden.Meta.UIOptions,
		GUIVersion:  "m1-test",
		GOOS:        "darwin",
		Extra: map[string]string{
			"font":      "Menlo:h14",
			"API_TOKEN": secretValue,
		},
	}
	if err := recorder.Export(bundleDir, meta); err != nil {
		t.Fatalf("recorder.Export: %v", err)
	}

	d := app.NewDiagnostics()
	if d == nil {
		t.Fatal("NewDiagnostics returned nil")
	}
	outDir := t.TempDir()
	if err := d.BundleToScenario(bundleDir, outDir); err != nil {
		t.Fatalf("BundleToScenario: %v", err)
	}

	scenarioPath := filepath.Join(outDir, "scenario.json")
	raw, err := os.ReadFile(scenarioPath)
	if err != nil {
		t.Fatalf("read scenario.json: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("scenario.json is not valid JSON: %s", string(raw))
	}
	if strings.Contains(string(raw), secretValue) {
		t.Fatalf("scenario.json leaked secret value %q", secretValue)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal scenario.json: %v", err)
	}
	if len(payload) == 0 {
		t.Fatal("scenario.json object is empty")
	}
}

func TestAppDiagnosticsShortcut(t *testing.T) {
	d := app.NewDiagnostics()
	if d == nil {
		t.Fatal("NewDiagnostics returned nil")
	}
	triggered := 0
	tester := ui.NewTester(func(c *ui.Context) {
		if d.Shortcut(c) {
			triggered++
		}
		ui.Box(c).Fill()
	}, 200, 100)
	if triggered != 0 {
		t.Fatalf("Shortcut triggered %d times before Cmd+Shift+D, want 0", triggered)
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyD)
	if triggered != 1 {
		t.Fatalf("Shortcut triggered %d times after Cmd+Shift+D, want 1", triggered)
	}
}
