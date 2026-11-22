package app

import (
	"context"

	"nvgui/input"
	"nvgui/render"
)

type NvimConfigSnapshot struct {
	CursorAnimation          bool         `json:"nvgui_cursor_animation"`
	CursorAnimationLength    float32      `json:"nvgui_cursor_animation_length"`
	CursorAnimateCommandLine bool         `json:"nvgui_cursor_animate_command_line"`
	CursorAnimateInsertMode  bool         `json:"nvgui_cursor_animate_insert_mode"`
	ScrollAnimation          bool         `json:"nvgui_scroll_animation"`
	ScrollAnimationLength    float32      `json:"nvgui_scroll_animation_length"`
	Ligatures                bool         `json:"nvgui_ligatures"`
	OpenIn                   OpenInPolicy `json:"nvgui_open_in"`
}

type DoctorOptions struct {
	Launch         LaunchRequest
	Source         LaunchSource
	Version        string
	BundleID       string
	ExecutablePath string
	SquirrelConfig []byte
	Workspace      *Workspace
	SocketPath     string
	ProbeLoginPATH bool
	PathProbe      PathProbeOptions
}

type DoctorReport struct {
	Version        string                     `json:"version"`
	NvimPath       string                     `json:"nvim_path"`
	NvimVersion    string                     `json:"nvim_version"`
	PATH           string                     `json:"path"`
	PathProbed     bool                       `json:"path_probed"`
	WorkingDir     string                     `json:"working_dir"`
	LaunchSource   LaunchSource               `json:"launch_source"`
	BundleID       string                     `json:"bundle_id"`
	ExecutablePath string                     `json:"executable_path"`
	SocketPath     string                     `json:"socket_path,omitempty"`
	ActiveWindows  int                        `json:"active_windows"`
	Squirrel       input.SquirrelBundleStatus `json:"squirrel"`
	Config         NvimConfigSnapshot         `json:"config"`
	Warnings       []string                   `json:"warnings,omitempty"`
}

func CollectDoctor(ctx context.Context, opts DoctorOptions) DoctorReport {
	return collectDoctor(ctx, opts)
}

func (r DoctorReport) FormatText() string {
	return formatDoctorReportText(r)
}

func (r DoctorReport) FormatJSON() ([]byte, error) {
	return formatDoctorReportJSON(r)
}

func (s *Shell) NvimConfig() NvimConfigSnapshot {
	return shellNvimConfig(s)
}

func (s *Shell) SyncNvimConfig(ctx context.Context) (NvimConfigSnapshot, error) {
	return shellSyncNvimConfig(s, ctx)
}

func (s *Shell) SetOpenInPolicy(policy OpenInPolicy) {
	shellSetOpenInPolicy(s, policy)
}

func (s *Shell) OpenInPolicy() OpenInPolicy {
	return shellOpenInPolicy(s)
}

func (s *Shell) ApplyAnimationConfigAndSync(ctx context.Context, cfg render.AnimationConfig) error {
	return shellApplyAnimationConfigAndSync(s, ctx, cfg)
}
