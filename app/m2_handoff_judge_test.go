package app_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"nvgui/app"
	"nvgui/diag"
	"nvgui/screen"
)

func m2HandoffWaitUntil(timeout time.Duration, cond func() bool) bool {
	if cond() {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			return cond()
		case <-ticker.C:
			if cond() {
				return true
			}
		}
	}
}

func m2HandoffReplayLatest(t *testing.T, shell *app.Shell) *screen.State {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := shell.ExportDiag(dir); err != nil {
		return nil
	}
	states, err := diag.Replay(dir)
	if err != nil || len(states) == 0 {
		return nil
	}
	return states[len(states)-1]
}

func m2HandoffWaitForText(t *testing.T, shell *app.Shell, clock *fakeClock, tester *ui.Tester, needle string) {
	t.Helper()
	ok := m2HandoffWaitUntil(5*time.Second, func() bool {
		if clock != nil {
			clock.Advance(10 * time.Millisecond)
		}
		if tester != nil {
			tester.Frame()
		}
		st := m2HandoffReplayLatest(t, shell)
		return st != nil && m4StateHasText(st, needle)
	})
	if !ok {
		t.Fatalf("timed out waiting for %q in shell screen state", needle)
	}
}

func m2HandoffHasUnescapedChar(s string, target byte) bool {
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == target {
			return true
		}
	}
	return false
}

func m2HandoffVimSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func m2HandoffEnvValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], prefix); ok {
			return v
		}
	}
	return ""
}

func TestM2Handoff_ParseCLILaunchAndFormatDropCommandSafety(t *testing.T) {
	workDir := t.TempDir()
	env := []string{"PATH=/opt/homebrew/bin:/usr/bin:/bin", "HOME=" + workDir}

	cli, err := app.ParseCLILaunch(
		[]string{"--new-window", "--no-single-instance", "--socket=/tmp/custom-nvgui.sock", "-u", "NONE", "foo.txt", "--", "--doctor", "-bar.txt"},
		env,
		workDir,
	)
	if err != nil {
		t.Fatalf("ParseCLILaunch: %v", err)
	}
	if !cli.NewWindow || !cli.NoSingleInstance || cli.Doctor || cli.DoctorJSON {
		t.Fatalf("ParseCLILaunch flags = %+v, want NewWindow=true NoSingleInstance=true Doctor=false DoctorJSON=false", cli)
	}
	if cli.SocketPath != "/tmp/custom-nvgui.sock" {
		t.Fatalf("ParseCLILaunch SocketPath = %q, want /tmp/custom-nvgui.sock", cli.SocketPath)
	}
	if len(cli.Files) != 1 || (cli.Files[0] != "foo.txt" && cli.Files[0] != filepath.Join(workDir, "foo.txt")) {
		t.Fatalf("ParseCLILaunch Files = %v, want single foo.txt (not -u value NONE or post--- args)", cli.Files)
	}
	hasPassthroughDoctor := false
	hasPassthroughBar := false
	for _, arg := range cli.Request.Args {
		if arg == "--new-window" || arg == "--no-single-instance" || strings.HasPrefix(arg, "--socket") {
			t.Fatalf("ParseCLILaunch leaked nvgui flag %q into Request.Args: %v", arg, cli.Request.Args)
		}
		if arg == "--doctor" {
			hasPassthroughDoctor = true
		}
		if arg == "-bar.txt" {
			hasPassthroughBar = true
		}
	}
	if !hasPassthroughDoctor || !hasPassthroughBar {
		t.Fatalf("ParseCLILaunch did not preserve post--- args in Request.Args: %v", cli.Request.Args)
	}

	docCLI, err := app.ParseCLILaunch([]string{"--doctor", "--doctor-json"}, env, workDir)
	if err != nil {
		t.Fatalf("ParseCLILaunch(--doctor --doctor-json): %v", err)
	}
	if !docCLI.Doctor || !docCLI.DoctorJSON {
		t.Fatalf("ParseCLILaunch doctor flags = (Doctor:%v, DoctorJSON:%v), want both true", docCLI.Doctor, docCLI.DoctorJSON)
	}

	if _, err := app.ParseCLILaunch([]string{"--socket"}, env, workDir); err == nil {
		t.Fatal("ParseCLILaunch(--socket without value) returned nil error, want error")
	}

	if pol, ok := app.ParseOpenInPolicy(" FRONT "); !ok || pol != app.OpenInFront {
		t.Fatalf("ParseOpenInPolicy(\" FRONT \") = (%q, %v), want (front, true)", pol, ok)
	}
	if pol, ok := app.ParseOpenInPolicy("New"); !ok || pol != app.OpenInNew {
		t.Fatalf("ParseOpenInPolicy(\"New\") = (%q, %v), want (new, true)", pol, ok)
	}
	if pol, ok := app.ParseOpenInPolicy("invalid"); ok || pol != "" {
		t.Fatalf("ParseOpenInPolicy(\"invalid\") = (%q, %v), want (\"\", false)", pol, ok)
	}

	// FormatDropCommand safety checks.
	for _, bad := range [][]string{
		nil,
		{},
		{""},
		{"bad\x00path.txt"},
		{"bad\npath.txt"},
		{"bad\rpath.txt"},
	} {
		if _, err := app.FormatDropCommand(bad); err == nil {
			t.Fatalf("FormatDropCommand(%q) returned nil error, want error", bad)
		}
	}

	tricky := `/tmp/my dir/a"b'c%#$|\中文.txt`
	cmd, err := app.FormatDropCommand([]string{tricky})
	if err != nil {
		t.Fatalf("FormatDropCommand(tricky): %v", err)
	}
	if !strings.HasPrefix(cmd, "drop ") {
		t.Fatalf("FormatDropCommand = %q, want prefix \"drop \"", cmd)
	}
	escapedArg := strings.TrimPrefix(cmd, "drop ")
	if !strings.Contains(escapedArg, "中文.txt") {
		t.Fatalf("FormatDropCommand lost CJK filename: %q", cmd)
	}
	for _, ch := range []byte{' ', '"', '\'', '%', '#', '$', '|'} {
		if m2HandoffHasUnescapedChar(escapedArg, ch) {
			t.Fatalf("FormatDropCommand left unescaped %q in %q", ch, cmd)
		}
	}

	// Real headless Shell.DropFiles check with literal Ex-injection filename.
	dropDir := t.TempDir()
	injectedMarker := filepath.Join(dropDir, "injected.marker")
	initLua := filepath.Join(dropDir, "init.lua")
	initSrc := "vim.cmd([[\nfunction! g:nvgui_injected()\n  call writefile(['INJECTED'], " + m2HandoffVimSingleQuote(injectedMarker) + ")\nendfunction\n]])\n"
	if err := os.WriteFile(initLua, []byte(initSrc), 0o644); err != nil {
		t.Fatalf("write init.lua: %v", err)
	}

	evilName := "note file |call g:nvgui_injected().txt"
	evilPath := filepath.Join(dropDir, evilName)
	if err := os.WriteFile(evilPath, []byte("m2_drop_payload_token_42\n"), 0o644); err != nil {
		t.Fatalf("write evil file: %v", err)
	}

	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	shell, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-u", initLua, "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: dropDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}, clock, &fakeClipboard{})
	if err != nil {
		t.Fatalf("app.Start(headless): %v", err)
	}
	t.Cleanup(func() {
		_ = shell.Close()
		if exited := shell.SessionExited(); exited != nil {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-exited:
			case <-timer.C:
			}
		}
	})

	tester := ui.NewTester(shell.View, 800, 480)
	if !m2HandoffWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		img := tester.Image()
		return img != nil && !m4IsUniformImage(img.Pix)
	}) {
		t.Fatal("Shell never produced initial boot frame")
	}

	if err := shell.DropFiles(context.Background(), []string{"bad\npath"}); err == nil {
		t.Fatal("DropFiles with newline path returned nil error, want error")
	}
	if err := shell.DropFiles(context.Background(), []string{evilPath}); err != nil {
		t.Fatalf("DropFiles(evilPath): %v", err)
	}

	m2HandoffWaitForText(t, shell, clock, tester, "m2_drop_payload_token_42")

	// Verify exact buffer name, zero leftover empty buffer, and zero command injection.
	probeOut := filepath.Join(dropDir, "drop_probe.txt")
	tester.Key(0, ui.KeyEscape)
	tester.Type(":call writefile([string(len(getbufinfo({'buflisted':1}))), bufname('%')], " + m2HandoffVimSingleQuote(probeOut) + ")")
	tester.Key(0, ui.KeyEnter)

	if !m2HandoffWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		_, statErr := os.Stat(probeOut)
		return statErr == nil
	}) {
		t.Fatal("timed out waiting for nvim buffer probe output")
	}

	rawProbe, err := os.ReadFile(probeOut)
	if err != nil {
		t.Fatalf("read drop_probe.txt: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawProbe)), "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected probe output: %q", string(rawProbe))
	}
	if strings.TrimSpace(lines[0]) != "1" {
		t.Fatalf("listed buffer count after DropFiles = %q, want \"1\" (no leftover empty buffer)", lines[0])
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), evilName) {
		t.Fatalf("current bufname = %q, want suffix %q", lines[1], evilName)
	}
	if _, err := os.Stat(injectedMarker); err == nil {
		t.Fatal("Ex command injection executed g:nvgui_injected()!")
	}
}

func TestM2Handoff_SingleInstanceSocketAndOpenInPolicy(t *testing.T) {
	// Verify DefaultInstanceSocketPath stays below macOS 104-byte sun_path limit.
	longTmp := "/tmp/" + strings.Repeat("a", 100)
	defSock := app.DefaultInstanceSocketPath([]string{"TMPDIR=" + longTmp})
	if defSock == "" || len(defSock) >= 104 {
		t.Fatalf("DefaultInstanceSocketPath with long TMPDIR = %q (len=%d), want non-empty and < 104 bytes", defSock, len(defSock))
	}
	if custom := app.DefaultInstanceSocketPath([]string{"NVGUI_SOCKET=/tmp/nvgui-override.sock"}); custom != "/tmp/nvgui-override.sock" {
		t.Fatalf("DefaultInstanceSocketPath(NVGUI_SOCKET) = %q, want /tmp/nvgui-override.sock", custom)
	}

	sockDir, err := os.MkdirTemp("/tmp", "nvgui-s-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "i.sock")

	workDir := t.TempDir()
	file1 := filepath.Join(workDir, "front_open.txt")
	file2 := filepath.Join(workDir, "new_win_flag.txt")
	file3 := filepath.Join(workDir, "new_win_policy.txt")
	for p, content := range map[string]string{
		file1: "handoff_front_token_1\n",
		file2: "handoff_newwin_token_2\n",
		file3: "handoff_policy_token_3\n",
	} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	baseReq := app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: workDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}
	ws := app.NewWorkspace(app.WorkspaceOptions{
		BaseRequest: baseReq,
		Clock:       clock,
		Clipboard:   &fakeClipboard{},
		Headless:    true,
	})
	if ws == nil {
		t.Fatal("NewWorkspace returned nil")
	}
	t.Cleanup(func() { _ = ws.Close() })

	id1, _, err := ws.NewWindow(context.Background(), baseReq)
	if err != nil {
		t.Fatalf("NewWindow #1: %v", err)
	}
	if got := ws.OpenInPolicy(); got != app.OpenInFront {
		t.Fatalf("default Workspace.OpenInPolicy() = %q, want %q", got, app.OpenInFront)
	}

	srv, err := app.ListenInstance(sockPath, ws.HandleHandoff)
	if err != nil {
		t.Fatalf("ListenInstance(%q): %v", sockPath, err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if srv.SocketPath() != sockPath {
		t.Fatalf("InstanceServer.SocketPath() = %q, want %q", srv.SocketPath(), sockPath)
	}

	// Default OpenInFront reuses front window.
	res1, forwarded1, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
		Files:      []string{file1},
		WorkingDir: workDir,
		Source:     app.LaunchSourceSingleInstance,
	})
	if err != nil || !forwarded1 {
		t.Fatalf("ForwardInstance #1 = (%+v, %v, %v), want forwarded=true err=nil", res1, forwarded1, err)
	}
	if res1.NewWindow || res1.WindowID != id1 || res1.Opened != 1 {
		t.Fatalf("ForwardInstance #1 result = %+v, want NewWindow=false WindowID=%d Opened=1", res1, id1)
	}
	if got := ws.WindowCount(); got != 1 {
		t.Fatalf("WindowCount after front handoff = %d, want 1", got)
	}

	// Explicit NewWindow: true opens a new window.
	res2, forwarded2, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
		Files:      []string{file2},
		WorkingDir: workDir,
		NewWindow:  true,
		Source:     app.LaunchSourceSingleInstance,
	})
	if err != nil || !forwarded2 {
		t.Fatalf("ForwardInstance #2 = (%+v, %v, %v), want forwarded=true err=nil", res2, forwarded2, err)
	}
	if !res2.NewWindow || res2.WindowID == 0 || res2.WindowID == id1 {
		t.Fatalf("ForwardInstance #2 result = %+v, want NewWindow=true and distinct WindowID", res2)
	}
	if got := ws.WindowCount(); got != 2 {
		t.Fatalf("WindowCount after NewWindow handoff = %d, want 2", got)
	}

	// SetOpenInPolicy(OpenInNew) opens a new window even when req.NewWindow == false.
	ws.SetOpenInPolicy(app.OpenInNew)
	if got := ws.OpenInPolicy(); got != app.OpenInNew {
		t.Fatalf("OpenInPolicy after SetOpenInPolicy(OpenInNew) = %q, want %q", got, app.OpenInNew)
	}
	res3, forwarded3, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
		Files:      []string{file3},
		WorkingDir: workDir,
		Source:     app.LaunchSourceSingleInstance,
	})
	if err != nil || !forwarded3 {
		t.Fatalf("ForwardInstance #3 = (%+v, %v, %v), want forwarded=true err=nil", res3, forwarded3, err)
	}
	if !res3.NewWindow || res3.WindowID == 0 || res3.WindowID == res2.WindowID {
		t.Fatalf("ForwardInstance #3 result = %+v, want NewWindow=true and distinct WindowID", res3)
	}
	if got := ws.WindowCount(); got != 3 {
		t.Fatalf("WindowCount after OpenInNew handoff = %d, want 3", got)
	}

	// Closing server causes ForwardInstance to report forwarded=false, err=nil.
	if err := srv.Close(); err != nil {
		t.Fatalf("InstanceServer.Close(): %v", err)
	}
	_, forwardedAfter, err := app.ForwardInstance(context.Background(), sockPath, app.HandoffRequest{
		Files: []string{file1},
	})
	if err != nil || forwardedAfter {
		t.Fatalf("ForwardInstance after Close = (forwarded=%v, err=%v), want (false, nil)", forwardedAfter, err)
	}

	// Stale dead socket on disk must be cleaned up and rebound cleanly.
	stalePath := filepath.Join(sockDir, "d.sock")
	rawLn, err := net.Listen("unix", stalePath)
	if err != nil {
		t.Fatalf("net.Listen stale socket: %v", err)
	}
	if ul, ok := rawLn.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = rawLn.Close()

	srv2, err := app.ListenInstance(stalePath, ws.HandleHandoff)
	if err != nil {
		t.Fatalf("ListenInstance over stale dead socket failed: %v", err)
	}
	if err := srv2.Close(); err != nil {
		t.Fatalf("srv2.Close(): %v", err)
	}
}

func TestM2Handoff_UIElementDropFilesAndLoginPathProbe(t *testing.T) {
	// Part 1: ui.Tester.DropFiles opens dropped file in current Shell.
	workDir := t.TempDir()
	dropFile := filepath.Join(workDir, "dropped_from_finder.txt")
	if err := os.WriteFile(dropFile, []byte("m2_ui_drop_file_token_99\n"), 0o644); err != nil {
		t.Fatalf("write dropFile: %v", err)
	}

	clock := &fakeClock{now: time.Unix(1_700_000_000, 0).UTC()}
	shell, err := app.Start(context.Background(), app.LaunchRequest{
		Args:       []string{"--clean", "-n"},
		Env:        m4FilteredEnv(),
		WorkingDir: workDir,
		Width:      80,
		Height:     24,
		Headless:   true,
	}, clock, &fakeClipboard{})
	if err != nil {
		t.Fatalf("app.Start(headless): %v", err)
	}
	t.Cleanup(func() {
		_ = shell.Close()
		if exited := shell.SessionExited(); exited != nil {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-exited:
			case <-timer.C:
			}
		}
	})

	tester := ui.NewTester(shell.View, 800, 480)
	if !m2HandoffWaitUntil(5*time.Second, func() bool {
		tester.Frame()
		img := tester.Image()
		return img != nil && !m4IsUniformImage(img.Pix)
	}) {
		t.Fatal("Shell never produced initial boot frame")
	}

	if ok := tester.DropFiles(100, 100, dropFile); !ok {
		t.Fatal("tester.DropFiles(100, 100, dropFile) returned false, want Shell input element to accept dropped files")
	}
	m2HandoffWaitForText(t, shell, clock, tester, "m2_ui_drop_file_token_99")

	// Part 2: ResolveLoginPATH merges probed Homebrew path and never blocks on hanging shell.
	baseEnv := []string{"PATH=/usr/bin:/bin", "HOME=" + workDir}
	merged, probed := app.ResolveLoginPATH(context.Background(), baseEnv, app.PathProbeOptions{
		NoCache: true,
		Probe: func(ctx context.Context, shell string, env []string) (string, error) {
			return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin", nil
		},
	})
	if !probed {
		t.Fatal("ResolveLoginPATH(minimal PATH) returned probed=false, want true")
	}
	gotPath := m2HandoffEnvValue(merged, "PATH")
	if !strings.Contains(gotPath, "/opt/homebrew/bin") || !strings.Contains(gotPath, "/usr/bin") || !strings.Contains(gotPath, "/bin") {
		t.Fatalf("ResolveLoginPATH merged PATH = %q, want /opt/homebrew/bin + /usr/bin + /bin", gotPath)
	}

	start := time.Now()
	fallback, probedHang := app.ResolveLoginPATH(context.Background(), baseEnv, app.PathProbeOptions{
		Timeout: 100,
		NoCache: true,
		Probe: func(ctx context.Context, shell string, env []string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	elapsed := time.Since(start)
	if probedHang {
		t.Fatal("ResolveLoginPATH(hanging probe) returned probed=true, want false")
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("ResolveLoginPATH(hanging probe, 100ms timeout) took %v, want fast bounded return", elapsed)
	}
	if got := m2HandoffEnvValue(fallback, "PATH"); got != "/usr/bin:/bin" {
		t.Fatalf("ResolveLoginPATH fallback PATH = %q, want original \"/usr/bin:/bin\"", got)
	}
}
