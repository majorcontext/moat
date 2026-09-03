//go:build linux

package serialport

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	getTermiosReq = unix.TCGETS
	setTermiosReq = unix.TCSETS
)

// baudCodes maps numeric baud rates to the kernel's CBAUD encoding. Linux
// encodes speed as flag bits rather than a number, so arbitrary rates need
// BOTHER/termios2; the standard table covers every rate serial tooling uses,
// including the 921600 esptool prefers.
var baudCodes = map[uint32]uint32{
	1200: unix.B1200, 2400: unix.B2400, 4800: unix.B4800, 9600: unix.B9600,
	19200: unix.B19200, 38400: unix.B38400, 57600: unix.B57600,
	115200: unix.B115200, 230400: unix.B230400, 460800: unix.B460800,
	500000: unix.B500000, 576000: unix.B576000, 921600: unix.B921600,
	1000000: unix.B1000000, 1152000: unix.B1152000, 1500000: unix.B1500000,
	2000000: unix.B2000000, 3000000: unix.B3000000,
}

func setBaud(tio *unix.Termios, baud uint32) error {
	code, ok := baudCodes[baud]
	if !ok {
		return fmt.Errorf("unsupported baud rate %d (supported: %s)", baud, supportedBauds())
	}
	tio.Cflag = (tio.Cflag &^ uint32(unix.CBAUD)) | code
	tio.Ispeed = code
	tio.Ospeed = code
	return nil
}

func supportedBauds() string {
	rates := make([]int, 0, len(baudCodes))
	for r := range baudCodes {
		rates = append(rates, int(r))
	}
	sort.Ints(rates)
	parts := make([]string, len(rates))
	for i, r := range rates {
		parts[i] = fmt.Sprint(r)
	}
	return strings.Join(parts, ", ")
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

func sendBreak(fd int) error {
	// TCSBRK with a zero argument drains output, then transmits a break of the
	// driver's default duration (0.25-0.5s).
	return unix.IoctlSetInt(fd, unix.TCSBRK, 0)
}

// flushBuffers implements Port.FlushBuffers. Linux's TIOCFLUSH ioctl is
// exposed as TCFLSH (same request number, 0x540B) with the queue selector as
// its argument; TCIOFLUSH discards both pending input and undelivered output.
func flushBuffers(fd int) error {
	return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIOFLUSH)
}

// currentBaud reports the rate encoded in a termios, or 0 if it is not one of
// the standard rates.
func currentBaud(tio *unix.Termios) uint32 {
	code := tio.Cflag & uint32(unix.CBAUD)
	for rate, c := range baudCodes {
		if c == code {
			return rate
		}
	}
	return 0
}
