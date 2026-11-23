package session

import (
	"testing"
	"time"

	"nvgui/diag"
	"nvgui/screen"
)

type redrawNotifyClock struct{}

func (redrawNotifyClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func TestRedrawsWakesOnCommittedFrameAndCoalesces(t *testing.T) {
	clock := redrawNotifyClock{}
	sess := &Session{
		reducer:      screen.NewReducer(),
		recorder:     diag.NewRecorder(30*time.Second, clock),
		watchdog:     diag.NewWatchdog(2*time.Second, clock, nil),
		redraws:      make(chan []any, 128),
		redrawNotify: make(chan struct{}, 1),
		done:         make(chan struct{}),
	}
	sess.wg.Add(1)
	go sess.run()
	t.Cleanup(func() {
		close(sess.done)
		sess.wg.Wait()
	})
	var nilSession *Session
	if nilSession.Redraws() != nil {
		t.Fatal("nil Session Redraws() returned a non-nil channel")
	}

	first := []any{[]any{"flush", []any{}}}
	sess.redraws <- first
	select {
	case <-sess.Redraws():
	case <-time.After(time.Second):
		t.Fatal("Redraws() did not wake on a committed frame")
	}

	for index := 0; index < 32; index++ {
		select {
		case sess.redraws <- []any{[]any{"flush", []any{}}}:
		default:
			t.Fatal("redraws channel blocked after rapid notifications")
		}
	}
	sess.syncReducer()
	select {
	case <-sess.Redraws():
	default:
		t.Fatal("rapid committed frames did not leave a coalesced notification")
	}
	select {
	case <-sess.Redraws():
		t.Fatal("32 rapid committed frames produced more than one notification")
	default:
	}
	if cap(sess.Redraws()) != 1 {
		t.Fatalf("Redraws() capacity = %d, want 1", cap(sess.Redraws()))
	}
}

func TestRedrawsCloseExitsCleanly(t *testing.T) {
	clock := redrawNotifyClock{}
	sess := &Session{
		reducer:      screen.NewReducer(),
		recorder:     diag.NewRecorder(30*time.Second, clock),
		watchdog:     diag.NewWatchdog(2*time.Second, clock, nil),
		redraws:      make(chan []any, 128),
		redrawNotify: make(chan struct{}, 1),
		done:         make(chan struct{}),
	}
	sess.wg.Add(1)
	go sess.run()
	sess.redraws <- []any{[]any{"flush", []any{}}}
	select {
	case <-sess.Redraws():
	case <-time.After(time.Second):
		t.Fatal("Redraws() did not wake before shutdown")
	}
	close(sess.done)
	done := make(chan struct{})
	go func() {
		sess.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Session.run() did not exit after Close signal")
	}
}
