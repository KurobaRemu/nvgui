package session

import (
	"context"
	"testing"
	"time"

	"nvgui/connection"
)

type closeTestClock struct{}

func (closeTestClock) Now() time.Time { return time.Unix(0, 0) }

func TestCloseWaitsForBackgroundGoroutines(t *testing.T) {
	session, err := Start(context.Background(), connection.Options{
		Args:   []string{"--clean", "-n"},
		Width:  80,
		Height: 24,
	}, closeTestClock{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	session.wg.Wait()
	select {
	case <-session.done:
	default:
		t.Fatal("done was not closed")
	}
}
