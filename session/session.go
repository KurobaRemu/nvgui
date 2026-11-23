package session

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neovim/go-client/nvim"

	"nvgui/connection"
	"nvgui/diag"
	"nvgui/protocol"
	"nvgui/screen"
)

const diagnosticWindow = 30 * time.Second
const stallTimeout = 2 * time.Second
const stallBuffer = 16

type Session struct {
	conn         *connection.Conn
	recorder     *diag.Recorder
	watchdog     *diag.Watchdog
	reducer      *screen.Reducer
	meta         diag.Meta
	stalls       chan int
	redraws      chan []any
	redrawNotify chan struct{}
	done         chan struct{}
	wg           sync.WaitGroup
	closeOnce    sync.Once
	closeMu      sync.Mutex
	closeErr     error
	metaMu       sync.Mutex
	metaReady    bool
}

func Start(ctx context.Context, opt connection.Options, clock diag.Clock) (*Session, error) {
	if ctx == nil {
		return nil, fmt.Errorf("session: context is nil")
	}
	if clock == nil {
		clock = realClock{}
	}
	recorder := diag.NewRecorder(diagnosticWindow, clock)
	reducer := screen.NewReducer()
	session := &Session{
		recorder:     recorder,
		reducer:      reducer,
		stalls:       make(chan int, stallBuffer),
		redraws:      make(chan []any, 128),
		redrawNotify: make(chan struct{}, 1),
		done:         make(chan struct{}),
		meta: diag.Meta{
			UIOptions: map[string]bool{
				"rgb":           true,
				"ext_linegrid":  true,
				"ext_multigrid": true,
				"ext_hlstate":   true,
			},
			GUIVersion: "dev",
			GOOS:       runtime.GOOS,
		},
	}
	session.watchdog = diag.NewWatchdog(stallTimeout, clock, session.reportStall)
	options := opt
	options.Tap = recorder
	options.OnRedraw = func(args []any) {
		select {
		case session.redraws <- normalizeRedrawArgs(args):
		case <-session.done:
		}
	}
	conn, err := connection.Start(ctx, options)
	if err != nil {
		return nil, err
	}
	session.conn = conn
	session.wg.Add(2)
	go session.run()
	go session.tick()
	return session, nil
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (s *Session) reportStall(pendingEvents int) {
	select {
	case s.stalls <- pendingEvents:
	default:
		select {
		case <-s.stalls:
		default:
		}
		select {
		case s.stalls <- pendingEvents:
		case <-s.done:
		default:
		}
	}
}

func (s *Session) run() {
	defer s.wg.Done()
	for {
		select {
		case args := <-s.redraws:
			if barrier, ok := barrierFromArgs(args); ok {
				close(barrier)
				continue
			}
			events, err := protocol.DecodeRedraw(args)
			if err != nil {
				events = nil
			}
			s.watchdog.Observe(events)
			committed, err := s.reducer.Apply(events)
			for _, state := range committed {
				s.recorder.Keyframe(state)
			}
			if len(committed) > 0 {
				select {
				case s.redrawNotify <- struct{}{}:
				default:
				}
			}
			if err != nil {
				continue
			}
		case <-s.done:
			return
		}
	}
}

func normalizeRedrawArgs(args []any) []any {
	normalized := make([]any, len(args))
	for index, item := range args {
		normalized[index] = normalizeRedrawValue(item)
	}
	return normalized
}

func normalizeRedrawValue(value any) any {
	switch typed := value.(type) {
	case nvim.Window:
		return protocol.Handle{Kind: 2, ID: int64(typed)}
	case nvim.Buffer:
		return protocol.Handle{Kind: 0, ID: int64(typed)}
	case nvim.Tabpage:
		return protocol.Handle{Kind: 1, ID: int64(typed)}
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizeRedrawValue(item)
		}
		return result
	default:
		return value
	}
}

func (s *Session) tick() {
	defer s.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.watchdog.Tick()
		case <-s.done:
			return
		}
	}
}

func (s *Session) Latest() *screen.State { return s.reducer.Latest() }

func (s *Session) Redraws() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.redrawNotify
}

func (s *Session) Nvim() *nvim.Nvim { return s.conn.Nvim() }

func (s *Session) Exited() <-chan struct{} { return s.conn.Exited() }

func (s *Session) ExportDiag(dir string) error {
	if err := s.refreshMeta(); err != nil {
		return err
	}
	s.syncReducer()
	s.metaMu.Lock()
	meta := s.meta
	s.metaMu.Unlock()
	return s.recorder.Export(dir, meta)
}

func (s *Session) RecordInput(ev diag.TraceEvent) {
	if s == nil || s.recorder == nil {
		return
	}
	s.recorder.RecordInput(ev)
}

func (s *Session) syncReducer() {
	barrier := make(chan struct{})
	select {
	case s.redraws <- []any{barrierMarker{barrier}}:
	case <-s.done:
		return
	}
	select {
	case <-barrier:
	case <-s.done:
	}
}

type barrierMarker struct{ done chan struct{} }

func barrierFromArgs(args []any) (chan struct{}, bool) {
	if len(args) != 1 {
		return nil, false
	}
	marker, ok := args[0].(barrierMarker)
	if !ok {
		return nil, false
	}
	return marker.done, true
}

func (s *Session) refreshMeta() error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	if s.metaReady {
		return nil
	}
	info, err := s.conn.Nvim().APIInfo()
	if err != nil {
		return fmt.Errorf("session: read nvim API info: %w", err)
	}
	if len(info) < 2 {
		return fmt.Errorf("session: nvim API info has %d fields, want 2", len(info))
	}
	metadata, ok := info[1].(map[string]any)
	if !ok {
		return fmt.Errorf("session: nvim API metadata is %T", info[1])
	}
	s.meta.APILevel = intFromAny(metadata["api_level"])
	s.meta.NvimVersion = versionString(metadata["version"])
	if options, ok := metadata["ui_options"].([]any); ok {
		s.meta.UIOptions = make(map[string]bool, len(options))
		for _, option := range options {
			if name, ok := option.(string); ok {
				s.meta.UIOptions[name] = true
			}
		}
	}
	s.metaReady = true
	return nil
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case uint64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func versionString(value any) string {
	version, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	major := intFromAny(version["major"])
	minor := intFromAny(version["minor"])
	patch := intFromAny(version["patch"])
	result := "v" + strconv.Itoa(major) + "." + strconv.Itoa(minor) + "." + strconv.Itoa(patch)
	if prerelease, ok := version["prerelease"].(bool); ok && prerelease {
		result += "-prerelease"
	}
	return result
}

func (s *Session) Stalls() <-chan int { return s.stalls }

func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.closeMu.Lock()
		defer s.closeMu.Unlock()
		close(s.done)
		if err := s.conn.Close(); err != nil && !isCleanExit(err) {
			s.closeErr = err
		}
		s.wg.Wait()
	})
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	return s.closeErr
}

func isCleanExit(err error) bool {
	return strings.Contains(err.Error(), "exit status") || strings.Contains(err.Error(), "signal:")
}
