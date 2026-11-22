package app

import "context"

type OpenInPolicy string

const (
	OpenInFront OpenInPolicy = "front"
	OpenInNew   OpenInPolicy = "new"
)

type LaunchSource string

const (
	LaunchSourceCLI            LaunchSource = "cli"
	LaunchSourceFile           LaunchSource = "file"
	LaunchSourceHeadless       LaunchSource = "headless"
	LaunchSourceOpenWith       LaunchSource = "open-with"
	LaunchSourceDrop           LaunchSource = "drop"
	LaunchSourceSingleInstance LaunchSource = "single-instance"
	LaunchSourceDock           LaunchSource = "dock"
)

type CLILaunch struct {
	Request          LaunchRequest
	NewWindow        bool
	Doctor           bool
	DoctorJSON       bool
	NoSingleInstance bool
	SocketPath       string
	Files            []string
	Source           LaunchSource
}

type HandoffRequest struct {
	Args       []string     `json:"args,omitempty"`
	Files      []string     `json:"files,omitempty"`
	Env        []string     `json:"env,omitempty"`
	WorkingDir string       `json:"working_dir,omitempty"`
	NewWindow  bool         `json:"new_window,omitempty"`
	Source     LaunchSource `json:"source,omitempty"`
}

type HandoffResult struct {
	WindowID  uint64 `json:"window_id"`
	NewWindow bool   `json:"new_window"`
	Opened    int    `json:"opened"`
}

type PathProbeFunc func(ctx context.Context, shell string, baseEnv []string) (string, error)

type PathProbeOptions struct {
	Timeout int64 // milliseconds; <= 0 defaults to 1500ms
	Probe   PathProbeFunc
	NoCache bool
	Shell   string
	Force   bool
}

type InstanceServer struct {
	impl *instanceServerImpl
}

func ParseCLILaunch(argv []string, env []string, cwd string) (CLILaunch, error) {
	return parseCLILaunch(argv, env, cwd)
}

func ParseOpenInPolicy(raw string) (OpenInPolicy, bool) {
	return parseOpenInPolicy(raw)
}

func FormatDropCommand(paths []string) (string, error) {
	return formatDropCommand(paths)
}

func ResolveLoginPATH(ctx context.Context, env []string, opts PathProbeOptions) ([]string, bool) {
	return resolveLoginPATH(ctx, env, opts)
}

func DefaultInstanceSocketPath(env []string) string {
	return defaultInstanceSocketPath(env)
}

func ListenInstance(socketPath string, handler func(context.Context, HandoffRequest) (HandoffResult, error)) (*InstanceServer, error) {
	return listenInstance(socketPath, handler)
}

func (s *InstanceServer) SocketPath() string {
	return instanceServerSocketPath(s)
}

func (s *InstanceServer) Close() error {
	return instanceServerClose(s)
}

func ForwardInstance(ctx context.Context, socketPath string, req HandoffRequest) (HandoffResult, bool, error) {
	return forwardInstance(ctx, socketPath, req)
}

func (w *Workspace) OpenInPolicy() OpenInPolicy {
	return workspaceOpenInPolicy(w)
}

func (w *Workspace) SetOpenInPolicy(policy OpenInPolicy) {
	workspaceSetOpenInPolicy(w, policy)
}

func (w *Workspace) HandleHandoff(ctx context.Context, req HandoffRequest) (HandoffResult, error) {
	return workspaceHandleHandoff(w, ctx, req)
}

func (w *Workspace) OpenFiles(ctx context.Context, paths []string, workingDir string, source LaunchSource) (HandoffResult, error) {
	return workspaceOpenFiles(w, ctx, paths, workingDir, source)
}

func (s *Shell) DropFiles(ctx context.Context, paths []string) error {
	return shellDropFiles(s, ctx, paths)
}
