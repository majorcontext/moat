package serialbroker

import (
	"net"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/serialport"
	"github.com/majorcontext/moat/internal/serialtest"
)

// recordingConn is a net.Conn whose Write blocks until the test closes it,
// modeling a client that stopped reading (full TCP window).
type stalledConn struct {
	net.Conn
	unblock chan struct{}
}

func (c *stalledConn) Write(b []byte) (int, error) {
	<-c.unblock
	return 0, net.ErrClosed
}

// TestRepliesAreWrittenOutsideTheSessionMutex pins the mutex discipline of
// handleCommand: the reply write must happen after s.mu is released. The rx
// pump takes the same mutex in record(), so a write held under it would stall
// the pump, the tty would stop being drained, and UART bytes would be lost
// while the client is stalled.
func TestRepliesAreWrittenOutsideTheSessionMutex(t *testing.T) {
	fp := serialtest.NewFakePort(t)

	// net.Pipe is fully in memory with no kernel buffering; a stalledConn
	// wrapping it blocks in Write until the test says otherwise.
	server, client := net.Pipe()
	t.Cleanup(func() { client.Close() })

	s := &session{
		listener: &listener{
			approved: Approved{Name: "esp32"},
			broker:   New(Options{OpenPort: func(string) (serialport.Port, error) { return fp, nil }}),
		},
		conn:     &stalledConn{Conn: server, unblock: make(chan struct{})},
		settings: serialport.Settings{},
	}
	s.setPort(fp)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// SET-BAUDRATE with a stalled client: the reply must be queued and
		// written outside the lock, so this call must return even though
		// the write below blocks in Write.
		s.handleCommand(1, []byte{0}) // 1 = CmdSetBaudRate; payload decodes to 0 -> reply with current baud
	}()

	// If the reply write were held under s.mu, record() could not take it
	// and this would time out while handleCommand is stuck in the blocked
	// Write.
	select {
	case <-done:
		t.Fatal("handleCommand returned before the blocked write finished — the test needs the write to still be pending")
	case <-time.After(50 * time.Millisecond):
	}

	// The mutex must be free while the reply write is parked.
	free := make(chan struct{})
	go func() {
		s.mu.Lock()
		_ = len(s.pendingReplies) // read guarded state — a real critical section
		s.mu.Unlock()
		close(free)
	}()
	select {
	case <-free:
	case <-time.After(2 * time.Second):
		t.Fatal("s.mu is held while the reply write is blocked — a stalled client stalls the rx pump and loses device bytes")
	}

	close(s.conn.(*stalledConn).unblock)
	<-done
	_ = client
}
