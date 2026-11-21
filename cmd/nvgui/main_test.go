package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvgui/app"
)

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
