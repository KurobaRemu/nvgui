package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"nvgui/diag"
	"nvgui/screen"
)

func TestRecordInput(t *testing.T) {
	t.Run("ordered export", func(t *testing.T) {
		var nilSession *Session
		nilSession.RecordInput(diag.TraceEvent{Kind: diag.TraceText, Text: "ignored"})
		empty := &Session{}
		empty.RecordInput(diag.TraceEvent{Kind: diag.TraceText, Text: "ignored"})

		recorder := diag.NewRecorder(30*time.Second, fixedClock{})
		session := &Session{recorder: recorder, metaReady: true}
		const events = 32
		ready := make([]chan struct{}, events)
		for index := range ready {
			ready[index] = make(chan struct{})
		}
		var wait sync.WaitGroup
		close(ready[0])
		for index := 0; index < events; index++ {
			index := index
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-ready[index]
				session.RecordInput(diag.TraceEvent{
					Seq:  uint64(index + 1),
					Kind: diag.TraceText,
					Text: "x",
				})
				if index+1 < events {
					close(ready[index+1])
				}
			}()
		}
		wait.Wait()
		dir := filepath.Join(t.TempDir(), "bundle")
		recorder.Keyframe(keyframeState())
		if err := recorder.Export(dir, diag.Meta{}); err != nil {
			t.Fatalf("Export: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "input-trace.jsonl"))
		if err != nil {
			t.Fatalf("read input-trace.jsonl: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("input-trace.jsonl is empty")
		}
		bundle, err := diag.ReadBundle(dir)
		if err != nil {
			t.Fatalf("ReadBundle: %v", err)
		}
		if len(bundle.InputEvents) != events {
			t.Fatalf("InputEvents len = %d, want %d", len(bundle.InputEvents), events)
		}
		for index, event := range bundle.InputEvents {
			if event.Seq != uint64(index+1) {
				t.Fatalf("InputEvents[%d].Seq = %d, want %d", index, event.Seq, index+1)
			}
		}
	})

	t.Run("concurrent record and export", func(t *testing.T) {
		const events = 64
		const exporters = 8
		recorder := diag.NewRecorder(30*time.Second, fixedClock{})
		recorder.Keyframe(keyframeState())
		done := make(chan struct{})
		close(done)
		session := &Session{
			recorder:  recorder,
			metaReady: true,
			redraws:   make(chan []any, 128),
			done:      done,
		}
		root := t.TempDir()
		start := make(chan struct{})
		errors := make(chan error, events+exporters)
		var wait sync.WaitGroup
		for index := 0; index < events; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				session.RecordInput(diag.TraceEvent{Kind: diag.TraceText, Text: strconv.Itoa(index)})
				errors <- nil
			}(index)
		}
		for index := 0; index < exporters; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				dir := filepath.Join(root, "concurrent-"+strconv.Itoa(index))
				if err := session.ExportDiag(dir); err != nil {
					errors <- fmt.Errorf("concurrent ExportDiag %d: %w", index, err)
					return
				}
				if _, err := diag.ReadBundle(dir); err != nil {
					errors <- fmt.Errorf("concurrent ReadBundle %d: %w", index, err)
					return
				}
				errors <- nil
			}(index)
		}
		close(start)
		wait.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}

		dir := filepath.Join(root, "final")
		if err := session.ExportDiag(dir); err != nil {
			t.Fatalf("final ExportDiag: %v", err)
		}
		bundle, err := diag.ReadBundle(dir)
		if err != nil {
			t.Fatalf("final ReadBundle: %v", err)
		}
		if len(bundle.InputEvents) != events {
			t.Fatalf("InputEvents len = %d, want %d", len(bundle.InputEvents), events)
		}
		seen := make(map[uint64]bool, events)
		for index, event := range bundle.InputEvents {
			if want := uint64(index + 1); event.Seq != want {
				t.Fatalf("InputEvents[%d].Seq = %d, want %d", index, event.Seq, want)
			}
			if seen[event.Seq] {
				t.Fatalf("InputEvents duplicate Seq %d", event.Seq)
			}
			seen[event.Seq] = true
		}
	})
}

func keyframeState() *screen.State {
	return &screen.State{
		Seq:        1,
		Grids:      map[int]*screen.Grid{1: {Width: 1, Height: 1, Cells: [][]screen.Cell{{{}}}}},
		Attrs:      map[int]screen.Attr{0: {}},
		Placements: map[int]screen.Placement{},
		Cursor:     screen.Cursor{Grid: 1},
		Mode:       "normal",
	}
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
