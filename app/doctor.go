package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nvgui/input"
)

const (
	defaultDoctorVersion  = "dev"
	defaultDoctorBundleID = "com.example.nvgui"
	defaultDoctorTimeout  = 1500 * time.Millisecond
)

func collectDoctor(ctx context.Context, opts DoctorOptions) DoctorReport {
	if ctx == nil {
		ctx = context.Background()
	}
	env := append([]string(nil), opts.Launch.Env...)
	if env == nil {
		env = os.Environ()
	}
	pathProbed := false
	if opts.ProbeLoginPATH {
		merged, probed := ResolveLoginPATH(ctx, env, opts.PathProbe)
		env = merged
		pathProbed = probed
	}
	path := envValue(env, "PATH")
	executablePath := opts.ExecutablePath
	if executablePath == "" {
		if resolved, err := os.Executable(); err == nil {
			executablePath = resolved
		}
	}
	bundleID := opts.BundleID
	if bundleID == "" {
		bundleID = envValue(env, "NVGUI_BUNDLE_ID")
	}
	if bundleID == "" && strings.Contains(executablePath, ".app/Contents/MacOS/") {
		bundleID = bundleIDFromInfoPlist(executablePath)
		if bundleID == "" {
			bundleID = defaultDoctorBundleID
		}
	}
	version := opts.Version
	if version == "" {
		version = defaultDoctorVersion
	}
	report := DoctorReport{
		Version:        version,
		PATH:           path,
		PathProbed:     pathProbed,
		WorkingDir:     opts.Launch.WorkingDir,
		LaunchSource:   opts.Source,
		BundleID:       bundleID,
		ExecutablePath: executablePath,
		SocketPath:     opts.SocketPath,
	}
	report.NvimPath = findNvimPath(opts.Launch, env)
	if report.NvimPath == "" {
		report.Warnings = append(report.Warnings, "nvim not found in PATH")
	} else {
		versionCtx, cancel := context.WithTimeout(ctx, defaultDoctorTimeout)
		cmd := exec.CommandContext(versionCtx, report.NvimPath, "--version")
		cmd.Env = env
		cmd.WaitDelay = 200 * time.Millisecond
		output, err := cmd.Output()
		cancel()
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("nvim --version: %v", err))
		} else {
			report.NvimVersion = firstOutputLine(string(output))
			if report.NvimVersion == "" {
				report.Warnings = append(report.Warnings, "nvim --version returned no output")
			}
		}
	}
	report.Squirrel, report.Warnings = collectSquirrelStatus(bundleID, executablePath, opts.SquirrelConfig, env, report.Warnings)
	report.ActiveWindows, report.Config = collectDoctorConfig(opts.Workspace, env)
	return report
}

func findNvimPath(launch LaunchRequest, env []string) string {
	lookup := launch
	lookup.Env = env
	return launchDoctor(lookup)["nvim"]
}

func firstOutputLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func bundleIDFromInfoPlist(executablePath string) string {
	plistPath := filepath.Join(filepath.Dir(filepath.Dir(executablePath)), "Info.plist")
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return ""
	}
	const key = "CFBundleIdentifier"
	index := strings.Index(string(data), "<key>"+key+"</key>")
	if index < 0 {
		return ""
	}
	rest := string(data)[index+len("<key>"+key+"</key>"):]
	start := strings.Index(rest, "<string>")
	end := strings.Index(rest, "</string>")
	if start < 0 || end < start+len("<string>") {
		return ""
	}
	return strings.TrimSpace(rest[start+len("<string>") : end])
}

func collectSquirrelStatus(bundleID, executablePath string, config []byte, env, warnings []string) (input.SquirrelBundleStatus, []string) {
	if len(config) == 0 {
		home := envValue(env, "HOME")
		if home == "" {
			if resolved, err := os.UserHomeDir(); err == nil {
				home = resolved
			}
		}
		if home != "" {
			for _, name := range []string{"squirrel.custom.yaml", "squirrel.yaml"} {
				data, err := os.ReadFile(filepath.Join(home, "Library", "Rime", name))
				if err == nil {
					config = data
					break
				}
			}
		}
	}
	if len(config) == 0 {
		return input.SquirrelBundleStatus{BundleID: bundleID}, warnings
	}
	options, err := input.ParseSquirrelAppOptions(config)
	if err != nil {
		return input.SquirrelBundleStatus{BundleID: bundleID}, append(warnings, fmt.Sprintf("Squirrel config: %v", err))
	}
	status := input.CheckSquirrelBundle(bundleID, executablePath, options)
	if status.Warning != "" {
		warnings = append(warnings, status.Warning)
	}
	return status, warnings
}

func collectDoctorConfig(workspace *Workspace, env []string) (int, NvimConfigSnapshot) {
	policy := defaultOpenInPolicy(env)
	if workspace == nil {
		return 0, nvimConfigSnapshotFrom(AnimationConfigFromEnv(env), policy)
	}
	_, front := workspace.Front()
	if front != nil {
		return workspace.WindowCount(), front.NvimConfig()
	}
	policy = workspace.OpenInPolicy()
	return workspace.WindowCount(), nvimConfigSnapshotFrom(AnimationConfigFromEnv(env), policy)
}

func defaultOpenInPolicy(env []string) OpenInPolicy {
	if parsed, ok := parseOpenInPolicy(envValue(env, "NVGUI_OPEN_IN")); ok {
		return parsed
	}
	return OpenInFront
}

func formatDoctorReportText(report DoctorReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nvgui: %s\n", report.Version)
	fmt.Fprintf(&b, "nvim: %s", report.NvimPath)
	if report.NvimVersion != "" {
		fmt.Fprintf(&b, " (%s)", report.NvimVersion)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "PATH: %s (probed: %t)\n", report.PATH, report.PathProbed)
	fmt.Fprintf(&b, "WorkingDir: %s\n", report.WorkingDir)
	fmt.Fprintf(&b, "LaunchSource: %s\n", report.LaunchSource)
	fmt.Fprintf(&b, "ExecutablePath: %s\n", report.ExecutablePath)
	fmt.Fprintf(&b, "BundleID: %s\n", report.BundleID)
	if report.SocketPath != "" {
		fmt.Fprintf(&b, "SocketPath: %s\n", report.SocketPath)
	}
	fmt.Fprintf(&b, "ActiveWindows: %d\n", report.ActiveWindows)
	fmt.Fprintf(&b, "Squirrel: bundle_id=%s matched=%t cli_mismatch=%t", report.Squirrel.BundleID, report.Squirrel.Matched, report.Squirrel.CLIMismatch)
	if report.Squirrel.Warning != "" {
		fmt.Fprintf(&b, " warning=%s", report.Squirrel.Warning)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "Config: cursor_animation=%t cursor_animation_length=%g cursor_animate_command_line=%t cursor_animate_insert_mode=%t scroll_animation=%t scroll_animation_length=%g ligatures=%t open_in=%s\n",
		report.Config.CursorAnimation,
		report.Config.CursorAnimationLength,
		report.Config.CursorAnimateCommandLine,
		report.Config.CursorAnimateInsertMode,
		report.Config.ScrollAnimation,
		report.Config.ScrollAnimationLength,
		report.Config.Ligatures,
		report.Config.OpenIn,
	)
	if len(report.Warnings) > 0 {
		b.WriteString("Warnings:\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	return b.String()
}

func formatDoctorReportJSON(report DoctorReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}
