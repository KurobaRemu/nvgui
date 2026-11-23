package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	instanceDialTimeout    = 200 * time.Millisecond
	instanceForwardTimeout = time.Second
	instanceIOTimeout      = 5 * time.Second
)

type instanceServerImpl struct {
	path       string
	listener   net.Listener
	socketInfo os.FileInfo
	handler    func(context.Context, HandoffRequest) (HandoffResult, error)

	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
	closeDone chan struct{}
}

type instanceResponse struct {
	OK     bool          `json:"ok"`
	Result HandoffResult `json:"result"`
	Error  string        `json:"error,omitempty"`
}

func defaultInstanceSocketPath(env []string) string {
	if custom := envValue(env, "NVGUI_SOCKET"); custom != "" {
		return custom
	}
	uid := os.Getuid()
	fallback := fmt.Sprintf("/tmp/nvgui-%d.sock", uid)
	for _, base := range []string{envValue(env, "XDG_RUNTIME_DIR"), envValue(env, "TMPDIR")} {
		if base == "" {
			continue
		}
		path := filepath.Join(base, fmt.Sprintf("nvgui-%d.sock", uid))
		if len(path) > 100 {
			return fallback
		}
		return path
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("nvgui-%d.sock", uid))
	if len(path) <= 100 {
		return path
	}
	return fallback
}

func listenInstance(socketPath string, handler func(context.Context, HandoffRequest) (HandoffResult, error)) (*InstanceServer, error) {
	if socketPath == "" {
		return nil, errors.New("app: instance socket path is empty")
	}
	if handler == nil {
		return nil, errors.New("app: instance handler is nil")
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("app: instance socket path %q is occupied by a non-socket file", socketPath)
		}
		connection, dialErr := net.DialTimeout("unix", socketPath, instanceDialTimeout)
		if dialErr == nil {
			_ = connection.Close()
			return nil, fmt.Errorf("app: instance socket %q is active", socketPath)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("app: probe instance socket: %w", dialErr)
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("app: remove stale instance socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("app: inspect instance socket: %w", err)
	}
	if directory := filepath.Dir(socketPath); directory != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("app: create instance socket directory: %w", err)
		}
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("app: listen instance socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, fmt.Errorf("app: chmod instance socket: %w", err)
	}
	socketInfo, err := os.Lstat(socketPath)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("app: inspect instance socket: %w", err)
	}
	if unixListener, ok := listener.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	server := &InstanceServer{impl: &instanceServerImpl{
		path:       socketPath,
		listener:   listener,
		socketInfo: socketInfo,
		handler:    handler,
		closeDone:  make(chan struct{}),
	}}
	server.impl.wg.Add(1)
	go server.impl.acceptLoop()
	return server, nil
}

func instanceServerSocketPath(server *InstanceServer) string {
	if server == nil || server.impl == nil {
		return ""
	}
	return server.impl.path
}

func (impl *instanceServerImpl) acceptLoop() {
	defer impl.wg.Done()
	for {
		connection, err := impl.listener.Accept()
		if err != nil {
			return
		}
		impl.wg.Add(1)
		go func() {
			defer impl.wg.Done()
			impl.serve(context.Background(), connection)
		}()
	}
}

func (impl *instanceServerImpl) serve(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(instanceDeadline(ctx))
	defer connection.SetDeadline(time.Time{})

	decoder := json.NewDecoder(connection)
	var request HandoffRequest
	response := instanceResponse{}
	if err := decoder.Decode(&request); err != nil {
		response.Error = fmt.Sprintf("app: decode handoff request: %v", err)
	} else {
		result, handlerErr := impl.handler(ctx, request)
		if handlerErr != nil {
			response.Error = handlerErr.Error()
		} else {
			response.OK = true
			response.Result = result
		}
	}
	if err := json.NewEncoder(connection).Encode(response); err != nil {
		return
	}
}

func instanceServerClose(server *InstanceServer) error {
	if server == nil || server.impl == nil {
		return nil
	}
	impl := server.impl
	impl.closeOnce.Do(func() {
		defer close(impl.closeDone)
		impl.closeErr = impl.listener.Close()
		impl.wg.Wait()
		info, err := os.Lstat(impl.path)
		if err == nil && info.Mode()&os.ModeSocket != 0 && os.SameFile(impl.socketInfo, info) {
			if removeErr := os.Remove(impl.path); removeErr != nil && !os.IsNotExist(removeErr) {
				impl.closeErr = errors.Join(impl.closeErr, removeErr)
			}
		}
	})
	<-impl.closeDone
	return impl.closeErr
}

func forwardInstance(ctx context.Context, socketPath string, request HandoffRequest) (HandoffResult, bool, error) {
	if socketPath == "" {
		return HandoffResult{}, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dialCtx, cancel := context.WithTimeout(ctx, instanceForwardTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialCtx, "unix", socketPath)
	if err != nil {
		if isInstanceUnavailable(err) {
			return HandoffResult{}, false, nil
		}
		return HandoffResult{}, false, fmt.Errorf("app: connect instance socket: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(instanceClientDeadline(ctx))
	defer connection.SetDeadline(time.Time{})
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return HandoffResult{}, false, fmt.Errorf("app: send handoff request: %w", err)
	}
	var response instanceResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return HandoffResult{}, false, fmt.Errorf("app: read handoff response: %w", err)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "app: handoff failed"
		}
		return HandoffResult{}, false, errors.New(response.Error)
	}
	return response.Result, true, nil
}

func instanceClientDeadline(ctx context.Context) time.Time {
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(time.Now().Add(instanceIOTimeout)) {
		return time.Now().Add(instanceIOTimeout)
	}
	return deadline
}

func instanceDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(instanceIOTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}

func isInstanceUnavailable(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == syscall.ECONNREFUSED || errno == syscall.ENOENT) {
		return true
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}
