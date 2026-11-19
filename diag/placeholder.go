package diag

// placeholder

import (
	"errors"
	"time"

	"nvgui/protocol"
	"nvgui/screen"
)

var errNotImplemented = errors.New("not implemented")

type Clock interface{ Now() time.Time }

type Recorder struct{}

func NewRecorder(window time.Duration, clock Clock) *Recorder { return &Recorder{} }

func (r *Recorder) Write(p []byte) (int, error) { return 0, errNotImplemented }

func (r *Recorder) Keyframe(s *screen.State) {}

type Meta struct {
	NvimVersion string            `json:"nvim_version"`
	APILevel    int               `json:"api_level"`
	UIOptions   map[string]bool   `json:"ui_options"`
	GUIVersion  string            `json:"gui_version"`
	GOOS        string            `json:"goos"`
	Extra       map[string]string `json:"extra,omitempty"`
}

func (r *Recorder) Export(dir string, meta Meta) error { return errNotImplemented }

func Replay(dir string) ([]*screen.State, error) { return nil, errNotImplemented }

type Watchdog struct{}

func NewWatchdog(timeout time.Duration, clock Clock, onStall func(pendingEvents int)) *Watchdog {
	return &Watchdog{}
}

func (w *Watchdog) Observe(events []protocol.Event) {}

func (w *Watchdog) Tick() {}
