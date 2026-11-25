package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nvgui/connection"
)

const (
	defaultColumns = 80
	defaultRows    = 24
)

func parseLaunch(argv []string, env []string, cwd string) (LaunchRequest, error) {
	if cwd == "" {
		dir, err := os.Getwd()
		if err != nil {
			return LaunchRequest{}, fmt.Errorf("app: resolve working directory: %w", err)
		}
		cwd = dir
	}
	if env == nil {
		env = os.Environ()
	}
	args := make([]string, 0, len(argv))
	headless := false
	for index, arg := range argv {
		if arg == "--" {
			args = append(args, argv[index:]...)
			break
		}
		switch arg {
		case "--headless":
			headless = true
		case "--new-window":
		default:
			args = append(args, arg)
		}
	}
	if envValue(env, "NVGUI_HEADLESS") == "1" {
		headless = true
	}
	return LaunchRequest{
		Args:       args,
		Env:        append([]string(nil), env...),
		WorkingDir: cwd,
		Width:      defaultColumns,
		Height:     defaultRows,
		Headless:   headless,
	}, nil
}

var cliValueOptions = map[string]bool{
	"-u":       true,
	"-i":       true,
	"-c":       true,
	"--cmd":    true,
	"-S":       true,
	"-s":       true,
	"--listen": true,
	"--server": true,
}

const defaultSystemPATH = "/usr/bin:/bin:/usr/sbin:/sbin"

func parseCLILaunch(argv []string, env []string, cwd string) (CLILaunch, error) {
	request, err := parseLaunch(nvimArgsForCLILaunch(argv), env, cwd)
	if err != nil {
		return CLILaunch{}, err
	}
	if env == nil {
		env = os.Environ()
	}
	launch := CLILaunch{
		Request:    request,
		SocketPath: envValue(env, "NVGUI_SOCKET"),
	}
	files := make([]string, 0)
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		if arg == "--" {
			break
		}
		switch {
		case arg == "--new-window":
			launch.NewWindow = true
		case arg == "--doctor":
			launch.Doctor = true
		case arg == "--doctor-json":
			launch.DoctorJSON = true
		case arg == "--no-single-instance":
			launch.NoSingleInstance = true
		case arg == "--headless":
			launch.Request.Headless = true
		case arg == "--socket":
			if index+1 >= len(argv) || argv[index+1] == "" {
				return CLILaunch{}, errors.New("app: --socket requires a non-empty path")
			}
			index++
			launch.SocketPath = argv[index]
		case strings.HasPrefix(arg, "--socket="):
			value := strings.TrimPrefix(arg, "--socket=")
			if value == "" {
				return CLILaunch{}, errors.New("app: --socket requires a non-empty path")
			}
			launch.SocketPath = value
		case !strings.HasPrefix(arg, "-"):
			files = append(files, arg)
		default:
			if cliValueOptions[arg] && index+1 < len(argv) {
				index++
			}
		}
	}
	for _, file := range files {
		if file == "" {
			continue
		}
		if !filepath.IsAbs(file) && request.WorkingDir != "" {
			file = filepath.Join(request.WorkingDir, file)
		}
		launch.Files = append(launch.Files, file)
	}
	launch.Source = cliLaunchSource(argv, env, launch.Request, files)
	return launch, nil
}

func nvimArgsForCLILaunch(argv []string) []string {
	args := make([]string, 0, len(argv))
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		if arg == "--" {
			args = append(args, argv[index:]...)
			break
		}
		switch arg {
		case "--new-window", "--doctor", "--doctor-json", "--no-single-instance":
			continue
		case "--socket":
			if index+1 < len(argv) {
				index++
			}
			continue
		default:
			if strings.HasPrefix(arg, "--socket=") {
				continue
			}
		}
		args = append(args, arg)
	}
	return args
}

func cliLaunchSource(argv []string, env []string, request LaunchRequest, files []string) LaunchSource {
	if request.Headless {
		return LaunchSourceHeadless
	}
	if len(argv) == 0 && cliLooksLikeDock(env) {
		return LaunchSourceDock
	}
	if len(files) > 0 {
		return LaunchSourceFile
	}
	return LaunchSourceCLI
}

func cliLooksLikeDock(env []string) bool {
	path := envValue(env, "PATH")
	if path != defaultSystemPATH {
		return false
	}
	for _, key := range []string{"TERM", "SHLVL", "SSH_TTY"} {
		if envHasKey(env, key) {
			return false
		}
	}
	return true
}

func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func launchConnectionOptions(req LaunchRequest) connection.Options {
	return connection.Options{
		Args:   append([]string(nil), req.Args...),
		Env:    guiChildEnv(req.Env),
		Dir:    req.WorkingDir,
		Width:  req.Width,
		Height: req.Height,
	}
}

// guiChildEnv marks the embedded Neovim process as started by nvgui so that
// user config can tell the GUI apart before any RPC message arrives. The flag
// is set at spawn time because g:nvgui only lands after the first sync.
func guiChildEnv(env []string) []string {
	child := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "NVGUI=") {
			continue
		}
		child = append(child, entry)
	}
	return append(child, "NVGUI=1")
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for index := len(env) - 1; index >= 0; index-- {
		if value, ok := strings.CutPrefix(env[index], prefix); ok {
			return value
		}
	}
	return ""
}

func launchDoctor(req LaunchRequest) map[string]string {
	path, defined := "", false
	for index := len(req.Env) - 1; index >= 0; index-- {
		if value, ok := strings.CutPrefix(req.Env[index], "PATH="); ok {
			path, defined = value, true
			break
		}
	}
	nvimPath := ""
	if defined {
		for _, directory := range filepath.SplitList(path) {
			if directory == "" {
				continue
			}
			candidate := filepath.Join(directory, "nvim")
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
				continue
			}
			nvimPath = candidate
			break
		}
	} else if resolved, err := exec.LookPath("nvim"); err == nil {
		nvimPath = resolved
	}
	return map[string]string{
		"PATH":         path,
		"nvim":         nvimPath,
		"WorkingDir":   req.WorkingDir,
		"LaunchSource": launchSource(req),
	}
}

func launchSource(req LaunchRequest) string {
	if req.Headless {
		return "headless"
	}
	if len(req.Args) > 0 {
		for _, arg := range req.Args {
			if !strings.HasPrefix(arg, "-") {
				return "file"
			}
		}
	}
	return "cli"
}
