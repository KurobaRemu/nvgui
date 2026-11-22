package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func writeResizePromptInit(test *testing.T, lines int) string {
	test.Helper()
	initPath := filepath.Join(test.TempDir(), "init.lua")
	var source strings.Builder
	for index := 1; index <= lines; index++ {
		fmt.Fprintf(&source, "vim.api.nvim_err_writeln('vim.pack: failed to load plugin %02d: network timeout while fetching repository; check the proxy and retry')\n", index)
	}
	if err := os.WriteFile(initPath, []byte(source.String()), 0o644); err != nil {
		test.Fatalf("write init.lua: %v", err)
	}
	return initPath
}

func startResizeTestShell(test *testing.T, initPath string) *Shell {
	test.Helper()
	args := []string{"--clean", "-n"}
	workingDir := test.TempDir()
	if initPath != "" {
		args = []string{"--clean", "-u", initPath, "-n"}
		workingDir = filepath.Dir(initPath)
	}
	req := LaunchRequest{
		Args:       args,
		Env:        withoutNVGUIHeadless(os.Environ()),
		WorkingDir: workingDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	shell, err := Start(context.Background(), req, nil, &testClipboard{})
	if err != nil {
		test.Fatalf("Start(headless): %v", err)
	}
	test.Cleanup(func() { _ = shell.Close() })
	return shell
}

func resizeMode(shell *Shell) (string, bool, error) {
	mode, err := shell.impl.session.Nvim().Mode()
	if err != nil {
		return "", false, err
	}
	if mode == nil {
		return "", false, fmt.Errorf("nvim mode is nil")
	}
	return mode.Mode, mode.Blocking, nil
}

func waitResizeMode(test *testing.T, shell *Shell, wantMode string, wantBlocking bool) {
	test.Helper()
	waitUntil(test, 5*time.Second, fmt.Sprintf("mode %q blocking=%v", wantMode, wantBlocking), func() bool {
		mode, blocking, err := resizeMode(shell)
		return err == nil && mode == wantMode && blocking == wantBlocking
	})
}

func resizeGridFor(shell *Shell, width, height int) (int, int) {
	cell := shell.impl.resolver.Cell()
	cols := cellsFor(width, cell.Width)
	rows := cellsFor(height, cell.Height)
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

func resizeEvalInt(test *testing.T, shell *Shell, expression string) int {
	test.Helper()
	var value int
	if err := shell.impl.session.Nvim().Eval(expression, &value); err != nil {
		test.Fatalf("Eval(%s): %v", expression, err)
	}
	return value
}

func assertSessionAlive(test *testing.T, shell *Shell) {
	test.Helper()
	select {
	case <-shell.SessionExited():
		test.Fatal("session exited unexpectedly")
	default:
	}
}

func assertResizeApplied(test *testing.T, shell *Shell, width, height int) {
	test.Helper()
	wantCols, wantRows := resizeGridFor(shell, width, height)
	waitUntil(test, 5*time.Second, fmt.Sprintf("resize %dx%d", wantCols, wantRows), func() bool {
		shell.impl.mu.Lock()
		lastCols, lastRows := shell.impl.lastColumns, shell.impl.lastRows
		shell.impl.mu.Unlock()
		return lastCols == wantCols && lastRows == wantRows &&
			resizeEvalInt(test, shell, "&columns") == wantCols &&
			resizeEvalInt(test, shell, "&lines") == wantRows
	})
}

func assertEditableBuffer(test *testing.T, tester *ui.Tester, shell *Shell) {
	test.Helper()
	tester.Type("iok")
	tester.Key(0, ui.KeyEscape)
	waitUntil(test, 5*time.Second, "edited buffer", func() bool {
		return bufferText(test, shell) == "ok"
	})
}

func assertCleanClose(test *testing.T, shell *Shell) {
	test.Helper()
	if err := shell.Close(); err != nil {
		test.Fatalf("Close: %v", err)
	}
	select {
	case <-shell.SessionExited():
	default:
		test.Fatal("SessionExited did not close after Close")
	}
}

func TestShellResizeNonBlockingDuringStartupHitEnterPrompt(test *testing.T) {
	shell := startResizeTestShell(test, writeResizePromptInit(test, 4))
	waitResizeMode(test, shell, "r", true)
	tester := ui.NewTester(shell.View, 1120, 660)
	tester.Frame()

	resizeStart := time.Now()
	for _, size := range [][2]int{{960, 540}, {1040, 600}, {1120, 660}} {
		if err := shell.Resize(size[0], size[1]); err != nil {
			test.Fatalf("Resize(%d, %d): %v", size[0], size[1], err)
		}
	}
	if elapsed := time.Since(resizeStart); elapsed >= 200*time.Millisecond {
		test.Fatalf("three Resize calls took %v, want < 200ms", elapsed)
	}
	assertSessionAlive(test, shell)

	tester.Key(0, ui.KeyEnter)
	waitResizeMode(test, shell, "n", false)
	assertResizeApplied(test, shell, 1120, 660)
	assertEditableBuffer(test, tester, shell)
	assertCleanClose(test, shell)
}

func TestShellResizeNonBlockingAcrossMoreToHitEnterTransition(test *testing.T) {
	shell := startResizeTestShell(test, writeResizePromptInit(test, 40))
	waitResizeMode(test, shell, "rm", true)
	tester := ui.NewTester(shell.View, 900, 520)
	tester.Frame()

	start := time.Now()
	if err := shell.Resize(900, 520); err != nil {
		test.Fatalf("Resize(900, 520): %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 200*time.Millisecond {
		test.Fatalf("Resize during -- More -- took %v, want < 200ms", elapsed)
	}
	assertSessionAlive(test, shell)

	tester.Type("G")
	waitResizeMode(test, shell, "r", true)
	start = time.Now()
	for _, size := range [][2]int{{980, 560}, {1020, 580}, {1080, 620}} {
		if err := shell.Resize(size[0], size[1]); err != nil {
			test.Fatalf("Resize(%d, %d): %v", size[0], size[1], err)
		}
	}
	if elapsed := time.Since(start); elapsed >= 200*time.Millisecond {
		test.Fatalf("three Resize calls during Press ENTER took %v, want < 200ms", elapsed)
	}
	assertSessionAlive(test, shell)

	tester.Key(0, ui.KeyEnter)
	waitResizeMode(test, shell, "n", false)
	assertResizeApplied(test, shell, 1080, 620)
	assertEditableBuffer(test, tester, shell)
	assertCleanClose(test, shell)
}

func TestShellResizeCoalescesRapidCallsAndCloseWhileBlocked(test *testing.T) {
	test.Run("close while resize blocked", func(test *testing.T) {
		shell := startResizeTestShell(test, writeResizePromptInit(test, 4))
		waitResizeMode(test, shell, "r", true)
		tester := ui.NewTester(shell.View, 1000, 600)
		tester.Frame()
		if err := shell.Resize(1000, 600); err != nil {
			test.Fatalf("Resize(1000, 600): %v", err)
		}
		waitUntil(test, 5*time.Second, "resize in flight", func() bool {
			shell.impl.mu.Lock()
			defer shell.impl.mu.Unlock()
			return shell.impl.resizeInFlight
		})
		if err := shell.Resize(1100, 640); err != nil {
			test.Fatalf("Resize(1100, 640): %v", err)
		}
		closeReturned := make(chan error, 1)
		go func() { closeReturned <- shell.Close() }()
		select {
		case err := <-closeReturned:
			if err != nil {
				test.Fatalf("Close: %v", err)
			}
		case <-time.After(3 * time.Second):
			test.Fatal("Close did not return within 3s")
		}
		select {
		case <-shell.SessionExited():
		default:
			test.Fatal("SessionExited did not close")
		}
		workerDone := make(chan struct{})
		go func() {
			shell.impl.wg.Wait()
			close(workerDone)
		}()
		select {
		case <-workerDone:
		case <-time.After(3 * time.Second):
			test.Fatal("shell goroutines did not stop")
		}
		if err := shell.Resize(800, 480); err == nil {
			test.Fatal("Resize after Close returned nil error")
		} else if !errors.Is(err, errShellClosed) {
			test.Fatalf("Resize after Close error = %v, want errShellClosed", err)
		}
	})

	test.Run("rapid coalescing and size fallback", func(test *testing.T) {
		shell := startResizeTestShell(test, "")
		tester := ui.NewTester(shell.View, 900, 500)
		tester.Frame()
		if err := shell.Resize(900, 500); err != nil {
			test.Fatalf("Resize(900, 500): %v", err)
		}
		assertResizeApplied(test, shell, 900, 500)
		for _, size := range [][2]int{{1000, 550}, {1100, 600}, {900, 500}, {1040, 580}} {
			if err := shell.Resize(size[0], size[1]); err != nil {
				test.Fatalf("Resize(%d, %d): %v", size[0], size[1], err)
			}
		}
		assertResizeApplied(test, shell, 1040, 580)
		assertCleanClose(test, shell)
	})
}
