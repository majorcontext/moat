//go:build darwin

package serialport

import (
	"time"

	"golang.org/x/sys/unix"
)

const (
	getTermiosReq = unix.TIOCGETA
	setTermiosReq = unix.TIOCSETA
)

// setBaud sets the speed. Unlike Linux, Darwin stores the numeric rate directly
// in Ispeed/Ospeed (its Bxxx constants are the numbers themselves), so any rate
// the driver supports works without a lookup table.
func setBaud(tio *unix.Termios, baud uint32) error {
	tio.Ispeed = uint64(baud)
	tio.Ospeed = uint64(baud)
	return nil
}

// breakDuration is how long the break condition is held. Darwin has no
// single-ioctl break, so it is asserted and cleared explicitly; 250ms matches
// the duration Linux's TCSBRK uses by default.
const breakDuration = 250 * time.Millisecond

func sendBreak(fd int) error {
	if err := unix.IoctlSetInt(fd, unix.TIOCSBRK, 0); err != nil {
		return err
	}
	time.Sleep(breakDuration)
	return unix.IoctlSetInt(fd, unix.TIOCCBRK, 0)
}

// currentBaud reports the rate stored in a termios.
func currentBaud(tio *unix.Termios) uint32 {
	// Ospeed is uint64 on Darwin, but it holds a baud rate — always well within
	// uint32 (the fastest standard rate is 4 Mbaud).
	return uint32(tio.Ospeed) //nolint:gosec // G115: a baud rate fits in uint32
}

// flushBuffers implements Port.FlushBuffers.
//
// TIOCFLUSH on Darwin is _IOW('t', 16, int): the kernel copies a 4-byte queue
// selector from the address passed as the ioctl argument — the selector must
// be passed by pointer, not by value. (Contrast Linux, whose TCFLSH takes the
// selector by value.) TCIOFLUSH discards both pending input and undelivered
// output.
func flushBuffers(fd int) error {
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, unix.TCIOFLUSH)
}
