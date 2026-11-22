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

	mu          sync.Mutex
	events      [queueCapacity]ui.InputEvent
	slotAllowed [queueCapacity]bool
	head        int
	length      int
	wakeup      *sync.Cond
	closing     chan struct{}
	stopped     chan struct{}
	sent        []string
	runErr      error
	started     bool
	closed      bool
	done        bool
	ctx         context.Context

	pending    *ui.InputEvent
	swallowKey ui.Key
	swallowAlt bool
	composing  bool
	lastCode   ui.KeyCode
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

func (impl *queueImpl) keys(ev ui.InputEvent, imeAllowed bool) string {
	switch ev.Kind {
	case ui.InputText:
		code := impl.lastCode
		impl.lastCode = ui.CodeUnknown
		impl.composing = false
		text := ev.Text
		if !imeAllowed {
			text = normalizeNormalModeCJKPunctuation(text, code)
		}
		if impl.pending != nil {
			impl.pending = nil
			impl.clearSwallow()
			return escapeText(text)
		}
		if impl.swallowKey != 0 {
			if swallowModifierTextEcho(ev, impl.swallowKey, impl.swallowAlt) {
				impl.clearSwallow()
				return ""
			}
			if !impl.swallowAlt {
				impl.clearSwallow()
			}
		}
		return escapeText(text)
	case ui.InputCompose:
		impl.lastCode = ui.CodeUnknown
		impl.pending = nil
		impl.clearSwallow()
		if ev.Canceled || ev.Text == "" {
			impl.composing = false
		} else {
			impl.composing = true
		}
		return ""
	case ui.InputKeyDown:
		if !isModifierOrLockCode(ev.Code) {
			impl.lastCode = ev.Code
		} else {
			impl.lastCode = ui.CodeUnknown
		}
		impl.pending = nil
		impl.clearSwallow()
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
		if ev.Code != ui.CodeUnknown && impl.lastCode == ev.Code {
			impl.lastCode = ui.CodeUnknown
		}
		if _, ok := Notation(ev); !ok {
			return ""
		}
		if impl.composing {
			impl.pending = nil
			impl.clearSwallow()
			return ""
		}
		if impl.pending != nil && impl.pending.Key == ev.Key {
			down := *impl.pending
			impl.pending = nil
			notation, _ := Notation(down)
			return notation
		}
		if impl.swallowAlt {
			impl.clearSwallow()
		} else if impl.swallowKey != 0 && impl.swallowKey == ev.Key {
			impl.clearSwallow()
		}
		return ""
	default:
		return ""
	}
}

func (impl *queueImpl) clearSwallow() {
	impl.swallowKey = 0
	impl.swallowAlt = false
}

func swallowModifierTextEcho(ev ui.InputEvent, key ui.Key, alt bool) bool {
	if alt {
		return true
	}
	if ev.Mods&ui.Ctrl != 0 {
		return ev.Key == ui.KeyUnknown || ev.Key == key
	}
	return ev.Mods == 0 && len(ev.Text) == 1 && keyTextEcho(key, ev.Text[0])
}

type keyEcho struct {
	plain   byte
	control byte
	shifted byte
}

var keyEchoes = func() map[ui.Key]keyEcho {
	echoes := make(map[ui.Key]keyEcho)
	for index := range 26 {
		char := byte('a' + index)
		echoes[ui.KeyA+ui.Key(index)] = keyEcho{plain: char, control: byte(index + 1), shifted: char}
	}
	for index, shifted := range []byte(")!@#$%^&*(") {
		char := byte('0' + index)
		echoes[ui.Key0+ui.Key(index)] = keyEcho{plain: char, control: noControlByte, shifted: shifted}
	}
	punctuation := map[ui.Key]keyEcho{
		ui.KeySpace:        {plain: ' ', control: 0x00, shifted: ' '},
		ui.KeyMinus:        {plain: '-', control: 0x1f, shifted: '_'},
		ui.KeyEqual:        {plain: '=', control: noControlByte, shifted: '+'},
		ui.KeyComma:        {plain: ',', control: noControlByte, shifted: '<'},
		ui.KeyPeriod:       {plain: '.', control: noControlByte, shifted: '>'},
		ui.KeySlash:        {plain: '/', control: noControlByte, shifted: '?'},
		ui.KeySemicolon:    {plain: ';', control: noControlByte, shifted: ':'},
		ui.KeyQuote:        {plain: '\'', control: noControlByte, shifted: '"'},
		ui.KeyBracketLeft:  {plain: '[', control: 0x1b, shifted: '{'},
		ui.KeyBracketRight: {plain: ']', control: 0x1d, shifted: '}'},
		ui.KeyBackslash:    {plain: '\\', control: 0x1c, shifted: '|'},
		ui.KeyBackquote:    {plain: '`', control: noControlByte, shifted: '~'},
		ui.Key6:            {plain: '6', control: 0x1e, shifted: '^'},
	}
	for key, echo := range punctuation {
		echoes[key] = echo
	}
	return echoes
}()

func keyTextEcho(key ui.Key, echo byte) bool {
	candidates, ok := keyEchoes[key]
	if !ok {
		return false
	}
	if echo >= 'A' && echo <= 'Z' {
		echo += 'a' - 'A'
	}
	switch {
	case echo == candidates.plain, echo == candidates.shifted:
		return true
	case candidates.control != noControlByte && echo == candidates.control:
		return true
	}
	return false
}

const noControlByte = 0xff

func (impl *queueImpl) finish(err error) {
	if impl.done {
		return
	}
	impl.runErr = err
	impl.done = true
	impl.wakeup.Broadcast()
	close(impl.stopped)
}

func (impl *queueImpl) pop() (ui.InputEvent, bool, bool, bool) {
	if impl.length == 0 {
		return ui.InputEvent{}, false, false, false
	}
	slot := impl.head
	ev := impl.events[impl.head]
	imeAllowed := impl.slotAllowed[slot]
	resetAfter := impl.resetAfter[slot]
	impl.events[impl.head] = ui.InputEvent{}
	impl.slotAllowed[slot] = false
	impl.resetAfter[slot] = false
	impl.head = (impl.head + 1) % queueCapacity
	impl.length--
	impl.wakeup.Broadcast()
	return ev, true, imeAllowed, resetAfter
}

func (impl *queueImpl) prepareDelivery() (string, bool, error) {
	ev, ok, imeAllowed, resetAfter := impl.pop()
	if !ok {
		return "", false, nil
	}
	keys := impl.keys(ev, imeAllowed)
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

func (queue *Queue) enqueue(ev ui.InputEvent, imeAllowed bool) error {
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
	impl.slotAllowed[slot] = imeAllowed
	impl.resetAfter[slot] = false
	impl.length++
	impl.wakeup.Broadcast()
	if impl.recorder != nil {
		impl.recorder.Observe(ev)
	}
	impl.mu.Unlock()
	return nil
}

func queueEnqueue(queue *Queue, ev ui.InputEvent) error {
	return queue.enqueue(ev, true)
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
