package diag

import (
	"sync"
	"time"

	"nvgui/protocol"
)

type Watchdog struct {
	mu      sync.Mutex
	timeout time.Duration
	clock   Clock
	onStall func(pendingEvents int)

	pending  int
	armed    bool
	reported bool
	deadline time.Time
}

func NewWatchdog(timeout time.Duration, clock Clock, onStall func(pendingEvents int)) *Watchdog {
	if timeout < 0 {
		timeout = 0
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Watchdog{timeout: timeout, clock: clock, onStall: onStall}
}

func (w *Watchdog) Observe(events []protocol.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, event := range events {
		if _, isFlush := event.(protocol.Flush); isFlush {
			w.pending = 0
			w.armed = false
			w.reported = false
			continue
		}
		if !w.armed {
			now := w.clock.Now()
			w.armed = true
			w.reported = false
			w.deadline = now.Add(w.timeout)
		}
		w.pending++
	}
}

func (w *Watchdog) Tick() {
	w.mu.Lock()
	if !w.armed || w.reported || w.clock.Now().Compare(w.deadline) < 0 {
		w.mu.Unlock()
		return
	}
	w.reported = true
	pending := w.pending
	onStall := w.onStall
	w.mu.Unlock()
	if onStall != nil {
		onStall(pending)
	}
}
