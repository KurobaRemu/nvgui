package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"nvgui/app"
)

func main() { os.Exit(mainWith(nil, nil, "")) }

type workspaceOpener interface {
	OpenFiles(context.Context, []string, string, app.LaunchSource) (app.HandoffResult, error)
}

type openFileQueue struct {
	mu        sync.Mutex
	paths     []string
	ready     bool
	hasWindow bool
	wake      chan struct{}
	closed    bool
}

func (queue *openFileQueue) add(path string) {
	queue.enqueue([]string{path})
}

func (queue *openFileQueue) signal(wake chan struct{}) {
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (queue *openFileQueue) enqueue(paths []string) {
	queue.mu.Lock()
	queue.paths = append(queue.paths, paths...)
	wake := queue.wake
	closed := queue.closed
	queue.mu.Unlock()
	if !closed {
		queue.signal(wake)
	}
}

func (queue *openFileQueue) drain(ready bool) []string {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if ready {
		queue.ready = true
	}
	return queue.takeLocked()
}

func (queue *openFileQueue) takeLocked() []string {
	if !queue.ready || !queue.hasWindow || len(queue.paths) == 0 || queue.closed {
		return nil
	}
	paths := queue.paths
	queue.paths = nil
	return paths
}

func (queue *openFileQueue) pending() []string {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]string(nil), queue.paths...)
}

func (queue *openFileQueue) markWindow() {
	queue.mu.Lock()
	queue.hasWindow = true
	wake := queue.wake
	queue.mu.Unlock()
	queue.signal(wake)
}

type openFileDispatcher struct {
	queue  *openFileQueue
	stop   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
}

func newOpenFileDispatcher(workspace workspaceOpener, workingDir string, report func(error)) *openFileDispatcher {
	workerCtx, cancel := context.WithCancel(context.Background())
	dispatcher := &openFileDispatcher{
		queue:  &openFileQueue{wake: make(chan struct{}, 1)},
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		cancel: cancel,
	}
	go func() {
		defer close(dispatcher.done)
		for {
			paths := dispatcher.queue.drain(false)
			if len(paths) > 0 {
				dispatcher.open(workerCtx, workspace, workingDir, report, paths)
				continue
			}
			select {
			case <-dispatcher.queue.wake:
			case <-dispatcher.stop:
				return
			}
		}
	}()
	return dispatcher
}

func (dispatcher *openFileDispatcher) open(workerCtx context.Context, workspace workspaceOpener, workingDir string, report func(error), paths []string) {
	ctx, cancel := context.WithTimeout(workerCtx, 30*time.Second)
	_, err := workspace.OpenFiles(ctx, paths, workingDir, app.LaunchSourceOpenWith)
	cancel()
	if err != nil {
		report(err)
	}
}

func (dispatcher *openFileDispatcher) enqueue(paths []string) {
	dispatcher.queue.enqueue(paths)
}

func (dispatcher *openFileDispatcher) onOpenFile(path string) {
	dispatcher.enqueue([]string{path})
}

func (dispatcher *openFileDispatcher) onWindowAdded() {
	dispatcher.queue.markWindow()
}

func (dispatcher *openFileDispatcher) onAppReady() {
	dispatcher.queue.mu.Lock()
	dispatcher.queue.ready = true
	dispatcher.queue.mu.Unlock()
	dispatcher.enqueue(nil)
}

func (dispatcher *openFileDispatcher) close() {
	dispatcher.once.Do(func() {
		dispatcher.queue.mu.Lock()
		dispatcher.queue.closed = true
		dispatcher.queue.mu.Unlock()
		dispatcher.cancel()
		close(dispatcher.stop)
	})
	<-dispatcher.done
}

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
	cli, err := app.ParseCLILaunch(argv, env, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: parse launch:", err)
		return 1
	}
	if !cli.Request.Headless {
		merged, _ := app.ResolveLoginPATH(context.Background(), env, app.PathProbeOptions{})
		env = merged
		cli.Request.Env = append([]string(nil), merged...)
	}
	socketPath := cli.SocketPath
	if socketPath == "" {
		socketPath = app.DefaultInstanceSocketPath(env)
	}
	if !cli.Request.Headless && !cli.NoSingleInstance {
		_, forwarded, err := app.ForwardInstance(context.Background(), socketPath, app.HandoffRequest{
			Files:      append([]string(nil), cli.Files...),
			Args:       append([]string(nil), cli.Request.Args...),
			Env:        append([]string(nil), env...),
			WorkingDir: cli.Request.WorkingDir,
			NewWindow:  cli.NewWindow,
			Source:     app.LaunchSourceSingleInstance,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: forward instance:", err)
			return 1
		}
		if forwarded {
			return 0
		}
	}
	opts := app.WorkspaceOptions{BaseRequest: cli.Request, Headless: cli.Request.Headless}
	if !cli.Request.Headless {
		opts.OnAllClosed = func() { mygo.App.Quit() }
	}
	var onWindowAdded func()
	opts.OnWindowAdded = func(uint64, *app.Shell) {
		if onWindowAdded != nil {
			onWindowAdded()
		}
	}
	ws := app.NewWorkspace(opts)
	dispatcher := newOpenFileDispatcher(ws, cli.Request.WorkingDir, func(err error) {
		fmt.Fprintln(os.Stderr, "nvgui: open files:", err)
	})
	defer dispatcher.close()
	onWindowAdded = dispatcher.onWindowAdded
	if !cli.Request.Headless {
		mygo.App.OnOpenFile(dispatcher.onOpenFile)
	}
	if _, _, err := ws.NewWindow(context.Background(), cli.Request); err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: start:", err)
		return 1
	}
	defer func() {
		if err := ws.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: close:", err)
		}
	}()
	if cli.Request.Headless {
		if err := ws.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: close:", err)
			return 1
		}
		return 0
	}

	if !cli.NoSingleInstance {
		server, err := app.ListenInstance(socketPath, ws.HandleHandoff)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nvgui: listen instance:", err)
			return 1
		}
		defer func() { _ = server.Close() }()
	}
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(ws.BuildMenu())
		dispatcher.onAppReady()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "nvgui: run:", err)
		return 1
	}
	return 0
}
