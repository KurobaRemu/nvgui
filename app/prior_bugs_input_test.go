// Ticket 42: keyboard and CJK input P0 regression set.
// Explicitly excluded/deferred (12 covered + 5 deferred = 17/17 relevant P0 IDs):
//   - neovide#1006: Linux fcitx5, not a macOS host deterministic scenario.
//   - neovide#1488 / neovide#2330: real-window initial focus; VM L2 l2_runner.py validates.
//   - neovim#27509: upstream Kitty key-release protocol proposal.
//   - neovim#23645: upstream modifiable terminal emulator feature request.
//
// These five are also registered in wiki/m3-exit.md.
package app_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/neovim/go-client/nvim"

	"nvgui/app"
)

type priorBugClock struct{ now time.Time }

func (c *priorBugClock) Now() time.Time { return c.now }

func dialPriorBugNvim(t *testing.T, address string) *nvim.Nvim {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", address, 100*time.Millisecond)
		if err == nil {
			client, err := nvim.New(conn, conn, conn, func(string, ...any) {})
			if err != nil {
				_ = conn.Close()
				t.Fatalf("nvim.New: %v", err)
			}
			go func() { _ = client.Serve() }()
			t.Cleanup(func() {
				_ = client.Close()
				_ = conn.Close()
			})
			return client
		}
	}
	t.Fatalf("nvim address %s did not become ready", address)
	return nil
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

func TestPriorBug_Neovide1263_MacOSCmdVPasteMultilineAndSpecialChars(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	tempDir := t.TempDir()
	address := filepath.Join(tempDir, "nvim.sock")
	clip := &fakeClipboard{}
	shell, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-n", "--listen", address},
		Env:        os.Environ(),
		WorkingDir: tempDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}, &priorBugClock{now: time.Unix(1_700_000_000, 0).UTC()}, clip)
	if err != nil {
		t.Fatalf("app.Start(headless): %v", err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	client := dialPriorBugNvim(t, address)
	tester := ui.NewTester(shell.View, 800, 480)
	tester.Type("i")

	if err := client.Command("call nvim_set_var('nvgui_prior_bug_rpc', v:true)"); err != nil {
		t.Fatalf("verify RPC connection: %v", err)
	}

	paste := "<lt>tag\\中文<Esc>\nsecond<lt>\\行\nthird¥！"
	clip.WriteText(paste)
	tester.SetClipboard(paste)
	if !shell.HandleEditCommand(nil, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyV, Mods: ui.Cmd}) {
		t.Fatal("HandleEditCommand(Cmd-V) = false, want true")
	}

	want := []string{"<lt>tag\\中文<Esc>", "second<lt>\\行", "third¥！"}
	deadline := time.Now().Add(5 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		tester.Frame()
		got = priorBugBufferLines(t, client)
		if reflect.DeepEqual(got, want) {
			break
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pasted buffer = %q, want %q", got, want)
	}
}
