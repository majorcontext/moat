//go:build linux || darwin

package serialport

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// tty is a serial device opened on the host.
type tty struct {
	f    *os.File
	path string

	mu    sync.Mutex
	modem Modem
}

// Open opens a serial device.
//
// It refuses anything that is not a tty, and that check is the device-class
// allowlist: storage, HID, and smartcard devices do not present as ttys, so
// there is no separate deny list that could drift out of sync with it.
//
// The port is opened non-blocking so Go's poller owns the fd, which lets Close
// interrupt a blocked Read — the broker relies on that to tear a session down.
func Open(path string) (Port, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("opening serial device %s: %w", path, err)
	}
	// A tty responds to TCGETS/TIOCGETA; anything else fails with ENOTTY.
	if _, err := unix.IoctlGetTermios(fd, getTermiosReq); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("%s is not a serial device: moat only exposes tty character devices", path)
	}
	// Exclusive access, so a second opener on the host gets EBUSY rather than
	// silently interleaving bytes with the container.
	if err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("claiming exclusive access to %s: %w", path, err)
	}
	t := &tty{f: os.NewFile(uintptr(fd), path), path: path}
	if err := t.makeRaw(); err != nil {
		t.f.Close()
		return nil, err
	}
	return t, nil
}

func (t *tty) Read(p []byte) (int, error)  { return t.f.Read(p) }
func (t *tty) Write(p []byte) (int, error) { return t.f.Write(p) }
func (t *tty) Close() error                { return t.f.Close() }
func (t *tty) Name() string                { return t.path }

// makeRaw puts the line in raw mode: no echo, no canonical processing, no
// input or output translation. Anything else would mangle firmware payloads.
func (t *tty) makeRaw() error {
	tio, err := unix.IoctlGetTermios(int(t.f.Fd()), getTermiosReq)
	if err != nil {
		return fmt.Errorf("reading terminal settings for %s: %w", t.path, err)
	}
	rawTermios(tio)
	if err := unix.IoctlSetTermios(int(t.f.Fd()), setTermiosReq, tio); err != nil {
		return fmt.Errorf("setting raw mode on %s: %w", t.path, err)
	}
	return nil
}

// ApplySettings implements Port.
func (t *tty) ApplySettings(s Settings) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	fd := int(t.f.Fd())
	tio, err := unix.IoctlGetTermios(fd, getTermiosReq)
	if err != nil {
		return fmt.Errorf("reading terminal settings for %s: %w", t.path, err)
	}
	if s.Baud != 0 {
		if err := setBaud(tio, s.Baud); err != nil {
			return fmt.Errorf("%s: %w", t.path, err)
		}
	}
	applyFraming(tio, s)
	if err := unix.IoctlSetTermios(fd, setTermiosReq, tio); err != nil {
		return fmt.Errorf("applying line settings to %s: %w", t.path, err)
	}
	return nil
}

// SetModem implements Port.
func (t *tty) SetModem(m Modem) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	fd := int(t.f.Fd())
	set, clear := 0, 0
	if m.DTR {
		set |= unix.TIOCM_DTR
	} else {
		clear |= unix.TIOCM_DTR
	}
	if m.RTS {
		set |= unix.TIOCM_RTS
	} else {
		clear |= unix.TIOCM_RTS
	}
	// Apply clears before sets so a transition never momentarily asserts both
	// lines; the ESP32 reset sequence depends on the exact transitions.
	if clear != 0 {
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCMBIC, clear); err != nil {
			return fmt.Errorf("clearing modem lines on %s: %w", t.path, err)
		}
	}
	if set != 0 {
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCMBIS, set); err != nil {
			return fmt.Errorf("setting modem lines on %s: %w", t.path, err)
		}
	}
	t.modem = m
	return nil
}

// ModemStatus implements Port. TIOCMGET writes the modem bit set through the
// ioctl argument pointer on both Linux and Darwin, and the TIOCM_* bit values
// are identical on both, so one implementation serves both platforms.
func (t *tty) ModemStatus() (ModemStatus, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	bits, err := unix.IoctlGetInt(int(t.f.Fd()), unix.TIOCMGET)
	if err != nil {
		return ModemStatus{}, fmt.Errorf("reading modem lines on %s: %w", t.path, err)
	}
	return ModemStatus{
		CTS: bits&unix.TIOCM_CTS != 0,
		DSR: bits&unix.TIOCM_DSR != 0,
		RI:  bits&unix.TIOCM_RI != 0,
		CD:  bits&unix.TIOCM_CD != 0,
	}, nil
}

// SendBreak implements Port.
func (t *tty) SendBreak() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := sendBreak(int(t.f.Fd())); err != nil {
		return fmt.Errorf("sending break on %s: %w", t.path, err)
	}
	return nil
}

// FlushBuffers implements Port. On both Linux and macOS the TIOCFLUSH ioctl
// takes the queue selector as its argument; TCIOFLUSH discards both pending
// input and undelivered output.
func (t *tty) FlushBuffers() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := flushBuffers(int(t.f.Fd())); err != nil {
		return fmt.Errorf("flushing %s: %w", t.path, err)
	}
	return nil
}

// currentSettings reads the line settings back from the device. It exists for
// tests: the port holds an exclusive claim (TIOCEXCL), so nothing else can open
// the device to inspect it.
func (t *tty) currentSettings() (Settings, error) {
	tio, err := unix.IoctlGetTermios(int(t.f.Fd()), getTermiosReq)
	if err != nil {
		return Settings{}, err
	}
	return Settings{Baud: currentBaud(tio)}, nil
}
