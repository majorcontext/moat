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

func rawTermios(tio *unix.Termios) {
	tio.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	tio.Oflag &^= unix.OPOST
	tio.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	tio.Cflag &^= unix.CSIZE | unix.PARENB
	tio.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	tio.Cc[unix.VMIN] = 1
	tio.Cc[unix.VTIME] = 0
}

func applyFraming(tio *unix.Termios, s Settings) {
	if s.DataBits != 0 {
		tio.Cflag &^= unix.CSIZE
		switch s.DataBits {
		case 5:
			tio.Cflag |= unix.CS5
		case 6:
			tio.Cflag |= unix.CS6
		case 7:
			tio.Cflag |= unix.CS7
		default:
			tio.Cflag |= unix.CS8
		}
	}
	if s.StopBits != 0 {
		if s.StopBits == 2 {
			tio.Cflag |= unix.CSTOPB
		} else {
			tio.Cflag &^= unix.CSTOPB
		}
	}
	switch s.Parity {
	case ParityOdd:
		tio.Cflag |= unix.PARENB | unix.PARODD
	case ParityEven:
		tio.Cflag |= unix.PARENB
		tio.Cflag &^= unix.PARODD
	default:
		tio.Cflag &^= unix.PARENB | unix.PARODD
	}
	switch s.FlowControl {
	case FlowRTSCTS:
		tio.Cflag |= unix.CRTSCTS
		tio.Iflag &^= unix.IXON | unix.IXOFF
	case FlowXONXOFF:
		tio.Cflag &^= unix.CRTSCTS
		tio.Iflag |= unix.IXON | unix.IXOFF
	default:
		tio.Cflag &^= unix.CRTSCTS
		tio.Iflag &^= unix.IXON | unix.IXOFF
	}
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
	return uint32(tio.Ospeed)
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
