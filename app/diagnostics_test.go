package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/neovim/go-client/msgpack"

	"nvgui/diag"
	"nvgui/protocol"
	"nvgui/screen"
)

type diagnosticsTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *diagnosticsTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

type diagnosticsTestBuffer struct{ data []byte }

func (buffer *diagnosticsTestBuffer) Write(p []byte) (int, error) {
	buffer.data = append(buffer.data, p...)
	return len(p), nil
}

func diagnosticsTestRedraw(t *testing.T, text string) []byte {
	t.Helper()
	var buffer diagnosticsTestBuffer
	events := []any{
		[]any{"grid_resize", []any{1, 1, 1}},
		[]any{"grid_line", []any{1, 0, 0, []any{[]any{text}}, false}},
		[]any{"flush", []any{}},
	}
	if err := msgpack.NewEncoder(&buffer).Encode([]any{2, "redraw", events}); err != nil {
		t.Fatalf("encode redraw: %v", err)
	}
	if _, err := protocol.ReadMessages(buffer.data); err != nil {
		t.Fatalf("decode redraw: %v", err)
	}
	return buffer.data
}

func diagnosticsTestBundle(t *testing.T, dir string, meta diag.Meta, inputText string) {
	t.Helper()
	clock := &diagnosticsTestClock{now: time.Unix(1_700_000_000, 0).UTC()}
	recorder := diag.NewRecorder(30*time.Second, clock)
	reducer := screen.NewReducer()
	message := diagnosticsTestRedraw(t, "buffer-secret-text")
	if _, err := recorder.Write(message); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parsed, err := protocol.ReadMessages(message)
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	events, err := protocol.DecodeRedraw(parsed[0].Args)
	if err != nil {
		t.Fatalf("DecodeRedraw: %v", err)
	}
	states, err := reducer.Apply(events)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, state := range states {
		recorder.Keyframe(state)
	}
	recorder.RecordInput(diag.TraceEvent{
		Seq:  1,
		At:   clock.Now(),
		Kind: diag.TraceText,
		Text: inputText,
	})
	if err := recorder.Export(dir, meta); err != nil {
		t.Fatalf("Export: %v", err)
	}
}

func TestAppDiagnosticsBundleToScenarioRedaction(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bundle")
	secret := "super-secret-token-should-not-leak-987654"
	meta := diag.Meta{
		NvimVersion: "nvim-0.12.0",
		APILevel:    12,
		GUIVersion:  "nvgui-test",
		GOOS:        "darwin",
		Extra: map[string]string{
			"FONT":           "Menlo:h14",
			"API_TOKEN":      secret,
			"HOME_DIRECTORY": "/Users/aidenalin/private/font.ttf",
		},
	}
	diagnosticsTestBundle(t, dir, meta, "password-original")
	out := filepath.Join(root, "scenario-dir")
	if err := NewDiagnostics().BundleToScenario(dir, out); err != nil {
		t.Fatalf("BundleToScenario: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "scenario.json"))
	if err != nil {
		t.Fatalf("read scenario.json: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("scenario.json is invalid JSON: %s", data)
	}
	for _, leaked := range []string{
		"buffer-secret-text", "password-original", secret, "API_TOKEN",
		"HOME_DIRECTORY", "/Users/aidenalin/private/font.ttf",
		dir, out, "/Users/", "font.ttf",
	} {
		if bytes.Contains(data, []byte(leaked)) {
			t.Errorf("scenario.json leaked %q", leaked)
		}
	}
	var payload struct {
		Font string `json:"font"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode scenario.json: %v", err)
	}
	if payload.Font != "Menlo:h14" {
		t.Fatalf("font summary = %q, want %q", payload.Font, "Menlo:h14")
	}
}

func TestAppDiagnosticsBundleToScenarioPathAndErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bundle")
	diagnosticsTestBundle(t, dir, diag.Meta{Extra: map[string]string{"guifont": "Menlo:h14"}}, "x")
	diagnostics := NewDiagnostics()

	outDir := filepath.Join(root, "scenario-out")
	if err := diagnostics.BundleToScenario(dir, outDir); err != nil {
		t.Fatalf("BundleToScenario directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "scenario.json")); err != nil {
		t.Fatalf("scenario.json in directory output: %v", err)
	}
	for _, name := range []string{"scenario.json", "scenario.JSON"} {
		path := filepath.Join(root, "nested", name)
		if err := diagnostics.BundleToScenario(dir, path); err != nil {
			t.Fatalf("BundleToScenario %s: %v", name, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("scenario.json at %s: %v", path, err)
		}
	}

	for _, tc := range []struct {
		name string
		dir  string
		out  string
	}{
		{"empty-dir", "", outDir},
		{"empty-out", dir, ""},
		{"bad-bundle", filepath.Join(root, "missing-bundle"), outDir},
	} {
		if err := diagnostics.BundleToScenario(tc.dir, tc.out); err == nil {
			t.Errorf("%s: BundleToScenario succeeded with invalid arguments", tc.name)
		}
	}
	var nilDiagnostics *Diagnostics
	if err := nilDiagnostics.BundleToScenario(dir, outDir); err == nil {
		t.Error("nil receiver BundleToScenario succeeded")
	}
}

func TestAppDiagnosticsShortcutGuards(t *testing.T) {
	diagnostics := NewDiagnostics()
	if diagnostics.Shortcut(nil) {
		t.Error("Shortcut(nil) returned true")
	}
	var nilDiagnostics *Diagnostics
	if nilDiagnostics.Shortcut(nil) {
		t.Error("nil receiver Shortcut(nil) returned true")
	}

	triggered := 0
	tester := ui.NewTester(func(c *ui.Context) {
		if diagnostics.Shortcut(c) || nilDiagnostics.Shortcut(c) {
			triggered++
		}
		ui.Box(c).Fill()
	}, 200, 100)
	tester.Key(0, ui.KeyD)
	tester.Key(ui.Cmd, ui.KeyD)
	tester.Key(ui.Shift, ui.KeyD)
	tester.Key(ui.Cmd|ui.Shift, ui.KeyE)
	if triggered != 0 {
		t.Fatalf("Shortcut triggered %d times for other keys, want 0", triggered)
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyD)
	if triggered != 1 {
		t.Fatalf("Shortcut triggered %d times for Cmd+Shift+D, want 1", triggered)
	}
}

func TestAppDiagnosticsSafeFontSummary(t *testing.T) {
	for _, value := range []string{
		"/Users/aidenalin/font.ttf",
		`C:\Windows\Fonts\font.ttf`,
		"Menlo\n:h14",
		"Menlo\x00:h14",
	} {
		extra := map[string]string{"GuIfOnT": value}
		if got := safeFontSummary(extra); got != "" {
			t.Errorf("safeFontSummary(%q) = %q, want empty", value, got)
		}
	}
	if got := safeFontSummary(map[string]string{"FONT_SIZE": "14"}); got != "14" {
		t.Errorf("safeFontSummary(font_size) = %q, want 14", got)
	}
	extra := map[string]string{
		"font":        "Menlo:h14",
		"FONT":        "FiraCode:h14",
		"guifont":     "Consolas:h12",
		"font_family": "SFMono",
		"font_size":   "12",
	}
	for call := 0; call < 64; call++ {
		if got := safeFontSummary(extra); got != "FiraCode:h14" {
			t.Fatalf("safeFontSummary call %d = %q, want %q", call, got, "FiraCode:h14")
		}
	}
}
