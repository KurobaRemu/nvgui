package main

import (
	"context"
	"fmt"
	"os"

	"github.com/egoist/mygo"

	"nvgui/app"
)

func main() { os.Exit(mainWith(nil, nil, "")) }

func mainWith(argv []string, env []string, cwd string) int {
	if argv == nil {
		argv = os.Args[1:]
	}
	if env == nil {
		env = os.Environ()
	}
	if cwd == "" {
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: resolve working directory:", err)
			return 1
		}
		cwd = dir
	}
	req, err := app.ParseLaunch(argv, env, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: parse launch:", err)
		return 1
	}
	shell, err := app.Start(context.Background(), req, nil, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: start:", err)
		return 1
	}
	defer func() {
		if err := shell.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: close:", err)
		}
	}()
	if req.Headless {
		if err := shell.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: close:", err)
			return 1
		}
		return 0
	}
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: run:", err)
		return 1
	}
	return 0
}
