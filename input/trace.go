package input

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/egoist/mygo/ui"
)

const maxTraceEvents = 65536

type traceRecorderImpl struct {
	mu      sync.Mutex
	nextSeq uint64
	events  []TraceEvent
}

func newTraceRecorder() *TraceRecorder {
	return &TraceRecorder{impl: &traceRecorderImpl{}}
}

func traceKindOf(kind ui.InputKind) TraceKind {
	switch kind {
	case ui.InputKeyDown:
		return KeyDown
	case ui.InputKeyUp:
		return KeyUp
	case ui.InputText:
		return Text
	case ui.InputCompose:
		return Compose
	case ui.InputCommand:
		return Command
	default:
		return 0
	}
}

func inputKindOf(kind TraceKind) (ui.InputKind, bool) {
	switch kind {
	case KeyDown:
		return ui.InputKeyDown, true
	case KeyUp:
		return ui.InputKeyUp, true
	case Text:
		return ui.InputText, true
	case Compose:
		return ui.InputCompose, true
	case Command:
		return ui.InputCommand, true
	default:
		return 0, false
	}
}

func traceRecorderObserve(recorder *TraceRecorder, ev ui.InputEvent) {
	if recorder == nil || recorder.impl == nil {
		return
	}
	impl := recorder.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	impl.nextSeq++
	impl.events = append(impl.events, TraceEvent{
		Seq:       impl.nextSeq,
		At:        ev.Time,
		Kind:      traceKindOf(ev.Kind),
		Key:       uint16(ev.Key),
		Code:      uint16(ev.Code),
		Mods:      uint8(ev.Mods),
		Repeat:    ev.Repeat,
		Text:      ev.Text,
		Caret:     ev.Caret,
		Cancelled: ev.Canceled,
	})
	if excess := len(impl.events) - maxTraceEvents; excess > 0 {
		kept := make([]TraceEvent, len(impl.events)-excess)
		copy(kept, impl.events[excess:])
		impl.events = kept
	}
}

func traceRecorderEvents(recorder *TraceRecorder) []TraceEvent {
	if recorder == nil || recorder.impl == nil {
		return nil
	}
	impl := recorder.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	return append([]TraceEvent(nil), impl.events...)
}

func marshalTrace(events []TraceEvent) ([]byte, error) {
	var buf bytes.Buffer
	for index := range events {
		line, err := json.Marshal(events[index])
		if err != nil {
			return nil, fmt.Errorf("input: marshal trace event %d: %w", index+1, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func unmarshalTrace(data []byte) ([]TraceEvent, error) {
	lines := bytes.Split(data, []byte("\n"))
	events := make([]TraceEvent, 0, len(lines))
	var prevSeq uint64
	for index, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev TraceEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("input: unmarshal trace line %d: %w", index+1, err)
		}
		if _, ok := inputKindOf(ev.Kind); !ok {
			return nil, fmt.Errorf("input: trace line %d has invalid kind %d", index+1, ev.Kind)
		}
		if ev.Seq == 0 || ev.Seq <= prevSeq {
			return nil, fmt.Errorf("input: trace line %d has Seq %d, want strictly increasing after %d", index+1, ev.Seq, prevSeq)
		}
		prevSeq = ev.Seq
		events = append(events, ev)
	}
	return events, nil
}

func replayTrace(events []TraceEvent, sink func(ui.InputEvent)) error {
	if sink == nil {
		return errors.New("input: replay sink is nil")
	}
	var prevSeq uint64
	for index := range events {
		kind, ok := inputKindOf(events[index].Kind)
		if !ok {
			return fmt.Errorf("input: replay event %d has invalid kind %d", index+1, events[index].Kind)
		}
		if events[index].Seq == 0 || events[index].Seq <= prevSeq {
			return fmt.Errorf("input: replay event %d has Seq %d, want strictly increasing after %d", index+1, events[index].Seq, prevSeq)
		}
		prevSeq = events[index].Seq
		sink(ui.InputEvent{
			Kind:     kind,
			Key:      ui.Key(events[index].Key),
			Code:     ui.KeyCode(events[index].Code),
			Mods:     ui.Modifiers(events[index].Mods),
			Repeat:   events[index].Repeat,
			Text:     events[index].Text,
			Caret:    events[index].Caret,
			Canceled: events[index].Cancelled,
			Time:     events[index].At,
		})
	}
	return nil
}
