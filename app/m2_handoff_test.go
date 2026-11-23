package app_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nvgui/app"
)

func m2ShortSocket(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "nvgui-s-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

func m2TestHandler(result app.HandoffResult, err error) func(context.Context, app.HandoffRequest) (app.HandoffResult, error) {
	return func(context.Context, app.HandoffRequest) (app.HandoffResult, error) {
		return result, err
	}
}

func TestM2HandoffStaleSocketIsRebound(t *testing.T) {
	sockPath := m2ShortSocket(t, "i.sock")
	raw, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen stale socket: %v", err)
	}
	if listener, ok := raw.(*net.UnixListener); ok {
		listener.SetUnlinkOnClose(false)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close stale listener: %v", err)
	}

	server, err := app.ListenInstance(sockPath, m2TestHandler(app.HandoffResult{WindowID: 7}, nil))
	if err != nil {
		t.Fatalf("ListenInstance over stale socket: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	result, forwarded, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{})
	if err != nil || !forwarded || result.WindowID != 7 {
		t.Fatalf("ForwardInstance = (%+v, %v, %v), want WindowID=7 forwarded=true", result, forwarded, err)
	}
	if info, statErr := os.Lstat(sockPath); statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket file mode after rebind: info=%v err=%v", info, statErr)
	}
}

func TestM2HandoffActiveInstanceIsNotOverwritten(t *testing.T) {
	sockPath := m2ShortSocket(t, "i.sock")
	first, err := app.ListenInstance(sockPath, m2TestHandler(app.HandoffResult{WindowID: 11}, nil))
	if err != nil {
		t.Fatalf("ListenInstance first: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if _, err := app.ListenInstance(sockPath, m2TestHandler(app.HandoffResult{}, nil)); err == nil {
		t.Fatal("ListenInstance overwrote an active instance socket")
	} else if !strings.Contains(err.Error(), "active") {
		t.Fatalf("active instance error = %v, want explicit active error", err)
	}
	result, forwarded, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{})
	if err != nil || !forwarded || result.WindowID != 11 {
		t.Fatalf("ForwardInstance after collision = (%+v, %v, %v), want WindowID=11", result, forwarded, err)
	}
}

func TestM2HandoffConcurrentForwardsAndErrorPropagation(t *testing.T) {
	sockPath := m2ShortSocket(t, "i.sock")
	var calls atomic.Int64
	server, err := app.ListenInstance(sockPath, func(_ context.Context, req app.HandoffRequest) (app.HandoffResult, error) {
		count := calls.Add(1)
		if req.NewWindow {
			return app.HandoffResult{}, errors.New("app: handler refused new window")
		}
		return app.HandoffResult{WindowID: uint64(count), Opened: len(req.Files)}, nil
	})
	if err != nil {
		t.Fatalf("ListenInstance: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	const workers = 16
	var group sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, forwarded, forwardErr := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
				Files: []string{"concurrent.txt"},
			})
			if forwardErr != nil {
				errs <- forwardErr
				return
			}
			if !forwarded || result.Opened != 1 || result.WindowID == 0 {
				errs <- errors.New("unexpected handoff result")
			}
		}()
	}
	group.Wait()
	close(errs)
	for forwardErr := range errs {
		t.Fatalf("concurrent ForwardInstance: %v", forwardErr)
	}
	if got := calls.Load(); got != workers {
		t.Fatalf("handler calls = %d, want %d", got, workers)
	}
	_, forwarded, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{NewWindow: true})
	if err == nil || forwarded || !strings.Contains(err.Error(), "refused new window") {
		t.Fatalf("ForwardInstance(handler error) = (%v, %v), want forwarded=false and remote error", forwarded, err)
	}
}

func TestM2HandoffDefaultSocketPathFallsBackOnLongBase(t *testing.T) {
	longBase := "/tmp/" + strings.Repeat("a", 120)
	got := app.DefaultInstanceSocketPath([]string{"TMPDIR=" + longBase})
	if len(got) > 100 || !strings.HasPrefix(got, "/tmp/nvgui-") {
		t.Fatalf("DefaultInstanceSocketPath(long TMPDIR) = %q (len=%d), want <=100-byte /tmp fallback", got, len(got))
	}
}

func TestM2HandoffResolveLoginPATHCacheAndCancellation(t *testing.T) {
	baseEnv := []string{"PATH=/usr/bin:/bin", "HOME=/tmp/nvgui-cache-home"}
	var calls atomic.Int64
	opts := app.PathProbeOptions{
		Shell: "/bin/zsh",
		Probe: func(context.Context, string, []string) (string, error) {
			calls.Add(1)
			return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin", nil
		},
	}
	first, probed := app.ResolveLoginPATH(context.Background(), baseEnv, opts)
	if !probed || !strings.Contains(m2HandoffEnvValue(first, "PATH"), "/opt/homebrew/bin") {
		t.Fatalf("first ResolveLoginPATH = (%v, %v), want merged Homebrew PATH", first, probed)
	}
	second, probed := app.ResolveLoginPATH(context.Background(), baseEnv, opts)
	if !probed || !strings.Contains(m2HandoffEnvValue(second, "PATH"), "/opt/homebrew/bin") {
		t.Fatalf("cached ResolveLoginPATH = (%v, %v), want merged Homebrew PATH", second, probed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want 1 cached call", got)
	}
	noCache, probed := app.ResolveLoginPATH(context.Background(), baseEnv, app.PathProbeOptions{
		Shell:   opts.Shell,
		Probe:   opts.Probe,
		NoCache: true,
	})
	if !probed || !strings.Contains(m2HandoffEnvValue(noCache, "PATH"), "/opt/homebrew/bin") {
		t.Fatalf("NoCache ResolveLoginPATH = (%v, %v), want merged Homebrew PATH", noCache, probed)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe calls after NoCache = %d, want 2", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	fallback, probed := app.ResolveLoginPATH(ctx, baseEnv, app.PathProbeOptions{
		NoCache: true,
		Probe: func(probeCtx context.Context, _ string, _ []string) (string, error) {
			<-probeCtx.Done()
			return "", probeCtx.Err()
		},
	})
	if probed || m2HandoffEnvValue(fallback, "PATH") != "/usr/bin:/bin" {
		t.Fatalf("canceled ResolveLoginPATH = (%v, %v), want original PATH and no probe", fallback, probed)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("canceled ResolveLoginPATH took %v, want immediate return", elapsed)
	}
}

func TestM2HandoffParseCLILaunchSourceAndPostSeparatorArgs(t *testing.T) {
	cwd := t.TempDir()
	env := []string{"PATH=/opt/homebrew/bin:/usr/bin:/bin", "HOME=" + cwd}
	headless, err := app.ParseCLILaunch([]string{"--headless", "--", "--doctor"}, env, cwd)
	if err != nil {
		t.Fatalf("ParseCLILaunch(--headless): %v", err)
	}
	if !headless.Request.Headless || headless.Source != app.LaunchSourceHeadless {
		t.Fatalf("headless launch = %+v, want Headless=true Source=headless", headless)
	}
	if strings.Join(headless.Request.Args, "\x00") != "--\x00--doctor" {
		t.Fatalf("headless Request.Args = %v, want post--- passthrough only", headless.Request.Args)
	}
	fileLaunch, err := app.ParseCLILaunch([]string{"-u", "NONE", "notes.md", "--", "--headless"}, env, cwd)
	if err != nil {
		t.Fatalf("ParseCLILaunch(file): %v", err)
	}
	if len(fileLaunch.Files) != 1 || filepath.Base(fileLaunch.Files[0]) != "notes.md" {
		t.Fatalf("ParseCLILaunch Files = %v, want only notes.md", fileLaunch.Files)
	}
	if fileLaunch.Source != app.LaunchSourceFile {
		t.Fatalf("ParseCLILaunch Source = %q, want %q", fileLaunch.Source, app.LaunchSourceFile)
	}
	dockEnv := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + cwd}
	dock, err := app.ParseCLILaunch(nil, dockEnv, cwd)
	if err != nil {
		t.Fatalf("ParseCLILaunch(dock): %v", err)
	}
	if dock.Source != app.LaunchSourceDock {
		t.Fatalf("ParseCLILaunch(dock) Source = %q, want %q", dock.Source, app.LaunchSourceDock)
	}
	if _, err := app.ParseCLILaunch([]string{"--socket="}, env, cwd); err == nil {
		t.Fatal("ParseCLILaunch(--socket=) returned nil error")
	}
}

func TestM2HandoffRelativeFilesAndArgsAreDeduplicated(t *testing.T) {
	sockPath := m2ShortSocket(t, "i.sock")
	workspace, request, _ := m2HandoffWorkspace(t)
	frontID, _, err := workspace.NewWindow(context.Background(), request)
	if err != nil {
		t.Fatalf("NewWindow(front): %v", err)
	}
	server, err := app.ListenInstance(sockPath, workspace.HandleHandoff)
	if err != nil {
		t.Fatalf("ListenInstance: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	result, err := workspace.HandleHandoff(context.Background(), app.HandoffRequest{
		Files:      []string{"deduplicated.txt"},
		Args:       []string{"--clean", "-n", "./deduplicated.txt"},
		WorkingDir: request.WorkingDir,
		Source:     app.LaunchSourceSingleInstance,
	})
	if err != nil {
		t.Fatalf("HandleHandoff(relative duplicate): %v", err)
	}
	if result.Opened != 1 || result.NewWindow || result.WindowID != frontID {
		t.Fatalf("HandleHandoff(relative duplicate) = %+v, want frontID=%d Opened=1", result, frontID)
	}
	forwardedResult, forwarded, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
		Files:      []string{"deduplicated.txt"},
		Args:       []string{"--clean", "-n", "./deduplicated.txt"},
		WorkingDir: request.WorkingDir,
	})
	if err != nil || !forwarded || forwardedResult.Opened != 1 {
		t.Fatalf("ForwardInstance(relative duplicate) = (%+v, %v, %v), want Opened=1", forwardedResult, forwarded, err)
	}
}

func m2HandoffWorkspace(t *testing.T) (*app.Workspace, app.LaunchRequest, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	request := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: t.TempDir(),
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	workspace := app.NewWorkspace(app.WorkspaceOptions{
		BaseRequest: request,
		Clock:       clock,
		Clipboard:   &fakeClipboard{},
		Headless:    true,
	})
	t.Cleanup(func() { _ = workspace.Close() })
	return workspace, request, clock
}
