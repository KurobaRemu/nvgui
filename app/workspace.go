package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type windowEntry struct {
	id          uint64
	shell       *Shell
	window      *mygo.Window
	workingDir  string
	closeOnce   sync.Once
	closeErr    error
	closeDone   chan struct{}
	cleanupOnce sync.Once
	cleanupDone chan struct{}
}

type workspaceImpl struct {
	opts WorkspaceOptions

	mu             sync.Mutex
	windows        map[uint64]*windowEntry
	nextID         uint64
	mru            []uint64
	wg             sync.WaitGroup
	closed         bool
	openInPolicy   OpenInPolicy
	openInExplicit bool

	allClosedOnce sync.Once
	allClosed     chan struct{}
	closeOnce     sync.Once
	closeErr      error
	closeDone     chan struct{}
}

func newWorkspace(opts WorkspaceOptions) *Workspace {
	impl := &workspaceImpl{
		opts:         opts,
		windows:      make(map[uint64]*windowEntry),
		openInPolicy: OpenInFront,
		allClosed:    make(chan struct{}),
		closeDone:    make(chan struct{}),
	}
	if policy, ok := parseOpenInPolicy(envValue(opts.BaseRequest.Env, "NVGUI_OPEN_IN")); ok {
		impl.openInPolicy = policy
		impl.openInExplicit = true
	}
	return &Workspace{impl: impl}
}

func (impl *workspaceImpl) currentOpenInPolicy() OpenInPolicy {
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if impl.openInPolicy == "" {
		return OpenInFront
	}
	return impl.openInPolicy
}

func workspaceImplOf(w *Workspace) *workspaceImpl {
	if w == nil {
		return nil
	}
	return w.impl
}

func (impl *workspaceImpl) fireAllClosed() {
	impl.allClosedOnce.Do(func() {
		impl.mu.Lock()
		impl.closed = true
		impl.mu.Unlock()
		close(impl.allClosed)
		if impl.opts.OnAllClosed != nil {
			impl.opts.OnAllClosed()
		} else if !impl.opts.Headless {
			mygo.App.Quit()
		}
	})
}

func (impl *workspaceImpl) promote(id uint64) {
	for index, candidate := range impl.mru {
		if candidate == id {
			copy(impl.mru[1:index+1], impl.mru[:index])
			impl.mru[0] = id
			return
		}
	}
	impl.mru = append([]uint64{id}, impl.mru...)
}

func (impl *workspaceImpl) remove(id uint64) bool {
	if _, ok := impl.windows[id]; !ok {
		return false
	}
	delete(impl.windows, id)
	remaining := make([]uint64, 0, len(impl.mru))
	for _, candidate := range impl.mru {
		if candidate != id {
			remaining = append(remaining, candidate)
		}
	}
	impl.mru = remaining
	return true
}

func (impl *workspaceImpl) cleanup(id uint64, shell *Shell) bool {
	impl.mu.Lock()
	entry := impl.windows[id]
	if entry == nil || entry.shell != shell {
		impl.mu.Unlock()
		return false
	}
	removed := impl.remove(id)
	empty := len(impl.windows) == 0
	window := entry.window
	entry.window = nil
	impl.mu.Unlock()
	if window != nil && !window.IsDestroyed() {
		window.Destroy()
	}
	_ = shell.Close()
	if removed && empty {
		impl.fireAllClosed()
	}
	impl.finishCleanup(entry)
	return removed
}

func (impl *workspaceImpl) closeEntry(entry *windowEntry) {
	entry.closeOnce.Do(func() {
		defer close(entry.closeDone)
		impl.mu.Lock()
		removed := impl.remove(entry.id)
		empty := len(impl.windows) == 0
		window := entry.window
		entry.window = nil
		impl.mu.Unlock()
		if window != nil && !window.IsDestroyed() {
			window.Destroy()
		}
		if err := entry.shell.Close(); err != nil {
			entry.closeErr = err
		}
		if removed && empty {
			impl.fireAllClosed()
		}
		impl.finishCleanup(entry)
	})
	<-entry.closeDone
	<-entry.cleanupDone
}

func (impl *workspaceImpl) finishCleanup(entry *windowEntry) {
	entry.cleanupOnce.Do(func() { close(entry.cleanupDone) })
}

func (impl *workspaceImpl) asyncTask(task func()) {
	impl.mu.Lock()
	if impl.closed {
		impl.mu.Unlock()
		return
	}
	impl.wg.Add(1)
	impl.mu.Unlock()
	go func() {
		defer impl.wg.Done()
		impl.mu.Lock()
		closed := impl.closed
		impl.mu.Unlock()
		if closed {
			return
		}
		task()
	}()
}

func (impl *workspaceImpl) watchSession(id uint64, shell *Shell) {
	defer impl.wg.Done()
	<-shell.SessionExited()
	impl.cleanup(id, shell)
}

func workspaceNewWindow(w *Workspace, ctx context.Context, req LaunchRequest) (uint64, *Shell, error) {
	impl := workspaceImplOf(w)
	if impl == nil {
		return 0, nil, fmt.Errorf("app: workspace is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	launch := deriveWorkspaceRequest(impl.opts.BaseRequest, req)
	headless := impl.opts.Headless || launch.Headless
	launch.Headless = headless
	workspace := &Workspace{impl: impl}
	impl.mu.Lock()
	policy := impl.openInPolicy
	explicitOpenIn := impl.openInExplicit
	impl.mu.Unlock()
	shell, err := startShell(ctx, launch, impl.opts.Clock, impl.opts.Clipboard, shellHooks{
		quitOnExit:     false,
		workspaceGUI:   false,
		workspace:      workspace,
		openInPolicy:   policy,
		explicitOpenIn: explicitOpenIn,
		onDrop: func(paths []string) {
			if _, err := workspaceOpenFiles(workspace, context.Background(), paths, launch.WorkingDir, LaunchSourceDrop); err != nil {
				fmt.Fprintln(os.Stderr, "nvgui: drop files:", err)
			}
		},
	})
	if err != nil {
		return 0, nil, err
	}

	impl.mu.Lock()
	if impl.closed {
		impl.mu.Unlock()
		_ = shell.Close()
		return 0, nil, fmt.Errorf("app: workspace is closed")
	}
	impl.nextID++
	id := impl.nextID
	entry := &windowEntry{
		id:          id,
		shell:       shell,
		workingDir:  launch.WorkingDir,
		closeDone:   make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}
	impl.windows[id] = entry
	impl.promote(id)
	impl.wg.Add(1)
	shell.impl.onShortcut = func(c *ui.Context) bool { return workspaceHandleShortcut(w, c, id) }
	impl.mu.Unlock()

	if !headless {
		mygo.App.WhenReady(func() {
			impl.mu.Lock()
			current := impl.windows[id]
			if current == nil || current.shell != shell || impl.closed || current.window != nil {
				impl.mu.Unlock()
				return
			}
			impl.mu.Unlock()
			window, offs := newWorkspaceWindow(shell, launch, id, impl)
			impl.mu.Lock()
			alive := impl.windows[id] == current && !impl.closed && current.window == nil
			if alive {
				current.window = window
			}
			impl.mu.Unlock()
			for _, off := range offs {
				if alive {
					registerDisplayCleanup(shell, []func(){off})
				} else {
					off()
				}
			}
			if !alive {
				window.Destroy()
			}
		})
	}

	if impl.opts.OnWindowAdded != nil {
		impl.opts.OnWindowAdded(id, shell)
	}
	go impl.watchSession(id, shell)
	return id, shell, nil
}

func deriveWorkspaceRequest(base LaunchRequest, req LaunchRequest) LaunchRequest {
	derived := base
	if len(req.Args) > 0 {
		derived.Args = append([]string(nil), req.Args...)
	} else {
		derived.Args = cleanBaseNvimArgs(base.Args)
	}
	derived.Env = append([]string(nil), base.Env...)
	if req.WorkingDir != "" {
		derived.WorkingDir = req.WorkingDir
	}
	if req.Env != nil {
		derived.Env = append([]string(nil), req.Env...)
	}
	if req.Width != 0 {
		derived.Width = req.Width
	}
	if req.Height != 0 {
		derived.Height = req.Height
	}
	if req.Headless || base.Headless {
		derived.Headless = true
	}
	if derived.WorkingDir == "" {
		derived.WorkingDir = req.WorkingDir
	}
	if derived.Width == 0 {
		derived.Width = defaultColumns
	}
	if derived.Height == 0 {
		derived.Height = defaultRows
	}
	return derived
}

var nvimOptionValues = map[string]bool{
	"--cmd":         true,
	"-c":            true,
	"-S":            true,
	"-s":            true,
	"-u":            true,
	"-i":            true,
	"--server":      true,
	"--startuptime": true,
}

func cleanBaseNvimArgs(args []string) []string {
	cleaned := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "+") {
			cleaned = append(cleaned, arg)
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		if arg == "--listen" || strings.HasPrefix(arg, "--listen=") {
			if arg == "--listen" && index+1 < len(args) {
				index++
			}
			continue
		}
		cleaned = append(cleaned, arg)
		if arg == "-l" {
			cleaned = append(cleaned, args[index+1:]...)
			break
		}
		if nvimOptionValues[arg] {
			if index+1 < len(args) {
				index++
				cleaned = append(cleaned, args[index])
			}
		}
	}
	return cleaned
}

func newWorkspaceWindow(shell *Shell, req LaunchRequest, id uint64, impl *workspaceImpl) (*mygo.Window, []func()) {
	cell := shell.impl.cell()
	window := mygo.NewWindow(mygo.WindowOptions{
		Title:          "nvgui",
		Width:          windowDimension(req.Width, defaultColumns, cell.Width),
		Height:         windowDimension(req.Height, defaultRows, cell.Height),
		UseContentSize: true,
		Content:        ui.View(shell.View),
	})
	shell.impl.mu.Lock()
	shell.impl.window = window
	shell.impl.mu.Unlock()
	offs := attachDisplayWindow(shell, window, func() { impl.recordFocus(id) })
	window.OnClose(func(*mygo.CloseEvent) {
		impl.asyncTask(func() { _ = workspaceCloseWindow(&Workspace{impl: impl}, id) })
	})
	window.OnFileDrop(func(event *mygo.FileDropEvent) {
		if event == nil || len(event.Paths) == 0 {
			return
		}
		paths := append([]string(nil), event.Paths...)
		workingDir := req.WorkingDir
		impl.asyncTask(func() {
			if _, err := workspaceOpenFiles(&Workspace{impl: impl}, context.Background(), paths, workingDir, LaunchSourceDrop); err != nil {
				fmt.Fprintln(os.Stderr, "nvgui: drop files:", err)
			}
		})
	})
	width, height := window.ContentSize()
	if err := shell.Resize(width, height); err != nil && !isCleanSessionError(err) {
		shell.impl.recordCloseError(fmt.Errorf("app: initial resize: %w", err))
	}
	return window, offs
}

func workspaceOpenInPolicy(w *Workspace) OpenInPolicy {
	impl := workspaceImplOf(w)
	if impl == nil {
		return OpenInFront
	}
	return impl.currentOpenInPolicy()
}

func workspaceSetOpenInPolicy(w *Workspace, policy OpenInPolicy) {
	impl := workspaceImplOf(w)
	if impl == nil {
		return
	}
	parsed, ok := parseOpenInPolicy(string(policy))
	if !ok {
		policy = OpenInFront
	} else {
		policy = parsed
	}
	impl.mu.Lock()
	impl.openInPolicy = policy
	impl.openInExplicit = true
	impl.mu.Unlock()
}

func workspaceHandleHandoff(w *Workspace, ctx context.Context, req HandoffRequest) (HandoffResult, error) {
	impl := workspaceImplOf(w)
	if impl == nil {
		return HandoffResult{}, errors.New("app: workspace is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workingDir := req.WorkingDir
	if workingDir == "" {
		workingDir = impl.opts.BaseRequest.WorkingDir
	}
	files := handoffFiles(req, workingDir)
	frontID, frontShell := workspaceFront(w)
	if impl.currentOpenInPolicy() == OpenInNew || req.NewWindow || workspaceWindowCount(w) == 0 || frontShell == nil {
		baseArgs := cleanBaseNvimArgs(impl.opts.BaseRequest.Args)
		child := LaunchRequest{Args: append(baseArgs, "--"), WorkingDir: workingDir}
		if len(files) > 0 {
			child.Args = append(child.Args, files...)
		}
		if len(req.Env) > 0 {
			child.Env = append([]string(nil), req.Env...)
		}
		id, _, err := workspaceNewWindow(w, ctx, child)
		if err != nil {
			return HandoffResult{}, err
		}
		return HandoffResult{WindowID: id, NewWindow: true, Opened: len(files)}, nil
	}
	if len(files) > 0 {
		if err := frontShell.DropFiles(ctx, files); err != nil {
			return HandoffResult{}, err
		}
	}
	workspaceFocusWindow(w, frontID)
	return HandoffResult{WindowID: frontID, NewWindow: false, Opened: len(files)}, nil
}

func workspaceOpenFiles(w *Workspace, ctx context.Context, paths []string, workingDir string, source LaunchSource) (HandoffResult, error) {
	return workspaceHandleHandoff(w, ctx, HandoffRequest{
		Files:      append([]string(nil), paths...),
		WorkingDir: workingDir,
		Source:     source,
	})
}

func handoffFiles(req HandoffRequest, workingDir string) []string {
	raw := append([]string(nil), req.Files...)
	for index := 0; index < len(req.Args); index++ {
		arg := req.Args[index]
		if arg == "--" {
			break
		}
		if cliValueOptions[arg] && index+1 < len(req.Args) {
			index++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		raw = append(raw, arg)
	}
	files := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, candidate := range raw {
		if candidate == "" {
			continue
		}
		if !filepath.IsAbs(candidate) && workingDir != "" {
			candidate = filepath.Join(workingDir, candidate)
		}
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		files = append(files, candidate)
	}
	return files
}

func (impl *workspaceImpl) recordFocus(id uint64) {
	impl.mu.Lock()
	if _, ok := impl.windows[id]; ok {
		impl.promote(id)
	}
	impl.mu.Unlock()
}

func workspaceCloseWindow(w *Workspace, id uint64) error {
	impl := workspaceImplOf(w)
	if impl == nil {
		return nil
	}
	impl.mu.Lock()
	entry := impl.windows[id]
	impl.mu.Unlock()
	if entry == nil {
		return nil
	}
	impl.closeEntry(entry)
	return entry.closeErr
}

func workspaceWindows(w *Workspace) []WindowInfo {
	impl := workspaceImplOf(w)
	if impl == nil {
		return nil
	}
	impl.mu.Lock()
	defer impl.mu.Unlock()
	infos := make([]WindowInfo, 0, len(impl.mru))
	for _, id := range impl.mru {
		entry := impl.windows[id]
		if entry == nil {
			continue
		}
		infos = append(infos, WindowInfo{
			ID:         id,
			WorkingDir: entry.workingDir,
			Focused:    len(infos) == 0,
		})
	}
	return infos
}

func workspaceWindowCount(w *Workspace) int {
	impl := workspaceImplOf(w)
	if impl == nil {
		return 0
	}
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return len(impl.windows)
}

func workspaceShell(w *Workspace, id uint64) *Shell {
	impl := workspaceImplOf(w)
	if impl == nil {
		return nil
	}
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if entry := impl.windows[id]; entry != nil {
		return entry.shell
	}
	return nil
}

func workspaceFront(w *Workspace) (uint64, *Shell) {
	impl := workspaceImplOf(w)
	if impl == nil {
		return 0, nil
	}
	impl.mu.Lock()
	defer impl.mu.Unlock()
	for _, id := range impl.mru {
		if entry := impl.windows[id]; entry != nil {
			return id, entry.shell
		}
	}
	return 0, nil
}

func workspaceFocusWindow(w *Workspace, id uint64) bool {
	impl := workspaceImplOf(w)
	if impl == nil {
		return false
	}
	impl.mu.Lock()
	entry := impl.windows[id]
	if entry == nil {
		impl.mu.Unlock()
		return false
	}
	impl.promote(id)
	window := entry.window
	impl.mu.Unlock()
	if window != nil && !window.IsDestroyed() {
		window.Focus()
	}
	return true
}

func workspaceHandleShortcut(w *Workspace, c *ui.Context, id uint64) bool {
	impl := workspaceImplOf(w)
	if impl == nil || c == nil {
		return false
	}
	newWin := c.Shortcut(ui.Cmd, ui.KeyN)
	closeWin := c.Shortcut(ui.Cmd, ui.KeyW)
	if newWin {
		impl.asyncTask(func() { _, _, _ = workspaceNewWindow(w, context.Background(), LaunchRequest{}) })
	}
	if closeWin {
		impl.asyncTask(func() { _ = workspaceCloseWindow(w, id) })
	}
	return newWin || closeWin
}

func workspaceBuildMenu(w *Workspace) *mygo.Menu {
	return workspaceBuildMenuFor(w)
}

func workspaceAllClosed(w *Workspace) <-chan struct{} {
	impl := workspaceImplOf(w)
	if impl == nil {
		return nil
	}
	return impl.allClosed
}

func workspaceClose(w *Workspace) error {
	impl := workspaceImplOf(w)
	if impl == nil {
		return nil
	}
	impl.closeOnce.Do(func() {
		defer close(impl.closeDone)
		impl.mu.Lock()
		impl.closed = true
		entries := make([]*windowEntry, 0, len(impl.windows))
		for _, entry := range impl.windows {
			entries = append(entries, entry)
		}
		impl.mu.Unlock()
		for _, entry := range entries {
			entry.closeOnce.Do(func() {
				defer close(entry.closeDone)
				impl.mu.Lock()
				removed := impl.remove(entry.id)
				empty := len(impl.windows) == 0
				window := entry.window
				entry.window = nil
				impl.mu.Unlock()
				if window != nil && !window.IsDestroyed() {
					window.Destroy()
				}
				if err := entry.shell.Close(); err != nil {
					entry.closeErr = err
				}
				if removed && empty {
					impl.fireAllClosed()
				}
				impl.finishCleanup(entry)
			})
		}
		for _, entry := range entries {
			<-entry.closeDone
			<-entry.cleanupDone
			if entry.closeErr != nil {
				impl.closeErr = errors.Join(impl.closeErr, entry.closeErr)
			}
		}
		impl.wg.Wait()
		impl.fireAllClosed()
	})
	<-impl.closeDone
	return impl.closeErr
}
