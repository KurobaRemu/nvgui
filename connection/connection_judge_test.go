package connection_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neovim/go-client/msgpack/rpc"

	"nvgui/connection"
	"nvgui/protocol"
)

type lockedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (buffer *lockedBuffer) Write(chunk []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.data = append(buffer.data, chunk...)
	return len(chunk), nil
}

func (buffer *lockedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.data...)
}

func TestConnectionLifecycleAndTap(t *testing.T) {
	tap := &lockedBuffer{}
	firstFlush := make(chan struct{})
	var flushOnce sync.Once
	var redrawMu sync.Mutex
	redrawCount := 0
	conn, err := connection.Start(context.Background(), connection.Options{
		Args:   []string{"--clean", "-n"},
		Width:  80,
		Height: 24,
		Tap:    tap,
		OnRedraw: func(args []any) {
			redrawMu.Lock()
			redrawCount++
			redrawMu.Unlock()
			for _, item := range args {
				group, ok := item.([]any)
				if ok && len(group) > 0 && group[0] == "flush" {
					flushOnce.Do(func() { close(firstFlush) })
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if conn.Nvim() == nil {
		t.Fatal("Nvim() = nil")
	}
	if channel := conn.Exited(); channel == nil {
		t.Fatal("Exited() = nil")
	} else {
		select {
		case <-channel:
			t.Fatal("Exited() closed before :qa!")
		default:
		}
	}
	if code := conn.ExitCode(); code != -1 {
		t.Fatalf("ExitCode() before exit = %d, want -1", code)
	}

	select {
	case <-firstFlush:
	case <-time.After(5 * time.Second):
		t.Fatal("no redraw batch containing flush within 5s")
	}
	pid := nvimPID(t, conn)
	redrawMu.Lock()
	observedRedraws := redrawCount
	redrawMu.Unlock()
	if observedRedraws == 0 {
		t.Fatal("OnRedraw was never called")
	}
	if err := conn.Nvim().Command("qa!"); err != nil &&
		!errors.Is(err, io.EOF) && !errors.Is(err, rpc.ErrClosed) &&
		!strings.Contains(err.Error(), "session closed") {
		t.Fatalf(":qa! returned an unexpected error: %v", err)
	}
	select {
	case <-conn.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("Exited() did not close within 5s after :qa!")
	}
	if code := conn.ExitCode(); code != 0 {
		t.Fatalf("ExitCode() = %d, want 0", code)
	}

	messages, err := protocol.ReadMessages(tap.Bytes())
	if err != nil {
		t.Fatalf("ReadMessages(Tap bytes): %v", err)
	}
	var joined []byte
	for _, message := range messages {
		joined = append(joined, message.Raw...)
	}
	if !bytes.Equal(joined, tap.Bytes()) {
		t.Fatalf("Tap bytes cannot be completely split: got %d bytes, want %d", len(joined), len(tap.Bytes()))
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if processExists(pid) {
		t.Fatalf("nvim child process %d still exists after Close", pid)
	}
}

func nvimPID(t *testing.T, conn *connection.Conn) int {
	t.Helper()
	var pid int
	if err := conn.Nvim().Eval("getpid()", &pid); err != nil {
		t.Fatalf("getpid(): %v", err)
	}
	if pid <= 0 {
		t.Fatalf("getpid() = %d", pid)
	}
	return pid
}

func processExists(pid int) bool {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if err != nil {
		return false
	}
	status := strings.TrimSpace(string(output))
	return status != "" && !strings.Contains(status, "Z")
}
