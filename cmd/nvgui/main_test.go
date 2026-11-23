package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nvgui/app"
)

type fakeWorkspace struct {
	mu    sync.Mutex
	calls [][]string
	gate  chan struct{}
}

func (workspace *fakeWorkspace) OpenFiles(_ context.Context, paths []string, _ string, _ app.LaunchSource) (app.HandoffResult, error) {
	workspace.mu.Lock()
	workspace.calls = append(workspace.calls, append([]string(nil), paths...))
	gate := workspace.gate
	workspace.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-time.After(2 * time.Second):
		}
	}
	return app.HandoffResult{Opened: len(paths)}, nil
}

func (workspace *fakeWorkspace) snapshot() [][]string {
	workspace.mu.Lock()
	defer workspace.mu.Unlock()
	calls := make([][]string, len(workspace.calls))
	for index, paths := range workspace.calls {
		calls[index] = append([]string(nil), paths...)
	}
	return calls
}

func embedNvimPIDs(test *testing.T) map[string]bool {
	test.Helper()
	output, err := exec.Command("pgrep", "-f", "--", "--embed").Output()
	pids := map[string]bool{}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return pids
		}
		test.Fatalf("pgrep --embed nvim: %v", err)
	}
	for _, pid := range strings.Fields(string(output)) {
		command, err := exec.Command("ps", "-p", pid, "-o", "command=").Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(command), "nvim") && strings.Contains(string(command), "--embed") {
			pids[pid] = true
		}
	}
	return pids
}

func assertNoLeakedNvim(test *testing.T, before, after map[string]bool) {
	test.Helper()
	for pid := range after {
		if !before[pid] {
			test.Errorf("leaked --embed nvim PID %s", pid)
		}
	}
}

func waitForNewNvimPIDsGone(test *testing.T, before map[string]bool) map[string]bool {
	test.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		after := embedNvimPIDs(test)
		stable := true
		for pid := range after {
			if !before[pid] {
				stable = false
				break
			}
		}
		if stable {
			return after
		}
		select {
		case <-deadline.C:
			return after
		case <-ticker.C:
		}
	}
}

func assertNoNewNvimPIDs(test *testing.T, before map[string]bool, call func() int) int {
	test.Helper()
	result := call()
	after := waitForNewNvimPIDsGone(test, before)
	assertNoLeakedNvim(test, before, after)
	return result
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

func TestMainWithDoctorTextAndJSONNoNvimSpawn(test *testing.T) {
	env := withoutNVGUIHeadless(os.Environ())
	env = append(env, "HOME="+test.TempDir())
	workDir := test.TempDir()
	for _, args := range [][]string{{"--doctor"}, {"--doctor-json"}} {
		before := embedNvimPIDs(test)
		var stdout, stderr bytes.Buffer
		got := mainWithIO(args, env, workDir, &stdout, &stderr)
		after := waitForNewNvimPIDsGone(test, before)
		assertNoLeakedNvim(test, before, after)
		if got != 0 {
			test.Fatalf("mainWithIO(%v) = %d, want 0; stderr=%q", args, got, stderr.String())
		}
		output := stdout.String()
		if output == "" {
			test.Fatalf("mainWithIO(%v) produced no stdout", args)
		}
		if args[0] == "--doctor-json" {
			var report app.DoctorReport
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				test.Fatalf("--doctor-json stdout is invalid JSON: %v\n%s", err, output)
			}
			if !strings.Contains(output, "nvim_path") || !strings.Contains(output, "launch_source") || !strings.Contains(output, "active_windows") {
				test.Fatalf("--doctor-json is missing report fields:\n%s", output)
			}
			continue
		}
		for _, needle := range []string{"nvgui:", "nvim:", "PATH:", "WorkingDir:", "LaunchSource:", "BundleID:", "ActiveWindows:", "Squirrel:", "Config:"} {
			if !strings.Contains(output, needle) {
				test.Fatalf("--doctor text missing %q:\n%s", needle, output)
			}
		}
	}
}

func TestMainWithHeadlessNoGUI(test *testing.T) {
	env := withoutNVGUIHeadless(os.Environ())
	env = append(env, "NVGUI_HEADLESS=0", "NVGUI_HEADLESS=1")
	before := embedNvimPIDs(test)
	got := assertNoNewNvimPIDs(test, before, func() int {
		return mainWith([]string{"--headless"}, env, test.TempDir())
	})
	if got != 0 {
		test.Fatalf("mainWith(--headless) = %d, want 0", got)
	}
}

func TestMainWithEnvironmentHeadless(test *testing.T) {
	env := withoutNVGUIHeadless(os.Environ())
	env = append(env, "NVGUI_HEADLESS=1")
	before := embedNvimPIDs(test)
	got := assertNoNewNvimPIDs(test, before, func() int {
		return mainWith([]string{}, env, test.TempDir())
	})
	if got != 0 {
		test.Fatalf("mainWith(NVGUI_HEADLESS=1) = %d, want 0", got)
	}
}

func TestMainWithInvalidWorkingDir(test *testing.T) {
	env := withoutNVGUIHeadless(os.Environ())
	env = append(env, "NVGUI_HEADLESS=1")
	missing := filepath.Join(test.TempDir(), "missing")
	if got := mainWith([]string{"--headless"}, env, missing); got != 1 {
		test.Fatalf("mainWith(invalid cwd) = %d, want 1", got)
	}
}

func TestParseLaunchNewWindowIsNotHeadless(test *testing.T) {
	env := withoutNVGUIHeadless(os.Environ())
	req, err := app.ParseLaunch([]string{"--new-window", "--", "--headless"}, env, test.TempDir())
	if err != nil {
		test.Fatalf("ParseLaunch: %v", err)
	}
	if req.Headless {
		test.Fatal("--new-window set Headless=true")
	}
	wantArgs := []string{"--", "--headless"}
	if strings.Join(req.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		test.Fatalf("Args = %v, want %v", req.Args, wantArgs)
	}
}

func TestMainDoesNotDiscardExitCode(test *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		test.Fatalf("read main.go: %v", err)
	}
	source := string(data)
	if !strings.Contains(source, "os.Exit(mainWith(nil, nil, \"\"))") {
		test.Error("main() does not pass mainWith exit code to os.Exit")
	}
	if strings.Contains(source, "head"+"lessLaunch") {
		test.Error("main.go still derives headless state outside ParseLaunch")
	}
}

func TestOpenFileDispatcherWaitsForFirstWindow(test *testing.T) {
	workspace := &fakeWorkspace{}
	dispatcher := newOpenFileDispatcher(workspace, "", func(error) {})
	test.Cleanup(dispatcher.close)
	dispatcher.onOpenFile("cold start.txt")
	dispatcher.onOpenFile("queued file.txt")
	if paths := dispatcher.queue.pending(); len(paths) != 2 {
		test.Fatalf("cold-start queue = %v, want both paths retained", paths)
	}
	if calls := workspace.snapshot(); len(calls) != 0 {
		test.Fatalf("OpenFiles before first window = %v, want none", calls)
	}
	dispatcher.onWindowAdded()
	dispatcher.onAppReady()
	if !testWaitUntil(2*time.Second, func() bool { return len(workspace.snapshot()) == 1 }) {
		test.Fatal("background worker did not flush queued Open With files")
	}
	calls := workspace.snapshot()
	if len(calls) != 1 || strings.Join(calls[0], "\x00") != "cold start.txt\x00queued file.txt" {
		test.Fatalf("OpenFiles after first window = %v, want one batched call", calls)
	}
	dispatcher.onOpenFile("running app.txt")
	if !testWaitUntil(2*time.Second, func() bool { return len(workspace.snapshot()) == 2 }) {
		test.Fatal("background worker did not process running-app Open With file")
	}
	calls = workspace.snapshot()
	if len(calls) != 2 || len(calls[1]) != 1 || calls[1][0] != "running app.txt" {
		test.Fatalf("OpenFiles after ready = %v, want immediate second call", calls)
	}
}

func TestOpenFileDispatcherCallbacksNeverBlock(test *testing.T) {
	workspace := &fakeWorkspace{gate: make(chan struct{})}
	reported := make(chan error, 1)
	dispatcher := newOpenFileDispatcher(workspace, "", func(err error) { reported <- err })
	var release sync.Once
	releaseGate := func() { release.Do(func() { close(workspace.gate) }) }
	test.Cleanup(func() {
		releaseGate()
		dispatcher.close()
	})
	dispatcher.onWindowAdded()
	dispatcher.onAppReady()
	dispatcher.onOpenFile("first.txt")
	if !testWaitUntil(2*time.Second, func() bool { return len(workspace.snapshot()) == 1 }) {
		test.Fatal("worker did not start first OpenFiles call")
	}
	callbackDone := make(chan struct{})
	go func() {
		dispatcher.onOpenFile("second.txt")
		dispatcher.onAppReady()
		close(callbackDone)
	}()
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		test.Fatal("OnOpenFile callback blocked behind a running OpenFiles RPC")
	}
	releaseGate()
	if !testWaitUntil(2*time.Second, func() bool { return len(workspace.snapshot()) == 2 }) {
		test.Fatal("worker did not process queued callback after RPC release")
	}
	select {
	case err := <-reported:
		test.Fatalf("unexpected OpenFiles error: %v", err)
	default:
	}
}

func TestOpenFileDispatcherReportsErrorsAndRecovers(test *testing.T) {
	workspace := &failingWorkspace{}
	reported := make(chan error, 1)
	dispatcher := newOpenFileDispatcher(workspace, "", func(err error) { reported <- err })
	test.Cleanup(dispatcher.close)
	dispatcher.onWindowAdded()
	dispatcher.onAppReady()
	dispatcher.onOpenFile("fails.txt")
	select {
	case err := <-reported:
		if !errors.Is(err, errWorkspaceOpenFiles) {
			test.Fatalf("reported error = %v, want %v", err, errWorkspaceOpenFiles)
		}
	case <-time.After(2 * time.Second):
		test.Fatal("worker did not report OpenFiles error")
	}
}

type failingWorkspace struct{}

var errWorkspaceOpenFiles = errors.New("open files failed")

func (workspace *failingWorkspace) OpenFiles(context.Context, []string, string, app.LaunchSource) (app.HandoffResult, error) {
	return app.HandoffResult{}, errWorkspaceOpenFiles
}

func TestNoSingleInstanceSkipsListenConflict(test *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "nvgui-s-*")
	if err != nil {
		test.Fatalf("MkdirTemp: %v", err)
	}
	test.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "i.sock")
	server, err := app.ListenInstance(socketPath, func(context.Context, app.HandoffRequest) (app.HandoffResult, error) {
		return app.HandoffResult{WindowID: 41}, nil
	})
	if err != nil {
		test.Fatalf("ListenInstance(active owner): %v", err)
	}
	test.Cleanup(func() { _ = server.Close() })

	source, err := os.ReadFile("main.go")
	if err != nil {
		test.Fatalf("read main.go: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "if !cli.Request.Headless && !cli.NoSingleInstance {") {
		test.Fatal("main.go forwards single-instance requests without guarding --no-single-instance")
	}
	if !strings.Contains(text, "if !cli.NoSingleInstance {") || !strings.Contains(text, "app.ListenInstance(socketPath, ws.HandleHandoff)") {
		test.Fatal("main.go does not guard ListenInstance with --no-single-instance")
	}
	if strings.Count(text, "app.ListenInstance(socketPath, ws.HandleHandoff)") != 1 {
		test.Fatal("main.go must listen on exactly one guarded path")
	}
	env := append(withoutNVGUIHeadless(os.Environ()), "NVGUI_HEADLESS=0", "NVGUI_SOCKET="+socketPath)
	before := embedNvimPIDs(test)
	exitCode := assertNoNewNvimPIDs(test, before, func() int {
		return mainWith([]string{"--headless", "--no-single-instance"}, env, test.TempDir())
	})
	if exitCode != 0 {
		test.Fatalf("mainWith(--no-single-instance) with active owner = %d, want 0", exitCode)
	}
	result, forwarded, err := app.ForwardInstance(context.Background(), socketPath, app.HandoffRequest{})
	if err != nil || !forwarded || result.WindowID != 41 {
		test.Fatalf("active owner ForwardInstance = (%+v, %v, %v), want WindowID=41", result, forwarded, err)
	}
}

func testWaitUntil(timeout time.Duration, condition func() bool) bool {
	if condition() {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			return condition()
		case <-ticker.C:
			if condition() {
				return true
			}
		}
	}
}
