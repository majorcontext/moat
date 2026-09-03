// Package rfc2217 implements the telnet com-port-control option (RFC 2217),
// which carries baud rate, data framing, DTR/RTS, and break over TCP.
//
// moat uses it because a pty cannot express control lines: TIOCMGET on a pty
// returns ENOTTY, so DTR and RTS — and therefore ESP32 auto-reset — have no pty
// representation at all. RFC2217 is the standard answer, and the one esptool
// and other pyserial-based tools already speak via rfc2217:// URLs. See
// docs/plans/2026-08-15-serial-devices-design.md.
package rfc2217

import "encoding/binary"

// Client-to-server com-port commands. Server-to-client replies use the same
// value plus 100, which is why CmdNotifyModemState below is 107 rather than 7.
const (
	CmdSignature          byte = 0
	CmdSetBaudRate        byte = 1
	CmdSetDataSize        byte = 2
	CmdSetParity          byte = 3
	CmdSetStopSize        byte = 4
	CmdSetControl         byte = 5
	CmdNotifyLineState    byte = 6
	CmdFlowControlSuspend byte = 8
	CmdFlowControlResume  byte = 9
	CmdSetLineStateMask   byte = 10
	CmdSetModemStateMask  byte = 11
	CmdPurgeData          byte = 12

	// CmdNotifyModemState is server-to-client (7 + 100).
	CmdNotifyModemState byte = 107
)

// SET-CONTROL values. Each signal has both an assert and a deassert encoding:
// the ESP32 reset sequence is a series of transitions, so a deassert that is
// dropped or mistaken for an assert leaves the chip out of its bootloader.
const (
	ControlBreakOn  byte = 5
	ControlBreakOff byte = 6
	ControlDTROn    byte = 8
	ControlDTROff   byte = 9
	ControlRTSOn    byte = 11
	ControlRTSOff   byte = 12
)

// ControlName returns a printable label for a SET-CONTROL value, for logs and
// audit entries. Unknown values render as "unknown" rather than empty so a log
// line never silently loses its subject.
func ControlName(v byte) string {
	switch v {
	case ControlBreakOn:
		return "break-on"
	case ControlBreakOff:
		return "break-off"
	case ControlDTROn:
		return "dtr-on"
	case ControlDTROff:
		return "dtr-off"
	case ControlRTSOn:
		return "rts-on"
	case ControlRTSOff:
		return "rts-off"
	default:
		return "unknown"
	}
}

// EncodeBaud renders a baud rate as the 4-byte big-endian value RFC2217 uses.
func EncodeBaud(baud uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, baud)
	return b
}

// DecodeBaud reads a 4-byte big-endian baud rate, returning 0 if the payload is
// malformed. Callers treat 0 as "leave the current rate alone".
func DecodeBaud(p []byte) uint32 {
	if len(p) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(p[:4])
}

// SET-CONTROL also carries outbound flow control, alongside the modem lines.
const (
	ControlFlowNone    byte = 1
	ControlFlowXONXOFF byte = 2
	ControlFlowRTSCTS  byte = 3
)

// PURGE_DATA values. pyserial's client sends one of these from inside
// Serial.open() — reset_input_buffer()/reset_output_buffer() are part of its
// connect sequence — and blocks until the server replies, so a server without
// a PURGE case fails every pyserial connect with "timeout while waiting for
// option 'purge'".
const (
	PurgeReceiveBuffer  byte = 1
	PurgeTransmitBuffer byte = 2
	PurgeBothBuffers    byte = 3
)
