package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nvgui/connection"
	"nvgui/diag"
	"nvgui/screen"
	"nvgui/session"
)

type sessionFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *sessionFakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *sessionFakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func TestSessionEndToEnd(t *testing.T) {
	clock := &sessionFakeClock{now: time.Unix(0, 0)}
	sess, err := session.Start(context.Background(), connection.Options{
		Args:   []string{"--clean", "-n"},
		Width:  80,
		Height: 24,
	}, clock)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sess.Nvim() == nil {
		t.Fatal("Nvim() = nil")
	}
	if sess.Exited() == nil {
		t.Fatal("Exited() is nil")
	}
	stalls := sess.Stalls()
	if stalls == nil || cap(stalls) < 1 {
		t.Fatalf("Stalls() = nil or unbuffered (capacity %d)", cap(stalls))
	}
	if _, err := sess.Nvim().Input("ihello<Esc>"); err != nil {
		t.Fatalf("Input: %v", err)
	}
	var latest *screen.State
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		latest = sess.Latest()
		if latest != nil && gridContains(latest, "hello") {
			break
		}
		clock.Advance(10 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)
	}
	if latest == nil || !gridContains(latest, "hello") {
		t.Fatal("Latest() never contained \"hello\"")
	}
	wantCanonical, err := screen.Canonical(latest)
	if err != nil {
		t.Fatalf("Canonical(Latest): %v", err)
	}
	directory := filepath.Join(t.TempDir(), "diag")
	if err := sess.ExportDiag(directory); err != nil {
		t.Fatalf("ExportDiag: %v", err)
	}
	for _, name := range []string{"meta.json", "keyframe.json", "keyframe.gob", "inbound.mpack", "timing.jsonl"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("diagnostic bundle missing %s: %v", name, err)
		}
	}
	metaData, err := os.ReadFile(filepath.Join(directory, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("decode meta.json: %v", err)
	}
	for _, key := range []string{"nvim_version", "api_level", "ui_options", "gui_version", "goos", "exported_at", "window_ms"} {
		if _, exists := meta[key]; !exists {
			t.Fatalf("meta.json missing %q", key)
		}
	}
	states, err := diag.Replay(directory)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(states) == 0 {
		t.Fatal("Replay returned no states")
	}
	gotCanonical, err := screen.Canonical(states[len(states)-1])
	if err != nil {
		t.Fatalf("Canonical(Replay last): %v", err)
	}
	_, difference := compareCanonical(wantCanonical, gotCanonical)
	if difference != "" {
		t.Fatalf("Replay last frame differs from Latest at %s", difference)
	}

	var pid int
	if err := sess.Nvim().Eval("getpid()", &pid); err != nil {
		t.Fatalf("getpid(): %v", err)
	}
	if err := sess.Nvim().Command("qa!"); err != nil {
		t.Fatalf(":qa!: %v", err)
	}
	select {
	case <-sess.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("Exited() did not close after :qa!")
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if processAlive(pid) {
		t.Fatalf("nvim child process %d still exists after Close", pid)
	}
}

func gridContains(state *screen.State, text string) bool {
	for _, grid := range state.Grids {
		for _, row := range grid.Cells {
			var builder strings.Builder
			for _, cell := range row {
				builder.WriteString(cell.Text)
			}
			if strings.Contains(builder.String(), text) {
				return true
			}
		}
	}
	return false
}

func processAlive(pid int) bool {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if err != nil {
		return false
	}
	status := strings.TrimSpace(string(output))
	return status != "" && !strings.Contains(status, "Z")
}

func compareCanonical(want, got []byte) (int, string) {
	var wantValue, gotValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		return 0, "want: " + err.Error()
	}
	if err := json.Unmarshal(got, &gotValue); err != nil {
		return 0, "got: " + err.Error()
	}
	wildcards := 0
	if difference := compareCanonicalValue(wantValue, gotValue, "$", &wildcards); difference != "" {
		return wildcards, difference
	}
	return wildcards, ""
}

func compareCanonicalValue(want, got any, path string, wildcards *int) string {
	if isWildcardCell(want) {
		*wildcards++
		return ""
	}
	switch expected := want.(type) {
	case map[string]any:
		actual, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: type %T, want object", path, got)
		}
		if len(expected) != len(actual) {
			return fmt.Sprintf("%s: object has %d keys, want %d", path, len(actual), len(expected))
		}
		for key, wantChild := range expected {
			gotChild, exists := actual[key]
			if !exists {
				return path + "." + key + ": missing key"
			}
			if difference := compareCanonicalValue(wantChild, gotChild, path+"."+key, wildcards); difference != "" {
				return difference
			}
		}
		return ""
	case []any:
		actual, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: type %T, want array", path, got)
		}
		if len(expected) != len(actual) {
			return fmt.Sprintf("%s: array length %d, want %d", path, len(actual), len(expected))
		}
		for index := range expected {
			if difference := compareCanonicalValue(expected[index], actual[index], path+"["+strconv.Itoa(index)+"]", wildcards); difference != "" {
				return difference
			}
		}
		return ""
	case float64:
		actual, ok := got.(float64)
		if !ok || expected != actual {
			return fmt.Sprintf("%s: %v, want %v", path, got, expected)
		}
		return ""
	case string:
		actual, ok := got.(string)
		if !ok || expected != actual {
			return fmt.Sprintf("%s: %q, want %q", path, got, expected)
		}
		return ""
	case bool:
		actual, ok := got.(bool)
		if !ok || expected != actual {
			return fmt.Sprintf("%s: %v, want %v", path, got, expected)
		}
		return ""
	case nil:
		if got != nil {
			return fmt.Sprintf("%s: %v, want null", path, got)
		}
		return ""
	default:
		return fmt.Sprintf("%s: unsupported canonical type %T", path, want)
	}
}

func isWildcardCell(value any) bool {
	cell, ok := value.([]any)
	if !ok || len(cell) != 2 {
		return false
	}
	text, ok := cell[0].(string)
	return ok && text == "\uFFFD"
}
