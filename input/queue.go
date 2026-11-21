package input

import (
	"context"
	"errors"
	"sync"

	"github.com/egoist/mygo/ui"
)

const queueCapacity = 256

var (
	errQueueClosed    = errors.New("input: queue closed")
	errQueueNilSender = errors.New("input: queue sender is nil")
)

type queueImpl struct {
	sender   Sender
	recorder *TraceRecorder

	mu      sync.Mutex
	events  [queueCapacity]ui.InputEvent
	head    int
	length  int
	wakeup  *sync.Cond
	closing chan struct{}
	stopped chan struct{}
	sent    []string
	runErr  error
	started bool
	closed  bool
	done    bool
	ctx     context.Context

	pending    *ui.InputEvent
	swallowKey ui.Key
	swallowAlt bool
	composing  bool
	resetAfter [queueCapacity]bool
}

func newQueue(sender Sender, recorder *TraceRecorder) *Queue {
	impl := &queueImpl{
		sender:   sender,
		recorder: recorder,
		closing:  make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	impl.wakeup = sync.NewCond(&impl.mu)
	return &Queue{impl: impl}
}

func (impl *queueImpl) keys(ev ui.InputEvent) string {
	switch ev.Kind {
	case ui.InputText:
		impl.composing = false
		if impl.pending != nil {
			impl.pending = nil
			impl.swallowKey = 0
			impl.swallowAlt = false
			return escapeText(ev.Text)
		}
		if impl.swallowKey != 0 {
			mods := ev.Mods & (ui.Ctrl | ui.Alt | ui.Super | ui.Shift)
			if impl.swallowAlt || mods&ui.Ctrl != 0 {
				if impl.swallowKey == ui.KeyUnknown || ev.Key == ui.KeyUnknown || impl.swallowKey == ev.Key {
					impl.swallowKey = 0
					impl.swallowAlt = false
					return ""
				}
			}
			impl.swallowKey = 0
			impl.swallowAlt = false
		}
		return escapeText(ev.Text)
	case ui.InputCompose:
		impl.pending = nil
		impl.swallowKey = 0
		impl.swallowAlt = false
		if ev.Canceled || ev.Text == "" {
			impl.composing = false
		} else {
			impl.composing = true
		}
		return ""
	case ui.InputKeyDown:
		impl.pending = nil
		impl.swallowKey = 0
		impl.swallowAlt = false
		if impl.composing {
			if endsComposition(ev) {
				impl.composing = false
			} else if ev.Mods&(ui.Ctrl|ui.Super) == 0 {
				return ""
			}
		}
		if ev.Mods&ui.Super != 0 {
			return ""
		}
		notation, ok := Notation(ev)
		if !ok {
			return ""
		}
		if isTypeableKey(ev.Key) && ev.Mods&(ui.Ctrl|ui.Alt|ui.Super) == 0 {
			down := ev
			impl.pending = &down
			return ""
		}
		if isTypeableKey(ev.Key) && ev.Mods&(ui.Ctrl|ui.Alt) != 0 {
			impl.swallowKey = ev.Key
			impl.swallowAlt = ev.Mods&ui.Alt != 0
		}
		return notation
	case ui.InputKeyUp:
		if impl.composing {
			impl.pending = nil
			impl.swallowKey = 0
			impl.swallowAlt = false
			return ""
		}
		if impl.pending != nil && impl.pending.Key == ev.Key {
			down := *impl.pending
			impl.pending = nil
			notation, _ := Notation(down)
			return notation
		}
		if impl.swallowKey != 0 && impl.swallowKey == ev.Key {
			impl.swallowKey = 0
			impl.swallowAlt = false
		}
		return ""
	default:
		return ""
	}
}

func (impl *queueImpl) finish(err error) {
	if impl.done {
		return
	}
	impl.runErr = err
	impl.done = true
	impl.wakeup.Broadcast()
	close(impl.stopped)
}

func (impl *queueImpl) pop() (ui.InputEvent, bool, bool) {
	if impl.length == 0 {
		return ui.InputEvent{}, false, false
	}
	slot := impl.head
	ev := impl.events[impl.head]
	resetAfter := impl.resetAfter[slot]
	impl.events[impl.head] = ui.InputEvent{}
	impl.resetAfter[slot] = false
	impl.head = (impl.head + 1) % queueCapacity
	impl.length--
	impl.wakeup.Broadcast()
	return ev, true, resetAfter
}

func (impl *queueImpl) prepareDelivery() (string, bool, error) {
	ev, ok, resetAfter := impl.pop()
	if !ok {
		return "", false, nil
	}
	keys := impl.keys(ev)
	if resetAfter {
		impl.composing = false
	}
	if keys != "" {
		if impl.sender == nil {
			return "", false, errQueueNilSender
		}
		impl.sent = append(impl.sent, keys)
	}
	return keys, true, nil
}

func (queue *Queue) resetComposition() {
	if queue == nil || queue.impl == nil {
		return
	}
	impl := queue.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if impl.length == 0 {
		impl.composing = false
		return
	}
	lastSlot := (impl.head + impl.length - 1) % queueCapacity
	impl.resetAfter[lastSlot] = true
}

func queueRun(queue *Queue, ctx context.Context) error {
	if queue == nil || queue.impl == nil {
		return errors.New("input: nil queue")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	impl := queue.impl
	impl.mu.Lock()
	if impl.done {
		err := impl.runErr
		impl.mu.Unlock()
		return err
	}
	if impl.started {
		impl.mu.Unlock()
		<-impl.stopped
		impl.mu.Lock()
		defer impl.mu.Unlock()
		return impl.runErr
	}
	impl.started = true
	impl.ctx = ctx
	if impl.closed {
		impl.finish(nil)
		impl.mu.Unlock()
		return nil
	}
	impl.mu.Unlock()

	stopAfter := context.AfterFunc(ctx, func() {
		impl.mu.Lock()
		defer impl.mu.Unlock()
		impl.wakeup.Broadcast()
	})
	defer stopAfter()
	defer func() {
		impl.mu.Lock()
		defer impl.mu.Unlock()
		impl.finish(ctxError(ctx))
	}()

	for {
		if err := ctx.Err(); err != nil {
			impl.mu.Lock()
			impl.finish(err)
			impl.mu.Unlock()
			return err
		}

		impl.mu.Lock()
		if err := ctx.Err(); err != nil {
			impl.finish(err)
			impl.mu.Unlock()
			return err
		}
		if impl.done {
			err := impl.runErr
			impl.mu.Unlock()
			return err
		}
		keys, ok, err := impl.prepareDelivery()
		if err != nil {
			impl.finish(err)
			impl.mu.Unlock()
			return err
		}
		if ok {
			impl.mu.Unlock()
			if keys != "" {
				if _, err := impl.sender.Input(keys); err != nil {
					impl.mu.Lock()
					impl.finish(err)
					impl.mu.Unlock()
					return err
				}
			}
			continue
		}
		if impl.closed {
			impl.finish(nil)
			impl.mu.Unlock()
			return nil
		}
		for impl.length == 0 && !impl.closed && !impl.done && ctx.Err() == nil {
			impl.wakeup.Wait()
		}
		impl.mu.Unlock()
	}
}

func queueClose(queue *Queue) error {
	if queue == nil || queue.impl == nil {
		return errors.New("input: nil queue")
	}
	impl := queue.impl
	impl.mu.Lock()
	if !impl.closed {
		impl.closed = true
		close(impl.closing)
		impl.wakeup.Broadcast()
	}
	if !impl.started {
		impl.started = true
		impl.mu.Unlock()
		for {
			impl.mu.Lock()
			keys, ok, err := impl.prepareDelivery()
			impl.mu.Unlock()
			if err != nil {
				impl.mu.Lock()
				impl.finish(err)
				impl.mu.Unlock()
				return err
			}
			if !ok {
				break
			}
			if keys != "" {
				if _, err := impl.sender.Input(keys); err != nil {
					impl.mu.Lock()
					impl.finish(err)
					impl.mu.Unlock()
					return err
				}
			}
		}
		impl.mu.Lock()
		impl.finish(nil)
		impl.mu.Unlock()
		return nil
	}
	impl.mu.Unlock()
	<-impl.stopped
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return impl.runErr
}

func queueEnqueue(queue *Queue, ev ui.InputEvent) error {
	if queue == nil || queue.impl == nil {
		return errors.New("input: nil queue")
	}
	impl := queue.impl
	impl.mu.Lock()
	if err := ctxError(impl.ctx); err != nil {
		impl.mu.Unlock()
		return err
	}
	if impl.closed || impl.done {
		impl.mu.Unlock()
		return errQueueClosed
	}
	for impl.length == queueCapacity && !impl.closed && !impl.done && ctxError(impl.ctx) == nil {
		impl.wakeup.Wait()
	}
	if err := ctxError(impl.ctx); err != nil {
		impl.mu.Unlock()
		return err
	}
	if impl.closed || impl.done {
		impl.mu.Unlock()
		return errQueueClosed
	}
	slot := (impl.head + impl.length) % queueCapacity
	impl.events[slot] = ev
	impl.resetAfter[slot] = false
	impl.length++
	impl.wakeup.Broadcast()
	if impl.recorder != nil {
		impl.recorder.Observe(ev)
	}
	impl.mu.Unlock()
	return nil
}

func ctxError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func queueSent(queue *Queue) []string {
	if queue == nil || queue.impl == nil {
		return nil
	}
	impl := queue.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return append([]string(nil), impl.sent...)
}
