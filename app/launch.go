package app

import (
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

func launchConnectionOptions(req LaunchRequest) connection.Options {
	return connection.Options{
		Args:   append([]string(nil), req.Args...),
		Env:    append([]string(nil), req.Env...),
		Dir:    req.WorkingDir,
		Width:  req.Width,
		Height: req.Height,
	}
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
