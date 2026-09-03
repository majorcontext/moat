// Package serialport opens host serial devices and applies line settings.
//
// The Port interface exists so the broker can be tested without hardware: see
// internal/serialtest for a pty-backed fake. Line settings are expressed in
// plain values here (baud as a number, 5-8 data bits) rather than in RFC2217 or
// termios encodings, so neither the wire format nor the kernel's bit layout
// leaks into the broker.
package serialport

import "io"

// Parity values.
const (
	ParityNone uint8 = iota
	ParityOdd
	ParityEven
)

// Flow control values.
const (
	FlowNone uint8 = iota
	FlowRTSCTS
	FlowXONXOFF
)

// Settings are the line settings of a serial port.
type Settings struct {
	Baud        uint32 // e.g. 115200; 0 means "leave unchanged"
	DataBits    uint8  // 5-8; 0 means "leave unchanged"
	StopBits    uint8  // 1 or 2; 0 means "leave unchanged"
	Parity      uint8  // ParityNone, ParityOdd, ParityEven
	FlowControl uint8  // FlowNone, FlowRTSCTS, FlowXONXOFF
}

// Modem holds the output control lines.
//
// These are the lines a pty cannot represent, and the reason moat speaks
// RFC2217: toggling DTR and RTS in sequence is how an ESP32 is driven into its
// bootloader.
type Modem struct {
	DTR bool
	RTS bool
}

// Port is an open serial device.
type Port interface {
	io.ReadWriteCloser

	// ApplySettings sets baud, framing, and flow control. Zero-valued fields
	// are left unchanged.
	ApplySettings(Settings) error

	// SetModem drives the DTR and RTS output lines.
	SetModem(Modem) error

	// SendBreak transmits a break condition.
	SendBreak() error

	// FlushBuffers discards pending input and output on the device. It backs
	// RFC2217 PURGE_DATA, which pyserial's client issues from inside
	// Serial.open() and blocks on — a server that ignores it fails every
	// pyserial connect, not just explicit flush calls.
	FlushBuffers() error

	// Name is the host device path, for logs and errors.
	Name() string
}
