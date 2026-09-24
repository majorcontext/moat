//go:build linux || darwin

package serialport

import "golang.org/x/sys/unix"

// rawTermios and applyFraming were byte-identical in termios_linux.go and
// termios_darwin.go. They use only portable unix constants, so the build-tag
// split bought nothing and made a framing bug fixable in one file and not the
// other. port_unix.go already sets the precedent for shared linux||darwin logic.
// Genuinely OS-specific pieces — the ioctl request numbers, baud encoding, and
// break handling — stay in their per-OS files.
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

// The decoders below are the inverse of applyFraming. They exist so a test can
// read back what was actually written to the tty: applyFraming hand-computes
// PARENB/PARODD/CSTOPB/CS5..CS8/CRTSCTS, and without a round trip an odd/even
// swap or a wrong CSIZE mask would pass every test and only show up as
// corruption on real hardware.

func dataBitsOf(tio *unix.Termios) uint8 {
	switch tio.Cflag & unix.CSIZE {
	case unix.CS5:
		return 5
	case unix.CS6:
		return 6
	case unix.CS7:
		return 7
	default:
		return 8
	}
}

func stopBitsOf(tio *unix.Termios) uint8 {
	if tio.Cflag&unix.CSTOPB != 0 {
		return 2
	}
	return 1
}

func parityOf(tio *unix.Termios) uint8 {
	if tio.Cflag&unix.PARENB == 0 {
		return ParityNone
	}
	if tio.Cflag&unix.PARODD != 0 {
		return ParityOdd
	}
	return ParityEven
}

func flowControlOf(tio *unix.Termios) uint8 {
	if tio.Cflag&unix.CRTSCTS != 0 {
		return FlowRTSCTS
	}
	if tio.Iflag&(unix.IXON|unix.IXOFF) != 0 {
		return FlowXONXOFF
	}
	return FlowNone
}
