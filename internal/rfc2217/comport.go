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

// Client-to-server com-port commands (RFC 2217 §3.3.1). Server-to-client
// replies use the same value plus 100 — the mapping the two blocks below keep
// apart. Mixing the namespaces once produced a notify sent as 207 on the wire.
const (
	CmdSignature          byte = 0
	CmdSetBaudRate        byte = 1
	CmdSetDataSize        byte = 2
	CmdSetParity          byte = 3
	CmdSetStopSize        byte = 4
	CmdSetControl         byte = 5
	CmdNotifyLineState    byte = 6 // client asks the server to report line state
	CmdNotifyModemState   byte = 7 // client polls for the modem status byte
	CmdFlowControlSuspend byte = 8
	CmdFlowControlResume  byte = 9
	CmdSetLineStateMask   byte = 10
	CmdSetModemStateMask  byte = 11
	CmdPurgeData          byte = 12
)

// Server-to-client com-port commands: the client-to-server value plus 100.
// SrvNotifyModemState doubles as the answer to a poll and as the unsolicited
// push on change: a client that has enabled polling (pyserial's `poll_modem`
// option) blocks reading .cts/.dsr/.ri/.cd until one arrives, raising
// "remote sends no NOTIFY_MODEMSTATE" otherwise.
const (
	SrvSetBaudRate        byte = 101
	SrvSetDataSize        byte = 102
	SrvSetParity          byte = 103
	SrvSetStopSize        byte = 104
	SrvSetControl         byte = 105
	SrvNotifyLineState    byte = 106
	SrvNotifyModemState   byte = 107
	SrvFlowControlSuspend byte = 108
	SrvFlowControlResume  byte = 109
	SrvSetLineStateMask   byte = 110
	SrvSetModemStateMask  byte = 111
	SrvPurgeData          byte = 112
)

// SET-CONTROL values (RFC 2217 §3.7). Each signal has both an assert and a
// deassert encoding: the ESP32 reset sequence is a series of transitions, so a
// deassert that is dropped or mistaken for an assert leaves the chip out of its
// bootloader.
//
// The zero values are queries — the server answers with the current setting
// for that signal, the same byte the client would send to set it. pyserial's
// client never sends them today, but a client that does (any implementation
// following the RFC) expects an answer, not silence.
const (
	// Query the current flow control (outbound/both).
	ControlQueryFlow   byte = 0
	ControlFlowNone    byte = 1
	ControlFlowXONXOFF byte = 2
	ControlFlowRTSCTS  byte = 3
	// Query the current break state.
	ControlQueryBreak byte = 4
	ControlBreakOn    byte = 5
	ControlBreakOff   byte = 6
	// Query the current DTR signal state.
	ControlQueryDTR byte = 7
	ControlDTROn    byte = 8
	ControlDTROff   byte = 9
	// Query the current RTS signal state.
	ControlQueryRTS byte = 10
	ControlRTSOn    byte = 11
	ControlRTSOff   byte = 12
	// Inbound flow control settings, and the remaining outbound variants.
	ControlQueryFlowIn   byte = 13
	ControlFlowNoneIn    byte = 14
	ControlFlowXONXOFFIn byte = 15
	ControlFlowRTSCTSIn  byte = 16
	ControlFlowDCD       byte = 17 // DCD flow control (outbound/both)
	ControlFlowDTRIn     byte = 18 // DTR flow control (inbound)
	ControlFlowDSR       byte = 19 // DSR flow control (outbound/both)
)

// ControlName returns a printable label for a SET-CONTROL value, for logs and
// audit entries. Unknown values render as "unknown" rather than empty so a log
// line never silently loses its subject.
func ControlName(v byte) string {
	switch v {
	case ControlQueryFlow:
		return "flow-query"
	case ControlFlowNone:
		return "flow-none"
	case ControlFlowXONXOFF:
		return "flow-xonxoff"
	case ControlFlowRTSCTS:
		return "flow-rtscts"
	case ControlQueryBreak:
		return "break-query"
	case ControlBreakOn:
		return "break-on"
	case ControlBreakOff:
		return "break-off"
	case ControlQueryDTR:
		return "dtr-query"
	case ControlDTROn:
		return "dtr-on"
	case ControlDTROff:
		return "dtr-off"
	case ControlQueryRTS:
		return "rts-query"
	case ControlRTSOn:
		return "rts-on"
	case ControlRTSOff:
		return "rts-off"
	case ControlQueryFlowIn:
		return "flow-in-query"
	case ControlFlowNoneIn:
		return "flow-in-none"
	case ControlFlowXONXOFFIn:
		return "flow-in-xonxoff"
	case ControlFlowRTSCTSIn:
		return "flow-in-rtscts"
	case ControlFlowDCD:
		return "flow-dcd"
	case ControlFlowDTRIn:
		return "flow-in-dtr"
	case ControlFlowDSR:
		return "flow-dsr"
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

// Modem state bits (RFC 2217 §3.3.2, NOTIFY-MODEMSTATE payload).
const (
	ModemCTS byte = 1 << 4
	ModemDSR byte = 1 << 5
	ModemRI  byte = 1 << 6
	ModemCD  byte = 1 << 7
)
