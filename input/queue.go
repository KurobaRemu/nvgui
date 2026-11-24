package input

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/neovim/go-client/nvim"
)

const queueCapacity = 256

var (
	errQueueClosed           = errors.New("input: queue closed")
	errQueueNilSender        = errors.New("input: queue sender is nil")
	errQueueMouseUnsupported = errors.New("input: queue sender does not support mouse input")
	errQueueEmptyMouseButton = errors.New("input: mouse button is empty")
)

type queueEntryKind uint8

const (
	queueEntryInput queueEntryKind = iota
	queueEntryMouse
)

type queueEntry struct {
	kind       queueEntryKind
	ev         ui.InputEvent
	mouse      MouseCommand
	imeAllowed bool
	resetAfter bool
}

type queueDelivery struct {
	kind  queueEntryKind
	keys  string
	mouse MouseCommand
}

type queueImpl struct {
	sender   Sender
	recorder *TraceRecorder

	mu        sync.Mutex
	entries   [queueCapacity]queueEntry
	head      int
	length    int
	wakeup    *sync.Cond
	closing   chan struct{}
	stopped   chan struct{}
	sent      []string
	sentMouse []MouseCommand
	runErr    error
	started   bool
	closed    bool
	done      bool
	ctx       context.Context

	pending    *ui.InputEvent
	swallowKey ui.Key
	swallowAlt bool
	composing  bool
	lastCode   ui.KeyCode
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

func (impl *queueImpl) pop() (queueEntry, bool) {
	if impl.length == 0 {
		return queueEntry{}, false
	}
	slot := impl.head
	entry := impl.entries[slot]
	impl.entries[slot] = queueEntry{}
	impl.head = (impl.head + 1) % queueCapacity
	impl.length--
	impl.wakeup.Broadcast()
	return entry, true
}

func canMergeWheel(existing queueEntry, next MouseCommand) bool {
	if existing.kind != queueEntryMouse {
		return false
	}
	return existing.mouse.Button == "wheel" && next.Button == "wheel" &&
		existing.mouse.Action == next.Action &&
		existing.mouse.Grid == next.Grid &&
		existing.mouse.Row == next.Row &&
		existing.mouse.Col == next.Col &&
		existing.mouse.Modifier == next.Modifier
}

func saturatingAddInt(a, b int) int {
	if b > 0 && a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

func mouseTraceEvent(cmd MouseCommand) ui.InputEvent {
	return ui.InputEvent{
		Kind:     ui.InputCommand,
		Mods:     cmd.Mods,
		Text:     fmt.Sprintf("mouse:%s:%s:mod=%s:grid=%d:row=%d:col=%d:count=%d", cmd.Button, cmd.Action, cmd.Modifier, cmd.Grid, cmd.Row, cmd.Col, cmd.Count),
		Canceled: cmd.Canceled,
		Time:     cmd.Time,
	}
}

func supportsMouseSender(sender Sender) bool {
	if sender == nil {
		return false
	}
	if _, ok := sender.(MouseBatchSender); ok {
		return true
	}
	if _, ok := sender.(*nvim.Nvim); ok {
		return true
	}
	_, ok := sender.(MouseSender)
	return ok
}

func (impl *queueImpl) prepareDelivery() (queueDelivery, bool, error) {
	entry, ok := impl.pop()
	if !ok {
		return queueDelivery{}, false, nil
	}
	switch entry.kind {
	case queueEntryInput:
		keys := impl.keys(entry.ev, entry.imeAllowed)
		if entry.resetAfter {
			impl.composing = false
		}
		if keys != "" {
			if impl.sender == nil {
				return queueDelivery{}, false, errQueueNilSender
			}
			impl.sent = append(impl.sent, keys)
		}
		return queueDelivery{kind: queueEntryInput, keys: keys}, true, nil
	case queueEntryMouse:
		if entry.resetAfter {
			impl.composing = false
		}
		if entry.mouse.Button == "wheel" {
			for impl.length > 0 && impl.entries[impl.head].kind == queueEntryMouse && canMergeWheel(entry, impl.entries[impl.head].mouse) {
				next, _ := impl.pop()
				entry.mouse.Count = saturatingAddInt(entry.mouse.Count, next.mouse.Count)
				if !next.mouse.Time.IsZero() {
					entry.mouse.Time = next.mouse.Time
				}
				if next.resetAfter {
					impl.composing = false
				}
			}
		}
		if impl.sender == nil {
			return queueDelivery{}, false, errQueueNilSender
		}
		if !supportsMouseSender(impl.sender) {
			return queueDelivery{}, false, errQueueMouseUnsupported
		}
		if entry.mouse.Count <= 0 {
			entry.mouse.Count = 1
		}
		impl.sentMouse = append(impl.sentMouse, entry.mouse)
		return queueDelivery{kind: queueEntryMouse, mouse: entry.mouse}, true, nil
	default:
		return queueDelivery{}, true, nil
	}
}

func (impl *queueImpl) sendDelivery(delivery queueDelivery) error {
	switch delivery.kind {
	case queueEntryInput:
		if delivery.keys == "" {
			return nil
		}
		_, err := impl.sender.Input(delivery.keys)
		return err
	case queueEntryMouse:
		cmd := delivery.mouse
		count := cmd.Count
		if count <= 0 {
			count = 1
		}
		if batchSender, ok := impl.sender.(MouseBatchSender); ok {
			return batchSender.InputMouseBatch(cmd.Button, cmd.Action, cmd.Modifier, cmd.Grid, cmd.Row, cmd.Col, count)
		}
		if count > 1 {
			if nv, ok := impl.sender.(*nvim.Nvim); ok {
				batch := nv.NewBatch()
				for range count {
					batch.InputMouse(cmd.Button, cmd.Action, cmd.Modifier, cmd.Grid, cmd.Row, cmd.Col)
				}
				return batch.Execute()
			}
		}
		mouseSender, ok := impl.sender.(MouseSender)
		if !ok {
			return errQueueMouseUnsupported
		}
		for range count {
			if err := mouseSender.InputMouse(cmd.Button, cmd.Action, cmd.Modifier, cmd.Grid, cmd.Row, cmd.Col); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
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
	impl.entries[lastSlot].resetAfter = true
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
		delivery, ok, err := impl.prepareDelivery()
		if err != nil {
			impl.finish(err)
			impl.mu.Unlock()
			return err
		}
		if ok {
			impl.mu.Unlock()
			if err := impl.sendDelivery(delivery); err != nil {
				impl.mu.Lock()
				impl.finish(err)
				impl.mu.Unlock()
				return err
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
			delivery, ok, err := impl.prepareDelivery()
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
			if err := impl.sendDelivery(delivery); err != nil {
				impl.mu.Lock()
				impl.finish(err)
				impl.mu.Unlock()
				return err
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
	impl.entries[slot] = queueEntry{
		kind:       queueEntryInput,
		ev:         ev,
		imeAllowed: imeAllowed,
	}
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

func queueInputMouse(queue *Queue, button, action, modifier string, grid, row, col int) error {
	return queueEnqueueMouse(queue, MouseCommand{
		Button:   button,
		Action:   action,
		Modifier: modifier,
		Grid:     grid,
		Row:      row,
		Col:      col,
		Count:    1,
	})
}

func queueEnqueueMouse(queue *Queue, cmd MouseCommand) error {
	if queue == nil || queue.impl == nil {
		return errors.New("input: nil queue")
	}
	if cmd.Button == "" {
		return errQueueEmptyMouseButton
	}
	if cmd.Count <= 0 {
		cmd.Count = 1
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
	if impl.length > 0 {
		tail := (impl.head + impl.length - 1) % queueCapacity
		if canMergeWheel(impl.entries[tail], cmd) {
			impl.entries[tail].mouse.Count = saturatingAddInt(impl.entries[tail].mouse.Count, cmd.Count)
			if !cmd.Time.IsZero() {
				impl.entries[tail].mouse.Time = cmd.Time
			}
			impl.wakeup.Broadcast()
			if impl.recorder != nil {
				impl.recorder.Observe(mouseTraceEvent(cmd))
			}
			impl.mu.Unlock()
			return nil
		}
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
	if impl.length > 0 {
		tail := (impl.head + impl.length - 1) % queueCapacity
		if canMergeWheel(impl.entries[tail], cmd) {
			impl.entries[tail].mouse.Count = saturatingAddInt(impl.entries[tail].mouse.Count, cmd.Count)
			if !cmd.Time.IsZero() {
				impl.entries[tail].mouse.Time = cmd.Time
			}
			impl.wakeup.Broadcast()
			if impl.recorder != nil {
				impl.recorder.Observe(mouseTraceEvent(cmd))
			}
			impl.mu.Unlock()
			return nil
		}
	}
	slot := (impl.head + impl.length) % queueCapacity
	impl.entries[slot] = queueEntry{
		kind:  queueEntryMouse,
		mouse: cmd,
	}
	impl.length++
	impl.wakeup.Broadcast()
	if impl.recorder != nil {
		impl.recorder.Observe(mouseTraceEvent(cmd))
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

func queueSentMouse(queue *Queue) []MouseCommand {
	if queue == nil || queue.impl == nil {
		return nil
	}
	impl := queue.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return append([]MouseCommand(nil), impl.sentMouse...)
}
