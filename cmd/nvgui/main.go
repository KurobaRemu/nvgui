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
	opts := app.WorkspaceOptions{BaseRequest: req, Headless: req.Headless}
	if !req.Headless {
		opts.OnAllClosed = func() { mygo.App.Quit() }
	}
	ws := app.NewWorkspace(opts)
	if _, _, err := ws.NewWindow(context.Background(), req); err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: start:", err)
		return 1
	}
	defer func() {
		if err := ws.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: close:", err)
		}
	}()
	if req.Headless {
		if err := ws.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: close:", err)
			return 1
		}
		return 0
	}
	mygo.App.WhenReady(func() { mygo.App.SetMenu(ws.BuildMenu()) })
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: run:", err)
		return 1
	}
	return 0
}
