// Package serialtest provides fakes for testing serial device handling without
// hardware.
//
// FakePort carries data over a real pty pair, so the read/write path under test
// is the one a real tty takes. Line settings and control lines are recorded
// rather than applied, because a pty cannot express them: TIOCMGET on a pty
// returns ENOTTY, which is the whole reason moat speaks RFC2217 rather than
// relaying through a pty. Tests assert on the recorded values.
package serialtest

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialport"
)

// FakePort implements serialport.Port over a pty pair.
type FakePort struct {
	master *os.File
	slave  *os.File
	name   string

	mu          sync.Mutex
	settings    serialport.Settings
	modem       serialport.Modem
	breaks      int
	purges      int
	settingsLog []serialport.Settings
	modemLog    []serialport.Modem
	closed      bool
}

// NewFakePort returns a fake port and registers its cleanup.
func NewFakePort(t *testing.T) *FakePort {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	// A fresh pty is in canonical mode with echo, which would buffer reads until
	// a newline and mangle binary payloads. The real port puts the line in raw
	// mode on open, so the fake must too, or tests would pass against behavior
	// no serial device has.
	if _, err := term.MakeRaw(int(slave.Fd())); err != nil {
		master.Close()
		slave.Close()
		t.Fatalf("putting pty in raw mode: %v", err)
	}
	p := &FakePort{master: master, slave: slave, name: slave.Name()}
	t.Cleanup(func() { p.Close() })
	return p
}

// Peer is the far end of the wire — what a device would read and write.
func (p *FakePort) Peer() io.ReadWriter { return p.slave }

func (p *FakePort) Read(b []byte) (int, error)  { return p.master.Read(b) }
func (p *FakePort) Write(b []byte) (int, error) { return p.master.Write(b) }
func (p *FakePort) Name() string                { return p.name }

// Close closes both ends. It is safe to call more than once, since both the
// test cleanup and the code under test may close the port.
func (p *FakePort) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	err := p.master.Close()
	if serr := p.slave.Close(); err == nil {
		err = serr
	}
	return err
}

// ApplySettings records the requested line settings.
func (p *FakePort) ApplySettings(s serialport.Settings) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Merge, matching the real port's contract that zero fields are unchanged.
	if s.Baud != 0 {
		p.settings.Baud = s.Baud
	}
	if s.DataBits != 0 {
		p.settings.DataBits = s.DataBits
	}
	if s.StopBits != 0 {
		p.settings.StopBits = s.StopBits
	}
	p.settings.Parity = s.Parity
	p.settings.FlowControl = s.FlowControl
	p.settingsLog = append(p.settingsLog, p.settings)
	return nil
}

// SetModem records the requested control lines.
func (p *FakePort) SetModem(m serialport.Modem) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.modem = m
	p.modemLog = append(p.modemLog, m)
	return nil
}

// SendBreak records a break.
func (p *FakePort) SendBreak() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.breaks++
	return nil
}

// FlushBuffers records a purge. A pty has no kernel receive queue to flush
// that would matter to assertions, so recording is enough to prove the broker
// forwards PURGE_DATA to the port.
func (p *FakePort) FlushBuffers() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.purges++
	return nil
}

// LastSettings returns the most recently applied line settings.
func (p *FakePort) LastSettings() serialport.Settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings
}

// LastModem returns the most recently applied control lines.
func (p *FakePort) LastModem() serialport.Modem {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.modem
}

// ModemSequence returns every control-line change in order.
//
// The sequence matters, not just the final state: driving an ESP32 into its
// bootloader is a specific series of DTR/RTS transitions, so a relay that
// coalesced them would leave the final state correct and the board unflashed.
func (p *FakePort) ModemSequence() []serialport.Modem {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]serialport.Modem(nil), p.modemLog...)
}

// SettingsSequence returns every line-settings change in order.
func (p *FakePort) SettingsSequence() []serialport.Settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]serialport.Settings(nil), p.settingsLog...)
}

// BreakCount returns how many breaks were sent.
func (p *FakePort) BreakCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.breaks
}

// PurgeCount returns how many buffer flushes were requested.
func (p *FakePort) PurgeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.purges
}

// NewFakeEnumerator returns an Enumerator yielding a fixed device list.
func NewFakeEnumerator(devs ...serialdev.Device) serialdev.Enumerator {
	return serialdev.EnumeratorFunc(func(context.Context) ([]serialdev.Device, error) {
		return append([]serialdev.Device(nil), devs...), nil
	})
}

// NewFailingEnumerator returns an Enumerator that always fails, for testing how
// callers report an unreadable device list.
func NewFailingEnumerator(err error) serialdev.Enumerator {
	return serialdev.EnumeratorFunc(func(context.Context) ([]serialdev.Device, error) {
		return nil, err
	})
}
